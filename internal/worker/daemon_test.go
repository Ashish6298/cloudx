package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/api"
	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

// setupMockControlPlane spins up a real in-memory gRPC control plane for worker integration tests.
func setupMockControlPlane(t *testing.T) (string, func()) {
	t.Helper()
	ctx := context.Background()

	memStore, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	srv, err := api.NewServer(api.ServerOptions{
		Address: "127.0.0.1:0",
		Store:   memStore,
		Logger:  logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create api server: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start api server: %v", err)
	}

	teardown := func() {
		srv.Stop()
		_ = memStore.Close()
	}

	return srv.Address(), teardown
}

func TestWorker_IdentityPersistence(t *testing.T) {
	tempDir := t.TempDir()

	idMgr := NewIdentityManager(tempDir)
	id1, err := idMgr.GetOrCreateIdentity("")
	if err != nil {
		t.Fatalf("failed to create identity: %v", err)
	}

	entityType, err := id1.Type()
	if err != nil {
		t.Fatalf("failed to parse id: %v", err)
	}
	if entityType != id.EntityWorker {
		t.Fatalf("expected prefix %s, got %s", id.EntityWorker, entityType)
	}

	// Verify file exists
	idFile := filepath.Join(tempDir, "worker.id")
	if _, err := os.Stat(idFile); os.IsNotExist(err) {
		t.Fatalf("worker.id file was not written")
	}

	// Read again using new manager
	idMgr2 := NewIdentityManager(tempDir)
	id2, err := idMgr2.GetOrCreateIdentity("")
	if err != nil {
		t.Fatalf("failed to reload identity: %v", err)
	}

	if id1.String() != id2.String() {
		t.Fatalf("worker ID was not stable across restarts! id1=%s, id2=%s", id1, id2)
	}
}

func TestWorker_Lifecycle_StartupRegistrationShutdown(t *testing.T) {
	cpAddr, teardown := setupMockControlPlane(t)
	defer teardown()

	tempDir := t.TempDir()
	cfg := config.NewDefaultConfig()
	cfg.Storage.Path = tempDir
	cfg.ControlPlane.Address = cpAddr
	cfg.Health.HeartbeatInterval = 100 * time.Millisecond

	daemon, err := NewDaemon(Options{
		Config: cfg,
		Logger: logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create daemon: %v", err)
	}

	if daemon.Status() != StatusStarting {
		t.Fatalf("expected initial status STARTING, got %s", daemon.Status())
	}

	// Start daemon
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = daemon.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start daemon: %v", err)
	}

	if daemon.Status() != StatusReady {
		t.Fatalf("expected status READY after registration, got %s", daemon.Status())
	}

	if daemon.ClusterID() == "" {
		t.Fatalf("expected non-empty cluster ID after registration, got empty")
	}

	// Wait for heartbeats to trigger
	time.Sleep(150 * time.Millisecond)

	// Verify still READY
	if daemon.Status() != StatusReady {
		t.Fatalf("expected status READY after heartbeats, got %s", daemon.Status())
	}

	// Graceful shutdown
	err = daemon.Stop(context.Background())
	if err != nil {
		t.Fatalf("failed to stop daemon: %v", err)
	}

	if daemon.Status() != StatusStopped {
		t.Fatalf("expected status STOPPED, got %s", daemon.Status())
	}

	// Re-start worker daemon (simulating reboot) -> safe re-registration
	daemon2, err := NewDaemon(Options{
		Config: cfg,
		Logger: logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create second daemon: %v", err)
	}
	if daemon2.ID() != daemon.ID() {
		t.Fatalf("expected stable worker ID across reboots, got %s vs %s", daemon2.ID(), daemon.ID())
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	err = daemon2.Start(ctx2)
	if err != nil {
		t.Fatalf("failed to re-register daemon: %v", err)
	}
	if daemon2.Status() != StatusReady {
		t.Fatalf("expected status READY after re-registration, got %s", daemon2.Status())
	}
	_ = daemon2.Stop(context.Background())
}

func TestWorker_ControlPlaneUnavailable(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.NewDefaultConfig()
	cfg.Storage.Path = tempDir
	// Point to unavailable port
	cfg.ControlPlane.Address = "127.0.0.1:1"

	daemon, err := NewDaemon(Options{
		Config: cfg,
		Logger: logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create daemon: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err = daemon.Start(ctx)
	if err == nil {
		t.Fatalf("expected error when control plane is unavailable, got nil")
	}

	if daemon.Status() != StatusDegraded {
		t.Fatalf("expected status DEGRADED, got %s", daemon.Status())
	}

	_ = daemon.Stop(context.Background())
	if daemon.Status() != StatusStopped {
		t.Fatalf("expected status STOPPED after stop, got %s", daemon.Status())
	}
}

func TestWorker_ConfigurationErrors(t *testing.T) {
	// 1. Invalid config
	invalidCfg := &config.Config{
		Node: config.NodeConfig{ID: ""}, // Empty Node ID fails validation
	}
	_, err := NewDaemon(Options{Config: invalidCfg})
	if err == nil {
		t.Fatalf("expected error for invalid config, got nil")
	}

	// 2. Default options fallback
	daemon, err := NewDaemon(Options{})
	if err != nil {
		t.Fatalf("failed to create daemon with default options: %v", err)
	}
	if daemon.ID().String() == "" {
		t.Fatalf("expected generated worker ID, got empty")
	}
}
