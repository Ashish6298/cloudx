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
