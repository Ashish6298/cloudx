package models

import (
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

func TestTaskActualStateTracking(t *testing.T) {
	now := time.Now().UTC()
	taskID := id.NewTaskID()
	srvID := id.NewServiceID()
	wrkID := id.NewWorkerID()

	task := &TaskActualState{
		ID:            taskID,
		ServiceID:     srvID,
		WorkerID:      wrkID,
		State:         TaskStateHealthy,
		PID:           8821,
		StartTime:     now.Add(-10 * time.Minute),
		ExitCode:      0,
		RuntimeState:  "active",
		HealthState:   "healthy",
		LastHeartbeat: now,
		Resources: TaskResourceUsage{
			CPUPercent: 12.5,
			MemoryUsed: 128 * 1024 * 1024,
		},
		UpdatedAt: now,
	}

	if !task.IsActive() {
		t.Errorf("expected healthy task to be active")
	}
	if !task.IsHealthy() {
		t.Errorf("expected task to be healthy")
	}

	task.State = TaskStateFailed
	task.ExitCode = 137
	if task.IsActive() {
		t.Errorf("expected failed task to not be active")
	}
	if task.IsHealthy() {
		t.Errorf("expected failed task to not be healthy")
	}
}

func TestDeriveServiceActualState(t *testing.T) {
	srvID := id.NewServiceID()
	wrkID := id.NewWorkerID()
	now := time.Now().UTC()

	// Scenario: 3 desired replicas, 2 running & healthy, 1 starting
	tasks := []*TaskActualState{
		{ID: id.NewTaskID(), ServiceID: srvID, WorkerID: wrkID, State: TaskStateHealthy, LastHeartbeat: now},
		{ID: id.NewTaskID(), ServiceID: srvID, WorkerID: wrkID, State: TaskStateRunning, LastHeartbeat: now},
		{ID: id.NewTaskID(), ServiceID: srvID, WorkerID: wrkID, State: TaskStateStarting, LastHeartbeat: now},
	}

	derived := DeriveServiceActualState(srvID, "web-api", 3, tasks)
	if derived.ActualReplicas != 3 {
		t.Errorf("expected 3 actual replicas, got %d", derived.ActualReplicas)
	}
	if derived.HealthyReplicas != 2 {
		t.Errorf("expected 2 healthy replicas, got %d", derived.HealthyReplicas)
	}
	if derived.Status != ServiceStatusDegraded {
		t.Errorf("expected status DEGRADED, got %s", derived.Status)
	}

	// Make 3rd task healthy
	tasks[2].State = TaskStateHealthy
	derivedHealthy := DeriveServiceActualState(srvID, "web-api", 3, tasks)
	if derivedHealthy.Status != ServiceStatusHealthy {
		t.Errorf("expected status HEALTHY, got %s", derivedHealthy.Status)
	}
}

func TestDesiredStateNotEqualsActualState(t *testing.T) {
	srvID := id.NewServiceID()
	wrkID := id.NewWorkerID()
	now := time.Now().UTC()

	desired := &ServiceDesiredState{
		ID:       srvID,
		Name:     "payment-gateway",
		Version:  "v1.0.0",
		Replicas: 4,
		Command:  "./payment-service",
	}

	// 1. Case: Under-replicated (Scale Up needed)
	actualTasks := []*TaskActualState{
		{ID: id.NewTaskID(), ServiceID: srvID, WorkerID: wrkID, State: TaskStateHealthy, LastHeartbeat: now},
		{ID: id.NewTaskID(), ServiceID: srvID, WorkerID: wrkID, State: TaskStateHealthy, LastHeartbeat: now},
	}
	actual := DeriveServiceActualState(srvID, "payment-gateway", 4, actualTasks)

	diff := ComputeStateDifference(desired, actual)
	if !diff.HasDiverged {
		t.Fatalf("expected state divergence, got false")
	}
	if diff.NeedsScaleUp != 2 {
		t.Errorf("expected NeedsScaleUp=2, got %d", diff.NeedsScaleUp)
	}
	if diff.NeedsScaleDown != 0 {
		t.Errorf("expected NeedsScaleDown=0, got %d", diff.NeedsScaleDown)
	}

	// 2. Case: Over-replicated (Scale Down needed)
	desired.Replicas = 1
	diffOver := ComputeStateDifference(desired, actual)
	if !diffOver.HasDiverged {
		t.Fatalf("expected state divergence, got false")
	}
	if diffOver.NeedsScaleDown != 1 {
		t.Errorf("expected NeedsScaleDown=1, got %d", diffOver.NeedsScaleDown)
	}

	// 3. Case: Converged (Desired == Actual)
	desired.Replicas = 2
	diffConverged := ComputeStateDifference(desired, actual)
	if diffConverged.HasDiverged {
		t.Errorf("expected converged state (HasDiverged=false), got true")
	}
}
