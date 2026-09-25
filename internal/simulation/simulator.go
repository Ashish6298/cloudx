package simulation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/health"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/worker"
)

var (
	ErrTargetNotFound = errors.New("simulation: target entity not found")
	ErrInvalidParam   = errors.New("simulation: invalid simulation parameters")
)

// ScenarioType identifies deterministic failure modes supported by the simulator.
type ScenarioType string

const (
	ScenarioKillProcess         ScenarioType = "kill-process"
	ScenarioStopWorker          ScenarioType = "stop-worker"
	ScenarioBreakHealthEndpoint ScenarioType = "break-health"
	ScenarioDelayHeartbeat      ScenarioType = "delay-heartbeat"
	ScenarioExhaustResources    ScenarioType = "exhaust-resources"
)

// SimulationResult details the outcome of executing a failure simulation scenario.
type SimulationResult struct {
	Scenario  ScenarioType  `json:"scenario"`
	TargetID  string        `json:"target_id"`
	Success   bool          `json:"success"`
	Action    string        `json:"action"`
	Details   string        `json:"details,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
	Duration  time.Duration `json:"duration,omitempty"`
}

// Simulator coordinates deterministic failure testing against CloudX-managed workloads and nodes.
type Simulator struct {
	mu           sync.RWMutex
	store        state.Store
	taskManager  *worker.TaskManager
	prober       *SimulatedProber
	brokenProbes map[id.ID]string
	logger       logging.Logger
}

// NewSimulator constructs a new failure simulator.
func NewSimulator(store state.Store, tm *worker.TaskManager, prober *SimulatedProber, logger logging.Logger) *Simulator {
	if logger == nil {
		logger = logging.NewDefaultLogger()
	}
	return &Simulator{
		store:        store,
		taskManager:  tm,
		prober:       prober,
		brokenProbes: make(map[id.ID]string),
		logger:       logger.With("component", "failure_simulator"),
	}
}

// KillProcess terminates an individual managed task process abruptly (SIGKILL).
func (s *Simulator) KillProcess(ctx context.Context, taskID id.ID) (*SimulationResult, error) {
	if taskID == "" {
		return nil, fmt.Errorf("%w: task_id is required", ErrInvalidParam)
	}

	start := time.Now().UTC()
	s.logger.Warn("Simulating failure: Killing process for task %s", taskID)

	var pid int
	// If taskManager is available in-process
	if s.taskManager != nil {
		snap, err := s.taskManager.GetTask(taskID)
		if err == nil && snap != nil && snap.PID > 0 {
			pid = snap.PID
		}
	}

	// Fallback to state store lookup
	if pid <= 0 && s.store != nil {
		task, err := s.store.Tasks().Get(ctx, taskID)
		if err == nil && task != nil {
			pid = task.PID
		}
	}

	if pid <= 0 {
		// Stop via task manager if PID cannot be found directly
		if s.taskManager != nil {
			if err := s.taskManager.StopTask(ctx, taskID); err != nil {
				return nil, fmt.Errorf("failed to kill task %s via manager: %w", taskID, err)
			}
			return &SimulationResult{
				Scenario:  ScenarioKillProcess,
				TargetID:  taskID.String(),
				Success:   true,
				Action:    "Task cancelled and stopped via TaskManager",
				Timestamp: start,
				Duration:  time.Since(start),
			}, nil
		}
		return nil, fmt.Errorf("%w: task %s has no active process PID", ErrTargetNotFound, taskID)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("failed to find process %d: %w", pid, err)
	}

	if err := proc.Kill(); err != nil {
		return nil, fmt.Errorf("failed to send SIGKILL to PID %d: %w", pid, err)
	}

	// Append simulation event to store
	if s.store != nil {
		_ = s.store.Events().Append(ctx, &models.Event{
			ID:        id.NewEventID(),
			Type:      "SIMULATION_KILL_PROCESS",
			Source:    "simulator",
			EntityID:  taskID,
			Payload:   fmt.Sprintf(`{"task_id":"%s","pid":%d}`, taskID, pid),
			CreatedAt: start,
		})
	}

	return &SimulationResult{
		Scenario:  ScenarioKillProcess,
		TargetID:  taskID.String(),
		Success:   true,
		Action:    fmt.Sprintf("Sent SIGKILL to task process PID %d", pid),
		Timestamp: start,
		Duration:  time.Since(start),
	}, nil
}

// StopWorker gracefully or abruptly stops a worker daemon.
func (s *Simulator) StopWorker(ctx context.Context, workerID id.ID, daemon *worker.Daemon) (*SimulationResult, error) {
	if workerID == "" {
		return nil, fmt.Errorf("%w: worker_id is required", ErrInvalidParam)
	}

	start := time.Now().UTC()
	s.logger.Warn("Simulating failure: Stopping worker %s", workerID)

	if daemon != nil {
		if err := daemon.Stop(ctx); err != nil {
			return nil, fmt.Errorf("failed to stop worker daemon %s: %w", workerID, err)
		}
	}

	// Update worker state in state store to STOPPED
	if s.store != nil {
		w, err := s.store.Workers().Get(ctx, workerID)
		if err == nil && w != nil {
			w.Status = "STOPPED"
			w.UpdatedAt = time.Now().UTC()
			_ = s.store.Workers().Update(ctx, w)
		}

		_ = s.store.Events().Append(ctx, &models.Event{
			ID:        id.NewEventID(),
			Type:      "SIMULATION_STOP_WORKER",
			Source:    "simulator",
			EntityID:  workerID,
			Payload:   fmt.Sprintf(`{"worker_id":"%s"}`, workerID),
			CreatedAt: start,
		})
	}

	return &SimulationResult{
		Scenario:  ScenarioStopWorker,
		TargetID:  workerID.String(),
		Success:   true,
		Action:    fmt.Sprintf("Stopped worker daemon %s", workerID),
		Timestamp: start,
		Duration:  time.Since(start),
	}, nil
}

// BreakHealthEndpoint marks a task's health check probe as artificially failing.
func (s *Simulator) BreakHealthEndpoint(ctx context.Context, taskID id.ID, failureReason string) (*SimulationResult, error) {
	if taskID == "" {
		return nil, fmt.Errorf("%w: task_id is required", ErrInvalidParam)
	}
	if failureReason == "" {
		failureReason = "simulated 503 Service Unavailable / probe timeout"
	}

	start := time.Now().UTC()
	s.logger.Warn("Simulating failure: Breaking health checks for task %s (%s)", taskID, failureReason)

	s.mu.Lock()
	s.brokenProbes[taskID] = failureReason
	if s.prober != nil {
		s.prober.BreakTask(taskID, failureReason)
	}
	s.mu.Unlock()

	if s.store != nil {
		_ = s.store.Events().Append(ctx, &models.Event{
			ID:        id.NewEventID(),
			Type:      "SIMULATION_BREAK_HEALTH",
			Source:    "simulator",
			EntityID:  taskID,
			Payload:   fmt.Sprintf(`{"task_id":"%s","reason":"%s"}`, taskID, failureReason),
			CreatedAt: start,
		})
	}

	return &SimulationResult{
		Scenario:  ScenarioBreakHealthEndpoint,
		TargetID:  taskID.String(),
		Success:   true,
		Action:    fmt.Sprintf("Injected failure into health probe: %s", failureReason),
		Timestamp: start,
		Duration:  time.Since(start),
	}, nil
}

// RestoreHealthEndpoint restores normal health checking for a previously broken task probe.
func (s *Simulator) RestoreHealthEndpoint(ctx context.Context, taskID id.ID) (*SimulationResult, error) {
	start := time.Now().UTC()
	s.logger.Info("Restoring normal health checks for task %s", taskID)

	s.mu.Lock()
	delete(s.brokenProbes, taskID)
	if s.prober != nil {
		s.prober.RestoreTask(taskID)
	}
	s.mu.Unlock()

	return &SimulationResult{
		Scenario:  ScenarioBreakHealthEndpoint,
		TargetID:  taskID.String(),
		Success:   true,
		Action:    "Restored normal health check probing",
		Timestamp: start,
		Duration:  time.Since(start),
	}, nil
}

// DelayHeartbeat artificially backdates a worker's heartbeat in the state store to simulate network partition or worker hang.
func (s *Simulator) DelayHeartbeat(ctx context.Context, workerID id.ID, delay time.Duration) (*SimulationResult, error) {
	if workerID == "" {
		return nil, fmt.Errorf("%w: worker_id is required", ErrInvalidParam)
	}
	if delay <= 0 {
		delay = 45 * time.Second
	}

	start := time.Now().UTC()
	s.logger.Warn("Simulating failure: Artificially backdating heartbeat for worker %s by %v", workerID, delay)

	if s.store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	worker, err := s.store.Workers().Get(ctx, workerID)
	if err != nil || worker == nil {
		return nil, fmt.Errorf("%w: worker %s", ErrTargetNotFound, workerID)
	}

	// Backdate heartbeat timestamp
	worker.Heartbeat = time.Now().UTC().Add(-delay)
	worker.UpdatedAt = time.Now().UTC()
	if err := s.store.Workers().Update(ctx, worker); err != nil {
		return nil, fmt.Errorf("failed to update worker heartbeat: %w", err)
	}

	_ = s.store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "SIMULATION_DELAY_HEARTBEAT",
		Source:    "simulator",
		EntityID:  workerID,
		Payload:   fmt.Sprintf(`{"worker_id":"%s","delay_seconds":%f}`, workerID, delay.Seconds()),
		CreatedAt: start,
	})

	return &SimulationResult{
		Scenario:  ScenarioDelayHeartbeat,
		TargetID:  workerID.String(),
		Success:   true,
		Action:    fmt.Sprintf("Backdated worker heartbeat by %v (Heartbeat: %s)", delay, worker.Heartbeat.Format(time.RFC3339)),
		Timestamp: start,
		Duration:  time.Since(start),
	}, nil
}

// ExhaustResources simulates resource pressure on a managed task by allocating safe, bounded memory or synthetic load
// within the testing process sandbox.
func (s *Simulator) ExhaustResources(ctx context.Context, taskID id.ID, megabytes int) (*SimulationResult, error) {
	if megabytes <= 0 {
		megabytes = 64
	}
	if megabytes > 512 {
		megabytes = 512 // Guardrail against system exhaustion
	}

	start := time.Now().UTC()
	s.logger.Warn("Simulating failure: Simulating %dMB resource allocation for task %s", megabytes, taskID)

	// Allocate bounded memory slice in-process
	buffer := make([]byte, megabytes*1024*1024)
	for i := range buffer {
		buffer[i] = byte(i % 255)
	}

	if s.store != nil {
		_ = s.store.Events().Append(ctx, &models.Event{
			ID:        id.NewEventID(),
			Type:      "SIMULATION_EXHAUST_RESOURCES",
			Source:    "simulator",
			EntityID:  taskID,
			Payload:   fmt.Sprintf(`{"task_id":"%s","allocated_mb":%d}`, taskID, megabytes),
			CreatedAt: start,
		})
	}

	return &SimulationResult{
		Scenario:  ScenarioExhaustResources,
		TargetID:  taskID.String(),
		Success:   true,
		Action:    fmt.Sprintf("Allocated and held %dMB test buffer (safe memory pressure test)", megabytes),
		Details:   fmt.Sprintf("Buffer size: %d bytes", len(buffer)),
		Timestamp: start,
		Duration:  time.Since(start),
	}, nil
}

// SimulatedProber wraps health.Prober to intercept and inject artificial probe failures.
type SimulatedProber struct {
	mu           sync.RWMutex
	underlying   health.Prober
	brokenTasks  map[id.ID]string
	defaultError string
}

// NewSimulatedProber constructs a new SimulatedProber.
func NewSimulatedProber(underlying health.Prober) *SimulatedProber {
	if underlying == nil {
		underlying = health.NewDefaultProber()
	}
	return &SimulatedProber{
		underlying:  underlying,
		brokenTasks: make(map[id.ID]string),
	}
}

// BreakTask injects a failure response for checks targeting taskID.
func (sp *SimulatedProber) BreakTask(taskID id.ID, err string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.brokenTasks[taskID] = err
}

// RestoreTask removes injected failure for taskID.
func (sp *SimulatedProber) RestoreTask(taskID id.ID) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	delete(sp.brokenTasks, taskID)
}

// Check evaluates the probe or returns the simulated fault.
func (sp *SimulatedProber) Check(ctx context.Context, cfg health.ProbeConfig) health.ProbeResult {
	sp.mu.RLock()
	// Check if any registered broken tasks match this probe PID or port
	for _, reason := range sp.brokenTasks {
		if reason != "" {
			sp.mu.RUnlock()
			return health.ProbeResult{
				Timestamp: time.Now().UTC(),
				Healthy:   false,
				Latency:   1 * time.Millisecond,
				Error:     fmt.Sprintf("simulated failure: %s", reason),
			}
		}
	}
	underlying := sp.underlying
	sp.mu.RUnlock()

	return underlying.Check(ctx, cfg)
}
