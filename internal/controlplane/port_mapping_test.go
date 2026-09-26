package controlplane_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

func TestPortMapping_ConflictRejection_And_Feasibility(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cloudx_port_test.db")
	ctx := context.Background()

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()

	cfg := config.NewDefaultConfig()
	cfg.Storage.Path = dir

	cp, err := controlplane.New(controlplane.Options{
		Config: cfg,
		Store:  store,
		Logger: logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("controlplane.New: %v", err)
	}

	now := time.Now().UTC()
	nodeID := id.NewNodeID()
	w1ID := id.NewWorkerID()
	w2ID := id.NewWorkerID()

	if err := store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "test-node",
		Address:   "127.0.0.1",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create test node: %v", err)
	}

	if err := store.Workers().Create(ctx, &models.Worker{
		ID:        w1ID,
		NodeID:    nodeID,
		Address:   "worker-1:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create worker-1: %v", err)
	}
	if err := store.Workers().Create(ctx, &models.Worker{
		ID:        w2ID,
		NodeID:    nodeID,
		Address:   "worker-2:7002",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create worker-2: %v", err)
	}

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(w1ID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return nil
	})
	dispatcher.RegisterWorkerHandler(w2ID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return nil
	})

	// Service 1 exposes port 8080:8000
	rep1 := 1
	s1Config := &spec.ServiceConfig{
		Name:     "web-service-1",
		Command:  "echo web1",
		Replicas: &rep1,
		Ports: []spec.PortSpec{
			{HostPort: 8080, ServicePort: 8000, Protocol: "tcp"},
		},
	}
	dep1, err := cp.DeployService(ctx, s1Config, dispatcher)
	if err != nil {
		t.Fatalf("failed to deploy web-service-1: %v", err)
	}

	tasks1, err := store.Tasks().ListByService(ctx, dep1.ServiceID)
	if err != nil || len(tasks1) == 0 {
		t.Fatalf("expected tasks for web-service-1, got %d (err: %v)", len(tasks1), err)
	}
	assignedWorkerS1 := tasks1[0].WorkerID
	t.Logf("web-service-1 assigned to worker %s (port 8080)", assignedWorkerS1)

	// Service 2 also wants port 8080:8000 (must be scheduled onto the other worker to prevent port conflict)
	rep2 := 1
	s2Config := &spec.ServiceConfig{
		Name:     "web-service-2",
		Command:  "echo web2",
		Replicas: &rep2,
		Ports: []spec.PortSpec{
			{HostPort: 8080, ServicePort: 8000, Protocol: "tcp"},
		},
	}
	dep2, err := cp.DeployService(ctx, s2Config, dispatcher)
	if err != nil {
		t.Fatalf("failed to deploy web-service-2: %v", err)
	}

	tasks2, err := store.Tasks().ListByService(ctx, dep2.ServiceID)
	if err != nil || len(tasks2) == 0 {
		t.Fatalf("expected tasks for web-service-2, got %d (err: %v)", len(tasks2), err)
	}
	assignedWorkerS2 := tasks2[0].WorkerID
	t.Logf("web-service-2 assigned to worker %s (port 8080)", assignedWorkerS2)

	if assignedWorkerS1 == assignedWorkerS2 {
		t.Fatalf("port conflict violated: both web-service-1 and web-service-2 were assigned to the same worker %s with conflicting port 8080", assignedWorkerS1)
	}

	// Service 3 also wants port 8080:8000 (both workers are now occupied with 8080, so scheduling must fail)
	rep3 := 1
	s3Config := &spec.ServiceConfig{
		Name:     "web-service-3",
		Command:  "echo web3",
		Replicas: &rep3,
		Ports: []spec.PortSpec{
			{HostPort: 8080, ServicePort: 8000, Protocol: "tcp"},
		},
	}
	dep3, err := cp.DeployService(ctx, s3Config, dispatcher)
	if err != nil {
		t.Fatalf("unexpected deploy service error: %v", err)
	}
	tasks3, _ := store.Tasks().ListByService(ctx, dep3.ServiceID)
	if len(tasks3) > 0 {
		t.Fatalf("expected web-service-3 to fail scheduling due to 8080 port exhaustion on all workers, but got %d tasks", len(tasks3))
	}
}
