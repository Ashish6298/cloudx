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

// TestRollingDeployment_ThreeReplicasProgressiveReplacement tests the exact scenario in Phase 34:
// 3 replicas: v1 (task-1, task-2, task-3).
// Deploy v2.
// CloudX replaces replicas progressively:
// Pass 1: 1 v2 added, 1 v1 stopped -> 2 v1, 1 v2 active
// Pass 2: 1 v2 added, 1 v1 stopped -> 1 v1, 2 v2 active
// Pass 3: 1 v2 added, 1 v1 stopped -> 0 v1, 3 v2 active
func TestRollingDeployment_ThreeReplicasProgressiveReplacement(t *testing.T) {
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

	tm := worker.NewTaskManager(worker.TaskManagerOptions{
		WorkerID: workerID,
		Runtime:  runtime.NewNativeRuntime(),
		Logger:   logger,
	})
	defer tm.Close()

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tm.AssignTask(ctx, worker.TaskAssignment{
			TaskID:      id.ID(req.Task.Id),
			Command:     req.Command,
			Args:        req.Args,
			Environment: req.Environment,
		})
	})

	cfg := config.NewDefaultConfig()
	cp, err := New(Options{
		Config: cfg,
		Store:  store,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to create control plane: %v", err)
	}

	// 2. Initial Deployment: 3 replicas on v1
	replicas := 3
	svcConfigV1 := &spec.ServiceConfig{
		Name:     "web-api",
		Version:  "v1",
		Command:  "go",
		Args:     []string{"version"},
		Replicas: &replicas,
		Runtime:  "native",
		UpdateStrategy: &spec.UpdateStrategyConfig{
			Type:           "rolling",
			MaxUnavailable: 1,
		},
	}

	resV1, err := cp.DeployService(ctx, svcConfigV1, dispatcher)
	if err != nil {
		t.Fatalf("v1 deployment failed: %v", err)
	}

	// Verify all 3 replicas created on v1
	tasksAfterV1, _ := store.Tasks().ListByService(ctx, resV1.ServiceID)
	var activeV1 []*models.Task
	for _, tsk := range tasksAfterV1 {
		if tsk.DeploymentID == resV1.DeploymentID && tsk.State != string(models.TaskStateStopped) {
			activeV1 = append(activeV1, tsk)
		}
	}
	if len(activeV1) != 3 {
		t.Fatalf("expected 3 active tasks on v1 initial deploy, got %d", len(activeV1))
	}

	// 3. Deploy v2: web-api:v2 with rolling update
	svcConfigV2 := &spec.ServiceConfig{
		Name:     "web-api",
		Version:  "v2",
		Command:  "go",
		Args:     []string{"env"},
		Replicas: &replicas,
		Runtime:  "native",
		UpdateStrategy: &spec.UpdateStrategyConfig{
			Type:           "rolling",
			MaxUnavailable: 1,
		},
	}

	resV2, err := cp.DeployService(ctx, svcConfigV2, dispatcher)
	if err != nil {
		t.Fatalf("v2 deployment failed: %v", err)
	}

	// Helper to count active tasks by deployment
	countActive := func() (v1Count int, v2Count int) {
		tsks, _ := store.Tasks().ListByService(ctx, resV1.ServiceID)
		for _, tsk := range tsks {
			if tsk.State != string(models.TaskStateStopped) && tsk.State != string(models.TaskStateFailed) {
				if tsk.DeploymentID == resV1.DeploymentID {
					v1Count++
				} else if tsk.DeploymentID == resV2.DeploymentID {
					v2Count++
				}
			}
		}
		return
	}

	// Step 1 (DeployService ran pass 1):
	// Expected: 2 v1 tasks, 1 v2 task (continuous availability: total live = 3, never all replaced at once)
	v1C, v2C := countActive()
	if v1C != 2 || v2C != 1 {
		t.Fatalf("step 1 expected (2 v1, 1 v2), got (v1=%d, v2=%d)", v1C, v2C)
	}

	// Step 2 (Pass 2):
	// Expected: 1 v1 task, 2 v2 tasks
	summaryPass2, err := cp.Reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconciler pass 2 failed: %v", err)
	}
	if summaryPass2.CreatedTasks != 1 || summaryPass2.RemovedTasks != 1 {
		t.Fatalf("step 2 expected 1 created, 1 removed, got (created=%d, removed=%d)", summaryPass2.CreatedTasks, summaryPass2.RemovedTasks)
	}
	v1C, v2C = countActive()
	if v1C != 1 || v2C != 2 {
		t.Fatalf("step 2 expected (1 v1, 2 v2), got (v1=%d, v2=%d)", v1C, v2C)
	}

	// Step 3 (Pass 3):
	// Expected: 0 v1 tasks, 3 v2 tasks
	summaryPass3, err := cp.Reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconciler pass 3 failed: %v", err)
	}
	if summaryPass3.CreatedTasks != 1 || summaryPass3.RemovedTasks != 1 {
		t.Fatalf("step 3 expected 1 created, 1 removed, got (created=%d, removed=%d)", summaryPass3.CreatedTasks, summaryPass3.RemovedTasks)
	}
	v1C, v2C = countActive()
	if v1C != 0 || v2C != 3 {
		t.Fatalf("step 3 expected (0 v1, 3 v2), got (v1=%d, v2=%d)", v1C, v2C)
	}

	// Step 4 (Pass 4: Steady state, no changes)
	summaryPass4, err := cp.Reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconciler pass 4 failed: %v", err)
	}
	if summaryPass4.CreatedTasks != 0 || summaryPass4.RemovedTasks != 0 {
		t.Fatalf("step 4 expected 0 created, 0 removed in steady state, got (created=%d, removed=%d)", summaryPass4.CreatedTasks, summaryPass4.RemovedTasks)
	}
}

// TestRollingDeployment_StopRolloutOnUnhealthy verifies failure detection during rolling deployment.
// If a new replica becomes UNHEALTHY or FAILS, the rollout is halted and existing replicas are preserved.
func TestRollingDeployment_StopRolloutOnUnhealthy(t *testing.T) {
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

	tm := worker.NewTaskManager(worker.TaskManagerOptions{WorkerID: workerID, Runtime: runtime.NewNativeRuntime(), Logger: logger})
	defer tm.Close()

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tm.AssignTask(ctx, worker.TaskAssignment{
			TaskID:      id.ID(req.Task.Id),
			Command:     req.Command,
			Args:        req.Args,
			Environment: req.Environment,
		})
	})

	cfg := config.NewDefaultConfig()
	cp, err := New(Options{
		Config: cfg,
		Store:  store,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to create control plane: %v", err)
	}

	// Deploy v1 with 3 replicas
	replicas := 3
	svcConfigV1 := &spec.ServiceConfig{
		Name:     "payment-svc",
		Version:  "v1",
		Command:  "go",
		Args:     []string{"version"},
		Replicas: &replicas,
		Runtime:  "native",
	}
	resV1, err := cp.DeployService(ctx, svcConfigV1, dispatcher)
	if err != nil {
		t.Fatalf("deploy v1 failed: %v", err)
	}

	// Deploy v2
	svcConfigV2 := &spec.ServiceConfig{
		Name:     "payment-svc",
		Version:  "v2",
		Command:  "go",
		Args:     []string{"env"},
		Replicas: &replicas,
		Runtime:  "native",
	}
	resV2, err := cp.DeployService(ctx, svcConfigV2, dispatcher)
	if err != nil {
		t.Fatalf("deploy v2 failed: %v", err)
	}

	// Mark the newly deployed v2 task as UNHEALTHY / CRASH_LOOP
	tsks, _ := store.Tasks().ListByService(ctx, resV1.ServiceID)
	for _, tsk := range tsks {
		if tsk.DeploymentID == resV2.DeploymentID {
			tsk.State = string(models.TaskStateUnhealthy)
			_ = store.Tasks().Update(ctx, tsk)
			break
		}
	}

	// Trigger reconciliation pass: it must detect the unhealthy v2 task and halt rollout
	summary, err := cp.Reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("reconciler failed: %v", err)
	}

	if summary.CreatedTasks != 0 || summary.RemovedTasks != 0 {
		t.Fatalf("expected rollout halted (0 created, 0 removed), got (created=%d, removed=%d)", summary.CreatedTasks, summary.RemovedTasks)
	}

	// Verify that remaining 2 v1 tasks are NOT removed
	tsksAfterHalt, _ := store.Tasks().ListByService(ctx, resV1.ServiceID)
	v1Active := 0
	for _, tsk := range tsksAfterHalt {
		if tsk.DeploymentID == resV1.DeploymentID && tsk.State != string(models.TaskStateStopped) {
			v1Active++
		}
	}
	if v1Active != 2 {
		t.Fatalf("expected 2 healthy v1 tasks preserved during rollout halt, got %d", v1Active)
	}
}

// TestRollingDeployment_RecreateStrategy verifies recreate update strategy.
func TestRollingDeployment_RecreateStrategy(t *testing.T) {
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

	tm := worker.NewTaskManager(worker.TaskManagerOptions{WorkerID: workerID, Runtime: runtime.NewNativeRuntime(), Logger: logger})
	defer tm.Close()

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tm.AssignTask(ctx, worker.TaskAssignment{
			TaskID:      id.ID(req.Task.Id),
			Command:     req.Command,
			Args:        req.Args,
			Environment: req.Environment,
		})
	})

	cfg := config.NewDefaultConfig()
	cp, err := New(Options{
		Config: cfg,
		Store:  store,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to create control plane: %v", err)
	}

	replicas := 2
	svcConfigV1 := &spec.ServiceConfig{
		Name:     "batch-worker",
		Version:  "v1",
		Command:  "go",
		Args:     []string{"version"},
		Replicas: &replicas,
		Runtime:  "native",
	}
	resV1, err := cp.DeployService(ctx, svcConfigV1, dispatcher)
	if err != nil {
		t.Fatalf("deploy v1 failed: %v", err)
	}

	// Deploy v2 with Recreate strategy
	svcConfigV2 := &spec.ServiceConfig{
		Name:     "batch-worker",
		Version:  "v2",
		Command:  "go",
		Args:     []string{"env"},
		Replicas: &replicas,
		Runtime:  "native",
		UpdateStrategy: &spec.UpdateStrategyConfig{
			Type: "recreate",
		},
	}
	resV2, err := cp.DeployService(ctx, svcConfigV2, dispatcher)
	if err != nil {
		t.Fatalf("deploy v2 with recreate failed: %v", err)
	}

	// With recreate, all v1 tasks should be stopped in the single pass, and v2 tasks provisioned
	tsks, _ := store.Tasks().ListByService(ctx, resV1.ServiceID)
	v1Active := 0
	v2Active := 0
	for _, tsk := range tsks {
		if tsk.State != string(models.TaskStateStopped) {
			if tsk.DeploymentID == resV1.DeploymentID {
				v1Active++
			} else if tsk.DeploymentID == resV2.DeploymentID {
				v2Active++
			}
		}
	}

	if v1Active != 0 || v2Active != 2 {
		t.Fatalf("recreate strategy expected 0 active v1 and 2 active v2, got v1=%d, v2=%d", v1Active, v2Active)
	}
	_ = resV2
}
