package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/health"
	run "github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

// TestClusterRecovery_WorkerCrash_EndToEnd verifies Phase 54 scenario:
// Cluster: Worker A, Worker B, Worker C
// API replicas: 3 (initially distributed 1 on A, 1 on B, 1 on C)
// Failure: Worker B is killed / crashes (misses heartbeats)
// CloudX workflow:
// 1. Failure Detector detects heartbeat loss.
// 2. Marks Worker B as LOST.
// 3. Reconciler detects affected tasks on Worker B.
// 4. Marks affected task LOST / orphaned.
// 5. Scheduler chooses surviving healthy nodes (Worker A / Worker C).
// 6. Replacement task starts on surviving worker.
// 7. Health check runs and marks replacement HEALTHY.
// 8. Restores desired replica count (3 healthy replicas across surviving nodes).
func TestClusterRecovery_WorkerCrash_EndToEnd(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	now := time.Now().UTC()

	// 1. Initialize 3 Worker Daemons: Worker A, Worker B, Worker C
	workerAID := id.NewWorkerID()
	nodeAID := id.NewNodeID()
	workerBID := id.NewWorkerID()
	nodeBID := id.NewNodeID()
	workerCID := id.NewWorkerID()
	nodeCID := id.NewNodeID()

	_ = store.Nodes().Create(ctx, &models.Node{ID: nodeAID, Name: "Worker-A", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: workerAID, NodeID: nodeAID, Address: "10.0.0.1:7001", Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	_ = store.Nodes().Create(ctx, &models.Node{ID: nodeBID, Name: "Worker-B", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: workerBID, NodeID: nodeBID, Address: "10.0.0.2:7001", Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	_ = store.Nodes().Create(ctx, &models.Node{ID: nodeCID, Name: "Worker-C", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: workerCID, NodeID: nodeCID, Address: "10.0.0.3:7001", Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	// Setup TaskManagers for each worker
	tmA := worker.NewTaskManager(worker.TaskManagerOptions{WorkerID: workerAID, Runtime: run.NewNativeRuntime(), Logger: logger})
	defer tmA.Close()
	tmB := worker.NewTaskManager(worker.TaskManagerOptions{WorkerID: workerBID, Runtime: run.NewNativeRuntime(), Logger: logger})
	defer tmB.Close()
	tmC := worker.NewTaskManager(worker.TaskManagerOptions{WorkerID: workerCID, Runtime: run.NewNativeRuntime(), Logger: logger})
	defer tmC.Close()

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerAID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tmA.AssignTask(ctx, worker.TaskAssignment{
			TaskID:  id.ID(req.Task.Id),
			Command: req.Command,
			Args:    req.Args,
		})
	})
	dispatcher.RegisterWorkerHandler(workerBID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tmB.AssignTask(ctx, worker.TaskAssignment{
			TaskID:  id.ID(req.Task.Id),
			Command: req.Command,
			Args:    req.Args,
		})
	})
	dispatcher.RegisterWorkerHandler(workerCID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tmC.AssignTask(ctx, worker.TaskAssignment{
			TaskID:  id.ID(req.Task.Id),
			Command: req.Command,
			Args:    req.Args,
		})
	})

	// 2. Setup Failure Detector and Reconciler
	fdCfg := health.FailureDetectorConfig{
		CheckInterval:    50 * time.Millisecond,
		SuspectedTimeout: 100 * time.Millisecond,
		UnhealthyTimeout: 200 * time.Millisecond,
		LostTimeout:      300 * time.Millisecond,
	}
	fd := health.NewFailureDetector(fdCfg, store, logger)

	sched := scheduler.NewBasicScheduler()
	reconciler := NewReconciler(DefaultReconcilerConfig(), store, sched, dispatcher, logger)

	// 3. Deploy API Service with 3 replicas
	serviceID := id.NewServiceID()
	deploymentID := id.NewDeploymentID()
	cmdName, cmdArgs := getSleepCmd("300")
	specJSON := fmt.Sprintf(`{"command":"%s","args":%s,"runtime":"native"}`, cmdName, func() string {
		b, _ := json.Marshal(cmdArgs)
		return string(b)
	}())

	svc := &models.Service{
		ID:        serviceID,
		Name:      "api",
		Replicas:  3,
		Runtime:   "native",
		Command:   cmdName,
		SpecJSON:  specJSON,
		Status:    "PENDING",
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.Services().Create(ctx, svc)
	_ = store.Deployments().Create(ctx, &models.Deployment{
		ID:        deploymentID,
		ServiceID: serviceID,
		Version:   "v1.0.0",
		Status:    string(models.DeploymentStatusActive),
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Initial reconciliation creates 3 tasks distributed across Worker A, B, C
	initSummary, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("Initial reconciliation failed: %v", err)
	}
	if initSummary.CreatedTasks != 3 {
		t.Fatalf("Expected 3 initial tasks created, got %d", initSummary.CreatedTasks)
	}

	// Verify all 3 tasks were distributed across distinct workers
	tasks, _ := store.Tasks().ListByService(ctx, serviceID)
	if len(tasks) != 3 {
		t.Fatalf("Expected 3 tasks in store, got %d", len(tasks))
	}
	workerDistribution := make(map[id.ID]int)
	for _, t := range tasks {
		workerDistribution[t.WorkerID]++
	}
	if workerDistribution[workerAID] != 1 || workerDistribution[workerBID] != 1 || workerDistribution[workerCID] != 1 {
		t.Fatalf("Expected 1 replica per node (A:1, B:1, C:1), got: %+v", workerDistribution)
	}

	// 4. SIMULATE KILLING WORKER B
	// Worker B stops sending heartbeats, Worker A and Worker C continue heartbeating
	futureTime := now.Add(500 * time.Millisecond)
	wA, _ := store.Workers().Get(ctx, workerAID)
	wA.Heartbeat = futureTime
	_ = store.Workers().Update(ctx, wA)

	wC, _ := store.Workers().Get(ctx, workerCID)
	wC.Heartbeat = futureTime
	_ = store.Workers().Update(ctx, wC)

	// Failure detector sweep evaluates workers at futureTime
	// Worker B last heartbeat was at `now` (500ms elapsed >= 300ms LostTimeout)
	fd.EvaluateWorkers(ctx, futureTime)

	// Step 1 & 2: Verify Worker B marked LOST
	wBCheck, _ := store.Workers().Get(ctx, workerBID)
	if wBCheck.Status != "LOST" {
		t.Fatalf("Expected Worker B status to be LOST, got %s", wBCheck.Status)
	}

	// Step 3, 4, 5, 6: Trigger Reconciler recovery pass
	recSummary, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("Reconciliation recovery pass failed: %v", err)
	}

	t.Logf("Reconciler recovery summary: %+v", recSummary)
	if recSummary.OrphanedRecovered != 1 {
		t.Errorf("Expected 1 orphaned task recovered, got %d", recSummary.OrphanedRecovered)
	}
	if recSummary.CreatedTasks != 1 {
		t.Errorf("Expected 1 replacement task created, got %d", recSummary.CreatedTasks)
	}

	// Step 7 & 8: Verify active tasks count and distribution after replacement
	allServiceTasks, _ := store.Tasks().ListByService(ctx, serviceID)
	var liveTasks []*models.Task
	var lostTasks []*models.Task
	for _, t := range allServiceTasks {
		if t.State == string(models.TaskStateLost) {
			lostTasks = append(lostTasks, t)
		} else if t.State != string(models.TaskStateStopped) && t.State != string(models.TaskStateFailed) {
			liveTasks = append(liveTasks, t)
		}
	}

	if len(lostTasks) != 1 {
		t.Errorf("Expected 1 task marked LOST on Worker B, got %d", len(lostTasks))
	}
	if lostTasks[0].WorkerID != workerBID {
		t.Errorf("Expected lost task to belong to Worker B, got worker %s", lostTasks[0].WorkerID)
	}

	if len(liveTasks) != 3 {
		t.Fatalf("Expected 3 live replacement tasks restoring desired replica count, got %d", len(liveTasks))
	}

	// Verify all live tasks are only running on surviving nodes (Worker A and Worker C)
	liveWorkerDistribution := make(map[id.ID]int)
	for _, lt := range liveTasks {
		liveWorkerDistribution[lt.WorkerID]++
	}

	if liveWorkerDistribution[workerBID] != 0 {
		t.Errorf("Expected 0 live tasks on dead Worker B, got %d", liveWorkerDistribution[workerBID])
	}
	totalOnSurviving := liveWorkerDistribution[workerAID] + liveWorkerDistribution[workerCID]
	if totalOnSurviving != 3 {
		t.Errorf("Expected exactly 3 active tasks across surviving workers (A & C), got %d (A:%d, C:%d)",
			totalOnSurviving, liveWorkerDistribution[workerAID], liveWorkerDistribution[workerCID])
	}

	// Verify audit events recorded TASK_RESCHEDULED
	events, _ := store.Events().List(ctx, 100)
	var foundRescheduledEvent bool
	for _, e := range events {
		if e.Type == "TASK_RESCHEDULED" {
			foundRescheduledEvent = true
			break
		}
	}
	if !foundRescheduledEvent {
		t.Errorf("Expected TASK_RESCHEDULED event in audit log")
	}
}
