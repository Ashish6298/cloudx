package controlplane_test

import (
	"context"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

func TestControlPlane_ServiceRegistryIntegration(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.Close()

	cfg := config.NewDefaultConfig()
	logger := logging.NewDefaultLogger()

	cp, err := controlplane.New(controlplane.Options{
		Config: cfg,
		Store:  store,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to initialize control plane: %v", err)
	}

	now := time.Now().UTC()
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "node-worker-1",
		Address:   "10.0.0.10",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})

	w1 := &models.Worker{
		ID:        id.NewWorkerID(),
		NodeID:    nodeID,
		Address:   "10.0.0.10:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.Workers().Create(ctx, w1)

	// Create web service
	svcID := id.NewServiceID()
	specJSON := `{"name":"web","command":"./web-server","ports":[{"host_port":8080,"service_port":8080,"protocol":"tcp"}]}`
	svc := &models.Service{
		ID:        svcID,
		Name:      "web",
		Replicas:  1,
		Runtime:   "native",
		Command:   "./web-server",
		Status:    "RUNNING",
		SpecJSON:  specJSON,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.Services().Create(ctx, svc)

	// Create Task in RUNNING state
	task1 := &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: svcID,
		WorkerID:  w1.ID,
		State:     string(models.TaskStateRunning),
		PID:       4410,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.Tasks().Create(ctx, task1)

	// 1. GetServiceEndpoints for "web"
	eps, err := cp.GetServiceEndpoints(ctx, "web")
	if err != nil {
		t.Fatalf("failed to get service endpoints: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint for 'web', got %d", len(eps))
	}
	if eps[0].Address != "10.0.0.10:8080" {
		t.Fatalf("expected address 10.0.0.10:8080, got %s", eps[0].Address)
	}

	// 2. Dynamic event: Task Stops
	task1.State = string(models.TaskStateStopped)
	_ = store.Tasks().Update(ctx, task1)
	if err := cp.RegistryManager.OnTaskStateChange(ctx, task1); err != nil {
		t.Fatalf("OnTaskStateChange failed: %v", err)
	}

	epsAfterStop, _ := cp.GetServiceEndpoints(ctx, "web")
	if len(epsAfterStop) != 0 {
		t.Fatalf("expected 0 endpoints after task stopped, got %d", len(epsAfterStop))
	}

	// 3. Dynamic event: Task restarts and becomes HEALTHY
	task1.State = string(models.TaskStateHealthy)
	_ = store.Tasks().Update(ctx, task1)
	if err := cp.RegistryManager.OnTaskStateChange(ctx, task1); err != nil {
		t.Fatalf("OnTaskStateChange healthy failed: %v", err)
	}

	epsAfterHealthy, _ := cp.GetServiceEndpoints(ctx, "web")
	if len(epsAfterHealthy) != 1 {
		t.Fatalf("expected 1 endpoint after task became healthy, got %d", len(epsAfterHealthy))
	}

	// 4. Dynamic event: Worker disappears / becomes LOST
	w1.Status = "LOST"
	_ = store.Workers().Update(ctx, w1)
	if err := cp.RegistryManager.OnWorkerStatusChange(ctx, w1.ID, "LOST"); err != nil {
		t.Fatalf("OnWorkerStatusChange failed: %v", err)
	}

	epsAfterWorkerLost, _ := cp.GetServiceEndpoints(ctx, "web")
	if len(epsAfterWorkerLost) != 0 {
		t.Fatalf("expected 0 endpoints after worker lost, got %d", len(epsAfterWorkerLost))
	}
}
