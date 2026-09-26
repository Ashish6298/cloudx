package worker

import (
	"context"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/api"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

// TestRemoteWorker_JoinClusterWithToken simulates multi-node environment:
// Machine A = ControlPlane host running on gRPC address with a cluster bootstrap token.
// Machine B = Remote Worker connecting from a separate node storage & address.
func TestRemoteWorker_JoinClusterWithToken(t *testing.T) {
	ctx := context.Background()
	bootstrapToken := "clx-btk-test-secret-9988"

	// 1. Initialize Machine A (Control Plane cluster)
	machineAStore, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to create Machine A sqlite store: %v", err)
	}
	defer machineAStore.Close()

	srv, err := api.NewServer(api.ServerOptions{
		Address:        "127.0.0.1:0",
		Store:          machineAStore,
		Logger:         logging.NewDefaultLogger(),
		BootstrapToken: bootstrapToken,
	})
	if err != nil {
		t.Fatalf("failed to create api server on Machine A: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start api server on Machine A: %v", err)
	}
	defer srv.Stop()

	controlPlaneAddr := srv.Address()

	// 2. Machine B (Remote Worker) attempts to join Machine A cluster with valid token
	machineBDir := t.TempDir()
	machineBCfg := config.NewDefaultConfig()
	machineBCfg.Storage.Path = machineBDir
	machineBCfg.ControlPlane.Address = controlPlaneAddr
	machineBCfg.Worker.Address = "192.168.1.105:7001"
	machineBCfg.Worker.BootstrapToken = bootstrapToken
	machineBCfg.Node.Name = "machine-b-worker"

	daemonB, err := NewDaemon(Options{
		Config: machineBCfg,
		Logger: logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create worker daemon for Machine B: %v", err)
	}

	joinCtx, cancelJoin := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelJoin()

	if err := daemonB.Start(joinCtx); err != nil {
		t.Fatalf("Machine B failed to join Machine A cluster: %v", err)
	}
	defer daemonB.Stop(context.Background())

	if daemonB.Status() != StatusReady {
		t.Fatalf("expected Machine B status READY, got %s", daemonB.Status())
	}
	if daemonB.ClusterID() == "" {
		t.Fatalf("expected non-empty ClusterID for Machine B")
	}

	// 3. Verify Machine A store registered Machine B with its distinct node and worker identity
	workerID := daemonB.ID()
	wrkRecord, err := machineAStore.Workers().Get(ctx, workerID)
	if err != nil || wrkRecord == nil {
		t.Fatalf("Machine A store does not contain worker record for Machine B: %v", err)
	}
	if wrkRecord.Address != "192.168.1.105:7001" {
		t.Fatalf("expected worker address 192.168.1.105:7001, got %s", wrkRecord.Address)
	}

	nodeRecord, err := machineAStore.Nodes().Get(ctx, wrkRecord.NodeID)
	if err != nil || nodeRecord == nil {
		t.Fatalf("Machine A store does not contain node record for Machine B: %v", err)
	}

	// 4. Verify Identity Persistence on Machine B across reboot/restarts
	idMgrB := NewIdentityManager(machineBDir)
	savedToken := idMgrB.GetBootstrapToken()
	if savedToken != bootstrapToken {
		t.Fatalf("expected bootstrap token %s saved on Machine B, got %s", bootstrapToken, savedToken)
	}

	reloadedWorkerID, err := idMgrB.GetOrCreateIdentity("")
	if err != nil || reloadedWorkerID != workerID {
		t.Fatalf("expected stable worker ID %s across restarts, got %s", workerID, reloadedWorkerID)
	}
}

// TestRemoteWorker_JoinCluster_InvalidTokenRejection ensures invalid tokens are rejected.
func TestRemoteWorker_JoinCluster_InvalidTokenRejection(t *testing.T) {
	ctx := context.Background()
	validToken := "clx-btk-valid-12345"

	machineAStore, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to create Machine A sqlite store: %v", err)
	}
	defer machineAStore.Close()

	srv, err := api.NewServer(api.ServerOptions{
		Address:        "127.0.0.1:0",
		Store:          machineAStore,
		Logger:         logging.NewDefaultLogger(),
		BootstrapToken: validToken,
	})
	if err != nil {
		t.Fatalf("failed to create api server: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start api server: %v", err)
	}
	defer srv.Stop()

	// Worker attempts to join with invalid token
	invalidCfg := config.NewDefaultConfig()
	invalidCfg.Storage.Path = t.TempDir()
	invalidCfg.ControlPlane.Address = srv.Address()
	invalidCfg.Worker.BootstrapToken = "wrong-token"

	invalidDaemon, err := NewDaemon(Options{
		Config: invalidCfg,
		Logger: logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create worker daemon: %v", err)
	}

	joinCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = invalidDaemon.Start(joinCtx)
	if err == nil {
		_ = invalidDaemon.Stop(context.Background())
		t.Fatalf("expected worker join with wrong token to fail, but succeeded")
	}

	if invalidDaemon.Status() != StatusDegraded {
		t.Fatalf("expected worker status DEGRADED after rejection, got %s", invalidDaemon.Status())
	}
}

// TestRemoteWorker_MultiNodeJoin tests multiple remote machines (Machine B, Machine C)
// joining Machine A cluster concurrently and being tracked in the cluster registry.
func TestRemoteWorker_MultiNodeJoin(t *testing.T) {
	ctx := context.Background()
	token := "clx-btk-multi-node-cluster"

	machineAStore, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer machineAStore.Close()

	srv, err := api.NewServer(api.ServerOptions{
		Address:        "127.0.0.1:0",
		Store:          machineAStore,
		Logger:         logging.NewDefaultLogger(),
		BootstrapToken: token,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop()

	// Machine B
	cfgB := config.NewDefaultConfig()
	cfgB.Storage.Path = t.TempDir()
	cfgB.ControlPlane.Address = srv.Address()
	cfgB.Worker.Address = "10.0.1.10:7001"
	cfgB.Worker.BootstrapToken = token
	cfgB.Node.Name = "machine-b"

	daemonB, err := NewDaemon(Options{Config: cfgB, Logger: logging.NewDefaultLogger()})
	if err != nil {
		t.Fatalf("failed daemonB: %v", err)
	}
	ctxB, cancelB := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelB()
	if err := daemonB.Start(ctxB); err != nil {
		t.Fatalf("failed daemonB start: %v", err)
	}
	defer daemonB.Stop(context.Background())

	// Machine C
	cfgC := config.NewDefaultConfig()
	cfgC.Storage.Path = t.TempDir()
	cfgC.ControlPlane.Address = srv.Address()
	cfgC.Worker.Address = "10.0.1.20:7001"
	cfgC.Worker.BootstrapToken = token
	cfgC.Node.Name = "machine-c"

	daemonC, err := NewDaemon(Options{Config: cfgC, Logger: logging.NewDefaultLogger()})
	if err != nil {
		t.Fatalf("failed daemonC: %v", err)
	}
	ctxC, cancelC := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelC()
	if err := daemonC.Start(ctxC); err != nil {
		t.Fatalf("failed daemonC start: %v", err)
	}
	defer daemonC.Stop(context.Background())

	// Check workers in store
	workers, err := machineAStore.Workers().List(ctx)
	if err != nil {
		t.Fatalf("failed to list workers: %v", err)
	}
	if len(workers) != 2 {
		t.Fatalf("expected 2 remote workers registered, found %d", len(workers))
	}

	foundB := false
	foundC := false
	for _, w := range workers {
		if w.ID == daemonB.ID() && w.Address == "10.0.1.10:7001" {
			foundB = true
		}
		if w.ID == daemonC.ID() && w.Address == "10.0.1.20:7001" {
			foundC = true
		}
	}
	if !foundB || !foundC {
		t.Fatalf("workers B (%v) and C (%v) registration check failed", foundB, foundC)
	}
}
