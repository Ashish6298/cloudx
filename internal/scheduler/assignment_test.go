package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

func setupTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createTestNodeAndWorker(ctx context.Context, store *sqlite.Store, status string, heartbeat time.Time) (id.ID, id.ID, error) {
	nodeID := id.NewNodeID()
	workerID := id.NewWorkerID()
	now := time.Now().UTC()

	if err := store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "node-" + nodeID.String(),
		Address:   "127.0.0.1:9090",
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return "", "", err
	}

	if err := store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   "127.0.0.1:9090",
		Status:    status,
		Heartbeat: heartbeat,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return "", "", err
	}

	return nodeID, workerID, nil
}

func TestAssignmentCoordinator_EndToEndSuccess(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	logger := logging.NewDefaultLogger()

	// 1. Register Node and Worker in Store
	now := time.Now().UTC()
	_, workerID, err := createTestNodeAndWorker(ctx, store, "READY", now)
	if err != nil {
		t.Fatalf("failed to create node & worker: %v", err)
	}

	// 2. Setup Worker TaskManager
	rt := runtime.NewNativeRuntime()
	tm := worker.NewTaskManager(worker.TaskManagerOptions{
		WorkerID: workerID,
		Runtime:  rt,
		Logger:   logger,
	})
	defer tm.Close()

	// 3. Setup InProcessDispatcher
	dispatcher := NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tm.AssignTask(ctx, worker.TaskAssignment{
			TaskID:      id.ID(req.Task.Id),
			ServiceID:   id.ID(req.Task.ServiceId),
			Command:     req.Command,
			Args:        req.Args,
			Environment: req.Environment,
		})
	})

	// 4. Create AssignmentCoordinator
	sched := NewBasicScheduler()
	coordinator := NewAssignmentCoordinator(store, sched, dispatcher, logger)

	// 5. Assign Task
	taskID := id.NewTaskID()
	res, err := coordinator.Assign(ctx, AssignOptions{
		TaskID: taskID,
		Spec: TaskSpec{
			Command: "go",
			Args:    []string{"version"},
		},
		Requirements: &TaskRequirements{
			CPU:    0.5,
			Memory: 256 * 1024 * 1024,
		},
	})

	if err != nil {
		t.Fatalf("expected successful task assignment, got: %v", err)
	}

	if res.WorkerID != workerID {
		t.Fatalf("expected assignment to worker %s, got %s", workerID, res.WorkerID)
	}

	// 6. Verify assignment persistence in StateStore
	persistedTask, err := store.Tasks().Get(ctx, taskID)
	if err != nil {
		t.Fatalf("failed to fetch persisted task: %v", err)
	}

	if persistedTask.WorkerID != workerID {
		t.Fatalf("expected persisted worker %s, got %s", workerID, persistedTask.WorkerID)
	}
	if persistedTask.State != string(models.TaskStateAssigned) {
		t.Fatalf("expected state ASSIGNED, got %s", persistedTask.State)
	}

	// 7. Verify TaskManager picked up and executed task
	time.Sleep(200 * time.Millisecond)
	snap, err := tm.GetTask(taskID)
	if err != nil {
		t.Fatalf("failed to get task from task manager: %v", err)
	}
	if snap.State != models.TaskStateRunning && snap.State != models.TaskStateHealthy && snap.State != models.TaskStateStopped {
		t.Fatalf("unexpected task state in worker: %s", snap.State)
	}
}

func TestAssignmentCoordinator_WorkerUnavailable(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	logger := logging.NewDefaultLogger()

	// Worker is LOST
	now := time.Now().UTC()
	_, _, err := createTestNodeAndWorker(ctx, store, "LOST", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("failed to create node & worker: %v", err)
	}

	coordinator := NewAssignmentCoordinator(store, NewBasicScheduler(), nil, logger)
	_, err = coordinator.Assign(ctx, AssignOptions{
		TaskID: id.NewTaskID(),
		Spec: TaskSpec{
			Command: "echo",
			Args:    []string{"hello"},
		},
	})

	if err == nil {
		t.Fatalf("expected error when no worker is READY, got success")
	}
}

func TestAssignmentCoordinator_WorkerRejection(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	logger := logging.NewDefaultLogger()

	now := time.Now().UTC()
	_, workerID, err := createTestNodeAndWorker(ctx, store, "READY", now)
	if err != nil {
		t.Fatalf("failed to create node & worker: %v", err)
	}

	dispatcher := NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return errors.New("worker execution rejected: disk full")
	})

	coordinator := NewAssignmentCoordinator(store, NewBasicScheduler(), dispatcher, logger)
	taskID := id.NewTaskID()
	_, err = coordinator.Assign(ctx, AssignOptions{
		TaskID: taskID,
		Spec: TaskSpec{
			Command: "echo",
			Args:    []string{"hello"},
		},
	})

	if err == nil {
		t.Fatalf("expected error when worker rejects task, got nil")
	}

	// Verify task state was updated to FAILED in store
	persisted, err := store.Tasks().Get(ctx, taskID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if persisted.State != string(models.TaskStateFailed) {
		t.Fatalf("expected task state FAILED after rejection, got %s", persisted.State)
	}
}

func TestAssignmentCoordinator_DuplicateAssignment_RejectsInvalidState(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	logger := logging.NewDefaultLogger()

	now := time.Now().UTC()
	_, workerID, err := createTestNodeAndWorker(ctx, store, "READY", now)
	if err != nil {
		t.Fatalf("failed to create node & worker: %v", err)
	}

	taskID := id.NewTaskID()
	// Pre-create task in RUNNING state
	_ = store.Tasks().Create(ctx, &models.Task{
		ID:        taskID,
		WorkerID:  workerID,
		State:     string(models.TaskStateRunning),
		CreatedAt: now,
		UpdatedAt: now,
	})

	coordinator := NewAssignmentCoordinator(store, NewBasicScheduler(), nil, logger)
	_, err = coordinator.Assign(ctx, AssignOptions{
		TaskID: taskID,
		Spec: TaskSpec{
			Command: "echo",
			Args:    []string{"hello"},
		},
	})

	if err == nil {
		t.Fatalf("expected error assigning already RUNNING task, got nil")
	}
}
