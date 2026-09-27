package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

func TestComprehensiveRepositoryMethods(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// 1. Worker Repository (Create, Get, List, Update, Delete)
	nodeID := id.NewNodeID()
	node := &models.Node{
		ID:        nodeID,
		Name:      "test-node-1",
		Address:   "127.0.0.1:7000",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Nodes().Create(ctx, node); err != nil {
		t.Fatalf("failed to create node: %v", err)
	}

	wrk1 := &models.Worker{
		ID:        id.NewWorkerID(),
		NodeID:    nodeID,
		Address:   "127.0.0.1:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}
	wrk2 := &models.Worker{
		ID:        id.NewWorkerID(),
		NodeID:    nodeID,
		Address:   "127.0.0.1:7002",
		Status:    "DRAINING",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Workers().Create(ctx, wrk1); err != nil {
		t.Fatalf("failed to create wrk1: %v", err)
	}
	if err := store.Workers().Create(ctx, wrk2); err != nil {
		t.Fatalf("failed to create wrk2: %v", err)
	}

	wrks, err := store.Workers().List(ctx)
	if err != nil || len(wrks) != 2 {
		t.Fatalf("expected 2 workers, got %d (err: %v)", len(wrks), err)
	}

	wrk1.Status = "BUSY"
	if err := store.Workers().Update(ctx, wrk1); err != nil {
		t.Fatalf("failed to update worker: %v", err)
	}

	if err := store.Workers().Delete(ctx, wrk2.ID); err != nil {
		t.Fatalf("failed to delete worker: %v", err)
	}
	if _, err := store.Workers().Get(ctx, wrk2.ID); err == nil {
		t.Errorf("expected deleted worker to return not found")
	}

	// 2. Service Repository (Create, Get, GetByName, List, Update, Delete)
	srv := &models.Service{
		ID:        id.NewServiceID(),
		Name:      "web-frontend",
		Replicas:  3,
		Runtime:   "native",
		Command:   "node server.js",
		Status:    "RUNNING",
		SpecJSON:  `{"name":"web-frontend"}`,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Services().Create(ctx, srv); err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	gotSrvByName, err := store.Services().GetByName(ctx, "web-frontend")
	if err != nil || gotSrvByName.ID != srv.ID {
		t.Fatalf("expected to find service by name: %v", err)
	}

	srv.Replicas = 5
	if err := store.Services().Update(ctx, srv); err != nil {
		t.Fatalf("failed to update service: %v", err)
	}

	services, err := store.Services().List(ctx)
	if err != nil || len(services) != 1 || services[0].Replicas != 5 {
		t.Fatalf("expected 1 service with 5 replicas: %v", err)
	}

	if err := store.Services().Delete(ctx, srv.ID); err != nil {
		t.Fatalf("failed to delete service: %v", err)
	}
	if _, err := store.Services().Get(ctx, srv.ID); err == nil {
		t.Errorf("expected deleted service to return not found")
	}

	// 3. Deployment Repository (Create, Get, ListByService, Update, Delete)
	depID := id.NewDeploymentID()
	srvForDep := &models.Service{
		ID:        id.NewServiceID(),
		Name:      "service-for-deploy",
		Replicas:  1,
		Runtime:   "native",
		Command:   "bin/app",
		Status:    "RUNNING",
		SpecJSON:  `{}`,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.Services().Create(ctx, srvForDep)

	dep := &models.Deployment{
		ID:        depID,
		ServiceID: srvForDep.ID,
		Version:   "v1.0.0",
		Status:    "ACTIVE",
		SpecJSON:  `{"version":"v1.0.0"}`,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Deployments().Create(ctx, dep); err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	gotDep, err := store.Deployments().Get(ctx, depID)
	if err != nil || gotDep.Version != "v1.0.0" {
		t.Fatalf("failed to get deployment: %v", err)
	}

	dep.Status = "SUPERSEDED"
	if err := store.Deployments().Update(ctx, dep); err != nil {
		t.Fatalf("failed to update deployment: %v", err)
	}

	deps, err := store.Deployments().ListByService(ctx, srvForDep.ID)
	if err != nil || len(deps) != 1 || deps[0].Status != "SUPERSEDED" {
		t.Fatalf("failed to list deployments by service: %v", err)
	}

	// 4. Task Repository (List, ListByService, ListByWorker, Update, Delete)
	task1 := &models.Task{
		ID:           id.NewTaskID(),
		ServiceID:    srvForDep.ID,
		DeploymentID: depID,
		WorkerID:     wrk1.ID,
		State:        "RUNNING",
		PID:          1234,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	task2 := &models.Task{
		ID:           id.NewTaskID(),
		ServiceID:    srvForDep.ID,
		DeploymentID: depID,
		WorkerID:     wrk1.ID,
		State:        "FAILED",
		ExitCode:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	_ = store.Tasks().Create(ctx, task1)
	_ = store.Tasks().Create(ctx, task2)

	allTasks, err := store.Tasks().List(ctx)
	if err != nil || len(allTasks) != 2 {
		t.Fatalf("expected 2 tasks in list, got %d", len(allTasks))
	}

	bySrv, err := store.Tasks().ListByService(ctx, srvForDep.ID)
	if err != nil || len(bySrv) != 2 {
		t.Fatalf("expected 2 tasks by service, got %d", len(bySrv))
	}

	byWrk, err := store.Tasks().ListByWorker(ctx, wrk1.ID)
	if err != nil || len(byWrk) != 2 {
		t.Fatalf("expected 2 tasks by worker, got %d", len(byWrk))
	}

	task1.State = "STOPPED"
	if err := store.Tasks().Update(ctx, task1); err != nil {
		t.Fatalf("failed to update task: %v", err)
	}

	_ = store.Tasks().Delete(ctx, task2.ID)
	if _, err := store.Tasks().Get(ctx, task2.ID); err == nil {
		t.Errorf("expected deleted task to return not found")
	}

	if err := store.Deployments().Delete(ctx, depID); err != nil {
		t.Fatalf("failed to delete deployment: %v", err)
	}
	if _, err := store.Deployments().Get(ctx, depID); err == nil {
		t.Errorf("expected deleted deployment to return not found")
	}

	// 5. Job Repository (Create, Get, List, Update, Delete)
	job := &models.Job{
		ID:        id.NewJobID(),
		Name:      "batch-export",
		Command:   "echo done",
		Status:    "PENDING",
		SpecJSON:  `{"name":"batch-export"}`,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Jobs().Create(ctx, job); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	gotJob, err := store.Jobs().Get(ctx, job.ID)
	if err != nil || gotJob.Name != "batch-export" {
		t.Fatalf("expected job: %v", err)
	}

	job.Status = "COMPLETED"
	if err := store.Jobs().Update(ctx, job); err != nil {
		t.Fatalf("failed to update job: %v", err)
	}

	jobsList, err := store.Jobs().List(ctx)
	if err != nil || len(jobsList) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobsList))
	}

	_ = store.Jobs().Delete(ctx, job.ID)
	if _, err := store.Jobs().Get(ctx, job.ID); err == nil {
		t.Errorf("expected deleted job to return not found")
	}

	// 6. Volume Repository (Create, Get, List, Update, Delete)
	vol := &models.Volume{
		ID:        id.NewVolumeID(),
		Name:      "postgres-data",
		Path:      "/data/db",
		Driver:    "local",
		SpecJSON:  `{"name":"postgres-data"}`,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Volumes().Create(ctx, vol); err != nil {
		t.Fatalf("failed to create volume: %v", err)
	}

	gotVol, err := store.Volumes().Get(ctx, vol.ID)
	if err != nil || gotVol.Name != "postgres-data" {
		t.Fatalf("expected volume: %v", err)
	}

	vol.Path = "/data/db_v2"
	if err := store.Volumes().Update(ctx, vol); err != nil {
		t.Fatalf("failed to update volume: %v", err)
	}

	vols, err := store.Volumes().List(ctx)
	if err != nil || len(vols) != 1 || vols[0].Path != "/data/db_v2" {
		t.Fatalf("expected 1 volume: %v", err)
	}

	_ = store.Volumes().Delete(ctx, vol.ID)
	if _, err := store.Volumes().Get(ctx, vol.ID); err == nil {
		t.Errorf("expected deleted volume to return not found")
	}

	// 7. Network Repository (Create, Get, GetByName, List, Update, Delete)
	network := &models.Network{
		ID:        id.NewNetworkID(),
		Name:      "frontend-net",
		Subnet:    "10.100.0.0/16",
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.Networks().Create(ctx, network)

	gotNetByName, err := store.Networks().GetByName(ctx, "frontend-net")
	if err != nil || gotNetByName.ID != network.ID {
		t.Fatalf("expected network by name: %v", err)
	}

	network.Subnet = "10.100.1.0/24"
	if err := store.Networks().Update(ctx, network); err != nil {
		t.Fatalf("failed to update network: %v", err)
	}

	networks, err := store.Networks().List(ctx)
	if err != nil || len(networks) != 1 {
		t.Fatalf("expected 1 network: %v", err)
	}

	_ = store.Networks().Delete(ctx, network.ID)
	if _, err := store.Networks().Get(ctx, network.ID); err == nil {
		t.Errorf("expected deleted network to return not found")
	}

	// 8. Event Repository (Append, Get, List with limit, ListByEntity)
	evtID := id.NewEventID()
	evt := &models.Event{
		ID:        evtID,
		Type:      "TEST_EVENT",
		Source:    "test",
		EntityID:  nodeID,
		Payload:   `{"val":1}`,
		CreatedAt: now,
	}
	_ = store.Events().Append(ctx, evt)

	gotEvt, err := store.Events().Get(ctx, evtID)
	if err != nil || gotEvt.Type != "TEST_EVENT" {
		t.Fatalf("expected event by ID: %v", err)
	}

	evts, err := store.Events().List(ctx, 10)
	if err != nil || len(evts) == 0 {
		t.Fatalf("expected events with limit=10: %v", err)
	}

	evtsByEntity, err := store.Events().ListByEntity(ctx, nodeID)
	if err != nil || len(evtsByEntity) == 0 {
		t.Fatalf("expected events by entity: %v", err)
	}
}
