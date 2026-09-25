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

// TestRollback_V2ToV1_ClusterConvergence tests the primary Phase 35 acceptance scenario:
// 1. Service deployed as v1 (3 replicas).
// 2. Upgraded to v2 (3 replicas).
// 3. Rollback requested: v2 -> rollback -> v1.
// 4. Desired state points to known previous deployment without creating fake reverse deployments.
// 5. Cluster converges back to v1.
// 6. Rollback audit event is recorded.
func TestRollback_V2ToV1_ClusterConvergence(t *testing.T) {
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

	// 2. Deploy v1 with 3 replicas
	replicas := 3
	svcConfigV1 := &spec.ServiceConfig{
		Name:     "api",
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

	// 3. Deploy v2 with 3 replicas
	svcConfigV2 := &spec.ServiceConfig{
		Name:     "api",
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

	// Run reconciliation to complete v2 convergence
	for i := 0; i < 3; i++ {
		_, _ = cp.Reconciler.ReconcileAll(ctx)
	}

	// Verify v2 is active and all active tasks belong to v2
	activeTasksV2, _ := store.Tasks().ListByService(ctx, resV1.ServiceID)
	v2ActiveCount := 0
	for _, tsk := range activeTasksV2 {
		if tsk.State != string(models.TaskStateStopped) && tsk.DeploymentID == resV2.DeploymentID {
			v2ActiveCount++
		}
	}
	if v2ActiveCount != 3 {
		t.Fatalf("expected 3 active tasks for v2 before rollback, got %d", v2ActiveCount)
	}

	// Check total deployment records before rollback = 2 (v1, v2)
	depsBeforeRollback, _ := store.Deployments().ListByService(ctx, resV1.ServiceID)
	if len(depsBeforeRollback) != 2 {
		t.Fatalf("expected 2 deployments before rollback, got %d", len(depsBeforeRollback))
	}

	// 4. Perform Rollback: cloudx rollback api
	rollbackRes, err := cp.RollbackService(ctx, "api", "", dispatcher)
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	if rollbackRes.TargetDeploymentID != resV1.DeploymentID {
		t.Fatalf("expected rollback target deployment to be v1 (%s), got %s", resV1.DeploymentID, rollbackRes.TargetDeploymentID)
	}
	if rollbackRes.TargetVersion != "v1" {
		t.Fatalf("expected rollback target version to be v1, got %s", rollbackRes.TargetVersion)
	}
	if rollbackRes.PreviousDeploymentID != resV2.DeploymentID {
		t.Fatalf("expected previous deployment to be v2 (%s), got %s", resV2.DeploymentID, rollbackRes.PreviousDeploymentID)
	}

	// 5. Verify NO fake reverse deployment was created in state
	depsAfterRollback, _ := store.Deployments().ListByService(ctx, resV1.ServiceID)
	if len(depsAfterRollback) != 2 {
		t.Fatalf("expected total deployments to remain 2 (no fake reverse deployment created), got %d", len(depsAfterRollback))
	}

	// Verify status of immutable deployments
	v1Dep, _ := store.Deployments().Get(ctx, resV1.DeploymentID)
	v2Dep, _ := store.Deployments().Get(ctx, resV2.DeploymentID)
	if v1Dep.Status != string(models.DeploymentStatusActive) {
		t.Errorf("expected v1 deployment status to be ACTIVE, got %s", v1Dep.Status)
	}
	if v2Dep.Status != string(models.DeploymentStatusRolledBack) && v2Dep.Status != string(models.DeploymentStatusSuperceded) {
		t.Errorf("expected v2 deployment status to be ROLLED_BACK/SUPERCEDED, got %s", v2Dep.Status)
	}

	// 6. Complete convergence back to v1
	for i := 0; i < 3; i++ {
		_, _ = cp.Reconciler.ReconcileAll(ctx)
	}

	// Verify that cluster converged back to 3 active v1 tasks and 0 active v2 tasks
	allTasksAfterRollback, _ := store.Tasks().ListByService(ctx, resV1.ServiceID)
	v1Live := 0
	v2Live := 0
	for _, tsk := range allTasksAfterRollback {
		if tsk.State != string(models.TaskStateStopped) {
			if tsk.DeploymentID == resV1.DeploymentID {
				v1Live++
			} else if tsk.DeploymentID == resV2.DeploymentID {
				v2Live++
			}
		}
	}

	if v1Live != 3 || v2Live != 0 {
		t.Fatalf("cluster failed to converge back to v1: expected (v1=3, v2=0), got (v1=%d, v2=%d)", v1Live, v2Live)
	}

	// 7. Verify Rollback Event was recorded in audit log
	events, err := store.Events().List(ctx, 50)
	if err != nil {
		t.Fatalf("failed to list events: %v", err)
	}

	var rollbackEventFound bool
	for _, ev := range events {
		if ev.Type == "SERVICE_ROLLED_BACK" && ev.EntityID == resV1.ServiceID {
			rollbackEventFound = true
			break
		}
	}

	if !rollbackEventFound {
		t.Fatalf("expected SERVICE_ROLLED_BACK event in event store")
	}
}

// TestRollback_SpecificVersionTargeting verifies rolling back to an explicit historical version (e.g. v1 in a v1 -> v2 -> v3 chain)
func TestRollback_SpecificVersionTargeting(t *testing.T) {
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
		return tm.AssignTask(ctx, worker.TaskAssignment{TaskID: id.ID(req.Task.Id), Command: req.Command})
	})

	cfg := config.NewDefaultConfig()
	cp, err := New(Options{Config: cfg, Store: store, Logger: logger})
	if err != nil {
		t.Fatalf("controlplane init failed: %v", err)
	}

	replicas := 2
	// Deploy v1
	resV1, err := cp.DeployService(ctx, &spec.ServiceConfig{Name: "auth", Version: "v1", Command: "go", Replicas: &replicas, Runtime: "native"}, dispatcher)
	if err != nil {
		t.Fatalf("deploy v1 failed: %v", err)
	}
	// Deploy v2
	resV2, err := cp.DeployService(ctx, &spec.ServiceConfig{Name: "auth", Version: "v2", Command: "go", Replicas: &replicas, Runtime: "native"}, dispatcher)
	if err != nil {
		t.Fatalf("deploy v2 failed: %v", err)
	}
	// Deploy v3
	resV3, err := cp.DeployService(ctx, &spec.ServiceConfig{Name: "auth", Version: "v3", Command: "go", Replicas: &replicas, Runtime: "native"}, dispatcher)
	if err != nil {
		t.Fatalf("deploy v3 failed: %v", err)
	}

	_ = resV2
	_ = resV3

	// Explicit Rollback to v1 (skipping v2)
	resRollback, err := cp.RollbackService(ctx, "auth", "v1", dispatcher)
	if err != nil {
		t.Fatalf("explicit rollback to v1 failed: %v", err)
	}

	if resRollback.TargetDeploymentID != resV1.DeploymentID || resRollback.TargetVersion != "v1" {
		t.Fatalf("expected explicit rollback to target v1 (%s), got version=%s, id=%s", resV1.DeploymentID, resRollback.TargetVersion, resRollback.TargetDeploymentID)
	}

	// Verify total deployments remain 3
	deps, _ := store.Deployments().ListByService(ctx, resV1.ServiceID)
	if len(deps) != 3 {
		t.Fatalf("expected 3 deployments in history, got %d", len(deps))
	}
}
