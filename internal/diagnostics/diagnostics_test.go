package diagnostics_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/diagnostics"
	"github.com/cloudx-org/cloudx/internal/health"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker/monitor"
)

type mockCollector struct {
	cpu float64
	mem int64
}

func (m *mockCollector) Collect(ctx context.Context) (*monitor.ResourceMetrics, error) {
	return &monitor.ResourceMetrics{
		Timestamp:        time.Now().UTC(),
		CPUUsagePercent:  m.cpu,
		MemoryUsedBytes:  m.mem,
		MemoryAvailBytes: 1024 * 1024 * 1024,
		TotalMemoryBytes: 2048 * 1024 * 1024,
		ProcessCount:     120,
		Platform:         "test-os",
	}, nil
}

func (m *mockCollector) Platform() string { return "test-os" }

func TestDiagnosticsEngineAllHealthy(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cloudx.db")

	cfg := config.NewDefaultConfig()
	cfg.Storage.Path = tmpDir

	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	// Seed healthy node & worker
	now := time.Now().UTC()
	nodeID := id.NewNodeID()
	err = store.Nodes().Create(context.Background(), &models.Node{
		ID:        nodeID,
		Name:      "node-1",
		Address:   "127.0.0.1",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create node: %v", err)
	}

	workerID := id.NewWorkerID()
	err = store.Workers().Create(context.Background(), &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   "127.0.0.1:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}

	// Seed healthy service
	svcID := id.NewServiceID()
	_ = store.Services().Create(context.Background(), &models.Service{
		ID:        svcID,
		Name:      "api",
		Replicas:  1,
		Status:    "HEALTHY",
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Seed healthy task assigned to worker
	_ = store.Tasks().Create(context.Background(), &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: svcID,
		WorkerID:  workerID,
		State:     "RUNNING",
		CreatedAt: now,
		UpdatedAt: now,
	})

	engine := diagnostics.NewEngine(cfg, store, dbPath)
	engine.SetCollector(&mockCollector{cpu: 15.0, mem: 512 * 1024 * 1024})

	report := engine.Run(context.Background())

	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if len(report.Checks) != 9 {
		t.Fatalf("expected 9 diagnostic checks, got %d", len(report.Checks))
	}
	if report.Overall != diagnostics.StatusPass && report.Overall != diagnostics.StatusWarn {
		for _, c := range report.Checks {
			t.Logf("Check [%s] status=%s summary=%s", c.Name, c.Status, c.Summary)
		}
		t.Fatalf("expected PASS or WARN, got %s", report.Overall)
	}
	if report.FailCount > 0 {
		for _, c := range report.Checks {
			if c.Status == diagnostics.StatusFail {
				t.Logf("Failed check [%s]: %s (details: %v)", c.Name, c.Summary, c.Details)
			}
		}
		t.Fatalf("expected 0 failures in healthy setup, got %d", report.FailCount)
	}
}

func TestDiagnosticsDetectsOrphanedTasks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cloudx.db")

	cfg := config.NewDefaultConfig()
	cfg.Storage.Path = tmpDir

	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	// Seed a LOST worker
	lostWorkerID := id.NewWorkerID()
	now := time.Now().UTC()
	_ = store.Workers().Create(context.Background(), &models.Worker{
		ID:        lostWorkerID,
		NodeID:    id.NewNodeID(),
		Status:    string(health.StatusLost),
		Heartbeat: now.Add(-5 * time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Seed an active task stranded on that lost worker
	_ = store.Tasks().Create(context.Background(), &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: id.NewServiceID(),
		WorkerID:  lostWorkerID,
		State:     "RUNNING", // active task on lost worker = orphaned
		CreatedAt: now,
		UpdatedAt: now,
	})

	engine := diagnostics.NewEngine(cfg, store, dbPath)
	report := engine.Run(context.Background())

	if report.Overall != diagnostics.StatusFail {
		t.Fatalf("expected overall FAIL when orphaned task exists, got %s", report.Overall)
	}

	// Verify Orphaned Tasks check failed
	var foundOrphanCheck bool
	for _, c := range report.Checks {
		if c.Name == "Orphaned Tasks" {
			foundOrphanCheck = true
			if c.Status != diagnostics.StatusFail {
				t.Fatalf("expected Orphaned Tasks status FAIL, got %s", c.Status)
			}
		}
	}
	if !foundOrphanCheck {
		t.Fatal("Orphaned Tasks check not present in report")
	}
}

func TestDiagnosticsDetectsResourcePressure(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cloudx.db")

	cfg := config.NewDefaultConfig()
	cfg.Storage.Path = tmpDir

	store, _ := sqlite.Open(context.Background(), dbPath)
	defer store.Close()

	engine := diagnostics.NewEngine(cfg, store, dbPath)
	// Mock 98% CPU load
	engine.SetCollector(&mockCollector{cpu: 98.5, mem: 2000 * 1024 * 1024})

	report := engine.Run(context.Background())

	var resourceCheck *diagnostics.CheckResult
	for i, c := range report.Checks {
		if c.Name == "Resource Pressure" {
			resourceCheck = &report.Checks[i]
			break
		}
	}

	if resourceCheck == nil {
		t.Fatal("Resource Pressure check not found")
	}
	if resourceCheck.Status != diagnostics.StatusFail {
		t.Fatalf("expected FAIL for 98.5%% CPU, got %s", resourceCheck.Status)
	}
}

func TestDiagnosticsDetectsFailedDeployments(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cloudx.db")

	cfg := config.NewDefaultConfig()
	cfg.Storage.Path = tmpDir

	store, _ := sqlite.Open(context.Background(), dbPath)
	defer store.Close()

	now := time.Now().UTC()
	svcID := id.NewServiceID()
	_ = store.Services().Create(context.Background(), &models.Service{
		ID:        svcID,
		Name:      "web",
		Status:    "DEGRADED",
		CreatedAt: now,
		UpdatedAt: now,
	})

	_ = store.Deployments().Create(context.Background(), &models.Deployment{
		ID:        id.NewDeploymentID(),
		ServiceID: svcID,
		Version:   "2.0.0",
		Status:    "FAILED",
		CreatedAt: now,
		UpdatedAt: now,
	})

	engine := diagnostics.NewEngine(cfg, store, dbPath)
	report := engine.Run(context.Background())

	var deployCheck *diagnostics.CheckResult
	for i, c := range report.Checks {
		if c.Name == "Failed Deployments" {
			deployCheck = &report.Checks[i]
			break
		}
	}

	if deployCheck == nil {
		t.Fatal("Failed Deployments check not found")
	}
	if deployCheck.Status != diagnostics.StatusFail {
		t.Fatalf("expected FAIL for failed deployment, got %s", deployCheck.Status)
	}
}
