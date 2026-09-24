package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

// Dispatcher defines the interface for delivering a task assignment to a worker.
type Dispatcher interface {
	Dispatch(ctx context.Context, worker *models.Worker, task *models.Task, spec *TaskSpec) error
}

// TaskSpec specifies execution parameters for a task workload.
type TaskSpec struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	WorkingDir  string            `json:"working_dir,omitempty"`
	Runtime     string            `json:"runtime,omitempty"`
	SpecJSON    string            `json:"spec_json,omitempty"`
}

// AssignOptions configures task assignment execution.
type AssignOptions struct {
	TaskID       id.ID
	ServiceID    id.ID
	JobID        id.ID
	DeploymentID id.ID
	Requirements *TaskRequirements
	Spec         TaskSpec
}

// AssignmentResult details the outcome of scheduling and assigning a task.
type AssignmentResult struct {
	TaskID    id.ID             `json:"task_id"`
	WorkerID  id.ID             `json:"worker_id"`
	Hostname  string            `json:"hostname"`
	State     models.TaskState  `json:"state"`
	Decision  *ScheduleDecision `json:"decision"`
	Timestamp time.Time         `json:"timestamp"`
}

// AssignmentCoordinator coordinates:
// 1. Task requirement modeling
// 2. Worker capacity aggregation from state store
// 3. Deterministic scheduling (Scheduler)
// 4. State store persistence of ASSIGNED state prior to transmission
// 5. Worker transmission / dispatch with failure handling (RPC failure, worker offline, rejection)
type AssignmentCoordinator struct {
	mu         sync.RWMutex
	store      state.Store
	scheduler  Scheduler
	dispatcher Dispatcher
	logger     logging.Logger
}

// NewAssignmentCoordinator creates a new AssignmentCoordinator instance.
func NewAssignmentCoordinator(store state.Store, scheduler Scheduler, dispatcher Dispatcher, logger logging.Logger) *AssignmentCoordinator {
	if logger == nil {
		logger = logging.NewDefaultLogger()
	}
	if scheduler == nil {
		scheduler = NewBasicScheduler()
	}
	return &AssignmentCoordinator{
		store:      store,
		scheduler:  scheduler,
		dispatcher: dispatcher,
		logger:     logger.With("component", "assignment_coordinator"),
	}
}

// Assign evaluates cluster capacity, picks the best worker, persists assignment, and dispatches to worker.
func (ac *AssignmentCoordinator) Assign(ctx context.Context, opts AssignOptions) (*AssignmentResult, error) {
	if opts.TaskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if opts.Spec.Command == "" {
		return nil, fmt.Errorf("task command is required")
	}
	if opts.Requirements == nil {
		opts.Requirements = &TaskRequirements{
			TaskID: opts.TaskID,
		}
	} else {
		opts.Requirements.TaskID = opts.TaskID
	}

	// 1. Fetch current workers from cluster state store
	workers, err := ac.store.Workers().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cluster workers: %w", err)
	}
	if len(workers) == 0 {
		return nil, fmt.Errorf("no workers registered in cluster")
	}

	// Also fetch active tasks to compute worker allocations & task counts
	tasks, err := ac.store.Tasks().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing tasks: %w", err)
	}

	workerTaskCount := make(map[id.ID]int)
	for _, t := range tasks {
		if t.WorkerID != "" && t.State != string(models.TaskStateStopped) && t.State != string(models.TaskStateFailed) && t.State != string(models.TaskStateLost) {
			workerTaskCount[t.WorkerID]++
		}
	}

	// Build WorkerCapacity snapshots
	capacities := make([]*WorkerCapacity, len(workers))
	workerMap := make(map[id.ID]*models.Worker)

	for i, w := range workers {
		workerMap[w.ID] = w
		// Default standard worker capacities if not overridden
		capacities[i] = &WorkerCapacity{
			WorkerID:            w.ID,
			NodeID:              w.NodeID,
			Hostname:            w.Address,
			Status:              w.Status,
			CPUTotal:            8.0,
			CPUAllocated:        float64(workerTaskCount[w.ID]) * 0.5,
			MemoryTotal:         16 * 1024 * 1024 * 1024,
			MemoryAllocated:     int64(workerTaskCount[w.ID]) * 512 * 1024 * 1024,
			RuntimeCapabilities: []string{"native", "docker"},
			TaskCount:           workerTaskCount[w.ID],
		}
	}

	// 2. Run Scheduler to select optimal worker
	decision, err := ac.scheduler.Schedule(ctx, opts.Requirements, capacities)
	if err != nil {
		return nil, fmt.Errorf("scheduling failed: %w", err)
	}

	targetWorker, ok := workerMap[decision.WorkerID]
	if !ok || targetWorker == nil {
		return nil, fmt.Errorf("scheduled worker %s not found in state store", decision.WorkerID)
	}

	// 3. PERSISTENCE FIRST: Persist task in state store as ASSIGNED before network transmission
	now := time.Now().UTC()
	existingTask, _ := ac.store.Tasks().Get(ctx, opts.TaskID)

	taskRecord := &models.Task{
		ID:           opts.TaskID,
		ServiceID:    opts.ServiceID,
		JobID:        opts.JobID,
		DeploymentID: opts.DeploymentID,
		WorkerID:     decision.WorkerID,
		State:        string(models.TaskStateAssigned),
		UpdatedAt:    now,
	}

	if existingTask == nil {
		taskRecord.CreatedAt = now
		if err := ac.store.Tasks().Create(ctx, taskRecord); err != nil {
			return nil, fmt.Errorf("failed to persist new task assignment: %w", err)
		}
	} else {
		// Verify transition validity
		if existingTask.State != string(models.TaskStatePending) && existingTask.State != string(models.TaskStateAssigned) {
			return nil, fmt.Errorf("cannot assign task %s: current state is %s", opts.TaskID, existingTask.State)
		}
		taskRecord.CreatedAt = existingTask.CreatedAt
		if err := ac.store.Tasks().Update(ctx, taskRecord); err != nil {
			return nil, fmt.Errorf("failed to update task assignment: %w", err)
		}
	}

	ac.logger.Info("Persisted task %s assignment to worker %s (Decision score: %.2f)",
		opts.TaskID, decision.WorkerID, decision.Score)

	// 4. TRANSMISSION: Dispatch task assignment to the worker
	if ac.dispatcher != nil {
		if err := ac.dispatcher.Dispatch(ctx, targetWorker, taskRecord, &opts.Spec); err != nil {
			ac.logger.Error("Failed to dispatch task %s to worker %s: %v", opts.TaskID, targetWorker.ID, err)
			// Mark task as failed or pending for retry
			taskRecord.State = string(models.TaskStateFailed)
			taskRecord.UpdatedAt = time.Now().UTC()
			_ = ac.store.Tasks().Update(ctx, taskRecord)
			return nil, fmt.Errorf("worker dispatch failed for task %s on worker %s: %w", opts.TaskID, targetWorker.ID, err)
		}
	}

	return &AssignmentResult{
		TaskID:    opts.TaskID,
		WorkerID:  decision.WorkerID,
		Hostname:  decision.Hostname,
		State:     models.TaskStateAssigned,
		Decision:  decision,
		Timestamp: now,
	}, nil
}

// InProcessDispatcher delivers task assignments directly to a local worker TaskManager or Daemon.
type InProcessDispatcher struct {
	handlers map[id.ID]func(ctx context.Context, req *v1.TaskAssignmentRequest) error
	mu       sync.RWMutex
}

// NewInProcessDispatcher initializes an InProcessDispatcher.
func NewInProcessDispatcher() *InProcessDispatcher {
	return &InProcessDispatcher{
		handlers: make(map[id.ID]func(ctx context.Context, req *v1.TaskAssignmentRequest) error),
	}
}

// RegisterWorkerHandler registers an assignment handler callback for a worker.
func (d *InProcessDispatcher) RegisterWorkerHandler(workerID id.ID, handler func(ctx context.Context, req *v1.TaskAssignmentRequest) error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[workerID] = handler
}

// Dispatch invokes the worker assignment handler.
func (d *InProcessDispatcher) Dispatch(ctx context.Context, worker *models.Worker, task *models.Task, spec *TaskSpec) error {
	if worker == nil {
		return fmt.Errorf("worker is nil")
	}

	d.mu.RLock()
	handler, ok := d.handlers[worker.ID]
	d.mu.RUnlock()

	if !ok || handler == nil {
		return fmt.Errorf("worker %s is unreachable / no dispatch handler registered", worker.ID)
	}

	req := &v1.TaskAssignmentRequest{
		WorkerId: worker.ID.String(),
		Task: &v1.Task{
			Id:           task.ID.String(),
			ServiceId:    task.ServiceID.String(),
			JobId:        task.JobID.String(),
			DeploymentId: task.DeploymentID.String(),
			WorkerId:     worker.ID.String(),
			State:        task.State,
		},
		Command:     spec.Command,
		Args:        spec.Args,
		Environment: spec.Environment,
		SpecJson:    spec.SpecJSON,
	}

	return handler(ctx, req)
}
