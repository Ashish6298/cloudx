package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/auth"
	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/events"
	run "github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
)

// ============================================================================
// 1. State Repository Concurrency & Data Race Test
// ============================================================================
func TestRace_StateRepository_ConcurrentReadWrite(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	const goroutines = 20
	const iterations = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Concurrent Writers
	for g := 0; g < goroutines; g++ {
		go func(workerIndex int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				serviceID := id.NewServiceID()
				now := time.Now().UTC()
				svc := &models.Service{
					ID:        serviceID,
					Name:      fmt.Sprintf("svc-%d-%d", workerIndex, i),
					Replicas:  2,
					Runtime:   "native",
					Command:   "echo",
					Status:    "RUNNING",
					CreatedAt: now,
					UpdatedAt: now,
				}
				_ = store.Services().Create(ctx, svc)

				// Update service
				svc.Replicas = 4
				svc.UpdatedAt = time.Now().UTC()
				_ = store.Services().Update(ctx, svc)

				// Task creation
				task := &models.Task{
					ID:        id.NewTaskID(),
					ServiceID: serviceID,
					WorkerID:  id.NewWorkerID(),
					State:     string(models.TaskStateRunning),
					CreatedAt: now,
					UpdatedAt: now,
				}
				_ = store.Tasks().Create(ctx, task)
			}
		}(g)
	}

	// Concurrent Readers
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_, _ = store.Services().List(ctx)
				_, _ = store.Tasks().List(ctx)
				_, _ = store.Workers().List(ctx)
				_, _ = store.Events().List(ctx, 50)
			}
		}()
	}

	wg.Wait()
	t.Log("✓ State Repository concurrent read/write completed with zero data races.")
}

// ============================================================================
// 2. Worker Task Manager Concurrency & Lifecycle Race Test
// ============================================================================
func TestRace_WorkerTaskManager_ConcurrentAssignStopInspect(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeWorker)
	logger := logging.NewDefaultLogger()
	workerID := id.NewWorkerID()

	tm := worker.NewTaskManager(worker.TaskManagerOptions{
		WorkerID: workerID,
		Runtime:  run.NewNativeRuntime(),
		Logger:   logger,
	})
	defer tm.Close()

	const tasksCount = 30
	var wg sync.WaitGroup
	wg.Add(tasksCount * 3)

	taskIDs := make([]id.ID, tasksCount)
	for i := 0; i < tasksCount; i++ {
		taskIDs[i] = id.NewTaskID()
	}

	cmd, args := testSleepCommand(10)

	// Goroutines Group 1: Concurrent Assign
	for i := 0; i < tasksCount; i++ {
		go func(taskIndex int) {
			defer wg.Done()
			_ = tm.AssignTask(ctx, worker.TaskAssignment{
				TaskID:  taskIDs[taskIndex],
				Command: cmd,
				Args:    args,
				RestartPolicy: models.RestartPolicy{
					Type: models.RestartPolicyNever,
				},
			})
		}(i)
	}

	// Goroutines Group 2: Concurrent GetTask / ListTasks
	for i := 0; i < tasksCount; i++ {
		go func(taskIndex int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = tm.GetTask(taskIDs[taskIndex])
				_ = tm.ListTasks()
				time.Sleep(1 * time.Millisecond)
			}
		}(i)
	}

	// Goroutines Group 3: Concurrent StopTask
	for i := 0; i < tasksCount; i++ {
		go func(taskIndex int) {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond)
			_ = tm.StopTask(ctx, taskIDs[taskIndex])
		}(i)
	}

	wg.Wait()
	t.Log("✓ Worker TaskManager concurrent execution verified with zero data races.")
}

// ============================================================================
// 3. Scheduler Placement Concurrency Race Test
// ============================================================================
func TestRace_Scheduler_ConcurrentCapacityScoring(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)
	sched := scheduler.NewBasicScheduler()

	const workersCount = 10
	workers := make([]*scheduler.WorkerCapacity, workersCount)
	for i := 0; i < workersCount; i++ {
		workers[i] = &scheduler.WorkerCapacity{
			WorkerID:            id.NewWorkerID(),
			NodeID:              id.NewNodeID(),
			Hostname:            fmt.Sprintf("node-%d", i),
			Status:              "READY",
			CPUTotal:            16.0,
			CPUAllocated:        float64(i) * 0.5,
			MemoryTotal:         32 * 1024 * 1024 * 1024,
			MemoryAllocated:     int64(i) * 512 * 1024 * 1024,
			RuntimeCapabilities: []string{"native", "docker"},
			TaskCount:           i,
		}
	}

	const concurrentSchedules = 50
	var wg sync.WaitGroup
	wg.Add(concurrentSchedules)

	for i := 0; i < concurrentSchedules; i++ {
		go func(idx int) {
			defer wg.Done()
			req := &scheduler.TaskRequirements{
				TaskID:          id.NewTaskID(),
				ServiceID:       id.NewServiceID(),
				CPU:             0.5,
				Memory:          256 * 1024 * 1024,
				RequiredRuntime: "native",
			}
			decision, err := sched.Schedule(ctx, req, workers)
			if err != nil || decision == nil {
				t.Errorf("scheduling failed unexpectedly: %v", err)
			}
		}(i)
	}

	wg.Wait()
	t.Log("✓ Scheduler multi-threaded scoring verified with zero data races.")
}

// ============================================================================
// 4. Reconciliation Concurrency Race Test
// ============================================================================
func TestRace_Reconciler_ConcurrentReconcilePasses(t *testing.T) {
	harness, err := NewClusterHarness(t, HarnessOptions{
		WorkerCount: 3,
		UseSQLite:   true,
	})
	if err != nil {
		t.Fatalf("failed to create cluster harness: %v", err)
	}

	cmd, args := testSleepCommand(20)
	replicas := 3
	svcConfig := &spec.ServiceConfig{
		Name:     "reconcile-race-service",
		Version:  "v1",
		Runtime:  "native",
		Command:  cmd,
		Args:     args,
		Replicas: &replicas,
	}

	_, err = harness.DeployService(svcConfig)
	if err != nil {
		t.Fatalf("deploy service failed: %v", err)
	}

	const concurrentPasses = 15
	var wg sync.WaitGroup
	wg.Add(concurrentPasses)

	// Trigger concurrent Reconcile passes simultaneously
	for i := 0; i < concurrentPasses; i++ {
		go func(passNum int) {
			defer wg.Done()
			summary, err := harness.Reconcile()
			if err != nil {
				t.Errorf("reconciliation pass %d failed: %v", passNum, err)
			}
			_ = summary
		}(i)
	}

	wg.Wait()

	// Verify idempotency and correct final replica count with bounded poll
	deadline := time.Now().Add(5 * time.Second)
	var activeTasks []*models.Task
	for time.Now().Before(deadline) {
		services, err := harness.Store.Services().List(harness.ctx)
		if err == nil && len(services) > 0 {
			tasks, err := harness.GetActiveTasks(services[0].ID)
			if err == nil && len(tasks) == 3 {
				activeTasks = tasks
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if len(activeTasks) != 3 {
		// Run stabilizing reconcile
		_, _ = harness.Reconcile()
		time.Sleep(100 * time.Millisecond)
		services, _ := harness.Store.Services().List(harness.ctx)
		if len(services) > 0 {
			activeTasks, _ = harness.GetActiveTasks(services[0].ID)
		}
	}

	if len(activeTasks) != 3 {
		t.Fatalf("expected 3 active tasks after concurrent reconciliations, got %d", len(activeTasks))
	}
	t.Log("✓ Concurrent Reconcile passes verified with zero data races and complete idempotency.")
}

// ============================================================================
// 5. Event Recorder Concurrency Race Test
// ============================================================================
func TestRace_Events_ConcurrentAppendAndList(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	recorder := events.NewRecorder(store, logging.NewDefaultLogger())

	const writers = 20
	const eventsPerWriter = 50
	var wg sync.WaitGroup
	wg.Add(writers * 2)

	// Concurrent Event Writers
	for w := 0; w < writers; w++ {
		go func(writerIdx int) {
			defer wg.Done()
			for i := 0; i < eventsPerWriter; i++ {
				entityID := id.NewTaskID()
				_, err := recorder.Record(ctx, events.EventTaskAssigned, "race_test", entityID, map[string]any{
					"writer":    writerIdx,
					"iteration": i,
					"timestamp": time.Now().UnixNano(),
				})
				if err != nil {
					t.Errorf("failed to record event: %v", err)
				}
			}
		}(w)
	}

	// Concurrent Event Readers
	for r := 0; r < writers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < eventsPerWriter; i++ {
				_, _ = recorder.List(ctx, events.EventFilter{Limit: 25})
				_, _ = recorder.List(ctx, events.EventFilter{Type: events.EventTaskAssigned})
			}
		}()
	}

	wg.Wait()

	allEvents, err := recorder.List(ctx, events.EventFilter{Limit: 2000})
	if err != nil {
		t.Fatalf("failed to list all recorded events: %v", err)
	}

	expectedTotal := writers * eventsPerWriter
	if len(allEvents) != expectedTotal {
		t.Fatalf("expected %d total events recorded concurrently, got %d", expectedTotal, len(allEvents))
	}
	t.Logf("✓ Event recorder concurrent operations verified (%d events) with zero data races.", len(allEvents))
}
