package health

import (
	"context"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

func TestFailureDetector_StateTransitions(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	cfg := FailureDetectorConfig{
		CheckInterval:    50 * time.Millisecond,
		SuspectedTimeout: 200 * time.Millisecond,
		UnhealthyTimeout: 400 * time.Millisecond,
		LostTimeout:      600 * time.Millisecond,
	}

	fd := NewFailureDetector(cfg, store, logging.NewDefaultLogger())

	now := time.Now().UTC()
	workerID := id.NewWorkerID()
	nodeID := id.NewNodeID()

	// 1. Create Node & Worker in READY state with recent heartbeat
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "test-node",
		Address:   "127.0.0.1:7001",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})

	worker := &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   "127.0.0.1:7001",
		Status:    string(StatusReady),
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Workers().Create(ctx, worker); err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}

	// 2. Regular Heartbeat -> remains READY
	fd.EvaluateWorkers(ctx, now.Add(100*time.Millisecond))
	w, _ := store.Workers().Get(ctx, workerID)
	if w.Status != string(StatusReady) {
		t.Fatalf("expected status READY at 100ms, got %s", w.Status)
	}

	// 3. Temporary delay -> transitions to SUSPECTED (>200ms)
	fd.EvaluateWorkers(ctx, now.Add(250*time.Millisecond))
	w, _ = store.Workers().Get(ctx, workerID)
	if w.Status != string(StatusSuspected) {
		t.Fatalf("expected status SUSPECTED at 250ms, got %s", w.Status)
	}

	// 4. Recovery: Heartbeat arrives, status recovers to READY on evaluation
	w.Heartbeat = now.Add(260 * time.Millisecond)
	_ = store.Workers().Update(ctx, w)
	fd.EvaluateWorkers(ctx, now.Add(300*time.Millisecond))
	w, _ = store.Workers().Get(ctx, workerID)
	if w.Status != string(StatusReady) {
		t.Fatalf("expected status READY after heartbeat recovery, got %s", w.Status)
	}

	// 5. Multiple missed heartbeats -> transitions to UNHEALTHY (>400ms after last hb)
	fd.EvaluateWorkers(ctx, now.Add(700*time.Millisecond)) // 700 - 260 = 440ms
	w, _ = store.Workers().Get(ctx, workerID)
	if w.Status != string(StatusUnhealthy) {
		t.Fatalf("expected status UNHEALTHY at 440ms elapsed, got %s", w.Status)
	}

	// 6. Complete Disappearance -> transitions to LOST (>600ms elapsed)
	fd.EvaluateWorkers(ctx, now.Add(1000*time.Millisecond)) // 1000 - 260 = 740ms
	w, _ = store.Workers().Get(ctx, workerID)
	if w.Status != string(StatusLost) {
		t.Fatalf("expected status LOST at 740ms elapsed, got %s", w.Status)
	}

	// Verify events were generated for all state transitions
	events, err := store.Events().List(ctx, 100)
	if err != nil {
		t.Fatalf("failed to list events: %v", err)
	}
	if len(events) < 4 {
		t.Fatalf("expected at least 4 transition events, got %d", len(events))
	}
}

func TestFailureDetector_StartStopLoop(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	cfg := FailureDetectorConfig{
		CheckInterval:    20 * time.Millisecond,
		SuspectedTimeout: 50 * time.Millisecond,
		UnhealthyTimeout: 100 * time.Millisecond,
		LostTimeout:      150 * time.Millisecond,
	}

	fd := NewFailureDetector(cfg, store, logging.NewDefaultLogger())

	workerID := id.NewWorkerID()
	nodeID := id.NewNodeID()
	now := time.Now().UTC().Add(-200 * time.Millisecond) // old heartbeat

	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "node-loop",
		Address:   "127.0.0.1:7001",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})

	err = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   "127.0.0.1:7001",
		Status:    string(StatusReady),
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create worker in test: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := fd.Start(runCtx); err != nil {
		t.Fatalf("failed to start failure detector: %v", err)
	}

	// Wait for loop ticker to trigger and transition worker
	time.Sleep(100 * time.Millisecond)

	w, err := store.Workers().Get(ctx, workerID)
	if err != nil {
		t.Fatalf("failed to get worker: %v", err)
	}

	if w.Status == string(StatusReady) {
		t.Fatalf("expected loop to transition stale worker away from READY, got %s", w.Status)
	}

	_ = fd.Stop(context.Background())
}
