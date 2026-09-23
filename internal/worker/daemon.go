package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/runtime"
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
	Config  *config.Config
	Logger  logging.Logger
	Runtime runtime.Runtime
}

// Daemon represents the machine-level CloudX execution agent.
type Daemon struct {
	mu           sync.RWMutex
	cfg          *config.Config
	logger       logging.Logger
	runtime      runtime.Runtime
	id           id.ID
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
		opts.Runtime = runtime.NewNativeRuntime()
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
		id:           workerID,
		status:       StatusStarting,
		heartbeatDur: opts.Config.Health.HeartbeatInterval,
		tasks:        make(map[string]*v1.Task),
	}

	return d, nil
}

// ID returns the stable worker ID.
func (d *Daemon) ID() id.ID {
	return d.id
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

	// 1. Register with Control Plane
	d.logger.Info("Registering worker %s with control plane...", d.id)
	regResp, err := d.cpClient.RegisterWorker(runCtx, &v1.RegisterWorkerRequest{
		NodeId:   d.cfg.Node.ID,
		WorkerId: d.id.String(),
		Address:  d.cfg.Worker.Address,
		Metadata: map[string]string{
			"runtime": d.cfg.Runtime.Type,
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

	d.setStatus(StatusReady)
	d.logger.Info("Worker %s registered successfully (Status: READY)", d.id)

	// 2. Start Background Heartbeat Loop
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
	d.mu.RUnlock()

	if client == nil {
		return
	}

	hbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	_, err := client.Heartbeat(hbCtx, &v1.HeartbeatRequest{
		WorkerId:   d.id.String(),
		Timestamp:  time.Now().UTC().UnixNano(),
		CpuUsage:   0.0,
		MemoryUsed: 0,
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
