package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

// mockDispatcher implements scheduler.Dispatcher for testing
type mockDispatcher struct{}

func (m *mockDispatcher) Dispatch(ctx context.Context, worker *models.Worker, task *models.Task, spec *scheduler.TaskSpec) error {
	return nil
}

// TestNodeDrain_Lifecycle_Ready_Draining_Empty validates the complete lifecycle:
// 1. Cluster has Worker A and Worker B (both READY).
// 2. Service running with 2 replicas (1 on Worker A, 1 on Worker B).
// 3. Worker B is set to DRAINING.
// 4. BasicScheduler rejects Worker B from receiving new tasks.
// 5. Reconciler detects Worker B is DRAINING, evicts task on Worker B, reschedules it to Worker A.
// 6. Worker B now has 0 active tasks -> Reconciler marks Worker B as EMPTY.
func TestNodeDrain_Lifecycle_Ready_Draining_Empty(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	workerAID := id.NewWorkerID()
	nodeAID := id.NewNodeID()
	workerBID := id.NewWorkerID()
	nodeBID := id.NewNodeID()

	// 1. Create 2 Workers: Worker A and Worker B (Status: READY)
	_ = store.Nodes().Create(ctx, &models.Node{
		ID: nodeAID, Name: "Machine-A", Status: "READY", CreatedAt: now, UpdatedAt: now,
	})
	_ = store.Workers().Create(ctx, &models.Worker{
		ID: workerAID, NodeID: nodeAID, Address: "10.0.0.1:7001", Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now,
	})

	_ = store.Nodes().Create(ctx, &models.Node{
		ID: nodeBID, Name: "Machine-B", Status: "READY", CreatedAt: now, UpdatedAt: now,
	})
	_ = store.Workers().Create(ctx, &models.Worker{
		ID: workerBID, NodeID: nodeBID, Address: "10.0.0.2:7001", Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now,
	})

	// 2. Create Service with 2 desired replicas
	serviceID := id.NewServiceID()
	deploymentID := id.NewDeploymentID()

	svc := &models.Service{
		ID:        serviceID,
		Name:      "web-api",
		Replicas:  2,
		Runtime:   "native",
		Command:   "sleep 1000",
		Status:    "RUNNING",
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

	// Place 1 task on Worker A and 1 task on Worker B
	task1ID := id.NewTaskID()
	_ = store.Tasks().Create(ctx, &models.Task{
		ID:           task1ID,
		ServiceID:    serviceID,
		DeploymentID: deploymentID,
		WorkerID:     workerAID,
		State:        string(models.TaskStateRunning),
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	task2ID := id.NewTaskID()
	_ = store.Tasks().Create(ctx, &models.Task{
		ID:           task2ID,
		ServiceID:    serviceID,
		DeploymentID: deploymentID,
		WorkerID:     workerBID,
		State:        string(models.TaskStateRunning),
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	// 3. Mark Worker B as DRAINING
	wB, _ := store.Workers().Get(ctx, workerBID)
	wB.Status = "DRAINING"
	wB.UpdatedAt = time.Now().UTC()
	_ = store.Workers().Update(ctx, wB)

	// 4. Verify Scheduler rejects Worker B
	sched := scheduler.NewBasicScheduler()
	taskReq := &scheduler.TaskRequirements{
		TaskID:          id.NewTaskID(),
		ServiceID:       serviceID,
		CPU:             0.5,
		Memory:          256 * 1024 * 1024,
		RequiredRuntime: "native",
	}

	wCapA := &scheduler.WorkerCapacity{
		WorkerID:            workerAID,
		Hostname:            "Machine-A",
		Status:              "READY",
		CPUTotal:            4.0,
		MemoryTotal:         8 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
	}
	wCapB := &scheduler.WorkerCapacity{
		WorkerID:            workerBID,
		Hostname:            "Machine-B",
		Status:              "DRAINING", // DRAINING status
		CPUTotal:            4.0,
		MemoryTotal:         8 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
	}

	decision, err := sched.Schedule(ctx, taskReq, []*scheduler.WorkerCapacity{wCapA, wCapB})
	if err != nil {
		t.Fatalf("Scheduler failed: %v", err)
	}
	if decision.WorkerID != workerAID {
		t.Fatalf("Expected scheduler to choose READY Worker A (%s), but got %s", workerAID, decision.WorkerID)
	}

	// 5. Run Reconciler to evict task on Worker B and move to Worker A
	logger := logging.NewDefaultLogger()
	dispatcher := &mockDispatcher{}
	reconciler := NewReconciler(DefaultReconcilerConfig(), store, sched, dispatcher, logger)

	summary, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("Reconciler failed: %v", err)
	}

	t.Logf("Reconciler summary: %+v", summary)

	// Verify task 2 on Worker B was STOPPED/evicted
	t2, _ := store.Tasks().Get(ctx, task2ID)
	if t2.State != string(models.TaskStateStopped) {
		t.Errorf("Expected task2 on draining worker to be STOPPED, got state %s", t2.State)
	}

	// Verify Worker B transitioned to EMPTY
	wBAfter, _ := store.Workers().Get(ctx, workerBID)
	if wBAfter.Status != "EMPTY" {
		t.Errorf("Expected Worker B to transition to EMPTY, got %s", wBAfter.Status)
	}

	// Verify new replacement task was created and scheduled on Worker A
	allTasks, _ := store.Tasks().ListByService(ctx, serviceID)
	var activeCountOnWorkerA int
	var activeCountOnWorkerB int
	for _, t := range allTasks {
		if t.State != string(models.TaskStateStopped) && t.State != string(models.TaskStateFailed) && t.State != string(models.TaskStateLost) {
			if t.WorkerID == workerAID {
				activeCountOnWorkerA++
			}
			if t.WorkerID == workerBID {
				activeCountOnWorkerB++
			}
		}
	}

	if activeCountOnWorkerB != 0 {
		t.Errorf("Expected 0 active tasks on Worker B, got %d", activeCountOnWorkerB)
	}
	if activeCountOnWorkerA != 2 {
		t.Errorf("Expected 2 active tasks on Worker A, got %d", activeCountOnWorkerA)
	}
}
