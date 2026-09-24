package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

func TestControlPlane_ScaleService_Sequence(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	now := time.Now().UTC()

	// 1. Setup Ready Node & Worker
	nodeID := id.NewNodeID()
	workerID := id.NewWorkerID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "node-scale",
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

	// Setup Worker TaskManager & Dispatcher
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

	// 2. Initialize ControlPlane
	cp, err := New(Options{
		Config: config.NewDefaultConfig(),
		Store:  store,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to initialize control plane: %v", err)
	}

	// 3. Initial Deploy: 1 Replica (0 -> 1)
	replicas := 1
	deployRes, err := cp.DeployService(ctx, &spec.ServiceConfig{
		Name:     "api",
		Command:  "go",
		Args:     []string{"version"},
		Replicas: &replicas,
		Runtime:  "native",
	}, dispatcher)
	if err != nil {
		t.Fatalf("initial deployment failed: %v", err)
	}
	if len(deployRes.Tasks) != 1 {
		t.Fatalf("expected 1 task on initial deploy, got %d", len(deployRes.Tasks))
	}

	// Helper to count active tasks
	getActiveTaskCount := func() int {
		tasks, _ := store.Tasks().ListByService(ctx, deployRes.ServiceID)
		count := 0
		for _, t := range tasks {
			if t.State != string(models.TaskStateStopped) && t.State != string(models.TaskStateFailed) && t.State != string(models.TaskStateLost) {
				count++
			}
		}
		return count
	}

	// Step 1: Scale 1 -> 3
	res1, err := cp.ScaleService(ctx, "api", 3, dispatcher)
	if err != nil {
		t.Fatalf("scale 1 -> 3 failed: %v", err)
	}
	if res1.DesiredReplicas != 3 || getActiveTaskCount() != 3 {
		t.Fatalf("expected 3 active tasks after 1 -> 3 scale, got %d", getActiveTaskCount())
	}
	if res1.Summary.CreatedTasks != 2 {
		t.Fatalf("expected 2 tasks created, got %d", res1.Summary.CreatedTasks)
	}

	// Step 2: Scale 3 -> 5
	res2, err := cp.ScaleService(ctx, "api", 5, dispatcher)
	if err != nil {
		t.Fatalf("scale 3 -> 5 failed: %v", err)
	}
	if res2.DesiredReplicas != 5 || getActiveTaskCount() != 5 {
		t.Fatalf("expected 5 active tasks after 3 -> 5 scale, got %d", getActiveTaskCount())
	}
	if res2.Summary.CreatedTasks != 2 {
		t.Fatalf("expected 2 tasks created, got %d", res2.Summary.CreatedTasks)
	}

	// Step 3: Scale 5 -> 2
	res3, err := cp.ScaleService(ctx, "api", 2, dispatcher)
	if err != nil {
		t.Fatalf("scale 5 -> 2 failed: %v", err)
	}
	if res3.DesiredReplicas != 2 || getActiveTaskCount() != 2 {
		t.Fatalf("expected 2 active tasks after 5 -> 2 scale, got %d", getActiveTaskCount())
	}
	if res3.Summary.RemovedTasks != 3 {
		t.Fatalf("expected 3 tasks removed, got %d", res3.Summary.RemovedTasks)
	}

	// Step 4: Scale 2 -> 0
	res4, err := cp.ScaleService(ctx, "api", 0, dispatcher)
	if err != nil {
		t.Fatalf("scale 2 -> 0 failed: %v", err)
	}
	if res4.DesiredReplicas != 0 || getActiveTaskCount() != 0 {
		t.Fatalf("expected 0 active tasks after 2 -> 0 scale, got %d", getActiveTaskCount())
	}
	if res4.Summary.RemovedTasks != 2 {
		t.Fatalf("expected 2 tasks removed, got %d", res4.Summary.RemovedTasks)
	}
	if res4.Status != "STOPPED" {
		t.Fatalf("expected status STOPPED when scaled to 0, got %s", res4.Status)
	}

	// Step 5: Scale 0 -> 1
	res5, err := cp.ScaleService(ctx, "api", 1, dispatcher)
	if err != nil {
		t.Fatalf("scale 0 -> 1 failed: %v", err)
	}
	if res5.DesiredReplicas != 1 || getActiveTaskCount() != 1 {
		t.Fatalf("expected 1 active task after 0 -> 1 scale, got %d", getActiveTaskCount())
	}
	if res5.Summary.CreatedTasks != 1 {
		t.Fatalf("expected 1 task created, got %d", res5.Summary.CreatedTasks)
	}
	if res5.Status != "RUNNING" {
		t.Fatalf("expected status RUNNING when scaled to 1, got %s", res5.Status)
	}
}
