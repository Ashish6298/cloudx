package health

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// WorkerHealthStatus represents the lifecycle health state of a worker tracked by the control plane.
type WorkerHealthStatus string

const (
	StatusReady      WorkerHealthStatus = "READY"
	StatusSuspected  WorkerHealthStatus = "SUSPECTED"
	StatusUnhealthy  WorkerHealthStatus = "UNHEALTHY"
	StatusLost       WorkerHealthStatus = "LOST"
)

// FailureDetectorConfig configures the thresholds and timings for worker failure detection.
type FailureDetectorConfig struct {
	CheckInterval     time.Duration // Frequency of health evaluation sweep (e.g. 1s)
	SuspectedTimeout  time.Duration // Time without heartbeat before transitioning READY -> SUSPECTED (e.g. 3 * interval)
	UnhealthyTimeout  time.Duration // Time without heartbeat before transitioning SUSPECTED -> UNHEALTHY (e.g. 6 * interval)
	LostTimeout       time.Duration // Time without heartbeat before transitioning UNHEALTHY -> LOST (e.g. 10 * interval)
}

// DefaultFailureDetectorConfig returns production defaults based on a 5s heartbeat interval.
func DefaultFailureDetectorConfig() FailureDetectorConfig {
	return FailureDetectorConfig{
		CheckInterval:    1 * time.Second,
		SuspectedTimeout: 15 * time.Second, // 3 missed heartbeats
		UnhealthyTimeout: 30 * time.Second, // 6 missed heartbeats
		LostTimeout:      60 * time.Second, // 12 missed heartbeats
	}
}

// FailureDetector evaluates worker liveness across heartbeat timestamps and updates persistent cluster state.
type FailureDetector struct {
	mu     sync.RWMutex
	cfg    FailureDetectorConfig
	store  state.Store
	logger logging.Logger
	cancel context.CancelFunc
}

// NewFailureDetector constructs a new FailureDetector.
func NewFailureDetector(cfg FailureDetectorConfig, store state.Store, logger logging.Logger) *FailureDetector {
	if logger == nil {
		logger = logging.NewDefaultLogger()
	}
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = 1 * time.Second
	}
	if cfg.SuspectedTimeout <= 0 {
		cfg.SuspectedTimeout = 3 * time.Second
	}
	if cfg.UnhealthyTimeout <= 0 {
		cfg.UnhealthyTimeout = 6 * time.Second
	}
	if cfg.LostTimeout <= 0 {
		cfg.LostTimeout = 10 * time.Second
	}

	return &FailureDetector{
		cfg:    cfg,
		store:  store,
		logger: logger.With("component", "failure_detector"),
	}
}

// Start runs the periodic failure detection evaluation loop until context cancellation.
func (fd *FailureDetector) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	fd.mu.Lock()
	fd.cancel = cancel
	fd.mu.Unlock()

	fd.logger.Info("Starting Failure Detector loop (check interval: %v, suspected: %v, unhealthy: %v, lost: %v)",
		fd.cfg.CheckInterval, fd.cfg.SuspectedTimeout, fd.cfg.UnhealthyTimeout, fd.cfg.LostTimeout)

	go fd.loop(runCtx)
	return nil
}

// Stop terminates the failure detector loop.
func (fd *FailureDetector) Stop(ctx context.Context) error {
	fd.mu.Lock()
	if fd.cancel != nil {
		fd.cancel()
	}
	fd.mu.Unlock()
	fd.logger.Info("Failure Detector stopped")
	return nil
}

func (fd *FailureDetector) loop(ctx context.Context) {
	ticker := time.NewTicker(fd.cfg.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fd.EvaluateWorkers(ctx, time.Now().UTC())
		}
	}
}

// EvaluateWorkers sweeps all workers in persistent state and updates health statuses based on elapsed time since last heartbeat.
func (fd *FailureDetector) EvaluateWorkers(ctx context.Context, now time.Time) {
	if fd.store == nil {
		return
	}

	workers, err := fd.store.Workers().List(ctx)
	if err != nil {
		fd.logger.Error("Failed to list workers for health evaluation: %v", err)
		return
	}

	for _, w := range workers {
		if w.Heartbeat.IsZero() {
			continue
		}

		elapsed := now.Sub(w.Heartbeat)
		var targetStatus WorkerHealthStatus

		switch {
		case elapsed >= fd.cfg.LostTimeout:
			targetStatus = StatusLost
		case elapsed >= fd.cfg.UnhealthyTimeout:
			targetStatus = StatusUnhealthy
		case elapsed >= fd.cfg.SuspectedTimeout:
			targetStatus = StatusSuspected
		default:
			targetStatus = StatusReady
		}

		if w.Status != string(targetStatus) {
			oldStatus := w.Status
			w.Status = string(targetStatus)
			w.UpdatedAt = now

			if err := fd.store.Workers().Update(ctx, w); err != nil {
				fd.logger.Error("Failed to update worker %s status to %s: %v", w.ID, targetStatus, err)
			} else {
				fd.logger.Warn("Worker %s state transitioned: %s -> %s (elapsed since heartbeat: %v)",
					w.ID, oldStatus, targetStatus, elapsed.Round(time.Millisecond))

				// Append state transition event
				eventType := fmt.Sprintf("WORKER_STATUS_%s", targetStatus)
				if targetStatus == StatusLost {
					eventType = "WORKER_LOST"
				}
				_ = fd.store.Events().Append(ctx, &models.Event{
					ID:        id.NewEventID(),
					Type:      eventType,
					Source:    "failure_detector",
					EntityID:  w.ID,
					Payload:   fmt.Sprintf(`{"previous":"%s","current":"%s","elapsed_ms":%d}`, oldStatus, targetStatus, elapsed.Milliseconds()),
					CreatedAt: now,
				})
			}
		}
	}
}
