package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/transitions"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

var (
	ErrTaskAlreadyExists = errors.New("task manager: task already assigned or running")
	ErrTaskNotFound      = errors.New("task manager: task not found")
	ErrInvalidTaskSpec   = errors.New("task manager: invalid task specification")
)

// TaskAssignment contains the parameters required to accept and execute a task workload.
type TaskAssignment struct {
	TaskID      id.ID             `json:"task_id"`
	ServiceID   id.ID             `json:"service_id,omitempty"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	WorkingDir  string            `json:"working_dir,omitempty"`
}

// ManagedTask holds the execution lifecycle and status of a worker task.
type ManagedTask struct {
	mu          sync.RWMutex
	Assignment  TaskAssignment
	State       models.TaskState
	PID         int
	ExitCode    int
	StartTime   time.Time
	StopTime    time.Time
	ErrorMsg    string
	cancel      context.CancelFunc
}

// TaskStatusSnapshot represents an immutable view of a task's state.
type TaskStatusSnapshot struct {
	TaskID    id.ID            `json:"task_id"`
	State     models.TaskState `json:"state"`
	PID       int              `json:"pid"`
	ExitCode  int              `json:"exit_code"`
	StartTime time.Time        `json:"start_time"`
	Duration  time.Duration    `json:"duration"`
	Error     string           `json:"error,omitempty"`
}

// StateReporter defines the interface for reporting task status updates back to the control plane.
type StateReporter interface {
	ReportTaskStatus(ctx context.Context, req *v1.ReportTaskStatusRequest) (*v1.ReportTaskStatusResponse, error)
}

// TaskManagerOptions configures the TaskManager.
type TaskManagerOptions struct {
	WorkerID id.ID
	Runtime  runtime.Runtime
	Reporter StateReporter
	Logger   logging.Logger
}

// TaskManager manages the local lifecycle of tasks executing on a worker.
type TaskManager struct {
	mu       sync.RWMutex
	workerID id.ID
	runtime  runtime.Runtime
	reporter StateReporter
	logger   logging.Logger
	tasks    map[id.ID]*ManagedTask
	closed   bool
}

// NewTaskManager creates a new worker TaskManager instance.
func NewTaskManager(opts TaskManagerOptions) *TaskManager {
	if opts.Logger == nil {
		opts.Logger = logging.NewDefaultLogger()
	}
	if opts.Runtime == nil {
		opts.Runtime = runtime.NewNativeRuntime()
	}

	return &TaskManager{
		workerID: opts.WorkerID,
		runtime:  opts.Runtime,
		reporter: opts.Reporter,
		logger:   opts.Logger.With("component", "task_manager"),
		tasks:    make(map[id.ID]*ManagedTask),
	}
}

// AssignTask accepts, validates, and starts a task assignment.
func (tm *TaskManager) AssignTask(ctx context.Context, assignment TaskAssignment) error {
	if assignment.TaskID == "" {
		return fmt.Errorf("%w: task_id is required", ErrInvalidTaskSpec)
	}
	if assignment.Command == "" {
		return fmt.Errorf("%w: command is required", ErrInvalidTaskSpec)
	}

	tm.mu.Lock()
	if tm.closed {
		tm.mu.Unlock()
		return errors.New("task manager is closed")
	}

	if _, exists := tm.tasks[assignment.TaskID]; exists {
		tm.mu.Unlock()
		return ErrTaskAlreadyExists
	}

	taskCtx, cancel := context.WithCancel(context.Background())
	task := &ManagedTask{
		Assignment: assignment,
		State:      models.TaskStatePending,
		cancel:     cancel,
	}
	tm.tasks[assignment.TaskID] = task
	tm.mu.Unlock()

	// Transition PENDING -> ASSIGNED
	if err := tm.transitionTask(ctx, task, models.TaskStateAssigned); err != nil {
		return fmt.Errorf("failed to transition task to ASSIGNED: %w", err)
	}

	// Launch execution in background goroutine
	go tm.executeTask(taskCtx, task)

	return nil
}

// transitionTask validates and transitions the task state, reporting to the control plane if available.
func (tm *TaskManager) transitionTask(ctx context.Context, task *ManagedTask, nextState models.TaskState) error {
	task.mu.Lock()
	currentState := task.State
	if err := transitions.Validate(currentState, nextState); err != nil {
		task.mu.Unlock()
		return fmt.Errorf("invalid transition %s -> %s: %w", currentState, nextState, err)
	}
	task.State = nextState
	taskPID := task.PID
	taskExitCode := task.ExitCode
	task.mu.Unlock()

	tm.logger.Info("Task %s transitioned: %s -> %s (PID: %d)", task.Assignment.TaskID, currentState, nextState, taskPID)

	// Report state to control plane asynchronously / best effort
	if tm.reporter != nil {
		reportCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		_, err := tm.reporter.ReportTaskStatus(reportCtx, &v1.ReportTaskStatusRequest{
			WorkerId: tm.workerID.String(),
			TaskId:   task.Assignment.TaskID.String(),
			State:    string(nextState),
			Pid:      int32(taskPID),
			ExitCode: int32(taskExitCode),
		})
		if err != nil {
			tm.logger.Warn("Failed to report task %s status %s to control plane: %v", task.Assignment.TaskID, nextState, err)
		}
	}

	return nil
}

// executeTask handles the full runtime lifecycle: ASSIGNED -> STARTING -> RUNNING -> STOPPED/FAILED.
func (tm *TaskManager) executeTask(ctx context.Context, task *ManagedTask) {
	// 1. Transition ASSIGNED -> STARTING
	if err := tm.transitionTask(ctx, task, models.TaskStateStarting); err != nil {
		tm.logger.Error("Failed to transition task %s to STARTING: %v", task.Assignment.TaskID, err)
		return
	}

	// 2. Start Runtime Process
	status, err := tm.runtime.Start(ctx, runtime.ProcessSpec{
		ID:          task.Assignment.TaskID,
		Command:     task.Assignment.Command,
		Args:        task.Assignment.Args,
		Environment: task.Assignment.Environment,
		WorkingDir:  task.Assignment.WorkingDir,
	})

	if err != nil {
		task.mu.Lock()
		task.ErrorMsg = err.Error()
		task.ExitCode = -1
		task.StopTime = time.Now().UTC()
		task.mu.Unlock()

		_ = tm.transitionTask(ctx, task, models.TaskStateFailed)
		return
	}

	// 3. Update PID & StartTime, transition STARTING -> RUNNING
	task.mu.Lock()
	task.PID = status.PID
	task.StartTime = status.StartTime
	task.mu.Unlock()

	if err := tm.transitionTask(ctx, task, models.TaskStateRunning); err != nil {
		tm.logger.Error("Failed to transition task %s to RUNNING: %v", task.Assignment.TaskID, err)
	}

	// 4. Supervise Process until completion or cancellation
	tm.superviseTask(ctx, task)
}

// superviseTask polls the runtime process state and handles completion/crash.
func (tm *TaskManager) superviseTask(ctx context.Context, task *ManagedTask) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Context canceled: stop process
			_ = tm.transitionTask(context.Background(), task, models.TaskStateStopping)
			_ = tm.runtime.Stop(context.Background(), task.Assignment.TaskID, 3*time.Second)
			
			inspectStatus, _ := tm.runtime.Inspect(context.Background(), task.Assignment.TaskID)
			task.mu.Lock()
			task.StopTime = time.Now().UTC()
			if inspectStatus != nil {
				task.ExitCode = inspectStatus.ExitCode
			}
			task.mu.Unlock()

			_ = tm.transitionTask(context.Background(), task, models.TaskStateStopped)
			return

		case <-ticker.C:
			status, err := tm.runtime.Inspect(context.Background(), task.Assignment.TaskID)
			if err != nil {
				continue
			}

			if !status.Running {
				// Process finished
				task.mu.Lock()
				task.ExitCode = status.ExitCode
				task.StopTime = time.Now().UTC()
				if status.Error != "" {
					task.ErrorMsg = status.Error
				}
				task.mu.Unlock()

				if status.ExitCode == 0 {
					_ = tm.transitionTask(context.Background(), task, models.TaskStateStopped)
				} else {
					// Non-zero exit code or crash -> FAILED
					_ = tm.transitionTask(context.Background(), task, models.TaskStateFailed)
				}
				return
			}
		}
	}
}

// StopTask terminates an actively running or assigned task.
func (tm *TaskManager) StopTask(ctx context.Context, taskID id.ID) error {
	tm.mu.RLock()
	task, ok := tm.tasks[taskID]
	tm.mu.RUnlock()

	if !ok {
		return ErrTaskNotFound
	}

	task.mu.RLock()
	isTerminal := transitions.IsTerminal(task.State)
	cancel := task.cancel
	task.mu.RUnlock()

	if isTerminal {
		return nil
	}

	if cancel != nil {
		cancel()
	}

	return nil
}

// GetTask returns a point-in-time snapshot of the specified task.
func (tm *TaskManager) GetTask(taskID id.ID) (*TaskStatusSnapshot, error) {
	tm.mu.RLock()
	task, ok := tm.tasks[taskID]
	tm.mu.RUnlock()

	if !ok {
		return nil, ErrTaskNotFound
	}

	task.mu.RLock()
	defer task.mu.RUnlock()

	duration := time.Since(task.StartTime)
	if transitions.IsTerminal(task.State) && !task.StopTime.IsZero() {
		duration = task.StopTime.Sub(task.StartTime)
	}

	return &TaskStatusSnapshot{
		TaskID:    task.Assignment.TaskID,
		State:     task.State,
		PID:       task.PID,
		ExitCode:  task.ExitCode,
		StartTime: task.StartTime,
		Duration:  duration,
		Error:     task.ErrorMsg,
	}, nil
}

// ListTasks returns snapshots of all tasks managed by this worker.
func (tm *TaskManager) ListTasks() []*TaskStatusSnapshot {
	tm.mu.RLock()
	tasks := make([]*ManagedTask, 0, len(tm.tasks))
	for _, t := range tm.tasks {
		tasks = append(tasks, t)
	}
	tm.mu.RUnlock()

	snapshots := make([]*TaskStatusSnapshot, 0, len(tasks))
	for _, t := range tasks {
		t.mu.RLock()
		duration := time.Since(t.StartTime)
		if transitions.IsTerminal(t.State) && !t.StopTime.IsZero() {
			duration = t.StopTime.Sub(t.StartTime)
		}
		snapshots = append(snapshots, &TaskStatusSnapshot{
			TaskID:    t.Assignment.TaskID,
			State:     t.State,
			PID:       t.PID,
			ExitCode:  t.ExitCode,
			StartTime: t.StartTime,
			Duration:  duration,
			Error:     t.ErrorMsg,
		})
		t.mu.RUnlock()
	}

	return snapshots
}

// Close stops all managed tasks and tears down the TaskManager.
func (tm *TaskManager) Close() error {
	tm.mu.Lock()
	tm.closed = true
	tasks := make([]*ManagedTask, 0, len(tm.tasks))
	for _, t := range tm.tasks {
		tasks = append(tasks, t)
	}
	tm.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, t := range tasks {
		_ = tm.StopTask(ctx, t.Assignment.TaskID)
	}

	return nil
}
