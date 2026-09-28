package controlplane

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

// setupBenchmarkCluster provisions n workers, s services, and t total tasks.
func setupBenchmarkCluster(ctx context.Context, store *sqlite.Store, numWorkers, numServices, totalTasks int) (scheduler.Dispatcher, func(), error) {
	logger := logging.New(logging.LevelError, logging.FormatText)
	now := time.Now().UTC()
	dispatcher := scheduler.NewInProcessDispatcher()
	var managers []*worker.TaskManager

	workerIDs := make([]id.ID, numWorkers)
	for i := 0; i < numWorkers; i++ {
		wID := id.NewWorkerID()
		nID := id.NewNodeID()
		workerIDs[i] = wID

		_ = store.Nodes().Create(ctx, &models.Node{
			ID:        nID,
			Name:      fmt.Sprintf("node-%d", i),
			Status:    "READY",
			CreatedAt: now,
			UpdatedAt: now,
		})
		_ = store.Workers().Create(ctx, &models.Worker{
			ID:        wID,
			NodeID:    nID,
			Address:   fmt.Sprintf("127.0.0.1:%d", 7001+i),
			Status:    "READY",
			Heartbeat: now,
			CreatedAt: now,
			UpdatedAt: now,
		})

		tm := worker.NewTaskManager(worker.TaskManagerOptions{
			WorkerID: wID,
			Runtime:  runtime.NewNativeRuntime(),
			Logger:   logger,
		})
		managers = append(managers, tm)

		capturedTM := tm
		dispatcher.RegisterWorkerHandler(wID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
			return capturedTM.AssignTask(ctx, worker.TaskAssignment{
				TaskID:  id.ID(req.Task.Id),
				Command: req.Command,
				Args:    req.Args,
			})
		})
	}

	tasksPerService := 1
	if numServices > 0 {
		tasksPerService = totalTasks / numServices
		if tasksPerService < 1 {
			tasksPerService = 1
		}
	}

	taskCounter := 0
	for i := 0; i < numServices; i++ {
		sID := id.NewServiceID()
		replicas := tasksPerService
		if taskCounter+replicas > totalTasks && totalTasks > 0 {
			replicas = totalTasks - taskCounter
		}
		taskCounter += replicas

		_ = store.Services().Create(ctx, &models.Service{
			ID:        sID,
			Name:      fmt.Sprintf("bench-service-%04d", i),
			Replicas:  replicas,
			Runtime:   "native",
			Command:   "echo bench",
			Status:    "ACTIVE",
			SpecJSON:  `{"command":"echo","args":["bench"],"runtime":"native","resources":{"cpu":"100m","memory":"64Mi"}}`,
			CreatedAt: now,
			UpdatedAt: now,
		})

		// Pre-populate tasks to simulate steady-state convergence
		for r := 0; r < replicas; r++ {
			assignedWorker := workerIDs[(i*tasksPerService+r)%numWorkers]
			_ = store.Tasks().Create(ctx, &models.Task{
				ID:        id.NewTaskID(),
				ServiceID: sID,
				WorkerID:  assignedWorker,
				State:     string(models.TaskStateRunning),
				PID:       1000 + r,
				CreatedAt: now,
				UpdatedAt: now,
			})
		}
	}

	cleanup := func() {
		for _, tm := range managers {
			_ = tm.Close()
		}
	}

	return dispatcher, cleanup, nil
}

// TestReconciliation_ScaleBenchmark performs detailed measurement across 10 services, 100 services, and 1000 tasks.
func TestReconciliation_ScaleBenchmark(t *testing.T) {
	type testScenario struct {
		name        string
		numWorkers  int
		numServices int
		totalTasks  int
	}

	scenarios := []testScenario{
		{name: "10 Services (20 Tasks, 3 Workers)", numWorkers: 3, numServices: 10, totalTasks: 20},
		{name: "100 Services (200 Tasks, 10 Workers)", numWorkers: 10, numServices: 100, totalTasks: 200},
		{name: "1000 Tasks (250 Services, 20 Workers)", numWorkers: 20, numServices: 250, totalTasks: 1000},
	}

	fmt.Printf("\n=== RECONCILIATION SCALE BENCHMARK REPORT (PHASE 77) ===\n")
	fmt.Printf("%-40s %-16s %-16s %-16s %-15s\n", "SCENARIO", "DURATION", "SERVICES EVAL", "TASKS EVAL", "EVENTS GEN")

	for _, sc := range scenarios {
		ctx := context.Background()
		store, err := sqlite.Open(ctx, ":memory:")
		if err != nil {
			t.Fatalf("failed to open sqlite in-memory store: %v", err)
		}

		dispatcher, cleanup, err := setupBenchmarkCluster(ctx, store, sc.numWorkers, sc.numServices, sc.totalTasks)
		if err != nil {
			store.Close()
			t.Fatalf("failed to setup benchmark cluster: %v", err)
		}

		logger := logging.New(logging.LevelError, logging.FormatText)
		sched := scheduler.NewBasicScheduler()
		recon := NewReconciler(DefaultReconcilerConfig(), store, sched, dispatcher, logger)

		// Measure single full reconciliation sweep
		startEvents, _ := store.Events().List(ctx, 10000)
		t0 := time.Now()
		summary, err := recon.ReconcileAll(ctx)
		elapsed := time.Since(t0)
		endEvents, _ := store.Events().List(ctx, 10000)
		eventsGen := len(endEvents) - len(startEvents)

		if err != nil {
			t.Fatalf("reconciliation failed for scenario %s: %v", sc.name, err)
		}

		fmt.Printf("%-40s %-16v %-16d %-16d %-15d\n",
			sc.name, elapsed, summary.EvaluatedServices, sc.totalTasks, eventsGen)

		cleanup()
		store.Close()
	}
	fmt.Printf("========================================================\n\n")
}

// BenchmarkReconciliation_Sweep measures pure ns/op and memory allocations of reconciliation passes.
func BenchmarkReconciliation_Sweep(b *testing.B) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		b.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	dispatcher, cleanup, err := setupBenchmarkCluster(ctx, store, 10, 50, 100)
	if err != nil {
		b.Fatalf("failed to setup cluster: %v", err)
	}
	defer cleanup()

	logger := logging.New(logging.LevelError, logging.FormatText)
	sched := scheduler.NewBasicScheduler()
	recon := NewReconciler(DefaultReconcilerConfig(), store, sched, dispatcher, logger)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := recon.ReconcileAll(ctx)
		if err != nil {
			b.Fatalf("reconciliation failed: %v", err)
		}
	}
}
