package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

func TestReconciler_ScaleUpDeficit(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	now := time.Now().UTC()

	// 1. Create worker
	workerID := id.NewWorkerID()
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "node-1",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	// 2. Setup Worker TaskManager & Dispatcher
	tm := worker.NewTaskManager(worker.TaskManagerOptions{
		WorkerID: workerID,
		Runtime:  runtime.NewNativeRuntime(),
		Logger:   logger,
	})
	defer tm.Close()

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tm.AssignTask(ctx, worker.TaskAssignment{
			TaskID:  id.ID(req.Task.Id),
			Command: req.Command,
		})
	})

	// 3. Create Service with desired replicas = 3, but 0 tasks existing
	serviceID := id.NewServiceID()
	_ = store.Services().Create(ctx, &models.Service{
		ID:        serviceID,
		Name:      "worker-pool",
		Replicas:  3,
		Runtime:   "native",
		Command:   "go",
		Status:    "PENDING",
		CreatedAt: now,
		UpdatedAt: now,
	})

	reconciler := NewReconciler(ReconcilerConfig{Interval: 50 * time.Millisecond}, store, scheduler.NewBasicScheduler(), dispatcher, logger)

	// 4. Run ReconcileAll pass
	summary, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}

	if summary.CreatedTasks != 3 {
		t.Fatalf("expected 3 tasks created to satisfy deficit, got %d", summary.CreatedTasks)
	}

	// 5. Verify tasks in store
	tasks, err := store.Tasks().ListByService(ctx, serviceID)
	if err != nil || len(tasks) != 3 {
		t.Fatalf("expected 3 tasks in store, got %d (err: %v)", len(tasks), err)
	}

	// 6. Test Idempotency: Running ReconcileAll again should create 0 new tasks
	summary2, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("second reconciliation failed: %v", err)
	}
	if summary2.CreatedTasks != 0 || summary2.RemovedTasks != 0 {
		t.Fatalf("expected 0 modifications in idempotent second pass, got created=%d, removed=%d",
			summary2.CreatedTasks, summary2.RemovedTasks)
	}
}

func TestReconciler_ScaleDownSurplus(t *testing.T) {
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

	// Service with desired replicas = 1
	serviceID := id.NewServiceID()
	_ = store.Services().Create(ctx, &models.Service{
		ID:        serviceID,
		Name:      "web",
		Replicas:  1,
		Command:   "echo",
		Status:    "RUNNING",
		CreatedAt: now,
		UpdatedAt: now,
	})

	// But 3 active tasks currently running
	for i := 0; i < 3; i++ {
		_ = store.Tasks().Create(ctx, &models.Task{
			ID:        id.NewTaskID(),
			ServiceID: serviceID,
			WorkerID:  workerID,
			State:     string(models.TaskStateRunning),
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	reconciler := NewReconciler(ReconcilerConfig{}, store, scheduler.NewBasicScheduler(), nil, logger)

	// Reconcile -> should remove 2 surplus tasks
	summary, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if summary.RemovedTasks != 2 {
		t.Fatalf("expected 2 tasks removed, got %d", summary.RemovedTasks)
	}

	// Verify only 1 active task remains
	tasks, _ := store.Tasks().ListByService(ctx, serviceID)
	activeCount := 0
	for _, t := range tasks {
		if t.State == string(models.TaskStateRunning) {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected 1 active task remaining, got %d", activeCount)
	}
}

func TestReconciler_OrphanedTaskOnLostWorker(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	now := time.Now().UTC()

	// 1. Worker 1 is LOST
	w1 := id.NewWorkerID()
	n1 := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{ID: n1, Name: "node-1", Status: "LOST", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: w1, NodeID: n1, Status: "LOST", Heartbeat: now.Add(-1 * time.Hour), CreatedAt: now, UpdatedAt: now})

	// 2. Worker 2 is READY
	w2 := id.NewWorkerID()
	n2 := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{ID: n2, Name: "node-2", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: w2, NodeID: n2, Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	// Setup Dispatcher for Worker 2
	tm2 := worker.NewTaskManager(worker.TaskManagerOptions{WorkerID: w2, Runtime: runtime.NewNativeRuntime(), Logger: logger})
	defer tm2.Close()
	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(w2, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tm2.AssignTask(ctx, worker.TaskAssignment{TaskID: id.ID(req.Task.Id), Command: req.Command})
	})

	// 3. Service with desired replicas = 1
	serviceID := id.NewServiceID()
	_ = store.Services().Create(ctx, &models.Service{
		ID:        serviceID,
		Name:      "critical-api",
		Replicas:  1,
		Command:   "go",
		Status:    "RUNNING",
		CreatedAt: now,
		UpdatedAt: now,
	})

	// 4. Task is currently assigned to LOST Worker 1
	orphanedTaskID := id.NewTaskID()
	_ = store.Tasks().Create(ctx, &models.Task{
		ID:        orphanedTaskID,
		ServiceID: serviceID,
		WorkerID:  w1,
		State:     string(models.TaskStateRunning),
		CreatedAt: now,
		UpdatedAt: now,
	})

	reconciler := NewReconciler(ReconcilerConfig{}, store, scheduler.NewBasicScheduler(), dispatcher, logger)

	// 5. Reconcile pass: identifies orphan, marks LOST, schedules replacement onto Worker 2
	summary, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if summary.OrphanedRecovered != 1 {
		t.Fatalf("expected 1 orphaned task recovered, got %d", summary.OrphanedRecovered)
	}
	if summary.CreatedTasks != 1 {
		t.Fatalf("expected 1 replacement task scheduled, got %d", summary.CreatedTasks)
	}

	// Verify old task is marked LOST
	oldTask, _ := store.Tasks().Get(ctx, orphanedTaskID)
	if oldTask.State != string(models.TaskStateLost) {
		t.Fatalf("expected orphaned task state LOST, got %s", oldTask.State)
	}

	// Verify new task is assigned to Worker 2
	allTasks, _ := store.Tasks().ListByService(ctx, serviceID)
	var replacementTask *models.Task
	for _, t := range allTasks {
		if t.ID != orphanedTaskID {
			replacementTask = t
			break
		}
	}
	if replacementTask == nil || replacementTask.WorkerID != w2 {
		t.Fatalf("expected replacement task assigned to w2, got %+v", replacementTask)
	}
}
