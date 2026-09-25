package events

import (
	"context"
	"testing"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

// TestEventRecorder_AppendAndFilter verifies persistent append-only event recording and filtering.
func TestEventRecorder_AppendAndFilter(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	recorder := NewRecorder(store, logger)

	serviceID := id.NewServiceID()
	workerID := id.NewWorkerID()
	taskID := id.NewTaskID()
	depID := id.NewDeploymentID()

	// 1. Record series of cluster events
	ev1, err := recorder.Record(ctx, EventWorkerRegistered, "controlplane_api", workerID, map[string]string{
		"address": "127.0.0.1:9001",
		"status":  "READY",
	})
	if err != nil {
		t.Fatalf("failed to record ev1: %v", err)
	}
	if ev1.Type != EventWorkerRegistered || ev1.EntityID != workerID {
		t.Fatalf("unexpected event 1 attributes: %+v", ev1)
	}

	ev2, err := recorder.Record(ctx, EventServiceCreated, "controlplane", serviceID, map[string]any{
		"service":  "api",
		"replicas": 3,
	})
	if err != nil {
		t.Fatalf("failed to record ev2: %v", err)
	}

	ev3, err := recorder.Record(ctx, EventDeploymentStarted, "controlplane", depID, map[string]string{
		"version": "v1",
	})
	if err != nil {
		t.Fatalf("failed to record ev3: %v", err)
	}

	ev4, err := recorder.Record(ctx, EventTaskAssigned, "assignment_coordinator", taskID, map[string]string{
		"worker_id": workerID.String(),
	})
	if err != nil {
		t.Fatalf("failed to record ev4: %v", err)
	}

	ev5, err := recorder.Record(ctx, EventProcessStarted, "worker_task_manager", taskID, map[string]int{
		"pid": 12345,
	})
	if err != nil {
		t.Fatalf("failed to record ev5: %v", err)
	}

	ev6, err := recorder.Record(ctx, EventHealthCheckFailed, "task_health_monitor", taskID, map[string]string{
		"reason": "connection refused",
	})
	if err != nil {
		t.Fatalf("failed to record ev6: %v", err)
	}

	ev7, err := recorder.Record(ctx, EventProcessCrashed, "worker_task_manager", taskID, map[string]int{
		"exit_code": 1,
	})
	if err != nil {
		t.Fatalf("failed to record ev7: %v", err)
	}

	ev8, err := recorder.Record(ctx, EventWorkerLost, "failure_detector", workerID, map[string]string{
		"reason": "heartbeat_timeout",
	})
	if err != nil {
		t.Fatalf("failed to record ev8: %v", err)
	}

	ev9, err := recorder.Record(ctx, EventTaskRescheduled, "reconciler", taskID, map[string]string{
		"reason": "worker_lost",
	})
	if err != nil {
		t.Fatalf("failed to record ev9: %v", err)
	}

	ev10, err := recorder.Record(ctx, EventDeploymentRolledBack, "controlplane", depID, map[string]string{
		"from": "v2",
		"to":   "v1",
	})
	if err != nil {
		t.Fatalf("failed to record ev10: %v", err)
	}

	// 2. Query all events (limit 20)
	allEvents, err := recorder.List(ctx, EventFilter{Limit: 20})
	if err != nil {
		t.Fatalf("failed to list events: %v", err)
	}
	if len(allEvents) != 10 {
		t.Fatalf("expected 10 recorded events, got %d", len(allEvents))
	}

	// 3. Filter by Type
	workerEvents, err := recorder.List(ctx, EventFilter{Type: EventWorkerRegistered})
	if err != nil || len(workerEvents) != 1 {
		t.Fatalf("expected 1 WORKER_REGISTERED event, got %d (err: %v)", len(workerEvents), err)
	}

	// 4. Query by Entity ID
	taskEvents, err := recorder.ListByEntity(ctx, taskID)
	if err != nil {
		t.Fatalf("failed to list events by entity: %v", err)
	}
	if len(taskEvents) != 5 { // TaskAssigned, ProcessStarted, HealthCheckFailed, ProcessCrashed, TaskRescheduled
		t.Fatalf("expected 5 events for task entity %s, got %d", taskID, len(taskEvents))
	}

	_ = ev2
	_ = ev3
	_ = ev4
	_ = ev5
	_ = ev6
	_ = ev7
	_ = ev8
	_ = ev9
	_ = ev10
}
