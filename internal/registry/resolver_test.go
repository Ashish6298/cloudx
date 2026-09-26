package registry_test

import (
	"context"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/registry"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

func TestStoreResolver_ResolveService(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()

	// 1. Create Node & Worker (READY)
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "compute-1",
		Address:   "10.0.0.12",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})

	workerID := id.NewWorkerID()
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   "10.0.0.12:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	// 2. Create Service "api" with port 8000
	svcID := id.NewServiceID()
	specJSON := `{"name":"api","command":"server","ports":[{"host_port":8000,"service_port":8000,"protocol":"tcp"}]}`
	_ = store.Services().Create(ctx, &models.Service{
		ID:        svcID,
		Name:      "api",
		Replicas:  2,
		Runtime:   "native",
		Command:   "server",
		Status:    "RUNNING",
		SpecJSON:  specJSON,
		CreatedAt: now,
		UpdatedAt: now,
	})

	// 3. Create 2 tasks for "api"
	t1 := &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: svcID,
		WorkerID:  workerID,
		State:     string(models.TaskStateRunning),
		PID:       101,
		CreatedAt: now,
		UpdatedAt: now,
	}
	t2 := &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: svcID,
		WorkerID:  workerID,
		State:     string(models.TaskStateHealthy),
		PID:       102,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.Tasks().Create(ctx, t1)
	_ = store.Tasks().Create(ctx, t2)

	inMemReg := registry.NewInMemoryRegistry(logging.NewDefaultLogger())
	resolver := registry.NewStoreResolver(inMemReg, store)

	// 4. ResolveService("api")
	eps, err := resolver.ResolveService(ctx, "api")
	if err != nil {
		t.Fatalf("ResolveService('api') failed: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("expected 2 healthy endpoints, got %d", len(eps))
	}
	for _, ep := range eps {
		if ep.Address != "10.0.0.12:8000" {
			t.Errorf("expected address 10.0.0.12:8000, got %s", ep.Address)
		}
		if ep.ServiceName != "api" {
			t.Errorf("expected service name 'api', got %s", ep.ServiceName)
		}
	}

	// 5. Case-insensitivity: ResolveService("API")
	epsUpper, err := resolver.ResolveService(ctx, "API")
	if err != nil {
		t.Fatalf("ResolveService('API') failed: %v", err)
	}
	if len(epsUpper) != 2 {
		t.Fatalf("expected 2 endpoints for 'API', got %d", len(epsUpper))
	}

	// 6. ResolveService by ServiceID
	epsByID, err := resolver.ResolveService(ctx, svcID.String())
	if err != nil {
		t.Fatalf("ResolveService(svcID) failed: %v", err)
	}
	if len(epsByID) != 2 {
		t.Fatalf("expected 2 endpoints for svcID, got %d", len(epsByID))
	}

	// 7. ResolveOne("api")
	one, err := resolver.ResolveOne(ctx, "api")
	if err != nil {
		t.Fatalf("ResolveOne('api') failed: %v", err)
	}
	if one == nil || one.Address != "10.0.0.12:8000" {
		t.Fatalf("expected valid endpoint from ResolveOne, got %v", one)
	}

	// 8. Resolve unknown service -> returns error
	_, err = resolver.ResolveService(ctx, "unknown-service")
	if err == nil {
		t.Fatalf("expected error resolving unknown service, got nil")
	}

	// 9. Resolve existing service with 0 running tasks -> returns empty list without error
	svc2ID := id.NewServiceID()
	_ = store.Services().Create(ctx, &models.Service{
		ID:        svc2ID,
		Name:      "database",
		Replicas:  1,
		Runtime:   "native",
		Command:   "db",
		Status:    "STOPPED",
		CreatedAt: now,
		UpdatedAt: now,
	})

	dbEps, err := resolver.ResolveService(ctx, "database")
	if err != nil {
		t.Fatalf("expected nil error for existing service with 0 endpoints, got %v", err)
	}
	if len(dbEps) != 0 {
		t.Fatalf("expected 0 endpoints for stopped database service, got %d", len(dbEps))
	}

	// ResolveOne on stopped service should return error
	_, err = resolver.ResolveOne(ctx, "database")
	if err == nil {
		t.Fatalf("expected error from ResolveOne on stopped database service, got nil")
	}
}
