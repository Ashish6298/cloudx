package models

import (
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// TaskState represents the actual execution state of a task on a worker.
type TaskState string

const (
	TaskStatePending    TaskState = "PENDING"
	TaskStateAssigned   TaskState = "ASSIGNED"
	TaskStateStarting   TaskState = "STARTING"
	TaskStateRunning    TaskState = "RUNNING"
	TaskStateHealthy    TaskState = "HEALTHY"
	TaskStateUnhealthy  TaskState = "UNHEALTHY"
	TaskStateStopping   TaskState = "STOPPING"
	TaskStateStopped    TaskState = "STOPPED"
	TaskStateFailed     TaskState = "FAILED"
	TaskStateBackoff    TaskState = "BACKOFF"
	TaskStateRestarting TaskState = "RESTARTING"
	TaskStateCrashLoop  TaskState = "CRASH_LOOP"
	TaskStateLost       TaskState = "LOST"
)

// ServiceActualStatus defines the high-level derived status of a service.
type ServiceActualStatus string

const (
	ServiceStatusPending   ServiceActualStatus = "PENDING"
	ServiceStatusRunning   ServiceActualStatus = "RUNNING"
	ServiceStatusHealthy   ServiceActualStatus = "HEALTHY"
	ServiceStatusDegraded  ServiceActualStatus = "DEGRADED"
	ServiceStatusFailed    ServiceActualStatus = "FAILED"
	ServiceStatusStopped   ServiceActualStatus = "STOPPED"
)

// TaskResourceUsage captures dynamic compute usage for a running task.
type TaskResourceUsage struct {
	CPUPercent float64 `json:"cpu_percent" yaml:"cpu_percent"`
	MemoryUsed int64   `json:"memory_used" yaml:"memory_used"` // in bytes
}

// TaskActualState represents the full reported actual state of a task.
type TaskActualState struct {
	ID            id.ID             `json:"id" yaml:"id"`
	ServiceID     id.ID             `json:"service_id,omitempty" yaml:"service_id,omitempty"`
	JobID         id.ID             `json:"job_id,omitempty" yaml:"job_id,omitempty"`
	DeploymentID  id.ID             `json:"deployment_id,omitempty" yaml:"deployment_id,omitempty"`
	WorkerID      id.ID             `json:"worker_id" yaml:"worker_id"`
	State         TaskState         `json:"state" yaml:"state"`
	PID           int               `json:"pid" yaml:"pid"`
	StartTime     time.Time         `json:"start_time" yaml:"start_time"`
	ExitCode      int               `json:"exit_code" yaml:"exit_code"`
	RuntimeState  string            `json:"runtime_state" yaml:"runtime_state"` // e.g. "active", "exited", "signaled"
	HealthState   string            `json:"health_state" yaml:"health_state"`   // e.g. "healthy", "unhealthy", "probing"
	LastHeartbeat time.Time         `json:"last_heartbeat" yaml:"last_heartbeat"`
	Resources     TaskResourceUsage `json:"resources" yaml:"resources"`
	UpdatedAt     time.Time         `json:"updated_at" yaml:"updated_at"`
}

// IsActive returns true if the task is currently consuming worker resources.
func (t *TaskActualState) IsActive() bool {
	switch t.State {
	case TaskStateStarting, TaskStateRunning, TaskStateHealthy, TaskStateUnhealthy, TaskStateStopping:
		return true
	default:
		return false
	}
}

// IsHealthy returns true if the task is in a healthy, serving state.
func (t *TaskActualState) IsHealthy() bool {
	return t.State == TaskStateHealthy || t.State == TaskStateRunning
}

// ServiceActualState represents the aggregate actual status derived from all active tasks of a service.
type ServiceActualState struct {
	ServiceID       id.ID               `json:"service_id" yaml:"service_id"`
	ServiceName     string              `json:"service_name" yaml:"service_name"`
	DesiredReplicas int                 `json:"desired_replicas" yaml:"desired_replicas"`
	ActualReplicas  int                 `json:"actual_replicas" yaml:"actual_replicas"`
	HealthyReplicas int                 `json:"healthy_replicas" yaml:"healthy_replicas"`
	FailedReplicas  int                 `json:"failed_replicas" yaml:"failed_replicas"`
	Status          ServiceActualStatus `json:"status" yaml:"status"`
	Tasks           []*TaskActualState  `json:"tasks" yaml:"tasks"`
	UpdatedAt       time.Time           `json:"updated_at" yaml:"updated_at"`
}

// DeriveServiceActualState aggregates task states into a derived ServiceActualState.
func DeriveServiceActualState(serviceID id.ID, serviceName string, desiredReplicas int, tasks []*TaskActualState) *ServiceActualState {
	var totalActive, healthyCount, failedCount int

	for _, task := range tasks {
		if task.IsActive() {
			totalActive++
		}
		if task.IsHealthy() {
			healthyCount++
		}
		if task.State == TaskStateFailed || task.State == TaskStateLost {
			failedCount++
		}
	}

	var status ServiceActualStatus
	if totalActive == 0 {
		if failedCount > 0 {
			status = ServiceStatusFailed
		} else {
			status = ServiceStatusPending
		}
	} else if healthyCount == desiredReplicas && desiredReplicas > 0 {
		status = ServiceStatusHealthy
	} else if healthyCount > 0 && healthyCount < desiredReplicas {
		status = ServiceStatusDegraded
	} else if totalActive > 0 && healthyCount == 0 {
		status = ServiceStatusDegraded
	} else {
		status = ServiceStatusRunning
	}

	return &ServiceActualState{
		ServiceID:       serviceID,
		ServiceName:     serviceName,
		DesiredReplicas: desiredReplicas,
		ActualReplicas:  totalActive,
		HealthyReplicas: healthyCount,
		FailedReplicas:  failedCount,
		Status:          status,
		Tasks:           tasks,
		UpdatedAt:       time.Now().UTC(),
	}
}

// StateDifference captures the delta between Desired State and Actual State for reconciliation.
type StateDifference struct {
	ServiceID       id.ID  `json:"service_id"`
	ServiceName     string `json:"service_name"`
	DesiredReplicas int    `json:"desired_replicas"`
	ActualReplicas  int    `json:"actual_replicas"`
	HealthyReplicas int    `json:"healthy_replicas"`
	NeedsScaleUp    int    `json:"needs_scale_up"`
	NeedsScaleDown  int    `json:"needs_scale_down"`
	HasDiverged     bool   `json:"has_diverged"`
	Reason          string `json:"reason,omitempty"`
}

// ComputeStateDifference compares a desired state with the actual derived state.
func ComputeStateDifference(desired *ServiceDesiredState, actual *ServiceActualState) StateDifference {
	diff := StateDifference{
		ServiceID:       desired.ID,
		ServiceName:     desired.Name,
		DesiredReplicas: desired.Replicas,
	}

	if actual != nil {
		diff.ActualReplicas = actual.ActualReplicas
		diff.HealthyReplicas = actual.HealthyReplicas
	}

	if diff.ActualReplicas < diff.DesiredReplicas {
		diff.NeedsScaleUp = diff.DesiredReplicas - diff.ActualReplicas
		diff.HasDiverged = true
		diff.Reason = "actual replicas below desired count"
	} else if diff.ActualReplicas > diff.DesiredReplicas {
		diff.NeedsScaleDown = diff.ActualReplicas - diff.DesiredReplicas
		diff.HasDiverged = true
		diff.Reason = "actual replicas exceed desired count"
	} else if diff.HealthyReplicas < diff.DesiredReplicas {
		diff.HasDiverged = true
		diff.Reason = "unhealthy tasks detected"
	}

	return diff
}
