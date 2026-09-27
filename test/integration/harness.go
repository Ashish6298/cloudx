package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/api"
	"github.com/cloudx-org/cloudx/internal/auth"
	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/cloudx-org/cloudx/internal/logs"
	"github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/simulation"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

// WorkerNode represents a managed worker daemon and its local components inside the test harness.
type WorkerNode struct {
	ID          id.ID
	NodeID      id.ID
	Daemon      *worker.Daemon
	TaskManager *worker.TaskManager
	StorageDir  string
}

// ClusterHarness manages an automated, completely local, in-memory/temp-file CloudX cluster
// with 1 Control Plane and N Worker nodes for end-to-end integration tests.
type ClusterHarness struct {
	mu           sync.RWMutex
	t            *testing.T
	ctx          context.Context
	cancel       context.CancelFunc
	baseDir      string
	Store        state.Store
	ControlPlane *controlplane.ControlPlane
	APIServer    *api.Server
	Dispatcher   *scheduler.InProcessDispatcher
	Workers      map[id.ID]*WorkerNode
	Simulator    *simulation.Simulator
	Logger       logging.Logger
}

// HarnessOptions provides configuration for the integration test harness.
type HarnessOptions struct {
	WorkerCount int
	LogLevel    string
	UseSQLite   bool // if true, uses temp file on disk; if false, uses in-memory SQLite
}

// NewClusterHarness sets up an end-to-end local cluster (Control Plane + N Workers + Dispatcher + Simulator).
func NewClusterHarness(t *testing.T, opts HarnessOptions) (*ClusterHarness, error) {
	t.Helper()

	if opts.WorkerCount <= 0 {
		opts.WorkerCount = 3
	}

	tempDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = auth.WithPermissionScope(ctx, auth.ScopeControlPlane) // Root control-plane and worker operations

	logger := logging.NewDefaultLogger()

	// 1. Initialize SQLite store
	dbPath := ":memory:"
	if opts.UseSQLite {
		dbPath = filepath.Join(tempDir, "cloudx_integration.db")
	}
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to open sqlite store: %w", err)
	}

	// 2. Initialize Control Plane
	cpConfig := config.NewDefaultConfig()
	cpConfig.Storage.Path = filepath.Join(tempDir, "controlplane")
	cpConfig.Health.HeartbeatInterval = 100 * time.Millisecond

	cp, err := controlplane.New(controlplane.Options{
		Config: cpConfig,
		Store:  store,
		Logger: logger,
	})
	if err != nil {
		cancel()
		_ = store.Close()
		return nil, fmt.Errorf("failed to create control plane: %w", err)
	}

	// 3. Initialize and boot gRPC API Server for the Control Plane
	apiServer, err := api.NewServer(api.ServerOptions{
		Address: "127.0.0.1:0", // dynamic loopback port
		Store:   store,
		Logger:  logger,
	})
	if err != nil {
		cancel()
		_ = store.Close()
		return nil, fmt.Errorf("failed to initialize api server: %w", err)
	}

	if err := apiServer.Start(); err != nil {
		cancel()
		_ = store.Close()
		return nil, fmt.Errorf("failed to start api server: %w", err)
	}

	// 4. Create Dispatcher and Simulator
	dispatcher := scheduler.NewInProcessDispatcher()
	cp.Reconciler.SetDispatcher(dispatcher)

	sim := simulation.NewSimulator(store, nil, nil, logger)

	harness := &ClusterHarness{
		t:            t,
		ctx:          ctx,
		cancel:       cancel,
		baseDir:      tempDir,
		Store:        store,
		ControlPlane: cp,
		APIServer:    apiServer,
		Dispatcher:   dispatcher,
		Workers:      make(map[id.ID]*WorkerNode),
		Simulator:    sim,
		Logger:       logger,
	}

	// Start Control Plane background services
	if err := cp.Start(ctx); err != nil {
		harness.Teardown()
		return nil, fmt.Errorf("failed to start control plane: %w", err)
	}

	// 5. Initialize and register N Worker Daemons
	for i := 1; i <= opts.WorkerCount; i++ {
		workerNode, err := harness.AddWorker(fmt.Sprintf("node-%d", i))
		if err != nil {
			harness.Teardown()
			return nil, fmt.Errorf("failed to initialize worker node %d: %w", i, err)
		}
		_ = workerNode
	}

	t.Cleanup(func() {
		harness.Teardown()
	})

	return harness, nil
}

// AddWorker spins up and registers a new Worker daemon with the harness.
func (h *ClusterHarness) AddWorker(nodeName string) (*WorkerNode, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	workerDir := filepath.Join(h.baseDir, "workers", nodeName)
	_ = os.MkdirAll(workerDir, 0755)

	workerCfg := config.NewDefaultConfig()
	workerCfg.Storage.Path = workerDir
	workerCfg.ControlPlane.Address = h.APIServer.Address()
	workerCfg.Health.HeartbeatInterval = 100 * time.Millisecond

	nodeID := id.NewNodeID()
	workerCfg.Node.ID = nodeID.String()

	nativeRuntime := runtime.NewNativeRuntime()
	logDir := filepath.Join(workerDir, "logs")
	wl := logs.NewWorkloadLogger(logDir, 1000)

	daemon, err := worker.NewDaemon(worker.Options{
		Config:  workerCfg,
		Logger:  h.Logger,
		Runtime: nativeRuntime,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate daemon for %s: %w", nodeName, err)
	}

	// Start and register daemon with control plane gRPC
	if err := daemon.Start(h.ctx); err != nil {
		return nil, fmt.Errorf("failed to start and register daemon for %s: %w", nodeName, err)
	}

	workerID := daemon.ID()
	tm := daemon.TaskManager()

	// Register in-process dispatcher callback
	h.Dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		var env map[string]string
		if req.Environment != nil {
			env = req.Environment
		}
		var args []string
		if req.Args != nil {
			args = req.Args
		}

		return tm.AssignTask(ctx, worker.TaskAssignment{
			TaskID:       id.ID(req.Task.Id),
			ServiceID:    id.ID(req.Task.ServiceId),
			JobID:        id.ID(req.Task.JobId),
			DeploymentID: id.ID(req.Task.DeploymentId),
			Command:      req.Command,
			Args:         args,
			Environment:  env,
		})
	})

	wn := &WorkerNode{
		ID:          workerID,
		NodeID:      nodeID,
		Daemon:      daemon,
		TaskManager: tm,
		StorageDir:  workerDir,
	}
	_ = wl

	h.Workers[workerID] = wn
	return wn, nil
}

// DeployService initiates a service deployment on the cluster.
func (h *ClusterHarness) DeployService(svcConfig *spec.ServiceConfig) (*controlplane.DeployResult, error) {
	return h.ControlPlane.DeployService(h.ctx, svcConfig, h.Dispatcher)
}

// ScaleService changes the desired replica count for a service and reconciles.
func (h *ClusterHarness) ScaleService(serviceName string, replicas int) (*controlplane.ScaleResult, error) {
	return h.ControlPlane.ScaleService(h.ctx, serviceName, replicas, h.Dispatcher)
}

// RollbackService rolls back a service to a previous deployment.
func (h *ClusterHarness) RollbackService(serviceName string, targetVersion string) (*controlplane.RollbackResult, error) {
	return h.ControlPlane.RollbackService(h.ctx, serviceName, targetVersion, h.Dispatcher)
}

// Reconcile executes an explicit reconciliation pass across the cluster.
func (h *ClusterHarness) Reconcile() (*controlplane.ReconciliationSummary, error) {
	return h.ControlPlane.Reconciler.ReconcileAll(h.ctx)
}

// GetActiveTasks returns all running/active tasks for a given service.
func (h *ClusterHarness) GetActiveTasks(serviceID id.ID) ([]*models.Task, error) {
	tasks, err := h.Store.Tasks().ListByService(h.ctx, serviceID)
	if err != nil {
		return nil, err
	}
	var active []*models.Task
	for _, t := range tasks {
		if t.State != string(models.TaskStateStopped) && t.State != string(models.TaskStateFailed) && t.State != string(models.TaskStateLost) {
			active = append(active, t)
		}
	}
	return active, nil
}

// CrashTask terminates a task's process and simulates crash failure.
func (h *ClusterHarness) CrashTask(taskID id.ID) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Find worker holding task
	task, err := h.Store.Tasks().Get(h.ctx, taskID)
	if err != nil {
		return err
	}

	wn, ok := h.Workers[task.WorkerID]
	if !ok || wn == nil {
		return fmt.Errorf("worker %s for task %s not found in harness", task.WorkerID, taskID)
	}

	sim := simulation.NewSimulator(h.Store, wn.TaskManager, nil, h.Logger)
	res, err := sim.KillProcess(h.ctx, taskID)
	if err != nil {
		return err
	}
	if !res.Success {
		return fmt.Errorf("kill process simulation failed: %s", res.Action)
	}

	// Update task status in store as FAILED
	task.State = string(models.TaskStateFailed)
	task.UpdatedAt = time.Now().UTC()
	return h.Store.Tasks().Update(h.ctx, task)
}

// StopWorker gracefully or abruptly terminates a worker daemon.
func (h *ClusterHarness) StopWorker(workerID id.ID) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	wn, ok := h.Workers[workerID]
	if !ok {
		return fmt.Errorf("worker %s not found", workerID)
	}

	if err := wn.Daemon.Stop(h.ctx); err != nil {
		return err
	}

	// Mark worker as LOST in state store to simulate node disappearance
	w, err := h.Store.Workers().Get(h.ctx, workerID)
	if err == nil && w != nil {
		w.Status = "LOST"
		w.UpdatedAt = time.Now().UTC()
		_ = h.Store.Workers().Update(h.ctx, w)
	}

	delete(h.Workers, workerID)
	return nil
}

// Teardown cleanly stops all workers, control plane, API server, and closes store.
func (h *ClusterHarness) Teardown() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.cancel != nil {
		h.cancel()
	}

	for _, wn := range h.Workers {
		_ = wn.Daemon.Stop(context.Background())
	}

	if h.APIServer != nil {
		h.APIServer.Stop()
	}

	if h.ControlPlane != nil {
		_ = h.ControlPlane.Stop(context.Background())
	}

	if h.Store != nil {
		_ = h.Store.Close()
	}
}
