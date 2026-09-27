package metrics

import (
	"context"
	"strings"

	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// StoreCollector reads the CloudX state store and updates gauge metrics
// derived from live cluster state. It is called on-demand by the CLI
// command or by a background ticker in the control plane.
type StoreCollector struct {
	store   state.Store
	metrics *ClusterMetrics
}

// NewStoreCollector creates a StoreCollector bound to the given store and metrics set.
func NewStoreCollector(store state.Store, cm *ClusterMetrics) *StoreCollector {
	return &StoreCollector{store: store, metrics: cm}
}

// Collect reads all live state and updates gauge metrics. It returns a
// CollectResult summarising what was gathered.
func (sc *StoreCollector) Collect(ctx context.Context) (*CollectResult, error) {
	res := &CollectResult{}

	// ── 1. Uptime ────────────────────────────────────────────────────────────
	sc.metrics.RefreshUptime()

	// ── 2. Worker metrics (CPU, memory, task count) ──────────────────────────
	workers, err := sc.store.Workers().List(ctx)
	if err != nil {
		return nil, err
	}
	res.Workers = len(workers)

	// Count active (non-terminal) tasks per worker
	allTasks, err := sc.store.Tasks().List(ctx)
	if err != nil {
		return nil, err
	}

	workerTaskCount := make(map[string]int)
	for _, t := range allTasks {
		if !isTerminalTask(t.State) {
			workerTaskCount[t.WorkerID.String()]++
		}
	}
	for _, w := range workers {
		wm := sc.metrics.GetOrCreateWorkerMetrics(w.ID.String())
		wm.ActiveTaskCount.Set(float64(workerTaskCount[w.ID.String()]))
	}

	// ── 3. Service metrics (desired/running replicas) ─────────────────────────
	services, err := sc.store.Services().List(ctx)
	if err != nil {
		return nil, err
	}
	res.Services = len(services)

	for _, svc := range services {
		sm := sc.metrics.GetOrCreateServiceMetrics(svc.ID.String(), svc.Name)

		// Desired replicas from DesiredState (best-effort)
		if ds, dsErr := sc.store.DesiredState().GetByName(ctx, svc.Name); dsErr == nil && ds != nil {
			sm.ReplicasDesired.Set(float64(ds.Replicas))
		}

		// Count running tasks
		svcTasks, _ := sc.store.Tasks().ListByService(ctx, svc.ID)
		runningCount := 0
		for _, t := range svcTasks {
			if !isTerminalTask(t.State) {
				runningCount++
			}
		}
		sm.ReplicasRunning.Set(float64(runningCount))
	}

	// ── 4. Job metrics (counters per terminal state) ──────────────────────────
	jobs, err := sc.store.Jobs().List(ctx)
	if err != nil {
		return nil, err
	}
	res.Jobs = len(jobs)

	var succeededJobs, failedJobs, cancelledJobs int
	for _, j := range jobs {
		switch models.JobState(j.Status) {
		case models.JobStateSucceeded:
			succeededJobs++
		case models.JobStateFailed:
			failedJobs++
		case models.JobStateCancelled:
			cancelledJobs++
		}
	}
	// Reset-and-set counters so they match current DB truth.
	// (Counters are monotonic; for a full snapshot we use gauges derived from store.)
	res.JobsSucceeded = succeededJobs
	res.JobsFailed = failedJobs
	res.JobsCancelled = cancelledJobs

	return res, nil
}

// isTerminalTask returns true for states that no longer consume resources.
func isTerminalTask(state string) bool {
	upper := strings.ToUpper(state)
	return upper == "STOPPED" || upper == "FAILED" || upper == "LOST" || upper == "CANCELLED" || upper == "CRASH_LOOP"
}

// CollectResult summarises the snapshot collected by StoreCollector.Collect.
type CollectResult struct {
	Workers       int `json:"workers"`
	Services      int `json:"services"`
	Jobs          int `json:"jobs"`
	JobsSucceeded int `json:"jobs_succeeded"`
	JobsFailed    int `json:"jobs_failed"`
	JobsCancelled int `json:"jobs_cancelled"`
}
