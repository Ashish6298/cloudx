package controlplane

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

type mockFailingComponent struct {
	name string
}

func (m *mockFailingComponent) Name() string                { return m.name }
func (m *mockFailingComponent) Start(ctx context.Context) error { return fmt.Errorf("simulated boot error") }
func (m *mockFailingComponent) Stop(ctx context.Context) error  { return nil }

func TestControlPlaneStartupAndShutdown(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}

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

	if cp.Status() != StatusInitialized {
		t.Errorf("expected status %s, got %s", StatusInitialized, cp.Status())
	}

	// 1. Test Startup
	if err := cp.Start(ctx); err != nil {
		t.Fatalf("control plane startup failed: %v", err)
	}

	if cp.Status() != StatusRunning {
		t.Errorf("expected status %s, got %s", StatusRunning, cp.Status())
	}

	// 2. Test Graceful Shutdown
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := cp.Stop(stopCtx); err != nil {
		t.Fatalf("control plane shutdown failed: %v", err)
	}

	if cp.Status() != StatusStopped {
		t.Errorf("expected status %s, got %s", StatusStopped, cp.Status())
	}
}

func TestControlPlaneFailureDuringStartup(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}

	cfg := config.NewDefaultConfig()
	failingComp := &mockFailingComponent{name: "BrokenEngine"}

	cp, err := New(Options{
		Config:     cfg,
		Store:      store,
		Components: []Component{failingComp},
	})
	if err != nil {
		t.Fatalf("failed to create control plane: %v", err)
	}

	err = cp.Start(ctx)
	if err == nil {
		t.Fatalf("expected startup to fail when a component fails, got nil")
	}

	if cp.Status() != StatusStopped && cp.Status() != StatusFailed {
		t.Errorf("expected status STOPPED or FAILED, got %s", cp.Status())
	}
}

func TestControlPlaneContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}

	cp, err := New(Options{
		Store: store,
	})
	if err != nil {
		t.Fatalf("failed to create control plane: %v", err)
	}

	// Cancel context prior to / during startup
	cancel()
	err = cp.Start(ctx)
	if err == nil {
		t.Fatalf("expected startup to fail with context cancellation, got nil")
	}

	if cp.Status() != StatusStopped {
		t.Errorf("expected status STOPPED after cancellation, got %s", cp.Status())
	}
}
