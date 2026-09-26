package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

func buildTestClusterForMetrics(t *testing.T) (string, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cloudx.db")
	ctx := context.Background()
	now := time.Now().UTC()

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	nodeID := id.NewNodeID()
	workerID := id.NewWorkerID()
	svcID := id.NewServiceID()
	jobID1 := id.NewJobID()
	jobID2 := id.NewJobID()

	_ = store.Nodes().Create(ctx, &models.Node{ID: nodeID, Name: "node-1", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: workerID, NodeID: nodeID, Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})
	_ = store.Services().Create(ctx, &models.Service{ID: svcID, Name: "api", Status: "ACTIVE", CreatedAt: now, UpdatedAt: now})

	for i := 0; i < 2; i++ {
		_ = store.Tasks().Create(ctx, &models.Task{
			ID: id.NewTaskID(), ServiceID: svcID, WorkerID: workerID,
			State: "RUNNING", CreatedAt: now, UpdatedAt: now,
		})
	}

	_ = store.Jobs().Create(ctx, &models.Job{ID: jobID1, Name: "migrate", Command: "migrate up", Status: "SUCCEEDED", CreatedAt: now, UpdatedAt: now})
	_ = store.Jobs().Create(ctx, &models.Job{ID: jobID2, Name: "seed", Command: "seed db", Status: "FAILED", CreatedAt: now, UpdatedAt: now})

	_ = store.Close()

	return tempDir, func() {} // cleanup handled by t.TempDir()
}

func TestMetricsShowCmd_HumanReadable(t *testing.T) {
	tempDir, cleanup := buildTestClusterForMetrics(t)
	defer cleanup()

	var buf bytes.Buffer
	rootCmd := newRootCmd()
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"--storage-path", tempDir, "metrics", "show"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("metrics show failed: %v\nOutput:\n%s", err, buf.String())
	}

	out := buf.String()
	t.Logf("metrics show output:\n%s", out)

	for _, want := range []string{
		"CloudX Cluster Metrics",
		"Workers:",
		"Services:",
		"Jobs:",
		"COUNTERS",
		"GAUGES",
		"HISTOGRAMS",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestMetricsShowCmd_JSON(t *testing.T) {
	tempDir, cleanup := buildTestClusterForMetrics(t)
	defer cleanup()

	var buf bytes.Buffer
	rootCmd := newRootCmd()
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"--storage-path", tempDir, "metrics", "show", "--json"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("metrics show --json failed: %v", err)
	}

	var result struct {
		CollectedAt string `json:"collected_at"`
		Summary     struct {
			Workers  int `json:"workers"`
			Services int `json:"services"`
			Jobs     int `json:"jobs"`
		} `json:"summary"`
		Metrics []map[string]interface{} `json:"metrics"`
	}

	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nOutput: %s", err, buf.String())
	}

	if result.Summary.Workers != 1 {
		t.Errorf("expected 1 worker, got %d", result.Summary.Workers)
	}
	if result.Summary.Services != 1 {
		t.Errorf("expected 1 service, got %d", result.Summary.Services)
	}
	if result.Summary.Jobs != 2 {
		t.Errorf("expected 2 jobs, got %d", result.Summary.Jobs)
	}
	if len(result.Metrics) == 0 {
		t.Error("expected metrics array to be non-empty")
	}
}

func TestMetricsShowCmd_DomainFilter(t *testing.T) {
	tempDir, cleanup := buildTestClusterForMetrics(t)
	defer cleanup()

	for _, domain := range []string{"controlplane", "worker", "service", "job"} {
		var buf bytes.Buffer
		rootCmd := newRootCmd()
		rootCmd.SetOut(&buf)
		rootCmd.SetErr(&buf)
		rootCmd.SetArgs([]string{"--storage-path", tempDir, "metrics", "show", "--domain", domain})

		err := rootCmd.Execute()
		if err != nil {
			t.Errorf("metrics show --domain %s failed: %v", domain, err)
			continue
		}
		t.Logf("domain=%s output:\n%s", domain, buf.String())
	}
}
