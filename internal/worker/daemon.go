package worker

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/common/version"
	"github.com/cloudx-org/cloudx/internal/config"
	run "github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/worker/monitor"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Status represents the operational lifecycle state of the Worker daemon.
type Status string

const (
	StatusStarting    Status = "STARTING"
	StatusRegistering Status = "REGISTERING"
	StatusReady       Status = "READY"
	StatusDegraded    Status = "DEGRADED"
	StatusStopping    Status = "STOPPING"
	StatusStopped     Status = "STOPPED"
)

// Options specifies configuration and dependencies for the Daemon.
type Options struct {
	Config    *config.Config
	Logger    logging.Logger
	Runtime   run.Runtime
	Collector monitor.Collector
}

// Daemon represents the machine-level CloudX execution agent.
type Daemon struct {
	mu           sync.RWMutex
	cfg          *config.Config
	logger       logging.Logger
	runtime      run.Runtime
	taskManager  *TaskManager
	collector    monitor.Collector
	id           id.ID
	clusterID    string
	status       Status
	cancel       context.CancelFunc
	clientConn   *grpc.ClientConn
	cpClient     v1.ControlPlaneServiceClient
	heartbeatDur time.Duration
	tasks        map[string]*v1.Task
}

// NewDaemon initializes a new Worker Daemon instance.
func NewDaemon(opts Options) (*Daemon, error) {
	if opts.Config == nil {
		opts.Config = config.NewDefaultConfig()
	}
	if err := opts.Config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid worker config: %w", err)
	}
	if opts.Logger == nil {
		opts.Logger = logging.NewDefaultLogger()
	}
	if opts.Runtime == nil {
		opts.Runtime = run.NewNativeRuntime()
	}
	if opts.Collector == nil {
		opts.Collector = monitor.NewPlatformCollector()
	}

	idMgr := NewIdentityManager(opts.Config.Storage.Path)
	workerID, err := idMgr.GetOrCreateIdentity("")
	if err != nil {
		workerID = id.NewWorkerID()
	}

	d := &Daemon{
		cfg:          opts.Config,
		logger:       opts.Logger.WithWorker(workerID.String()),
		runtime:      opts.Runtime,
		collector:    opts.Collector,
		id:           workerID,
		status:       StatusStarting,
		heartbeatDur: opts.Config.Health.HeartbeatInterval,
		tasks:        make(map[string]*v1.Task),
	}

	d.taskManager = NewTaskManager(TaskManagerOptions{
		WorkerID: workerID,
		Runtime:  opts.Runtime,
		Reporter: d,
		Logger:   d.logger,
	})

	return d, nil
}

// ID returns the stable worker ID.
func (d *Daemon) ID() id.ID {
	return d.id
}

// ClusterID returns the cluster ID returned by the control plane.
func (d *Daemon) ClusterID() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.clusterID
}

// TaskManager returns the worker task manager.
func (d *Daemon) TaskManager() *TaskManager {
	return d.taskManager
}

// ReportTaskStatus reports a task status update to the control plane.
func (d *Daemon) ReportTaskStatus(ctx context.Context, req *v1.ReportTaskStatusRequest) (*v1.ReportTaskStatusResponse, error) {
	d.mu.RLock()
	client := d.cpClient
	d.mu.RUnlock()

	if client == nil {
		return &v1.ReportTaskStatusResponse{Acknowledged: false}, nil
	}

	return client.ReportTaskStatus(ctx, req)
}

// Status returns the current lifecycle status of the worker.
func (d *Daemon) Status() Status {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.status
}

func (d *Daemon) setStatus(s Status) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status = s
	d.logger.Debug("Worker status changed to: %s", s)
}

// Start connects to the control plane, registers, starts heartbeats, and runs the daemon loop.
func (d *Daemon) Start(ctx context.Context) error {
	d.setStatus(StatusRegistering)
	runCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()

	d.logger.Info("Connecting to CloudX Control Plane at %s...", d.cfg.ControlPlane.Address)

	// Dial Control Plane gRPC
	conn, err := grpc.DialContext(runCtx, d.cfg.ControlPlane.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		d.setStatus(StatusDegraded)
		d.logger.Error("Failed to connect to control plane: %v", err)
		return fmt.Errorf("failed to connect to control plane at %s: %w", d.cfg.ControlPlane.Address, err)
	}

	d.mu.Lock()
	d.clientConn = conn
	d.cpClient = v1.NewControlPlaneServiceClient(conn)
	d.mu.Unlock()

	// 1. Gather hardware telemetry for registration
	hostname, _ := os.Hostname()
	cpuCapacity := float64(runtime.NumCPU())
	var memCapacity int64
	if d.collector != nil {
		if metrics, err := d.collector.Collect(runCtx); err == nil && metrics != nil {
			memCapacity = metrics.TotalMemoryBytes
		}
	}

	// 2. Register with Control Plane
	d.logger.Info("Registering worker %s (host: %s, CPUs: %.0f) with control plane...", d.id, hostname, cpuCapacity)
	regResp, err := d.cpClient.RegisterWorker(runCtx, &v1.RegisterWorkerRequest{
		NodeId:              d.cfg.Node.ID,
		WorkerId:            d.id.String(),
		Hostname:            hostname,
		Address:             d.cfg.Worker.Address,
		RuntimeCapabilities: []string{d.cfg.Runtime.Type, d.runtime.Type()},
		CpuCapacity:         cpuCapacity,
		MemoryCapacity:      memCapacity,
		Version:             version.Get().Version,
		Metadata: map[string]string{
			"runtime":  d.cfg.Runtime.Type,
			"platform": fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		},
	})
	if err != nil {
		d.setStatus(StatusDegraded)
		d.logger.Error("Worker registration failed: %v", err)
		return fmt.Errorf("registration failed: %w", err)
	}

	if !regResp.Accepted {
		d.setStatus(StatusDegraded)
		return fmt.Errorf("registration rejected by control plane: %s", regResp.Message)
	}

	d.mu.Lock()
	d.clusterID = regResp.ClusterId
	if regResp.HeartbeatIntervalMs > 0 {
		d.heartbeatDur = time.Duration(regResp.HeartbeatIntervalMs) * time.Millisecond
	}
	d.mu.Unlock()

	d.setStatus(StatusReady)
	d.logger.Info("Worker %s registered successfully with cluster %s (Status: READY)", d.id, regResp.ClusterId)

	// 3. Start Background Heartbeat Loop
	go d.heartbeatLoop(runCtx)

	return nil
}

func (d *Daemon) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(d.heartbeatDur)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.sendHeartbeat(ctx)
		}
	}
}

func (d *Daemon) sendHeartbeat(ctx context.Context) {
	d.mu.RLock()
	client := d.cpClient
	collector := d.collector
	d.mu.RUnlock()

	if client == nil {
		return
	}

	var cpuUsage float64
	var memUsed int64
	if collector != nil {
		if m, err := collector.Collect(ctx); err == nil && m != nil {
			cpuUsage = m.CPUUsagePercent
			memUsed = m.MemoryUsedBytes
		}
	}

	hbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	_, err := client.Heartbeat(hbCtx, &v1.HeartbeatRequest{
		WorkerId:   d.id.String(),
		Timestamp:  time.Now().UTC().UnixNano(),
		CpuUsage:   cpuUsage,
		MemoryUsed: memUsed,
	})

	if err != nil {
		d.logger.Warn("Heartbeat missed: %v", err)
		if d.Status() == StatusReady {
			d.setStatus(StatusDegraded)
		}
	} else if d.Status() == StatusDegraded {
		d.setStatus(StatusReady)
	}
}

// Stop gracefully shuts down the worker daemon.
func (d *Daemon) Stop(ctx context.Context) error {
	d.setStatus(StatusStopping)
	d.logger.Info("Gracefully stopping Worker %s...", d.id)

	if d.taskManager != nil {
		_ = d.taskManager.Close()
	}

	d.mu.Lock()
	if d.cancel != nil {
		d.cancel()
	}
	if d.clientConn != nil {
		_ = d.clientConn.Close()
	}
	d.mu.Unlock()

	d.setStatus(StatusStopped)
	d.logger.Info("Worker %s STOPPED", d.id)
	return nil
}
