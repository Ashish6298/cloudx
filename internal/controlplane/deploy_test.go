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

func setupTestControlPlane(t *testing.T) (*ControlPlane, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.NewDefaultConfig()
	logger := logging.NewDefaultLogger()

	cp, err := New(Options{
		Config: cfg,
		Store:  store,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to create control plane: %v", err)
	}

	return cp, store
}

func TestControlPlane_DeployService_Success(t *testing.T) {
	ctx := context.Background()
	cp, store := setupTestControlPlane(t)

	// 1. Register Ready Node and Worker
	nodeID := id.NewNodeID()
	workerID := id.NewWorkerID()
	now := time.Now().UTC()

	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "node-1",
		Address:   "127.0.0.1:9001",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   "127.0.0.1:9001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	// 2. Setup Worker TaskManager & Dispatcher
	rt := runtime.NewNativeRuntime()
	tm := worker.NewTaskManager(worker.TaskManagerOptions{
		WorkerID: workerID,
		Runtime:  rt,
		Logger:   logging.NewDefaultLogger(),
	})
	defer tm.Close()

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tm.AssignTask(ctx, worker.TaskAssignment{
			TaskID:      id.ID(req.Task.Id),
			ServiceID:   id.ID(req.Task.ServiceId),
			Command:     req.Command,
			Args:        req.Args,
			Environment: req.Environment,
		})
	})

	// 3. Deploy Service with 2 replicas
	replicas := 2
	svcConfig := &spec.ServiceConfig{
		Name:     "web-api",
		Command:  "go",
		Args:     []string{"version"},
		Replicas: &replicas,
		Runtime:  "native",
		Resources: spec.ResourceConfig{
			CPU:    "500m",
			Memory: "256MiB",
		},
		RestartPolicy: &spec.RestartPolicySpec{
			Type: "always",
		},
	}

	res, err := cp.DeployService(ctx, svcConfig, dispatcher)
	if err != nil {
		t.Fatalf("expected successful deployment, got: %v", err)
	}

	if res.ServiceName != "web-api" {
		t.Errorf("expected service name 'web-api', got '%s'", res.ServiceName)
	}
	if res.Replicas != 2 {
		t.Errorf("expected 2 replicas, got %d", res.Replicas)
	}
	if res.Status != "RUNNING" {
		t.Errorf("expected status 'RUNNING', got '%s'", res.Status)
	}
	if len(res.Tasks) != 2 {
		t.Fatalf("expected 2 assigned tasks, got %d", len(res.Tasks))
	}

	// 4. Test InspectService
	inspectRes, err := cp.InspectService(ctx, "web-api")
	if err != nil {
		t.Fatalf("failed to inspect service: %v", err)
	}

	if inspectRes.Service.Name != "web-api" {
		t.Errorf("expected inspected service name 'web-api', got '%s'", inspectRes.Service.Name)
	}
	if len(inspectRes.Deployments) != 1 {
		t.Errorf("expected 1 deployment record, got %d", len(inspectRes.Deployments))
	}
	if len(inspectRes.Tasks) != 2 {
		t.Errorf("expected 2 tasks in inspect, got %d", len(inspectRes.Tasks))
	}
}

func TestControlPlane_DeployService_ValidationFailure(t *testing.T) {
	ctx := context.Background()
	cp, _ := setupTestControlPlane(t)

	// Missing command
	svcConfig := &spec.ServiceConfig{
		Name:    "invalid-svc",
		Command: "",
	}

	_, err := cp.DeployService(ctx, svcConfig, nil)
	if err == nil {
		t.Fatalf("expected validation error for empty command, got nil")
	}
}

func TestControlPlane_DeployVersion_TransitionWorkload(t *testing.T) {
	ctx := context.Background()
	cp, store := setupTestControlPlane(t)

	// 1. Register Ready Node and Worker
	nodeID := id.NewNodeID()
	workerID := id.NewWorkerID()
	now := time.Now().UTC()

	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "node-1",
		Address:   "127.0.0.1:9001",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   "127.0.0.1:9001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	rt := runtime.NewNativeRuntime()
	tm := worker.NewTaskManager(worker.TaskManagerOptions{
		WorkerID: workerID,
		Runtime:  rt,
		Logger:   logging.NewDefaultLogger(),
	})
	defer tm.Close()

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tm.AssignTask(ctx, worker.TaskAssignment{
			TaskID:      id.ID(req.Task.Id),
			ServiceID:   id.ID(req.Task.ServiceId),
			Command:     req.Command,
			Args:        req.Args,
			Environment: req.Environment,
		})
	})

	// 2. Initial deployment: v1 (replicas = 2)
	replicas := 2
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
		t.Fatalf("v1 deployment failed: %v", err)
	}

	if resV1.DeploymentID == "" {
		t.Fatalf("expected non-empty deployment ID for v1")
	}

	// Verify 2 tasks running for v1
	tasksV1, _ := store.Tasks().ListByService(ctx, resV1.ServiceID)
	var activeV1 []*models.Task
	for _, tsk := range tasksV1 {
		if tsk.DeploymentID == resV1.DeploymentID && tsk.State != string(models.TaskStateStopped) {
			activeV1 = append(activeV1, tsk)
		}
	}
	if len(activeV1) != 2 {
		t.Fatalf("expected 2 active tasks on v1, got %d", len(activeV1))
	}

	// 3. Deploy v2: api:v2
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
		t.Fatalf("v2 deployment failed: %v", err)
	}

	if resV2.DeploymentID == resV1.DeploymentID {
		t.Fatalf("expected different deployment IDs for v1 and v2")
	}

	// 4. Verify Multiple Versions Exist Simultaneously in State
	allDeployments, err := store.Deployments().ListByService(ctx, resV1.ServiceID)
	if err != nil || len(allDeployments) != 2 {
		t.Fatalf("expected 2 deployments in state store, got %d (err: %v)", len(allDeployments), err)
	}

	v1Dep, _ := store.Deployments().Get(ctx, resV1.DeploymentID)
	v2Dep, _ := store.Deployments().Get(ctx, resV2.DeploymentID)

	if v1Dep.Status != string(models.DeploymentStatusSuperceded) {
		t.Errorf("expected v1 deployment status SUPERCEDED, got %s", v1Dep.Status)
	}
	if v2Dep == nil || v2Dep.ID != resV2.DeploymentID {
		t.Errorf("expected valid v2 deployment record")
	}
	// 5. Verify Workload Transition:
	// Since update strategy is rolling (default maxUnavailable = 1),
	// after 1st deploy pass, 1 v2 task is created and 1 v1 task is stopped.
	// Running a second reconciliation pass completes the rolling replacement to 2 v2 tasks and 0 v1 active tasks.
	tasksAfterPass1, _ := store.Tasks().ListByService(ctx, resV1.ServiceID)
	var activeV2Pass1 int
	var activeV1Pass1 int
	for _, tsk := range tasksAfterPass1 {
		if tsk.State != string(models.TaskStateStopped) {
			if tsk.DeploymentID == resV2.DeploymentID {
				activeV2Pass1++
			} else if tsk.DeploymentID == resV1.DeploymentID {
				activeV1Pass1++
			}
		}
	}
	if activeV2Pass1 != 1 || activeV1Pass1 != 1 {
		t.Fatalf("expected progressive rolling state (1 v2, 1 v1), got v2=%d, v1=%d", activeV2Pass1, activeV1Pass1)
	}

	// 2nd reconciliation pass progresses rolling deployment to completion
	_, err = cp.Reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("second reconciliation pass failed: %v", err)
	}

	allTasksAfterV2, _ := store.Tasks().ListByService(ctx, resV1.ServiceID)
	var activeTasks []*models.Task
	var stoppedTasks []*models.Task
	for _, tsk := range allTasksAfterV2 {
		if tsk.State == string(models.TaskStateStopped) {
			stoppedTasks = append(stoppedTasks, tsk)
		} else {
			activeTasks = append(activeTasks, tsk)
		}
	}

	if len(activeTasks) != 2 {
		t.Errorf("expected 2 active tasks after rolling v2 completion, got %d", len(activeTasks))
	}
	for _, tsk := range activeTasks {
		if tsk.DeploymentID != resV2.DeploymentID {
			t.Errorf("expected active task to belong to v2 deployment (%s), got %s", resV2.DeploymentID, tsk.DeploymentID)
		}
	}
	if len(stoppedTasks) < 2 {
		t.Errorf("expected at least 2 stopped tasks from previous v1 deployment, got %d", len(stoppedTasks))
	}

	// 6. Test DeployVersion to switch back / deploy specific version
	resSwitch, err := cp.DeployVersion(ctx, "api", "v1", dispatcher)
	if err != nil {
		t.Fatalf("DeployVersion back to v1 failed: %v", err)
	}

	if resSwitch.DeploymentID != resV1.DeploymentID {
		t.Errorf("expected switched deployment ID to match v1 (%s), got %s", resV1.DeploymentID, resSwitch.DeploymentID)
	}
}
