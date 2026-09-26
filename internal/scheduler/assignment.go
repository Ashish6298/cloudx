package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/spec"
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
	Command         string                  `json:"command"`
	Args            []string                `json:"args,omitempty"`
	Environment     map[string]string       `json:"environment,omitempty"`
	WorkingDir      string                  `json:"working_dir,omitempty"`
	Runtime         string                  `json:"runtime,omitempty"`
	RestartPolicy   models.RestartPolicy    `json:"restart_policy,omitempty"`
	HealthCheck     *spec.HealthCheckConfig `json:"health_check,omitempty"`
	SpecJSON        string                  `json:"spec_json,omitempty"`
	// RequiredVolumes names volumes the task needs; passed through to scheduling constraints.
	RequiredVolumes []string                `json:"required_volumes,omitempty"`
	// RequiredPorts names host ports the task needs; passed through to scheduling constraints.
	RequiredPorts   []int                   `json:"required_ports,omitempty"`
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
	// If the task spec carries volume requirements but the caller didn't set them on
	// Requirements directly, propagate them now so the scheduler can enforce the constraint.
	if len(opts.Spec.RequiredVolumes) > 0 && len(opts.Requirements.RequiredVolumes) == 0 {
		opts.Requirements.RequiredVolumes = opts.Spec.RequiredVolumes
	}
	// If the task spec carries port requirements but the caller didn't set them on
	// Requirements directly, propagate them now so the scheduler can enforce port conflict checks.
	if len(opts.Spec.RequiredPorts) > 0 && len(opts.Requirements.RequiredPorts) == 0 {
		opts.Requirements.RequiredPorts = opts.Spec.RequiredPorts
	} else if len(opts.Requirements.RequiredPorts) == 0 && opts.Spec.SpecJSON != "" {
		// Attempt to extract ports from SpecJSON (service or deployment)
		var svcConfig spec.ServiceConfig
		if err := json.Unmarshal([]byte(opts.Spec.SpecJSON), &svcConfig); err == nil && len(svcConfig.Ports) > 0 {
			var ports []int
			for _, p := range svcConfig.Ports {
				if p.HostPort > 0 {
					ports = append(ports, p.HostPort)
				}
			}
			opts.Requirements.RequiredPorts = ports
		} else {
			var depConfig models.DeploymentConfig
			if err := json.Unmarshal([]byte(opts.Spec.SpecJSON), &depConfig); err == nil && len(depConfig.Ports) > 0 {
				var ports []int
				for _, p := range depConfig.Ports {
					if p.HostPort > 0 {
						ports = append(ports, p.HostPort)
					}
				}
				opts.Requirements.RequiredPorts = ports
			}
		}
	}

	// 1. Fetch current workers from cluster state store
	workers, err := ac.store.Workers().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cluster workers: %w", err)
	}
	if len(workers) == 0 {
		return nil, fmt.Errorf("no workers registered in cluster")
	}

	// Also fetch active tasks to compute worker allocations, task counts, and allocated host ports
	tasks, err := ac.store.Tasks().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing tasks: %w", err)
	}

	// Pre-load services & deployments cache to resolve ports for active tasks
	services, _ := ac.store.Services().List(ctx)
	servicePortMap := make(map[id.ID][]int)
	for _, s := range services {
		if s.SpecJSON != "" {
			var sc spec.ServiceConfig
			if err := json.Unmarshal([]byte(s.SpecJSON), &sc); err == nil {
				for _, p := range sc.Ports {
					if p.HostPort > 0 {
						servicePortMap[s.ID] = append(servicePortMap[s.ID], p.HostPort)
					}
				}
			}
		}
	}

	workerTaskCount := make(map[id.ID]int)
	workerAllocatedPorts := make(map[id.ID][]int)
	for _, t := range tasks {
		if t.WorkerID != "" && t.State != string(models.TaskStateStopped) && t.State != string(models.TaskStateFailed) && t.State != string(models.TaskStateLost) {
			workerTaskCount[t.WorkerID]++
			if t.ServiceID != "" {
				if ports, ok := servicePortMap[t.ServiceID]; ok {
					workerAllocatedPorts[t.WorkerID] = append(workerAllocatedPorts[t.WorkerID], ports...)
				}
			}
		}
	}

	// Fetch volumes to compute per-worker volume ownership for storage-aware scheduling.
	// A local volume is "owned" by the worker whose WorkerID is recorded on the VolumeRecord,
	// OR by any worker when WorkerID is empty (volume was created without a specific binding).
	workerVolumeNames := make(map[id.ID][]string)
	if volumes, volErr := ac.store.Volumes().List(ctx); volErr == nil {
		for _, v := range volumes {
			if v.WorkerID != "" {
				// Volume is pinned to a specific worker
				workerVolumeNames[v.WorkerID] = append(workerVolumeNames[v.WorkerID], v.Name)
			} else {
				// Volume has no worker binding — expose it on all known workers.
				// (For local driver volumes, the directory lives on the node running the server;
				// treat this as universally accessible in single-node configurations.)
				for _, w := range workers {
					workerVolumeNames[w.ID] = append(workerVolumeNames[w.ID], v.Name)
				}
			}
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
			// Storage affinity: populate volumes that physically reside on this worker
			VolumeNames:         workerVolumeNames[w.ID],
			// Port mappings: populate host ports currently in use on this worker
			AllocatedPorts:      workerAllocatedPorts[w.ID],
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

	// 5. Append TASK_ASSIGNED audit event
	_ = ac.store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "TASK_ASSIGNED",
		Source:    "assignment_coordinator",
		EntityID:  opts.TaskID,
		Payload:   fmt.Sprintf(`{"worker_id":"%s","service_id":"%s","deployment_id":"%s","score":%.2f}`, decision.WorkerID, opts.ServiceID, opts.DeploymentID, decision.Score),
		CreatedAt: now,
	})

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
