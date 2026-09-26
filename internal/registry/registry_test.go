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

func TestInMemoryRegistry_DirectRegisterLookup(t *testing.T) {
	reg := registry.NewInMemoryRegistry(logging.NewDefaultLogger())

	svcID := id.NewServiceID()
	taskID1 := id.NewTaskID()
	taskID2 := id.NewTaskID()
	workerID1 := id.NewWorkerID()
	workerID2 := id.NewWorkerID()

	ep1 := &registry.Endpoint{
		ServiceID:   svcID,
		ServiceName: "api",
		TaskID:      taskID1,
		WorkerID:    workerID1,
		Host:        "10.0.0.5",
		Port:        8000,
		Address:     "10.0.0.5:8000",
		Protocol:    "tcp",
		Healthy:     true,
	}

	ep2 := &registry.Endpoint{
		ServiceID:   svcID,
		ServiceName: "api",
		TaskID:      taskID2,
		WorkerID:    workerID2,
		Host:        "10.0.0.6",
		Port:        8000,
		Address:     "10.0.0.6:8000",
		Protocol:    "tcp",
		Healthy:     true,
	}

	// 1. Register ep1 and ep2
	if err := reg.Register(ep1); err != nil {
		t.Fatalf("failed to register ep1: %v", err)
	}
	if err := reg.Register(ep2); err != nil {
		t.Fatalf("failed to register ep2: %v", err)
	}

	// 2. Lookup by name (case-insensitive)
	eps := reg.Lookup("api")
	if len(eps) != 2 {
		t.Fatalf("expected 2 endpoints for 'api', got %d", len(eps))
	}

	epsUpper := reg.Lookup("API")
	if len(epsUpper) != 2 {
		t.Fatalf("expected 2 endpoints for 'API' (case-insensitive), got %d", len(epsUpper))
	}

	// 3. Lookup by ID
	epsByID := reg.LookupByID(svcID)
	if len(epsByID) != 2 {
		t.Fatalf("expected 2 endpoints by service ID, got %d", len(epsByID))
	}

	// 4. Deregister task 1 (task stops)
	if err := reg.Deregister(taskID1); err != nil {
		t.Fatalf("failed to deregister ep1: %v", err)
	}

	epsAfter := reg.Lookup("api")
	if len(epsAfter) != 1 {
		t.Fatalf("expected 1 endpoint after deregistering task1, got %d", len(epsAfter))
	}
	if epsAfter[0].TaskID != taskID2 {
		t.Fatalf("expected remaining endpoint to be taskID2, got %s", epsAfter[0].TaskID)
	}

	// 5. Register with Healthy=false (deregisters)
	ep2Unhealthy := *ep2
	ep2Unhealthy.Healthy = false
	if err := reg.Register(&ep2Unhealthy); err != nil {
		t.Fatalf("registering unhealthy endpoint failed: %v", err)
	}

	epsEmpty := reg.Lookup("api")
	if len(epsEmpty) != 0 {
		t.Fatalf("expected 0 endpoints after marking ep2 unhealthy, got %d", len(epsEmpty))
	}
}

func TestInMemoryRegistry_RefreshFromStateStore(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()

	// 1. Create Workers (w1 is READY, w2 is LOST)
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "node-1",
		Address:   "10.0.0.5",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})

	// 1. Create Workers (w1 is READY, w2 is LOST)
	w1 := &models.Worker{
		ID:        id.NewWorkerID(),
		NodeID:    nodeID,
		Address:   "10.0.0.5:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}
	w2 := &models.Worker{
		ID:        id.NewWorkerID(),
		NodeID:    nodeID,
		Address:   "10.0.0.6:7001",
		Status:    "LOST", // Not usable!
		Heartbeat: now.Add(-5 * time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Workers().Create(ctx, w1); err != nil {
		t.Fatalf("failed to create w1: %v", err)
	}
	if err := store.Workers().Create(ctx, w2); err != nil {
		t.Fatalf("failed to create w2: %v", err)
	}

	// 2. Create Service with port 8000
	svcID := id.NewServiceID()
	specJSON := `{"name":"api","command":"server","ports":[{"host_port":8000,"service_port":8000,"protocol":"tcp"}]}`
	svc := &models.Service{
		ID:        svcID,
		Name:      "api",
		Replicas:  2,
		Runtime:   "native",
		Command:   "server",
		Status:    "RUNNING",
		SpecJSON:  specJSON,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.Services().Create(ctx, svc)

	// 3. Create Tasks:
	// t1 on w1 (READY) -> RUNNING (Usable!)
	// t2 on w1 (READY) -> STOPPED (Not usable!)
	// t3 on w2 (LOST)  -> RUNNING (Worker is LOST, Not usable!)
	t1 := &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: svcID,
		WorkerID:  w1.ID,
		State:     string(models.TaskStateRunning),
		PID:       1234,
		CreatedAt: now,
		UpdatedAt: now,
	}
	t2 := &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: svcID,
		WorkerID:  w1.ID,
		State:     string(models.TaskStateStopped),
		PID:       1235,
		CreatedAt: now,
		UpdatedAt: now,
	}
	t3 := &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: svcID,
		WorkerID:  w2.ID,
		State:     string(models.TaskStateRunning),
		PID:       1236,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.Tasks().Create(ctx, t1)
	_ = store.Tasks().Create(ctx, t2)
	_ = store.Tasks().Create(ctx, t3)

	reg := registry.NewInMemoryRegistry(logging.NewDefaultLogger())

	// 4. Run Refresh
	if err := reg.Refresh(ctx, store); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}

	// 5. Lookup should ONLY return t1 (10.0.0.5:8000)
	eps := reg.Lookup("api")
	if len(eps) != 1 {
		t.Fatalf("expected exactly 1 healthy endpoint for 'api', got %d", len(eps))
	}

	ep := eps[0]
	if ep.TaskID != t1.ID {
		t.Fatalf("expected task ID %s, got %s", t1.ID, ep.TaskID)
	}
	if ep.Host != "10.0.0.5" {
		t.Fatalf("expected host 10.0.0.5, got %s", ep.Host)
	}
	if ep.Port != 8000 {
		t.Fatalf("expected port 8000, got %d", ep.Port)
	}
	if ep.Address != "10.0.0.5:8000" {
		t.Fatalf("expected address 10.0.0.5:8000, got %s", ep.Address)
	}
	if !ep.Healthy {
		t.Fatalf("expected endpoint to be healthy")
	}

	// 6. Recover worker w2 to READY -> t3 becomes usable upon refresh
	w2.Status = "READY"
	_ = store.Workers().Update(ctx, w2)
	_ = reg.Refresh(ctx, store)

	epsAfterRecovery := reg.Lookup("api")
	if len(epsAfterRecovery) != 2 {
		t.Fatalf("expected 2 endpoints after w2 recovery, got %d", len(epsAfterRecovery))
	}

	// 7. Stop t1 -> only t3 remains
	t1.State = string(models.TaskStateStopped)
	_ = store.Tasks().Update(ctx, t1)
	_ = reg.Refresh(ctx, store)

	epsAfterStop := reg.Lookup("api")
	if len(epsAfterStop) != 1 {
		t.Fatalf("expected 1 endpoint after stopping t1, got %d", len(epsAfterStop))
	}
	if epsAfterStop[0].TaskID != t3.ID {
		t.Fatalf("expected remaining endpoint to be t3 (%s), got %s", t3.ID, epsAfterStop[0].TaskID)
	}
}

func TestInMemoryRegistry_ListAllAndClear(t *testing.T) {
	reg := registry.NewInMemoryRegistry(logging.NewDefaultLogger())

	svc1ID := id.NewServiceID()
	svc2ID := id.NewServiceID()

	_ = reg.Register(&registry.Endpoint{
		ServiceID:   svc1ID,
		ServiceName: "frontend",
		TaskID:      id.NewTaskID(),
		WorkerID:    id.NewWorkerID(),
		Host:        "127.0.0.1",
		Port:        3000,
		Address:     "127.0.0.1:3000",
		Protocol:    "tcp",
		Healthy:     true,
	})

	_ = reg.Register(&registry.Endpoint{
		ServiceID:   svc2ID,
		ServiceName: "backend",
		TaskID:      id.NewTaskID(),
		WorkerID:    id.NewWorkerID(),
		Host:        "127.0.0.1",
		Port:        8080,
		Address:     "127.0.0.1:8080",
		Protocol:    "tcp",
		Healthy:     true,
	})

	all := reg.ListAll()
	if len(all) != 2 {
		t.Fatalf("expected 2 services in ListAll, got %d", len(all))
	}
	if len(all["frontend"]) != 1 {
		t.Fatalf("expected 1 endpoint for frontend, got %d", len(all["frontend"]))
	}
	if len(all["backend"]) != 1 {
		t.Fatalf("expected 1 endpoint for backend, got %d", len(all["backend"]))
	}

	reg.Clear()
	allCleared := reg.ListAll()
	if len(allCleared) != 0 {
		t.Fatalf("expected 0 services after Clear, got %d", len(allCleared))
	}
}
