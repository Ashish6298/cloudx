package controlplane

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudx-org/cloudx/internal/auth"
	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/logs"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// JobRunResult captures the outcome of scheduling and executing a job workload.
type JobRunResult struct {
	JobID       id.ID                       `json:"job_id"`
	JobName     string                      `json:"job_name"`
	TaskID      id.ID                       `json:"task_id"`
	WorkerID    id.ID                       `json:"worker_id"`
	Hostname    string                      `json:"hostname"`
	State       models.JobState             `json:"state"`
	ConfigHash  string                      `json:"config_hash"`
	Assignment  *scheduler.AssignmentResult `json:"assignment,omitempty"`
	ScheduledAt time.Time                   `json:"scheduled_at"`
}

// RunJob validates a job specification, evaluates cluster capacity using the unified scheduler,
// persists the job record and task in the state store, appends audit events, and dispatches execution to the assigned worker.
func (cp *ControlPlane) RunJob(ctx context.Context, jobConfig *spec.JobConfig, dispatcher scheduler.Dispatcher) (*JobRunResult, error) {
	if err := auth.EnsureScope(ctx, auth.ScopeControlPlane); err != nil {
		return nil, err
	}

	if jobConfig == nil {
		return nil, fmt.Errorf("job configuration is nil")
	}

	// Validate job name against directory traversal or illegal characters
	if err := auth.ValidateResourceID(jobConfig.Name, id.EntityJob); err != nil {
		return nil, fmt.Errorf("invalid job name '%s': %w", jobConfig.Name, err)
	}

	// 1. Validate Job Configuration
	parsedSettings, err := jobConfig.Validate()
	if err != nil {
		return nil, fmt.Errorf("invalid job configuration: %w", err)
	}

	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	now := time.Now().UTC()
	jobID := id.NewJobID()
	taskID := id.NewTaskID()

	// Build immutable JobRecord
	jobRec := &models.JobRecord{
		ID:    jobID,
		Name:  jobConfig.Name,
		State: models.JobStatePending,
		Config: models.JobConfig{
			Command:     jobConfig.Command,
			Args:        jobConfig.Args,
			Environment: jobConfig.Environment,
			WorkingDir:  jobConfig.WorkingDir,
			Runtime:     jobConfig.Runtime,
			Resources: models.ResourceRequirements{
				CPU: func() float64 {
					if parsedSettings.Resources != nil {
						return parsedSettings.Resources.CPUCores
					}
					return 0.5
				}(),
				Memory: func() int64 {
					if parsedSettings.Resources != nil {
						return parsedSettings.Resources.MemoryBytes
					}
					return 256 * 1024 * 1024
				}(),
			},
			RetryPolicy: models.JobRetryPolicy{
				MaxRetries:    parsedSettings.RetryRetries,
				BackoffPeriod: parsedSettings.RetryBackoff,
			},
			Timeout: parsedSettings.Timeout,
		},
		TaskID:    taskID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRec.ConfigHash = jobRec.Config.ComputeHash()

	specJSON, err := jobRec.ToSpecJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize job record spec: %w", err)
	}

	// 2. Persist Job Record in SQLite store
	dbJob := &models.Job{
		ID:        jobID,
		Name:      jobRec.Name,
		Command:   jobRec.Config.Command,
		Status:    string(models.JobStatePending),
		SpecJSON:  specJSON,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Jobs().Create(ctx, dbJob); err != nil {
		return nil, fmt.Errorf("failed to persist job record: %w", err)
	}

	// Append JOB_CREATED audit event
	_ = store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "JOB_CREATED",
		Source:    "controlplane",
		EntityID:  jobID,
		Payload:   fmt.Sprintf(`{"name":"%s","command":"%s","runtime":"%s"}`, jobRec.Name, jobRec.Config.Command, jobRec.Config.Runtime),
		CreatedAt: now,
	})

	// 3. Schedule Job using the unified Scheduler & Assignment Coordinator
	coordinator := scheduler.NewAssignmentCoordinator(store, scheduler.NewBasicScheduler(), dispatcher, cp.logger)

	assignRes, err := coordinator.Assign(ctx, scheduler.AssignOptions{
		TaskID: taskID,
		JobID:  jobID,
		Requirements: &scheduler.TaskRequirements{
			TaskID:          taskID,
			CPU:             jobRec.Config.Resources.CPU,
			Memory:          jobRec.Config.Resources.Memory,
			RequiredRuntime: jobRec.Config.Runtime,
		},
		Spec: scheduler.TaskSpec{
			Command:     jobRec.Config.Command,
			Args:        jobRec.Config.Args,
			Environment: jobRec.Config.Environment,
			WorkingDir:  jobRec.Config.WorkingDir,
			Runtime:     jobRec.Config.Runtime,
			RestartPolicy: models.RestartPolicy{
				Type:       models.RestartPolicyNever,
				MaxRetries: jobRec.Config.RetryPolicy.MaxRetries,
			},
			SpecJSON: specJSON,
		},
	})
	if err != nil {
		// Transition job to FAILED if scheduling could not find capacity
		_ = jobRec.Transition(models.JobStateFailed)
		dbJob.Status = string(models.JobStateFailed)
		dbJob.UpdatedAt = time.Now().UTC()
		_ = store.Jobs().Update(ctx, dbJob)

		_ = store.Events().Append(ctx, &models.Event{
			ID:        id.NewEventID(),
			Type:      "JOB_FAILED",
			Source:    "controlplane",
			EntityID:  jobID,
			Payload:   fmt.Sprintf(`{"reason":"scheduling_failed: %v"}`, err),
			CreatedAt: time.Now().UTC(),
		})

		return nil, fmt.Errorf("failed to schedule job '%s': %w", jobRec.Name, err)
	}

	// 4. Update Job with Assignment details
	_ = jobRec.Transition(models.JobStateAssigned)
	jobRec.AssignedTo = assignRes.WorkerID
	jobRec.UpdatedAt = time.Now().UTC()

	updatedSpecJSON, _ := jobRec.ToSpecJSON()
	dbJob.Status = string(models.JobStateAssigned)
	dbJob.SpecJSON = updatedSpecJSON
	dbJob.UpdatedAt = jobRec.UpdatedAt
	_ = store.Jobs().Update(ctx, dbJob)

	// Append JOB_ASSIGNED audit event
	_ = store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "JOB_ASSIGNED",
		Source:    "controlplane",
		EntityID:  jobID,
		Payload:   fmt.Sprintf(`{"worker_id":"%s","task_id":"%s","hostname":"%s"}`, assignRes.WorkerID, taskID, assignRes.Hostname),
		CreatedAt: time.Now().UTC(),
	})

	return &JobRunResult{
		JobID:       jobID,
		JobName:     jobRec.Name,
		TaskID:      taskID,
		WorkerID:    assignRes.WorkerID,
		Hostname:    assignRes.Hostname,
		State:       jobRec.State,
		ConfigHash:  jobRec.ConfigHash,
		Assignment:  assignRes,
		ScheduledAt: now,
	}, nil
}

// JobInspectResult holds detailed inspection data for a job workload.
type JobInspectResult struct {
	Job  *models.JobRecord `json:"job"`
	Task *models.Task      `json:"task,omitempty"`
}

// InspectJob retrieves job details and its associated execution task.
func (cp *ControlPlane) InspectJob(ctx context.Context, nameOrID string) (*JobInspectResult, error) {
	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	var targetJob *models.Job
	// Try lookup by ID
	j, err := store.Jobs().Get(ctx, id.ID(nameOrID))
	if err == nil && j != nil {
		targetJob = j
	} else {
		// Lookup by Name
		jobs, err := store.Jobs().List(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list jobs: %w", err)
		}
		for _, item := range jobs {
			if strings.EqualFold(item.Name, nameOrID) {
				targetJob = item
				break
			}
		}
	}

	if targetJob == nil {
		return nil, fmt.Errorf("job '%s' not found", nameOrID)
	}

	jobRec, err := models.JobFromModel(targetJob)
	if err != nil {
		return nil, fmt.Errorf("failed to parse job model: %w", err)
	}

	var assocTask *models.Task
	if jobRec.TaskID != "" {
		t, _ := store.Tasks().Get(ctx, jobRec.TaskID)
		assocTask = t
	}

	return &JobInspectResult{
		Job:  jobRec,
		Task: assocTask,
	}, nil
}

// GetJobLogs queries logs for a job workload.
func (cp *ControlPlane) GetJobLogs(ctx context.Context, jobNameOrID string, filter logs.LogFilter) ([]logs.LogEntry, []id.ID, error) {
	inspectRes, err := cp.InspectJob(ctx, jobNameOrID)
	if err != nil {
		return nil, nil, err
	}

	jobRec := inspectRes.Job
	filter.JobID = jobRec.ID
	filter.JobName = jobRec.Name

	var taskIDs []id.ID
	if jobRec.TaskID != "" {
		taskIDs = append(taskIDs, jobRec.TaskID)
	}

	logger := logs.DefaultWorkloadLogger()
	if cp.cfg.Storage.Path != "" {
		logger.SetBaseDir(filepath.Join(cp.cfg.Storage.Path, "logs"))
	}

	entries := logger.ReadFilteredLogs(filter, taskIDs)
	return entries, taskIDs, nil
}

// CancelJob terminates an active or assigned job workload.
func (cp *ControlPlane) CancelJob(ctx context.Context, jobNameOrID string) error {
	store := cp.StateManager.Store()
	if store == nil {
		return fmt.Errorf("state store is not available")
	}

	inspectRes, err := cp.InspectJob(ctx, jobNameOrID)
	if err != nil {
		return err
	}

	jobRec := inspectRes.Job
	if models.IsTerminalJobState(jobRec.State) {
		return fmt.Errorf("job '%s' is already in terminal state %s", jobRec.Name, jobRec.State)
	}

	now := time.Now().UTC()
	if err := jobRec.Transition(models.JobStateCancelled); err != nil {
		return fmt.Errorf("failed to cancel job: %w", err)
	}

	// Update DB job record
	dbJob, _ := store.Jobs().Get(ctx, jobRec.ID)
	if dbJob != nil {
		dbJob.Status = string(models.JobStateCancelled)
		dbJob.UpdatedAt = now
		if specJSON, err := jobRec.ToSpecJSON(); err == nil {
			dbJob.SpecJSON = specJSON
		}
		_ = store.Jobs().Update(ctx, dbJob)
	}

	// Terminate associated active task if present
	if jobRec.TaskID != "" {
		if task, err := store.Tasks().Get(ctx, jobRec.TaskID); err == nil && task != nil {
			task.State = string(models.TaskStateStopped)
			task.UpdatedAt = now
			_ = store.Tasks().Update(ctx, task)
		}
	}

	// Append JOB_CANCELLED audit event
	_ = store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "JOB_CANCELLED",
		Source:    "controlplane",
		EntityID:  jobRec.ID,
		Payload:   fmt.Sprintf(`{"name":"%s","task_id":"%s"}`, jobRec.Name, jobRec.TaskID),
		CreatedAt: now,
	})

	return nil
}

// RetryJob attempts to re-execute a failed job workload according to its retry policy or explicit trigger.
func (cp *ControlPlane) RetryJob(ctx context.Context, jobNameOrID string, dispatcher scheduler.Dispatcher) (*JobRunResult, error) {
	inspectRes, err := cp.InspectJob(ctx, jobNameOrID)
	if err != nil {
		return nil, err
	}

	jobRec := inspectRes.Job
	if jobRec.State != models.JobStateFailed {
		return nil, fmt.Errorf("job '%s' is in state %s (only FAILED jobs can be retried)", jobRec.Name, jobRec.State)
	}

	// Transition FAILED -> PENDING
	if err := jobRec.Transition(models.JobStatePending); err != nil {
		return nil, fmt.Errorf("failed to transition job for retry: %w", err)
	}
	jobRec.RetryCount++

	store := cp.StateManager.Store()
	newTaskID := id.NewTaskID()
	jobRec.TaskID = newTaskID
	now := time.Now().UTC()
	jobRec.UpdatedAt = now

	specJSON, _ := jobRec.ToSpecJSON()
	dbJob, _ := store.Jobs().Get(ctx, jobRec.ID)
	if dbJob != nil {
		dbJob.Status = string(models.JobStatePending)
		dbJob.SpecJSON = specJSON
		dbJob.UpdatedAt = now
		_ = store.Jobs().Update(ctx, dbJob)
	}

	// Append JOB_RETRY_TRIGGERED audit event
	_ = store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "JOB_RETRY_TRIGGERED",
		Source:    "controlplane",
		EntityID:  jobRec.ID,
		Payload:   fmt.Sprintf(`{"name":"%s","retry_count":%d,"new_task_id":"%s"}`, jobRec.Name, jobRec.RetryCount, newTaskID),
		CreatedAt: now,
	})

	// Schedule retry task using AssignmentCoordinator
	coordinator := scheduler.NewAssignmentCoordinator(store, scheduler.NewBasicScheduler(), dispatcher, cp.logger)
	assignRes, err := coordinator.Assign(ctx, scheduler.AssignOptions{
		TaskID: newTaskID,
		JobID:  jobRec.ID,
		Requirements: &scheduler.TaskRequirements{
			TaskID:          newTaskID,
			CPU:             jobRec.Config.Resources.CPU,
			Memory:          jobRec.Config.Resources.Memory,
			RequiredRuntime: jobRec.Config.Runtime,
		},
		Spec: scheduler.TaskSpec{
			Command:     jobRec.Config.Command,
			Args:        jobRec.Config.Args,
			Environment: jobRec.Config.Environment,
			WorkingDir:  jobRec.Config.WorkingDir,
			Runtime:     jobRec.Config.Runtime,
			RestartPolicy: models.RestartPolicy{
				Type:       models.RestartPolicyNever,
				MaxRetries: jobRec.Config.RetryPolicy.MaxRetries,
			},
			SpecJSON: specJSON,
		},
	})
	if err != nil {
		_ = jobRec.Transition(models.JobStateFailed)
		if dbJob != nil {
			dbJob.Status = string(models.JobStateFailed)
			dbJob.UpdatedAt = time.Now().UTC()
			_ = store.Jobs().Update(ctx, dbJob)
		}
		return nil, fmt.Errorf("failed to schedule retry task for job '%s': %w", jobRec.Name, err)
	}

	_ = jobRec.Transition(models.JobStateAssigned)
	jobRec.AssignedTo = assignRes.WorkerID
	jobRec.UpdatedAt = time.Now().UTC()

	updatedSpecJSON, _ := jobRec.ToSpecJSON()
	if dbJob != nil {
		dbJob.Status = string(models.JobStateAssigned)
		dbJob.SpecJSON = updatedSpecJSON
		dbJob.UpdatedAt = jobRec.UpdatedAt
		_ = store.Jobs().Update(ctx, dbJob)
	}

	return &JobRunResult{
		JobID:       jobRec.ID,
		JobName:     jobRec.Name,
		TaskID:      newTaskID,
		WorkerID:    assignRes.WorkerID,
		Hostname:    assignRes.Hostname,
		State:       jobRec.State,
		ConfigHash:  jobRec.ConfigHash,
		Assignment:  assignRes,
		ScheduledAt: now,
	}, nil
}
