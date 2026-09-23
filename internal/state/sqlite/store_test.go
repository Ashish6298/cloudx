package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite store: %v", err)
	}

	return store, func() {
		_ = store.Close()
	}
}

func TestDatabaseCreationAndMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-cloudx.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite database at %s: %v", dbPath, err)
	}
	defer store.Close()

	// Verify we can list nodes from fresh schema
	nodes, err := store.Nodes().List(ctx)
	if err != nil {
		t.Fatalf("failed to list nodes from fresh DB: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(nodes))
	}
}

func TestCRUDAllEntities(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// 1. Node CRUD
	node := &models.Node{
		ID:        id.NewNodeID(),
		Name:      "master-node",
		Address:   "192.168.1.10:7000",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Nodes().Create(ctx, node); err != nil {
		t.Fatalf("failed to create node: %v", err)
	}

	gotNode, err := store.Nodes().Get(ctx, node.ID)
	if err != nil {
		t.Fatalf("failed to get node: %v", err)
	}
	if gotNode.Name != "master-node" {
		t.Errorf("expected node name 'master-node', got %q", gotNode.Name)
	}

	gotNode.Status = "DEGRADED"
	if err := store.Nodes().Update(ctx, gotNode); err != nil {
		t.Fatalf("failed to update node: %v", err)
	}

	// 2. Worker CRUD
	worker := &models.Worker{
		ID:        id.NewWorkerID(),
		NodeID:    node.ID,
		Address:   "192.168.1.10:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Workers().Create(ctx, worker); err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}

	gotWorker, err := store.Workers().Get(ctx, worker.ID)
	if err != nil {
		t.Fatalf("failed to get worker: %v", err)
	}
	if gotWorker.NodeID != node.ID {
		t.Errorf("expected worker node_id %s, got %s", node.ID, gotWorker.NodeID)
	}

	// 3. Service CRUD
	srv := &models.Service{
		ID:        id.NewServiceID(),
		Name:      "api-server",
		Replicas:  3,
		Runtime:   "native",
		Command:   "./api --port 8080",
		Status:    "RUNNING",
		SpecJSON:  `{"port":8080}`,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Services().Create(ctx, srv); err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	gotSrv, err := store.Services().GetByName(ctx, "api-server")
	if err != nil {
		t.Fatalf("failed to get service by name: %v", err)
	}
	if gotSrv.Replicas != 3 {
		t.Errorf("expected 3 replicas, got %d", gotSrv.Replicas)
	}

	// 4. Deployment CRUD
	dep := &models.Deployment{
		ID:        id.NewDeploymentID(),
		ServiceID: srv.ID,
		Version:   "v1.0.0",
		Status:    "DEPLOYED",
		SpecJSON:  `{"version":"v1.0.0"}`,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Deployments().Create(ctx, dep); err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	deps, err := store.Deployments().ListByService(ctx, srv.ID)
	if err != nil || len(deps) != 1 {
		t.Fatalf("expected 1 deployment, got %d (err: %v)", len(deps), err)
	}

	// 5. Task CRUD
	task := &models.Task{
		ID:           id.NewTaskID(),
		ServiceID:    srv.ID,
		DeploymentID: dep.ID,
		WorkerID:     worker.ID,
		State:        "RUNNING",
		PID:          12345,
		ExitCode:     0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.Tasks().Create(ctx, task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	tasks, err := store.Tasks().ListByService(ctx, srv.ID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("expected 1 task for service, got %d (err: %v)", len(tasks), err)
	}

	// 6. Job CRUD
	job := &models.Job{
		ID:        id.NewJobID(),
		Name:      "db-migration",
		Command:   "migrate up",
		Status:    "PENDING",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Jobs().Create(ctx, job); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	gotJob, err := store.Jobs().Get(ctx, job.ID)
	if err != nil || gotJob.Name != "db-migration" {
		t.Fatalf("failed to get job: %v", err)
	}

	// 7. Volume CRUD
	vol := &models.Volume{
		ID:        id.NewVolumeID(),
		Name:      "data-volume",
		WorkerID:  worker.ID,
		Path:      "/var/cloudx/data",
		Driver:    "local",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Volumes().Create(ctx, vol); err != nil {
		t.Fatalf("failed to create volume: %v", err)
	}

	vols, err := store.Volumes().List(ctx)
	if err != nil || len(vols) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(vols))
	}

	// 8. Network CRUD
	net := &models.Network{
		ID:        id.NewNetworkID(),
		Name:      "backend-net",
		Subnet:    "10.244.0.0/16",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Networks().Create(ctx, net); err != nil {
		t.Fatalf("failed to create network: %v", err)
	}

	gotNet, err := store.Networks().Get(ctx, net.ID)
	if err != nil || gotNet.Name != "backend-net" {
		t.Fatalf("failed to get network: %v", err)
	}

	// 9. Event Append & List
	evt := &models.Event{
		ID:        id.NewEventID(),
		Type:      "SERVICE_CREATED",
		Source:    "controlplane",
		EntityID:  srv.ID,
		Payload:   `{"name":"api-server"}`,
		CreatedAt: now,
	}
	if err := store.Events().Append(ctx, evt); err != nil {
		t.Fatalf("failed to append event: %v", err)
	}

	events, err := store.Events().ListByEntity(ctx, srv.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("expected 1 event for service, got %d", len(events))
	}
}

func TestTransactionCommitAndRollback(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Rollback test
	err := store.Transaction(ctx, func(tx state.Store) error {
		n := &models.Node{
			ID:        id.NewNodeID(),
			Name:      "temp-node",
			Address:   "127.0.0.1:9000",
			Status:    "READY",
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := tx.Nodes().Create(ctx, n); err != nil {
			return err
		}
		// intentional rollback
		return context.Canceled
	})

	if err == nil {
		t.Fatalf("expected transaction error on rollback, got nil")
	}

	nodes, err := store.Nodes().List(ctx)
	if err != nil || len(nodes) != 0 {
		t.Fatalf("expected 0 nodes after rollback, got %d", len(nodes))
	}

	// 2. Commit test
	committedID := id.NewNodeID()
	err = store.Transaction(ctx, func(tx state.Store) error {
		n := &models.Node{
			ID:        committedID,
			Name:      "permanent-node",
			Address:   "127.0.0.1:9000",
			Status:    "READY",
			CreatedAt: now,
			UpdatedAt: now,
		}
		return tx.Nodes().Create(ctx, n)
	})

	if err != nil {
		t.Fatalf("transaction commit failed: %v", err)
	}

	gotNode, err := store.Nodes().Get(ctx, committedID)
	if err != nil || gotNode.Name != "permanent-node" {
		t.Fatalf("expected committed node in database: %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const concurrentRoutines = 20
	var wg sync.WaitGroup

	for i := 0; i < concurrentRoutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			now := time.Now().UTC()
			n := &models.Node{
				ID:        id.NewNodeID(),
				Name:      "concurrent-node",
				Address:   "127.0.0.1:8000",
				Status:    "READY",
				CreatedAt: now,
				UpdatedAt: now,
			}
			_ = store.Nodes().Create(ctx, n)
		}(i)
	}

	wg.Wait()

	nodes, err := store.Nodes().List(ctx)
	if err != nil {
		t.Fatalf("failed to list nodes after concurrent writes: %v", err)
	}
	if len(nodes) != concurrentRoutines {
		t.Errorf("expected %d nodes, got %d", concurrentRoutines, len(nodes))
	}
}
