package metrics

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Control-Plane Metrics
// ──────────────────────────────────────────────────────────────────────────────

// ControlPlaneMetrics holds all instrumentation counters/gauges/histograms for
// the CloudX control plane.
//
// Tracked per Phase 58:
//   - reconciliation cycles (total, failures)
//   - scheduling latency (histogram)
//   - RPC failures (counter)
//   - state operations (create/update/delete counters)
type ControlPlaneMetrics struct {
	// Reconciliation
	ReconciliationCycles         *Counter   // total reconcile loop iterations
	ReconciliationFailures       *Counter   // reconcile iterations that returned an error
	ReconciliationDuration       *Histogram // wall-clock time per reconcile pass
	ReconciliationTasksCreated   *Counter   // task replicas created across all passes
	ReconciliationTasksRemoved   *Counter   // task replicas removed (scale-down / drain)
	ReconciliationOrphansHandled *Counter   // orphaned-task recoveries

	// Scheduling
	SchedulingDecisions *Counter   // placement decisions made
	SchedulingFailures  *Counter   // scheduling calls that returned an error
	SchedulingLatency   *Histogram // time from requirement evaluation to decision

	// RPC
	RPCFailures *Counter // gRPC/in-process dispatch failures

	// State operations
	StateCreates *Counter // successful store.X().Create calls
	StateUpdates *Counter // successful store.X().Update calls
	StateDeletes *Counter // successful store.X().Delete calls
}

// NewControlPlaneMetrics creates a set of control-plane metrics registered in reg.
func NewControlPlaneMetrics(reg *Registry) *ControlPlaneMetrics {
	cp := "cloudx_controlplane_"
	return &ControlPlaneMetrics{
		ReconciliationCycles:         reg.Counter(cp+"reconciliation_cycles_total", nil),
		ReconciliationFailures:       reg.Counter(cp+"reconciliation_failures_total", nil),
		ReconciliationDuration:       reg.Histogram(cp+"reconciliation_duration_seconds", nil),
		ReconciliationTasksCreated:   reg.Counter(cp+"reconciliation_tasks_created_total", nil),
		ReconciliationTasksRemoved:   reg.Counter(cp+"reconciliation_tasks_removed_total", nil),
		ReconciliationOrphansHandled: reg.Counter(cp+"reconciliation_orphans_handled_total", nil),

		SchedulingDecisions: reg.Counter(cp+"scheduling_decisions_total", nil),
		SchedulingFailures:  reg.Counter(cp+"scheduling_failures_total", nil),
		SchedulingLatency:   reg.Histogram(cp+"scheduling_latency_seconds", nil),

		RPCFailures: reg.Counter(cp+"rpc_failures_total", nil),

		StateCreates: reg.Counter(cp+"state_creates_total", nil),
		StateUpdates: reg.Counter(cp+"state_updates_total", nil),
		StateDeletes: reg.Counter(cp+"state_deletes_total", nil),
	}
}

// ObserveReconciliation is a helper that records a full reconcile pass.
// Call it with defer: defer m.ObserveReconciliation(time.Now(), &err)
func (m *ControlPlaneMetrics) ObserveReconciliation(start time.Time, errPtr *error) {
	m.ReconciliationCycles.Inc()
	m.ReconciliationDuration.Observe(time.Since(start))
	if errPtr != nil && *errPtr != nil {
		m.ReconciliationFailures.Inc()
	}
}

// ObserveScheduling records a scheduling attempt.
func (m *ControlPlaneMetrics) ObserveScheduling(latency time.Duration, failed bool) {
	m.SchedulingDecisions.Inc()
	m.SchedulingLatency.Observe(latency)
	if failed {
		m.SchedulingFailures.Inc()
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Worker Metrics
// ──────────────────────────────────────────────────────────────────────────────

// WorkerMetrics holds per-worker instrumentation.
//
// Tracked per Phase 58:
//   - CPU usage percent (gauge)
//   - memory usage / available bytes (gauges)
//   - active task count (gauge)
//   - process restarts (counter)
type WorkerMetrics struct {
	CPUUsagePercent   *Gauge   // [0, 100]
	MemoryUsedBytes   *Gauge   // bytes currently allocated
	MemoryAvailBytes  *Gauge   // bytes currently available
	MemoryTotalBytes  *Gauge   // total physical RAM
	ActiveTaskCount   *Gauge   // tasks in non-terminal states
	ProcessRestarts   *Counter // cumulative task/process restart events
	HeartbeatsSent    *Counter // heartbeat RPCs dispatched to control plane
	HeartbeatFailures *Counter // heartbeat RPCs that returned an error
}

// NewWorkerMetrics creates a set of worker metrics registered in reg.
// workerID is used as a label for multi-worker scenarios.
func NewWorkerMetrics(reg *Registry, workerID string) *WorkerMetrics {
	w := "cloudx_worker_"
	labels := map[string]string{"worker_id": workerID}
	return &WorkerMetrics{
		CPUUsagePercent:   reg.Gauge(w+"cpu_usage_percent", labels),
		MemoryUsedBytes:   reg.Gauge(w+"memory_used_bytes", labels),
		MemoryAvailBytes:  reg.Gauge(w+"memory_avail_bytes", labels),
		MemoryTotalBytes:  reg.Gauge(w+"memory_total_bytes", labels),
		ActiveTaskCount:   reg.Gauge(w+"active_task_count", labels),
		ProcessRestarts:   reg.Counter(w+"process_restarts_total", labels),
		HeartbeatsSent:    reg.Counter(w+"heartbeats_sent_total", labels),
		HeartbeatFailures: reg.Counter(w+"heartbeat_failures_total", labels),
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Service Metrics
// ──────────────────────────────────────────────────────────────────────────────

// ServiceMetrics holds per-service instrumentation.
//
// Tracked per Phase 58:
//   - replicas_desired (gauge)
//   - replicas_running (gauge)
//   - health_failures  (counter)
//   - restart_count    (counter)
type ServiceMetrics struct {
	ReplicasDesired *Gauge   // target replica count from desired state
	ReplicasRunning *Gauge   // actual non-terminal task count
	HealthFailures  *Counter // total task health-check failures
	RestartCount    *Counter // total task restarts (any restart policy trigger)
}

// NewServiceMetrics creates a set of service metrics registered in reg.
func NewServiceMetrics(reg *Registry, serviceID, serviceName string) *ServiceMetrics {
	s := "cloudx_service_"
	labels := map[string]string{
		"service_id":   serviceID,
		"service_name": serviceName,
	}
	return &ServiceMetrics{
		ReplicasDesired: reg.Gauge(s+"replicas_desired", labels),
		ReplicasRunning: reg.Gauge(s+"replicas_running", labels),
		HealthFailures:  reg.Counter(s+"health_failures_total", labels),
		RestartCount:    reg.Counter(s+"restarts_total", labels),
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Job Metrics
// ──────────────────────────────────────────────────────────────────────────────

// JobMetrics holds cluster-wide job execution instrumentation.
//
// Tracked per Phase 58:
//   - execution_duration  (histogram)
//   - succeeded_total     (counter)
//   - failed_total        (counter)
type JobMetrics struct {
	ExecutionDuration *Histogram // wall-clock time from START to completion
	SucceededTotal    *Counter   // jobs that ended in SUCCEEDED state
	FailedTotal       *Counter   // jobs that ended in FAILED state
	CancelledTotal    *Counter   // jobs cancelled before completion
	RetryTotal        *Counter   // retry attempts triggered by retry policy
}

// NewJobMetrics creates a set of job metrics registered in reg.
func NewJobMetrics(reg *Registry) *JobMetrics {
	j := "cloudx_job_"
	return &JobMetrics{
		ExecutionDuration: reg.Histogram(j+"execution_duration_seconds", nil),
		SucceededTotal:    reg.Counter(j+"succeeded_total", nil),
		FailedTotal:       reg.Counter(j+"failed_total", nil),
		CancelledTotal:    reg.Counter(j+"cancelled_total", nil),
		RetryTotal:        reg.Counter(j+"retries_total", nil),
	}
}

// ObserveJobCompletion records the outcome of a finished job.
// state is expected to be one of "SUCCEEDED", "FAILED", "CANCELLED".
func (m *JobMetrics) ObserveJobCompletion(state string, duration time.Duration) {
	m.ExecutionDuration.Observe(duration)
	switch state {
	case "SUCCEEDED":
		m.SucceededTotal.Inc()
	case "FAILED":
		m.FailedTotal.Inc()
	case "CANCELLED":
		m.CancelledTotal.Inc()
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ClusterMetrics — convenience wrapper grouping all domains
// ──────────────────────────────────────────────────────────────────────────────

// ClusterMetrics groups all domain-level metrics and is the recommended entry
// point for the control-plane and CLI.
type ClusterMetrics struct {
	Registry     *Registry
	ControlPlane *ControlPlaneMetrics
	Jobs         *JobMetrics

	// Per-service and per-worker metrics are created dynamically; use
	// GetOrCreateServiceMetrics / GetOrCreateWorkerMetrics.
	svcMu  sync.RWMutex
	svcMap map[string]*ServiceMetrics // key: serviceID
	wkMu   sync.RWMutex
	wkMap  map[string]*WorkerMetrics // key: workerID

	// Runtime uptime
	startedAt int64 // Unix nano — used to compute UptimeSeconds gauge
	Uptime    *Gauge
}

// Global cluster-level metrics singleton (lazy init via NewClusterMetrics).
var globalMu sync.RWMutex
var globalMetrics *ClusterMetrics

// NewClusterMetrics creates and returns the cluster-wide metrics set.
// Calling it multiple times with the same registry is safe (idempotent).
func NewClusterMetrics(reg *Registry) *ClusterMetrics {
	if reg == nil {
		reg = Default
	}
	cm := &ClusterMetrics{
		Registry:     reg,
		ControlPlane: NewControlPlaneMetrics(reg),
		Jobs:         NewJobMetrics(reg),
		svcMap:       make(map[string]*ServiceMetrics),
		wkMap:        make(map[string]*WorkerMetrics),
		startedAt:    time.Now().UnixNano(),
		Uptime:       reg.Gauge("cloudx_cluster_uptime_seconds", nil),
	}
	globalMu.Lock()
	globalMetrics = cm
	globalMu.Unlock()
	return cm
}

// Global returns the process-wide ClusterMetrics instance, creating a default
// one from Default registry if none has been initialised.
func Global() *ClusterMetrics {
	globalMu.RLock()
	if globalMetrics != nil {
		defer globalMu.RUnlock()
		return globalMetrics
	}
	globalMu.RUnlock()
	return NewClusterMetrics(Default)
}

// GetOrCreateServiceMetrics returns the ServiceMetrics for serviceID, creating
// it lazily if not yet registered.
func (cm *ClusterMetrics) GetOrCreateServiceMetrics(serviceID, serviceName string) *ServiceMetrics {
	cm.svcMu.Lock()
	defer cm.svcMu.Unlock()
	if m, ok := cm.svcMap[serviceID]; ok {
		return m
	}
	m := NewServiceMetrics(cm.Registry, serviceID, serviceName)
	cm.svcMap[serviceID] = m
	return m
}

// GetOrCreateWorkerMetrics returns the WorkerMetrics for workerID, creating
// it lazily if not yet registered.
func (cm *ClusterMetrics) GetOrCreateWorkerMetrics(workerID string) *WorkerMetrics {
	cm.wkMu.Lock()
	defer cm.wkMu.Unlock()
	if m, ok := cm.wkMap[workerID]; ok {
		return m
	}
	m := NewWorkerMetrics(cm.Registry, workerID)
	cm.wkMap[workerID] = m
	return m
}

// RefreshUptime updates the uptime gauge to the current elapsed seconds.
func (cm *ClusterMetrics) RefreshUptime() {
	elapsed := time.Duration(time.Now().UnixNano() - atomic.LoadInt64(&cm.startedAt))
	cm.Uptime.Set(elapsed.Seconds())
}

// CollectUptime is a simple convenience call that updates only the uptime gauge.
// Full store-based collection is done via StoreCollector.Collect (see store_collector.go).
func (cm *ClusterMetrics) CollectUptime() {
	cm.RefreshUptime()
}

// Snapshot returns a point-in-time copy of all metrics in the registry.
func (cm *ClusterMetrics) Snapshot() []MetricValue {
	return cm.Registry.Snapshot()
}

// Format renders all metrics to human-readable text.
func (cm *ClusterMetrics) Format() string {
	return cm.Registry.Format()
}

// ensure context is used
var _ context.Context = (context.Context)(nil)
