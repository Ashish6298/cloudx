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

func TestNetwork_Lifecycle_And_LogicalDiscovery(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cloudx_net_test.db")
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

	// 1. Create logical networks: "backend" and "frontend"
	netBackend, err := cp.CreateNetwork(ctx, controlplane.NetworkCreateOptions{
		Name:   "backend",
		Subnet: "10.244.1.0/24",
	})
	if err != nil {
		t.Fatalf("failed to create backend network: %v", err)
	}
	if netBackend.NetworkName != "backend" {
		t.Errorf("expected network name 'backend', got %s", netBackend.NetworkName)
	}

	netFrontend, err := cp.CreateNetwork(ctx, controlplane.NetworkCreateOptions{
		Name: "frontend",
	})
	if err != nil {
		t.Fatalf("failed to create frontend network: %v", err)
	}
	if netFrontend.Subnet != "10.244.0.0/16" {
		t.Errorf("expected default subnet '10.244.0.0/16', got %s", netFrontend.Subnet)
	}

	// 2. Duplicate network creation should fail
	_, err = cp.CreateNetwork(ctx, controlplane.NetworkCreateOptions{Name: "backend"})
	if err == nil {
		t.Fatalf("expected duplicate network creation to fail, got nil")
	}

	// 3. List networks
	nets, err := cp.ListNetworks(ctx)
	if err != nil || len(nets) != 2 {
		t.Fatalf("expected 2 networks, got %d (err: %v)", len(nets), err)
	}

	// 4. Register a compute node and worker
	now := time.Now().UTC()
	nodeID := id.NewNodeID()
	wID := id.NewWorkerID()

	if err := store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "compute-node-1",
		Address:   "10.0.0.20",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}

	if err := store.Workers().Create(ctx, &models.Worker{
		ID:        wID,
		NodeID:    nodeID,
		Address:   "10.0.0.20:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create worker: %v", err)
	}

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(wID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return nil
	})

	// 5. Deploy services joining networks:
	// - auth-api joins ["backend"]
	// - database joins ["backend"]
	// - web-ui joins ["frontend"]
	rep := 1
	sAuth := &spec.ServiceConfig{
		Name:     "auth-api",
		Command:  "./auth-service",
		Replicas: &rep,
		Ports:    []spec.PortSpec{{HostPort: 8081, ServicePort: 8081, Protocol: "tcp"}},
		Networks: []string{"backend"},
	}
	sDB := &spec.ServiceConfig{
		Name:     "db-service",
		Command:  "./db-service",
		Replicas: &rep,
		Ports:    []spec.PortSpec{{HostPort: 5432, ServicePort: 5432, Protocol: "tcp"}},
		Networks: []string{"backend"},
	}
	sWeb := &spec.ServiceConfig{
		Name:     "web-ui",
		Command:  "./web-ui",
		Replicas: &rep,
		Ports:    []spec.PortSpec{{HostPort: 3000, ServicePort: 3000, Protocol: "tcp"}},
		Networks: []string{"frontend"},
	}

	depAuth, err := cp.DeployService(ctx, sAuth, dispatcher)
	if err != nil {
		t.Fatalf("failed to deploy auth-api: %v", err)
	}
	depDB, err := cp.DeployService(ctx, sDB, dispatcher)
	if err != nil {
		t.Fatalf("failed to deploy db-service: %v", err)
	}
	depWeb, err := cp.DeployService(ctx, sWeb, dispatcher)
	if err != nil {
		t.Fatalf("failed to deploy web-ui: %v", err)
	}

	// 6. Set active tasks to RUNNING state so they are registered in ServiceRegistry
	for _, depID := range []id.ID{depAuth.DeploymentID, depDB.DeploymentID, depWeb.DeploymentID} {
		dep, _ := store.Deployments().Get(ctx, depID)
		tasks, _ := store.Tasks().ListByService(ctx, dep.ServiceID)
		for _, tsk := range tasks {
			tsk.State = string(models.TaskStateRunning)
			tsk.UpdatedAt = time.Now().UTC()
			_ = store.Tasks().Update(ctx, tsk)
			_ = cp.RegistryManager.OnTaskStateChange(ctx, tsk)
		}
	}

	// 7. Test Logical Network Discovery
	// Inspect "backend" network: must contain auth-api and db-service
	inspBackend, err := cp.InspectNetwork(ctx, "backend")
	if err != nil {
		t.Fatalf("inspect backend network failed: %v", err)
	}
	if len(inspBackend.Services) != 2 {
		t.Errorf("expected 2 services in backend, got %d (%v)", len(inspBackend.Services), inspBackend.Services)
	}
	if len(inspBackend.Endpoints) != 2 {
		t.Errorf("expected 2 endpoints in backend, got %d", len(inspBackend.Endpoints))
	}

	// Resolve all endpoints in "backend" network
	backendEndpoints, err := cp.ResolveNetwork(ctx, "backend")
	if err != nil || len(backendEndpoints) != 2 {
		t.Fatalf("expected 2 endpoints in backend network, got %d (err: %v)", len(backendEndpoints), err)
	}

	// Resolve "auth-api" within "backend" network -> SUCCESS
	authInBackend, err := cp.ResolveServiceInNetwork(ctx, "auth-api", "backend")
	if err != nil || len(authInBackend) != 1 {
		t.Fatalf("expected 1 endpoint for auth-api in backend network, got %d (err: %v)", len(authInBackend), err)
	}
	if authInBackend[0].Address != "10.0.0.20:8081" {
		t.Errorf("expected address 10.0.0.20:8081, got %s", authInBackend[0].Address)
	}

	// Resolve "web-ui" within "backend" network -> EMPTY (web-ui is in frontend, not backend)
	webInBackend, err := cp.ResolveServiceInNetwork(ctx, "web-ui", "backend")
	if err != nil || len(webInBackend) != 0 {
		t.Fatalf("expected 0 endpoints for web-ui in backend network, got %d (err: %v)", len(webInBackend), err)
	}

	// Resolve "web-ui" within "frontend" network -> SUCCESS
	webInFrontend, err := cp.ResolveServiceInNetwork(ctx, "web-ui", "frontend")
	if err != nil || len(webInFrontend) != 1 {
		t.Fatalf("expected 1 endpoint for web-ui in frontend network, got %d (err: %v)", len(webInFrontend), err)
	}
	if webInFrontend[0].Address != "10.0.0.20:3000" {
		t.Errorf("expected address 10.0.0.20:3000, got %s", webInFrontend[0].Address)
	}

	// 8. Delete network protection: cannot delete backend while services are attached
	err = cp.DeleteNetwork(ctx, "backend")
	if err == nil {
		t.Fatalf("expected error deleting network with attached services, got nil")
	}

	// Delete frontend network after stopping web-ui service
	sWebRecord, _ := store.Services().GetByName(ctx, "web-ui")
	if sWebRecord != nil {
		sWebRecord.Status = "STOPPED"
		_ = store.Services().Update(ctx, sWebRecord)
	}
	err = cp.DeleteNetwork(ctx, "frontend")
	if err != nil {
		t.Fatalf("failed to delete unattached frontend network: %v", err)
	}

	netsAfterDel, _ := cp.ListNetworks(ctx)
	if len(netsAfterDel) != 1 {
		t.Errorf("expected 1 remaining network after deletion, got %d", len(netsAfterDel))
	}
}
