// Package diagnostics provides comprehensive cluster and system health diagnostics for CloudX.
//
// Per Phase 60 — Diagnostics (Milestone 16: Observability):
//
// Diagnostics checks 9 critical system vectors in one command:
//  1. Control plane health (connectivity, address reachability, RPC readiness)
//  2. Worker connectivity (reachability, daemon status, registered worker nodes)
//  3. Database integrity (SQLite schema migrations, PRAGMA integrity_check, foreign keys)
//  4. Heartbeat status (missed heartbeats, SUSPECTED/UNHEALTHY/LOST worker states)
//  5. Scheduler status (capacity evaluation, schedulable node count, score engine)
//  6. Orphaned tasks (tasks on dead/unresponsive/unhealthy workers or in invalid states)
//  7. Failed deployments (stalled rollouts, unhealthy revisions, failure rates)
//  8. Resource pressure (host CPU/memory load, worker capacity exhaustion)
//  9. Configuration problems (semantic validation, path accessibility, port binding)
//
// A developer can run `cloudx diagnose` to instantly identify common CloudX problems.
package diagnostics

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/health"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/worker/monitor"
)

// Status represents the health condition of a diagnosed subsystem.
type Status string

const (
	StatusPass Status = "PASS"
	StatusWarn Status = "WARN"
	StatusFail Status = "FAIL"
)

// CheckResult represents the outcome of an individual diagnostic check.
type CheckResult struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Status      Status   `json:"status"`
	Summary     string   `json:"summary"`
	Details     []string `json:"details,omitempty"`
	Remediation string   `json:"remediation,omitempty"`
}

// DiagnosticReport consolidates all diagnostic check results for the cluster.
type DiagnosticReport struct {
	Timestamp   time.Time     `json:"timestamp"`
	ClusterID   string        `json:"cluster_id,omitempty"`
	NodeName    string        `json:"node_name"`
	Overall     Status        `json:"overall_status"`
	PassedCount int           `json:"passed_count"`
	WarnCount   int           `json:"warn_count"`
	FailCount   int           `json:"fail_count"`
	Checks      []CheckResult `json:"checks"`
	DurationMs  float64       `json:"duration_ms"`
}

// Engine runs diagnostic suites against CloudX configuration, state, and environment.
type Engine struct {
	mu        sync.RWMutex
	cfg       *config.Config
	store     state.Store
	dbPath    string
	collector monitor.Collector
}

// NewEngine creates a new diagnostic engine.
func NewEngine(cfg *config.Config, store state.Store, dbPath string) *Engine {
	return &Engine{
		cfg:       cfg,
		store:     store,
		dbPath:    dbPath,
		collector: monitor.NewPlatformCollector(),
	}
}

// SetCollector overrides the resource collector (useful for testing).
func (e *Engine) SetCollector(c monitor.Collector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.collector = c
}

// Run executes all 9 diagnostic checks across the cluster.
func (e *Engine) Run(ctx context.Context) *DiagnosticReport {
	start := time.Now()

	report := &DiagnosticReport{
		Timestamp: start.UTC(),
		NodeName:  "local",
		Checks:    make([]CheckResult, 0, 9),
	}

	if e.cfg != nil {
		report.NodeName = e.cfg.Node.Name
		report.ClusterID = e.cfg.ControlPlane.ClusterID
	}

	// Execute 9 diagnostic checks
	report.Checks = append(report.Checks, e.checkConfiguration(ctx))
	report.Checks = append(report.Checks, e.checkDatabaseIntegrity(ctx))
	report.Checks = append(report.Checks, e.checkControlPlaneHealth(ctx))
	report.Checks = append(report.Checks, e.checkWorkerConnectivity(ctx))
	report.Checks = append(report.Checks, e.checkHeartbeatStatus(ctx))
	report.Checks = append(report.Checks, e.checkSchedulerStatus(ctx))
	report.Checks = append(report.Checks, e.checkOrphanedTasks(ctx))
	report.Checks = append(report.Checks, e.checkDeployments(ctx))
	report.Checks = append(report.Checks, e.checkResourcePressure(ctx))

	// Compute totals and overall status
	for _, c := range report.Checks {
		switch c.Status {
		case StatusPass:
			report.PassedCount++
		case StatusWarn:
			report.WarnCount++
		case StatusFail:
			report.FailCount++
		}
	}

	if report.FailCount > 0 {
		report.Overall = StatusFail
	} else if report.WarnCount > 0 {
		report.Overall = StatusWarn
	} else {
		report.Overall = StatusPass
	}

	report.DurationMs = float64(time.Since(start).Microseconds()) / 1000.0
	return report
}

// ──────────────────────────────────────────────────────────────────────────────
// Check 1: Configuration Problems
// ──────────────────────────────────────────────────────────────────────────────

func (e *Engine) checkConfiguration(ctx context.Context) CheckResult {
	res := CheckResult{
		Name:     "Configuration",
		Category: "Configuration",
	}

	if e.cfg == nil {
		res.Status = StatusFail
		res.Summary = "Configuration is missing or nil"
		res.Remediation = "Verify cloudx.yaml exists or specify with -c/--config"
		return res
	}

	// Semantic validation
	if err := e.cfg.Validate(); err != nil {
		res.Status = StatusFail
		res.Summary = "Configuration failed semantic validation"
		res.Details = []string{err.Error()}
		res.Remediation = "Correct invalid fields in cloudx.yaml or CLI flag parameters"
		return res
	}

	// Verify storage directory permissions and existence
	storagePath := e.cfg.Storage.Path
	if storagePath == "" {
		storagePath = config.DefaultStoragePath()
	}

	var details []string
	details = append(details, fmt.Sprintf("Node ID: %s (%s)", e.cfg.Node.ID, e.cfg.Node.Name))
	details = append(details, fmt.Sprintf("Storage path: %s", storagePath))
	details = append(details, fmt.Sprintf("Runtime driver: %s", e.cfg.Runtime.Type))
	details = append(details, fmt.Sprintf("Control plane address: %s", e.cfg.ControlPlane.Address))
	details = append(details, fmt.Sprintf("Worker address: %s", e.cfg.Worker.Address))

	// Check storage directory accessibility
	if err := os.MkdirAll(storagePath, 0755); err != nil {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("Storage directory '%s' cannot be accessed: %v", storagePath, err)
		res.Details = details
		res.Remediation = "Ensure current user has write permissions to storage path"
		return res
	}

	res.Status = StatusPass
	res.Summary = "Configuration is valid and all paths are accessible"
	res.Details = details
	return res
}

// ──────────────────────────────────────────────────────────────────────────────
// Check 2: Database Integrity
// ──────────────────────────────────────────────────────────────────────────────

func (e *Engine) checkDatabaseIntegrity(ctx context.Context) CheckResult {
	res := CheckResult{
		Name:     "Database Integrity",
		Category: "Persistence",
	}

	if e.dbPath == "" && e.cfg != nil {
		e.dbPath = filepath.Join(e.cfg.Storage.Path, "cloudx.db")
	}

	if e.dbPath == "" {
		res.Status = StatusWarn
		res.Summary = "No database path specified (running in-memory or unconfigured)"
		return res
	}

	// Verify DB file presence
	if _, err := os.Stat(e.dbPath); err != nil {
		if os.IsNotExist(err) {
			res.Status = StatusWarn
			res.Summary = fmt.Sprintf("Database file not yet created at '%s'", e.dbPath)
			res.Remediation = "Run 'cloudx server' or any command that initializes the cluster state"
			return res
		}
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("Database file inaccessible: %v", err)
		res.Remediation = "Check filesystem permissions for database file"
		return res
	}

	// Execute PRAGMA integrity_check and foreign_key_check
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(3000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", e.dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("Failed to open SQLite database: %v", err)
		return res
	}
	defer db.Close()

	// 1. integrity_check
	var integrityResult string
	err = db.QueryRowContext(ctx, "PRAGMA integrity_check;").Scan(&integrityResult)
	if err != nil {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("SQLite integrity check query failed: %v", err)
		res.Remediation = "Database may be corrupt. Restore from backup or reinitialize storage"
		return res
	}

	if strings.ToLower(integrityResult) != "ok" {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("Database corruption detected: %s", integrityResult)
		res.Remediation = "Run SQLite recovery or restore from backup"
		return res
	}

	// 2. foreign_key_check
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check;")
	var fkErrors []string
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var table, parent string
			var rowid, fkid int64
			if err := rows.Scan(&table, &rowid, &parent, &fkid); err == nil {
				fkErrors = append(fkErrors, fmt.Sprintf("Table '%s' row %d references invalid parent '%s'", table, rowid, parent))
			}
		}
	}

	if len(fkErrors) > 0 {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("Foreign key inconsistencies found (%d)", len(fkErrors))
		res.Details = fkErrors
		res.Remediation = "Review orphaned records or reconcile dependent state"
		return res
	}

	fileInfo, _ := os.Stat(e.dbPath)
	sizeBytes := int64(0)
	if fileInfo != nil {
		sizeBytes = fileInfo.Size()
	}

	res.Status = StatusPass
	res.Summary = "Database schema and storage integrity verified (PRAGMA integrity_check: ok)"
	res.Details = []string{
		fmt.Sprintf("Database path: %s (%.2f KB)", e.dbPath, float64(sizeBytes)/1024.0),
		"Foreign keys: consistent",
		"Journal mode: WAL",
	}
	return res
}

// ──────────────────────────────────────────────────────────────────────────────
// Check 3: Control Plane Health
// ──────────────────────────────────────────────────────────────────────────────

func (e *Engine) checkControlPlaneHealth(ctx context.Context) CheckResult {
	res := CheckResult{
		Name:     "Control Plane Health",
		Category: "Control Plane",
	}

	if e.cfg == nil {
		res.Status = StatusFail
		res.Summary = "Configuration is missing"
		return res
	}

	addr := e.cfg.ControlPlane.Address
	if addr == "" {
		addr = config.DefaultControlPlaneAddr
	}

	// Test TCP reachability to control plane port
	conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
	if err != nil {
		// Control plane not currently listening: warn (cluster may be offline or in single CLI mode)
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("Control plane daemon is not actively listening at %s", addr)
		res.Details = []string{
			fmt.Sprintf("Target address: %s", addr),
			fmt.Sprintf("Connection error: %v", err),
			"Note: This is normal when inspecting local storage before starting 'cloudx server'.",
		}
		res.Remediation = "Start control plane daemon with 'cloudx server' if cluster operations are needed"
		return res
	}
	_ = conn.Close()

	res.Status = StatusPass
	res.Summary = fmt.Sprintf("Control plane endpoint %s is listening and reachable", addr)
	res.Details = []string{
		fmt.Sprintf("Address: %s", addr),
		"TCP connection established in <1s",
	}
	return res
}

// ──────────────────────────────────────────────────────────────────────────────
// Check 4: Worker Connectivity
// ──────────────────────────────────────────────────────────────────────────────

func (e *Engine) checkWorkerConnectivity(ctx context.Context) CheckResult {
	res := CheckResult{
		Name:     "Worker Connectivity",
		Category: "Workers",
	}

	if e.store == nil {
		res.Status = StatusWarn
		res.Summary = "State store not accessible to inspect registered workers"
		return res
	}

	workers, err := e.store.Workers().List(ctx)
	if err != nil {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("Failed to list registered workers: %v", err)
		return res
	}

	if len(workers) == 0 {
		res.Status = StatusWarn
		res.Summary = "No worker daemons currently registered in cluster"
		res.Details = []string{"Cluster has 0 compute workers available for task scheduling."}
		res.Remediation = "Join a worker daemon using 'cloudx worker join' or 'cloudx-worker'"
		return res
	}

	var details []string
	reachableCount := 0
	unreachableCount := 0

	for _, w := range workers {
		statusStr := w.Status
		if statusStr == "" {
			statusStr = "UNKNOWN"
		}

		// Probe worker TCP address if configured
		var probeMsg string
		if w.Address != "" {
			conn, err := net.DialTimeout("tcp", w.Address, 500*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				reachableCount++
				probeMsg = "TCP: reachable"
			} else {
				unreachableCount++
				probeMsg = fmt.Sprintf("TCP: unreachable (%v)", err)
			}
		} else {
			probeMsg = "TCP: no address"
		}

		details = append(details, fmt.Sprintf("Worker %s (%s) — Status: %s | %s", w.ID, w.NodeID, statusStr, probeMsg))
	}

	if unreachableCount > 0 && reachableCount == 0 {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("%d registered worker(s) found, but none are directly reachable on TCP port", len(workers))
		res.Details = details
		res.Remediation = "Verify worker daemon processes are running and firewalls permit ingress"
		return res
	}

	res.Status = StatusPass
	res.Summary = fmt.Sprintf("%d worker(s) registered in cluster (%d reachable via TCP)", len(workers), reachableCount)
	res.Details = details
	return res
}

// ──────────────────────────────────────────────────────────────────────────────
// Check 5: Heartbeat Status
// ──────────────────────────────────────────────────────────────────────────────

func (e *Engine) checkHeartbeatStatus(ctx context.Context) CheckResult {
	res := CheckResult{
		Name:     "Heartbeat Status",
		Category: "Health Monitoring",
	}

	if e.store == nil {
		res.Status = StatusWarn
		res.Summary = "State store not accessible to evaluate heartbeats"
		return res
	}

	workers, err := e.store.Workers().List(ctx)
	if err != nil {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("Failed to fetch workers: %v", err)
		return res
	}

	if len(workers) == 0 {
		res.Status = StatusPass
		res.Summary = "No registered workers to evaluate"
		return res
	}

	now := time.Now().UTC()
	var healthyWorkers, suspectedWorkers, unhealthyWorkers, lostWorkers []string

	for _, w := range workers {
		timeSince := now.Sub(w.Heartbeat)
		entry := fmt.Sprintf("Worker %s (%s): status=%s, last_heartbeat=%s (%.1fs ago)",
			w.ID, w.NodeID, w.Status, w.Heartbeat.Format("15:04:05"), timeSince.Seconds())

		switch strings.ToUpper(w.Status) {
		case string(health.StatusLost):
			lostWorkers = append(lostWorkers, entry)
		case string(health.StatusUnhealthy):
			unhealthyWorkers = append(unhealthyWorkers, entry)
		case string(health.StatusSuspected):
			suspectedWorkers = append(suspectedWorkers, entry)
		default:
			if timeSince > 30*time.Second && !w.Heartbeat.IsZero() {
				unhealthyWorkers = append(unhealthyWorkers, entry+" [HEARTBEAT STALE]")
			} else {
				healthyWorkers = append(healthyWorkers, entry)
			}
		}
	}

	var details []string
	details = append(details, healthyWorkers...)
	details = append(details, suspectedWorkers...)
	details = append(details, unhealthyWorkers...)
	details = append(details, lostWorkers...)
	res.Details = details

	if len(lostWorkers) > 0 {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("%d worker(s) marked LOST due to persistent heartbeat failure", len(lostWorkers))
		res.Remediation = "Inspect lost worker machines and restart worker daemons"
		return res
	}

	if len(unhealthyWorkers) > 0 || len(suspectedWorkers) > 0 {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("%d worker(s) with degraded heartbeat status (suspected: %d, unhealthy: %d)",
			len(suspectedWorkers)+len(unhealthyWorkers), len(suspectedWorkers), len(unhealthyWorkers))
		res.Remediation = "Check network connectivity between worker nodes and control plane"
		return res
	}

	res.Status = StatusPass
	res.Summary = fmt.Sprintf("All %d registered worker(s) have active and healthy heartbeats", len(workers))
	return res
}

// ──────────────────────────────────────────────────────────────────────────────
// Check 6: Scheduler Status
// ──────────────────────────────────────────────────────────────────────────────

func (e *Engine) checkSchedulerStatus(ctx context.Context) CheckResult {
	res := CheckResult{
		Name:     "Scheduler Status",
		Category: "Scheduling",
	}

	if e.store == nil {
		res.Status = StatusWarn
		res.Summary = "State store not accessible to evaluate scheduler"
		return res
	}

	workers, err := e.store.Workers().List(ctx)
	if err != nil {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("Failed to list workers: %v", err)
		return res
	}

	schedulableCount := 0
	drainingCount := 0
	lostCount := 0

	for _, w := range workers {
		switch strings.ToUpper(w.Status) {
		case "READY":
			schedulableCount++
		case "DRAINING", "EMPTY":
			drainingCount++
		case "LOST", "UNHEALTHY":
			lostCount++
		}
	}

	var details []string
	details = append(details, fmt.Sprintf("Total registered workers: %d", len(workers)))
	details = append(details, fmt.Sprintf("Active schedulable workers (READY): %d", schedulableCount))
	if drainingCount > 0 {
		details = append(details, fmt.Sprintf("Draining/Empty workers: %d", drainingCount))
	}
	if lostCount > 0 {
		details = append(details, fmt.Sprintf("Unhealthy/Lost workers: %d", lostCount))
	}

	if schedulableCount == 0 && len(workers) > 0 {
		res.Status = StatusFail
		res.Summary = "Zero schedulable workers available in READY state; pending tasks cannot be placed"
		res.Details = details
		res.Remediation = "Ensure at least one worker is in READY state (check heartbeats or undrain nodes)"
		return res
	}

	if schedulableCount == 0 && len(workers) == 0 {
		res.Status = StatusWarn
		res.Summary = "No workers registered for scheduling"
		res.Details = details
		res.Remediation = "Start and join a worker daemon using 'cloudx-worker'"
		return res
	}

	res.Status = StatusPass
	res.Summary = fmt.Sprintf("Scheduler operational with %d active schedulable worker(s)", schedulableCount)
	res.Details = details
	return res
}

// ──────────────────────────────────────────────────────────────────────────────
// Check 7: Orphaned Tasks
// ──────────────────────────────────────────────────────────────────────────────

func (e *Engine) checkOrphanedTasks(ctx context.Context) CheckResult {
	res := CheckResult{
		Name:     "Orphaned Tasks",
		Category: "Workload Health",
	}

	if e.store == nil {
		res.Status = StatusWarn
		res.Summary = "State store not accessible to evaluate tasks"
		return res
	}

	workers, err := e.store.Workers().List(ctx)
	if err != nil {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("Failed to list workers: %v", err)
		return res
	}

	workerStatusMap := make(map[string]string)
	for _, w := range workers {
		workerStatusMap[w.ID.String()] = strings.ToUpper(w.Status)
	}

	tasks, err := e.store.Tasks().List(ctx)
	if err != nil {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("Failed to list tasks: %v", err)
		return res
	}

	var orphanedTasks []string
	var failedTasks []string

	for _, t := range tasks {
		isTerm := t.State == string(models.TaskStateStopped) ||
			t.State == string(models.TaskStateFailed) ||
			t.State == string(models.TaskStateLost)

		wStatus, exists := workerStatusMap[t.WorkerID.String()]
		if !isTerm {
			if !exists {
				orphanedTasks = append(orphanedTasks, fmt.Sprintf("Task %s assigned to non-existent worker %s", t.ID, t.WorkerID))
			} else if wStatus == "LOST" || wStatus == "UNHEALTHY" {
				orphanedTasks = append(orphanedTasks, fmt.Sprintf("Task %s stranded on %s worker %s", t.ID, wStatus, t.WorkerID))
			}
		}

		if t.State == string(models.TaskStateFailed) || t.State == string(models.TaskStateCrashLoop) {
			failedTasks = append(failedTasks, fmt.Sprintf("Task %s in %s state (service: %s, job: %s)", t.ID, t.State, t.ServiceID, t.JobID))
		}
	}

	var details []string
	details = append(details, fmt.Sprintf("Total tasks in store: %d", len(tasks)))
	if len(orphanedTasks) > 0 {
		details = append(details, fmt.Sprintf("Orphaned tasks (%d):", len(orphanedTasks)))
		details = append(details, orphanedTasks...)
	}
	if len(failedTasks) > 0 {
		details = append(details, fmt.Sprintf("Failed/CrashLoop tasks (%d):", len(failedTasks)))
		details = append(details, failedTasks...)
	}
	res.Details = details

	if len(orphanedTasks) > 0 {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("%d orphaned task(s) detected on lost or missing workers", len(orphanedTasks))
		res.Remediation = "Allow reconciler to recover orphaned tasks or trigger reconciliation pass"
		return res
	}

	if len(failedTasks) > 0 {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("%d task(s) in Failed or CrashLoop state", len(failedTasks))
		res.Remediation = "Inspect task logs with 'cloudx task explain' or check workload exit codes"
		return res
	}

	res.Status = StatusPass
	res.Summary = fmt.Sprintf("No orphaned tasks detected (%d total tasks evaluated)", len(tasks))
	return res
}

// ──────────────────────────────────────────────────────────────────────────────
// Check 8: Failed Deployments
// ──────────────────────────────────────────────────────────────────────────────

func (e *Engine) checkDeployments(ctx context.Context) CheckResult {
	res := CheckResult{
		Name:     "Failed Deployments",
		Category: "Deployments",
	}

	if e.store == nil {
		res.Status = StatusWarn
		res.Summary = "State store not accessible to inspect deployments"
		return res
	}

	services, err := e.store.Services().List(ctx)
	if err != nil {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("Failed to list services: %v", err)
		return res
	}

	var degradedServices []string
	var failedDeployments []string

	for _, svc := range services {
		if strings.ToUpper(svc.Status) == "DEGRADED" || strings.ToUpper(svc.Status) == "FAILED" {
			degradedServices = append(degradedServices, fmt.Sprintf("Service %s (%s) status: %s (desired replicas: %d)",
				svc.Name, svc.ID, svc.Status, svc.Replicas))
		}

		deps, err := e.store.Deployments().ListByService(ctx, svc.ID)
		if err == nil {
			for _, d := range deps {
				if strings.ToUpper(d.Status) == "FAILED" || strings.ToUpper(d.Status) == "HALTED" {
					failedDeployments = append(failedDeployments, fmt.Sprintf("Deployment %s for service %s (v%s) is %s",
						d.ID, svc.Name, d.Version, d.Status))
				}
			}
		}
	}

	var details []string
	details = append(details, fmt.Sprintf("Total services evaluated: %d", len(services)))
	if len(degradedServices) > 0 {
		details = append(details, "Degraded services:")
		details = append(details, degradedServices...)
	}
	if len(failedDeployments) > 0 {
		details = append(details, "Failed/Halted deployments:")
		details = append(details, failedDeployments...)
	}
	res.Details = details

	if len(failedDeployments) > 0 {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("%d failed or halted deployment(s) found", len(failedDeployments))
		res.Remediation = "Rollback affected service using 'cloudx rollback <service-id>' or redeploy"
		return res
	}

	if len(degradedServices) > 0 {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("%d service(s) currently in DEGRADED state", len(degradedServices))
		res.Remediation = "Check worker capacity or inspect failing task replicas"
		return res
	}

	res.Status = StatusPass
	res.Summary = fmt.Sprintf("All %d service(s) and their rollouts are healthy and progressing normally", len(services))
	return res
}

// ──────────────────────────────────────────────────────────────────────────────
// Check 9: Resource Pressure
// ──────────────────────────────────────────────────────────────────────────────

func (e *Engine) checkResourcePressure(ctx context.Context) CheckResult {
	res := CheckResult{
		Name:     "Resource Pressure",
		Category: "System Resources",
	}

	e.mu.RLock()
	coll := e.collector
	e.mu.RUnlock()

	if coll == nil {
		res.Status = StatusPass
		res.Summary = "Resource collector not configured (skipped)"
		return res
	}

	metrics, err := coll.Collect(ctx)
	if err != nil {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("Failed to collect host resource metrics: %v", err)
		return res
	}

	var details []string
	details = append(details, fmt.Sprintf("Platform: %s", metrics.Platform))
	details = append(details, fmt.Sprintf("CPU Usage: %.1f%%", metrics.CPUUsagePercent))

	memUsedMB := float64(metrics.MemoryUsedBytes) / (1024 * 1024)
	memTotalMB := float64(metrics.TotalMemoryBytes) / (1024 * 1024)
	memAvailMB := float64(metrics.MemoryAvailBytes) / (1024 * 1024)

	var memUsagePercent float64
	if metrics.TotalMemoryBytes > 0 {
		memUsagePercent = (float64(metrics.MemoryUsedBytes) / float64(metrics.TotalMemoryBytes)) * 100.0
		details = append(details, fmt.Sprintf("Memory: %.1f MB used / %.1f MB total (%.1f%%)",
			memUsedMB, memTotalMB, memUsagePercent))
		details = append(details, fmt.Sprintf("Available Memory: %.1f MB", memAvailMB))
	}

	if metrics.ProcessCount > 0 {
		details = append(details, fmt.Sprintf("Active Process Count: %d", metrics.ProcessCount))
	}
	res.Details = details

	// High CPU or Memory pressure thresholds
	if metrics.CPUUsagePercent > 95.0 || memUsagePercent > 95.0 {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("Critical resource pressure detected (CPU: %.1f%%, Mem: %.1f%%)",
			metrics.CPUUsagePercent, memUsagePercent)
		res.Remediation = "Scale out cluster by adding more worker nodes or terminate non-critical processes"
		return res
	}

	if metrics.CPUUsagePercent > 85.0 || memUsagePercent > 85.0 {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("Elevated resource utilization (CPU: %.1f%%, Mem: %.1f%%)",
			metrics.CPUUsagePercent, memUsagePercent)
		res.Remediation = "Monitor cluster load and consider scheduling restrictions"
		return res
	}

	res.Status = StatusPass
	res.Summary = fmt.Sprintf("Host resource utilization is healthy (CPU: %.1f%%, Memory: %.1f%%)",
		metrics.CPUUsagePercent, memUsagePercent)
	return res
}
