package metrics_test

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/metrics"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

// ──────────────────────────────────────────────────────────────────────────────
// Counter tests
// ──────────────────────────────────────────────────────────────────────────────

func TestCounter_IncAndAdd(t *testing.T) {
	reg := metrics.NewRegistry()
	c := reg.Counter("test_counter", nil)

	if c.Value() != 0 {
		t.Fatalf("expected initial value 0, got %d", c.Value())
	}
	c.Inc()
	c.Inc()
	c.Add(5)
	if c.Value() != 7 {
		t.Errorf("expected 7 after Inc×2 + Add(5), got %d", c.Value())
	}
	// Negative add should be ignored
	c.Add(-100)
	if c.Value() != 7 {
		t.Errorf("expected 7 after negative Add (no-op), got %d", c.Value())
	}
}

func TestCounter_Idempotent(t *testing.T) {
	reg := metrics.NewRegistry()
	c1 := reg.Counter("shared_counter", map[string]string{"env": "test"})
	c2 := reg.Counter("shared_counter", map[string]string{"env": "test"})
	c1.Inc()
	if c2.Value() != 1 {
		t.Errorf("expected same underlying counter, got %d", c2.Value())
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Gauge tests
// ──────────────────────────────────────────────────────────────────────────────

func TestGauge_SetAndAdd(t *testing.T) {
	reg := metrics.NewRegistry()
	g := reg.Gauge("cpu_usage", nil)

	g.Set(45.5)
	if g.Value() != 45.5 {
		t.Errorf("expected 45.5, got %f", g.Value())
	}
	g.Add(10.0)
	if g.Value() != 55.5 {
		t.Errorf("expected 55.5, got %f", g.Value())
	}
	g.Set(0)
	if g.Value() != 0 {
		t.Errorf("expected 0 after reset, got %f", g.Value())
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Histogram tests
// ──────────────────────────────────────────────────────────────────────────────

func TestHistogram_ObserveAndQuantile(t *testing.T) {
	reg := metrics.NewRegistry()
	h := reg.Histogram("scheduling_latency", nil)

	if !math.IsNaN(h.Quantile(0.5)) {
		t.Error("expected NaN quantile before any observations")
	}

	// Observe 10 values: 10ms, 20ms, ..., 100ms
	for i := 1; i <= 10; i++ {
		h.Observe(time.Duration(i*10) * time.Millisecond)
	}

	if h.Count() != 10 {
		t.Errorf("expected count 10, got %d", h.Count())
	}
	// Sum = 550ms = 0.55s
	if math.Abs(h.Sum()-0.55) > 0.001 {
		t.Errorf("expected sum ~0.55s, got %f", h.Sum())
	}
	// Mean ~ 55ms
	if math.Abs(h.Mean()-0.055) > 0.001 {
		t.Errorf("expected mean ~0.055s, got %f", h.Mean())
	}
	// P50 should be around 0.05s (50ms)
	p50 := h.Quantile(0.50)
	if p50 < 0.04 || p50 > 0.07 {
		t.Errorf("expected p50 near 0.055s, got %f", p50)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Registry snapshot / format tests
// ──────────────────────────────────────────────────────────────────────────────

func TestRegistry_Snapshot(t *testing.T) {
	reg := metrics.NewRegistry()
	c := reg.Counter("total_rpc_failures", nil)
	g := reg.Gauge("active_tasks", nil)
	h := reg.Histogram("reconcile_duration", nil)

	c.Add(3)
	g.Set(7)
	h.Observe(100 * time.Millisecond)
	h.Observe(200 * time.Millisecond)

	snap := reg.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 metrics in snapshot, got %d", len(snap))
	}

	found := map[string]bool{}
	for _, mv := range snap {
		found[mv.Name] = true
	}
	for _, name := range []string{"total_rpc_failures", "active_tasks", "reconcile_duration"} {
		if !found[name] {
			t.Errorf("metric %q not found in snapshot", name)
		}
	}
}

func TestRegistry_Format(t *testing.T) {
	reg := metrics.NewRegistry()
	reg.Counter("cloudx_controlplane_rpc_failures_total", nil).Add(5)
	reg.Gauge("cloudx_worker_cpu_usage_percent", map[string]string{"worker_id": "wrk-1"}).Set(72.3)
	reg.Histogram("cloudx_controlplane_scheduling_latency_seconds", nil).Observe(50 * time.Millisecond)

	out := reg.Format()
	if !strings.Contains(out, "cloudx_controlplane_rpc_failures_total") {
		t.Errorf("expected counter name in format output, got:\n%s", out)
	}
	if !strings.Contains(out, "72.3") {
		t.Errorf("expected gauge value 72.3 in format output, got:\n%s", out)
	}
	if !strings.Contains(out, "p50") {
		t.Errorf("expected histogram quantiles in format output, got:\n%s", out)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Domain-level metric construction tests
// ──────────────────────────────────────────────────────────────────────────────

func TestControlPlaneMetrics_ObserveReconciliation(t *testing.T) {
	reg := metrics.NewRegistry()
	cpm := metrics.NewControlPlaneMetrics(reg)

	start := time.Now().Add(-50 * time.Millisecond)
	var nilErr error
	cpm.ObserveReconciliation(start, &nilErr)

	if cpm.ReconciliationCycles.Value() != 1 {
		t.Errorf("expected 1 reconciliation cycle, got %d", cpm.ReconciliationCycles.Value())
	}
	if cpm.ReconciliationFailures.Value() != 0 {
		t.Errorf("expected 0 failures, got %d", cpm.ReconciliationFailures.Value())
	}
	if cpm.ReconciliationDuration.Count() != 1 {
		t.Errorf("expected 1 histogram observation, got %d", cpm.ReconciliationDuration.Count())
	}

	// Now fail
	someErr := context.DeadlineExceeded
	cpm.ObserveReconciliation(time.Now(), &someErr)
	if cpm.ReconciliationFailures.Value() != 1 {
		t.Errorf("expected 1 failure, got %d", cpm.ReconciliationFailures.Value())
	}
}

func TestControlPlaneMetrics_ObserveScheduling(t *testing.T) {
	reg := metrics.NewRegistry()
	cpm := metrics.NewControlPlaneMetrics(reg)

	cpm.ObserveScheduling(10*time.Millisecond, false)
	cpm.ObserveScheduling(20*time.Millisecond, true)

	if cpm.SchedulingDecisions.Value() != 2 {
		t.Errorf("expected 2 scheduling decisions, got %d", cpm.SchedulingDecisions.Value())
	}
	if cpm.SchedulingFailures.Value() != 1 {
		t.Errorf("expected 1 scheduling failure, got %d", cpm.SchedulingFailures.Value())
	}
}

func TestWorkerMetrics(t *testing.T) {
	reg := metrics.NewRegistry()
	wm := metrics.NewWorkerMetrics(reg, "wrk-test-001")

	wm.CPUUsagePercent.Set(65.3)
	wm.MemoryUsedBytes.Set(512 * 1024 * 1024)
	wm.ActiveTaskCount.Set(4)
	wm.ProcessRestarts.Add(2)

	if math.Abs(wm.CPUUsagePercent.Value()-65.3) > 0.001 {
		t.Errorf("expected CPU 65.3, got %f", wm.CPUUsagePercent.Value())
	}
	if wm.ActiveTaskCount.Value() != 4 {
		t.Errorf("expected 4 active tasks, got %f", wm.ActiveTaskCount.Value())
	}
	if wm.ProcessRestarts.Value() != 2 {
		t.Errorf("expected 2 restarts, got %d", wm.ProcessRestarts.Value())
	}
}

func TestServiceMetrics(t *testing.T) {
	reg := metrics.NewRegistry()
	sm := metrics.NewServiceMetrics(reg, "svc-001", "api")

	sm.ReplicasDesired.Set(3)
	sm.ReplicasRunning.Set(2)
	sm.HealthFailures.Add(5)
	sm.RestartCount.Add(1)

	if sm.ReplicasDesired.Value() != 3 {
		t.Errorf("expected 3 desired, got %f", sm.ReplicasDesired.Value())
	}
	if sm.ReplicasRunning.Value() != 2 {
		t.Errorf("expected 2 running, got %f", sm.ReplicasRunning.Value())
	}
	if sm.HealthFailures.Value() != 5 {
		t.Errorf("expected 5 health failures, got %d", sm.HealthFailures.Value())
	}
}

func TestJobMetrics_ObserveCompletion(t *testing.T) {
	reg := metrics.NewRegistry()
	jm := metrics.NewJobMetrics(reg)

	jm.ObserveJobCompletion("SUCCEEDED", 500*time.Millisecond)
	jm.ObserveJobCompletion("SUCCEEDED", 300*time.Millisecond)
	jm.ObserveJobCompletion("FAILED", 100*time.Millisecond)
	jm.ObserveJobCompletion("CANCELLED", 50*time.Millisecond)

	if jm.SucceededTotal.Value() != 2 {
		t.Errorf("expected 2 succeeded, got %d", jm.SucceededTotal.Value())
	}
	if jm.FailedTotal.Value() != 1 {
		t.Errorf("expected 1 failed, got %d", jm.FailedTotal.Value())
	}
	if jm.CancelledTotal.Value() != 1 {
		t.Errorf("expected 1 cancelled, got %d", jm.CancelledTotal.Value())
	}
	if jm.ExecutionDuration.Count() != 4 {
		t.Errorf("expected 4 histogram observations, got %d", jm.ExecutionDuration.Count())
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ClusterMetrics integration test
// ──────────────────────────────────────────────────────────────────────────────

func TestClusterMetrics_Snapshot(t *testing.T) {
	reg := metrics.NewRegistry()
	cm := metrics.NewClusterMetrics(reg)

	cm.ControlPlane.ReconciliationCycles.Add(10)
	cm.ControlPlane.SchedulingDecisions.Add(25)
	cm.ControlPlane.RPCFailures.Add(2)
	cm.Jobs.SucceededTotal.Add(8)
	cm.Jobs.FailedTotal.Add(1)

	sm := cm.GetOrCreateServiceMetrics("svc-001", "api")
	sm.ReplicasDesired.Set(3)
	sm.ReplicasRunning.Set(3)

	wm := cm.GetOrCreateWorkerMetrics("wrk-001")
	wm.CPUUsagePercent.Set(40.0)
	wm.ActiveTaskCount.Set(3)

	cm.CollectUptime()

	snap := cm.Snapshot()
	if len(snap) == 0 {
		t.Fatal("expected non-empty snapshot")
	}

	found := map[string]bool{}
	for _, mv := range snap {
		found[mv.Name] = true
	}

	expected := []string{
		"cloudx_controlplane_reconciliation_cycles_total",
		"cloudx_controlplane_scheduling_decisions_total",
		"cloudx_controlplane_rpc_failures_total",
		"cloudx_job_succeeded_total",
		"cloudx_service_replicas_desired",
		"cloudx_worker_cpu_usage_percent",
		"cloudx_cluster_uptime_seconds",
	}
	for _, name := range expected {
		if !found[name] {
			t.Errorf("metric %q missing from snapshot; found: %v", name, snap)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// StoreCollector integration test (uses real SQLite store)
// ──────────────────────────────────────────────────────────────────────────────

func TestStoreCollector_Collect(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cloudx.db")
	ctx := context.Background()
	now := time.Now().UTC()

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	// Setup: 2 workers
	nodeID := id.NewNodeID()
	workerID1 := id.NewWorkerID()
	workerID2 := id.NewWorkerID()

	_ = store.Nodes().Create(ctx, &models.Node{ID: nodeID, Name: "node-1", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: workerID1, NodeID: nodeID, Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: workerID2, NodeID: nodeID, Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	// 1 service with 3 running tasks + 1 failed
	svcID := id.NewServiceID()
	_ = store.Services().Create(ctx, &models.Service{ID: svcID, Name: "api", Status: "ACTIVE", CreatedAt: now, UpdatedAt: now})

	for i := 0; i < 3; i++ {
		_ = store.Tasks().Create(ctx, &models.Task{
			ID:        id.NewTaskID(),
			ServiceID: svcID,
			WorkerID:  workerID1,
			State:     "RUNNING",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	_ = store.Tasks().Create(ctx, &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: svcID,
		WorkerID:  workerID1,
		State:     "FAILED",
		CreatedAt: now,
		UpdatedAt: now,
	})

	// 2 jobs: 1 succeeded, 1 failed
	_ = store.Jobs().Create(ctx, &models.Job{ID: id.NewJobID(), Name: "job-a", Command: "echo a", Status: "SUCCEEDED", CreatedAt: now, UpdatedAt: now})
	_ = store.Jobs().Create(ctx, &models.Job{ID: id.NewJobID(), Name: "job-b", Command: "echo b", Status: "FAILED", CreatedAt: now, UpdatedAt: now})

	// Run collector
	reg := metrics.NewRegistry()
	cm := metrics.NewClusterMetrics(reg)
	sc := metrics.NewStoreCollector(store, cm)

	result, err := sc.Collect(ctx)
	if err != nil {
		t.Fatalf("StoreCollector.Collect failed: %v", err)
	}

	if result.Workers != 2 {
		t.Errorf("expected 2 workers, got %d", result.Workers)
	}
	if result.Services != 1 {
		t.Errorf("expected 1 service, got %d", result.Services)
	}
	if result.Jobs != 2 {
		t.Errorf("expected 2 jobs, got %d", result.Jobs)
	}
	if result.JobsSucceeded != 1 {
		t.Errorf("expected 1 succeeded job, got %d", result.JobsSucceeded)
	}
	if result.JobsFailed != 1 {
		t.Errorf("expected 1 failed job, got %d", result.JobsFailed)
	}

	// api service should show 3 running (FAILED is terminal and excluded)
	svcMetrics := cm.GetOrCreateServiceMetrics(svcID.String(), "api")
	if svcMetrics.ReplicasRunning.Value() != 3 {
		t.Errorf("expected 3 running replicas for api, got %f", svcMetrics.ReplicasRunning.Value())
	}

	// worker1 has 3 running tasks
	wm1 := cm.GetOrCreateWorkerMetrics(workerID1.String())
	if wm1.ActiveTaskCount.Value() != 3 {
		t.Errorf("expected 3 active tasks on worker1, got %f", wm1.ActiveTaskCount.Value())
	}

	// Snapshot should have metrics
	snap := cm.Snapshot()
	if len(snap) == 0 {
		t.Fatal("expected non-empty snapshot after collect")
	}
	t.Logf("Collected %d metrics. Sample text output:\n%s", len(snap), cm.Format())
}
