package controlplane

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	run "github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

func getSleepCmd(seconds string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-Command", fmt.Sprintf("Start-Sleep -Seconds %s", seconds)}
	}
	return "sh", []string{"-c", fmt.Sprintf("sleep %s", seconds)}
}

// TestReconciliationReliability_ControlPlaneRestart tests that when the Control Plane
// or Reconciler restarts, it reads persistent state from SQLite without creating duplicate tasks or dropping replicas.
func TestReconciliationReliability_ControlPlaneRestart(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	now := time.Now().UTC()

	// 1. Setup Worker
	workerID := id.NewWorkerID()
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{ID: nodeID, Name: "node-1", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: workerID, NodeID: nodeID, Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	tm := worker.NewTaskManager(worker.TaskManagerOptions{WorkerID: workerID, Runtime: run.NewNativeRuntime(), Logger: logger})
	defer tm.Close()

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tm.AssignTask(ctx, worker.TaskAssignment{
			TaskID:  id.ID(req.Task.Id),
			Command: req.Command,
			Args:    req.Args,
		})
	})

	// 2. Deploy service with 2 desired replicas
	serviceID := id.NewServiceID()
	cmd, args := getSleepCmd("5")
	_ = store.Services().Create(ctx, &models.Service{
		ID:        serviceID,
		Name:      "api-web",
		Replicas:  2,
		Runtime:   "native",
		Command:   cmd,
		Status:    "PENDING",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = args

	// 3. First Control Plane instance reconciles
	r1 := NewReconciler(ReconcilerConfig{Interval: 50 * time.Millisecond}, store, scheduler.NewBasicScheduler(), dispatcher, logger)
	sum1, err := r1.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconcile 1 failed: %v", err)
	}
	if sum1.CreatedTasks != 2 {
		t.Fatalf("expected 2 tasks created, got %d", sum1.CreatedTasks)
	}

	tasks1, _ := store.Tasks().ListByService(ctx, serviceID)
	if len(tasks1) != 2 {
		t.Fatalf("expected 2 tasks in store, got %d", len(tasks1))
	}

	// 4. "Crash / Restart" control plane by creating a new Reconciler instance pointing to the same persistent state
	r2 := NewReconciler(ReconcilerConfig{Interval: 50 * time.Millisecond}, store, scheduler.NewBasicScheduler(), dispatcher, logger)

	sum2, err := r2.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconcile after restart failed: %v", err)
	}
	if sum2.CreatedTasks != 0 || sum2.RemovedTasks != 0 {
		t.Fatalf("reconciler after restart created %d tasks and removed %d tasks (must be 0)", sum2.CreatedTasks, sum2.RemovedTasks)
	}

	tasks2, _ := store.Tasks().ListByService(ctx, serviceID)
	if len(tasks2) != 2 {
		t.Fatalf("task count changed after control plane restart: expected 2, got %d", len(tasks2))
	}
}

// TestReconciliationReliability_WorkerRestartAndRecovery tests worker crashing, being marked LOST,
// having tasks rescheduled to a new worker, and then the original worker re-registering and recovering.
func TestReconciliationReliability_WorkerRestartAndRecovery(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	now := time.Now().UTC()

	// Worker 1 (will crash / lose heartbeat)
	w1 := id.NewWorkerID()
	n1 := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{ID: n1, Name: "node-1", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: w1, NodeID: n1, Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	// Worker 2 (healthy standby)
	w2 := id.NewWorkerID()
	n2 := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{ID: n2, Name: "node-2", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: w2, NodeID: n2, Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	tm2 := worker.NewTaskManager(worker.TaskManagerOptions{WorkerID: w2, Runtime: run.NewNativeRuntime(), Logger: logger})
	defer tm2.Close()

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(w2, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tm2.AssignTask(ctx, worker.TaskAssignment{
			TaskID:  id.ID(req.Task.Id),
			Command: req.Command,
			Args:    req.Args,
		})
	})

	// Service with 1 replica
	serviceID := id.NewServiceID()
	cmd, args := getSleepCmd("5")
	_ = store.Services().Create(ctx, &models.Service{
		ID:        serviceID,
		Name:      "db-service",
		Replicas:  1,
		Runtime:   "native",
		Command:   cmd,
		Status:    "RUNNING",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = args

	// Task running on Worker 1
	task1ID := id.NewTaskID()
	_ = store.Tasks().Create(ctx, &models.Task{
		ID:        task1ID,
		ServiceID: serviceID,
		WorkerID:  w1,
		State:     string(models.TaskStateRunning),
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Worker 1 goes LOST (heartbeat timeout)
	worker1Rec, _ := store.Workers().Get(ctx, w1)
	worker1Rec.Status = "LOST"
	worker1Rec.Heartbeat = now.Add(-10 * time.Minute)
	_ = store.Workers().Update(ctx, worker1Rec)

	reconciler := NewReconciler(ReconcilerConfig{}, store, scheduler.NewBasicScheduler(), dispatcher, logger)

	// Reconcile: Worker 1 is LOST -> orphan task1 is marked LOST -> replacement task scheduled to Worker 2
	sum1, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if sum1.OrphanedRecovered != 1 {
		t.Fatalf("expected 1 orphan recovered, got %d", sum1.OrphanedRecovered)
	}
	if sum1.CreatedTasks != 1 {
		t.Fatalf("expected 1 replacement task created, got %d", sum1.CreatedTasks)
	}

	// Verify old task is LOST and new task is ASSIGNED on w2
	oldTask, _ := store.Tasks().Get(ctx, task1ID)
	if oldTask.State != string(models.TaskStateLost) {
		t.Fatalf("expected old task state LOST, got %s", oldTask.State)
	}

	tasksOnW2, _ := store.Tasks().ListByWorker(ctx, w2)
	if len(tasksOnW2) != 1 {
		t.Fatalf("expected 1 task assigned to worker 2, got %d", len(tasksOnW2))
	}

	// Worker 1 comes back online (Worker Recovery)
	worker1Rec.Status = "READY"
	worker1Rec.Heartbeat = time.Now().UTC()
	_ = store.Workers().Update(ctx, worker1Rec)

	// Next reconciliation pass: system must remain stable at 1 desired replica (no duplicate task created)
	sum2, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconciliation after worker recovery failed: %v", err)
	}
	if sum2.CreatedTasks != 0 || sum2.RemovedTasks != 0 {
		t.Fatalf("unexpected task churn after worker recovery: created=%d, removed=%d", sum2.CreatedTasks, sum2.RemovedTasks)
	}
}

// TestReconciliationReliability_RPCFailureAndLostAssignment tests handling of transient RPC dispatch errors
// and verifying that the reconciler recovers and avoids infinite loops or corrupted state.
func TestReconciliationReliability_RPCFailureAndLostAssignment(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	now := time.Now().UTC()

	workerID := id.NewWorkerID()
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{ID: nodeID, Name: "node-1", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: workerID, NodeID: nodeID, Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	var shouldFail uint32 = 1

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		if atomic.LoadUint32(&shouldFail) == 1 {
			return fmt.Errorf("transient RPC connection reset / timeout")
		}
		return nil
	})

	serviceID := id.NewServiceID()
	cmd, _ := getSleepCmd("5")
	_ = store.Services().Create(ctx, &models.Service{
		ID:        serviceID,
		Name:      "payment-worker",
		Replicas:  1,
		Runtime:   "native",
		Command:   cmd,
		Status:    "PENDING",
		CreatedAt: now,
		UpdatedAt: now,
	})

	reconciler := NewReconciler(ReconcilerConfig{}, store, scheduler.NewBasicScheduler(), dispatcher, logger)

	// 1. First pass: RPC fails -> task created with FAILED state in store, assignment rejected
	sum1, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if sum1.CreatedTasks != 0 {
		t.Fatalf("expected 0 successfully created tasks due to RPC failure, got %d", sum1.CreatedTasks)
	}

	// 2. Worker RPC heals
	atomic.StoreUint32(&shouldFail, 0)

	// 3. Second pass: reconciler sees deficit and successfully dispatches
	sum2, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("second reconcile error: %v", err)
	}
	if sum2.CreatedTasks != 1 {
		t.Fatalf("expected 1 task created on healed RPC, got %d", sum2.CreatedTasks)
	}

	// 4. Third pass: idempotent, no infinite loops
	sum3, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("third reconcile error: %v", err)
	}
	if sum3.CreatedTasks != 0 || sum3.RemovedTasks != 0 {
		t.Fatalf("expected 0 modifications in 3rd pass, got created=%d, removed=%d", sum3.CreatedTasks, sum3.RemovedTasks)
	}
}

// TestReconciliationReliability_DuplicateEventsAndDelayedResponses tests duplicate assignment requests
// and delayed status updates to ensure deterministic state convergence without duplicate executions.
func TestReconciliationReliability_DuplicateEventsAndDelayedResponses(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	now := time.Now().UTC()

	workerID := id.NewWorkerID()
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{ID: nodeID, Name: "node-1", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: workerID, NodeID: nodeID, Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	tm := worker.NewTaskManager(worker.TaskManagerOptions{
		WorkerID: workerID,
		Runtime:  run.NewNativeRuntime(),
		Logger:   logger,
	})
	defer tm.Close()

	// Direct assignment to TaskManager twice (simulating duplicate gRPC delivery)
	taskID := id.NewTaskID()
	cmd, args := getSleepCmd("3")
	assignReq := worker.TaskAssignment{
		TaskID:  taskID,
		Command: cmd,
		Args:    args,
	}

	err1 := tm.AssignTask(ctx, assignReq)
	if err1 != nil {
		t.Fatalf("initial assignment failed: %v", err1)
	}

	// Duplicate event
	err2 := tm.AssignTask(ctx, assignReq)
	if err2 != nil {
		t.Fatalf("duplicate assignment should be handled idempotently without error, got: %v", err2)
	}

	// Verify only 1 managed task exists
	tasks := tm.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected exactly 1 task running despite duplicate event, got %d", len(tasks))
	}
}
