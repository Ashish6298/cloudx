package controlplane

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	run "github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

func getEchoCmd(text string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-Command", fmt.Sprintf("Write-Output '%s'", text)}
	}
	return "sh", []string{"-c", fmt.Sprintf("echo '%s'", text)}
}

func TestJobLifecycle_CancelAndRetry(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	now := time.Now().UTC()

	// 1. Setup Worker
	workerID := id.NewWorkerID()
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{ID: nodeID, Name: "node-1", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: workerID, NodeID: nodeID, Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	cp, err := New(Options{
		Store:  store,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to initialize control plane: %v", err)
	}

	cmdName, cmdArgs := getEchoCmd("batch-run")
	jobCfg := &spec.JobConfig{
		Name:    "cancellable-job",
		Command: cmdName,
		Args:    cmdArgs,
		Timeout: "10m",
		RetryPolicy: &spec.JobRetrySpec{
			MaxRetries:    3,
			BackoffPeriod: "2s",
		},
	}

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return nil
	})

	// 2. Submit Job
	runRes, err := cp.RunJob(ctx, jobCfg, dispatcher)
	if err != nil {
		t.Fatalf("RunJob failed: %v", err)
	}

	// 3. Test Cancellation
	if err := cp.CancelJob(ctx, "cancellable-job"); err != nil {
		t.Fatalf("CancelJob failed: %v", err)
	}

	inspectRes, err := cp.InspectJob(ctx, "cancellable-job")
	if err != nil {
		t.Fatalf("InspectJob failed: %v", err)
	}
	if inspectRes.Job.State != models.JobStateCancelled {
		t.Fatalf("expected CANCELLED state, got %s", inspectRes.Job.State)
	}

	// 4. Test Retry on Failed Job
	// Mark a job as FAILED
	dbJob, _ := store.Jobs().Get(ctx, runRes.JobID)
	dbJob.Status = string(models.JobStateFailed)
	jobRec, _ := models.JobFromModel(dbJob)
	jobRec.State = models.JobStateFailed
	specJSON, _ := jobRec.ToSpecJSON()
	dbJob.SpecJSON = specJSON
	_ = store.Jobs().Update(ctx, dbJob)

	retryRes, err := cp.RetryJob(ctx, "cancellable-job", dispatcher)
	if err != nil {
		t.Fatalf("RetryJob failed: %v", err)
	}

	if retryRes.State != models.JobStateAssigned {
		t.Fatalf("expected ASSIGNED state after retry, got %s", retryRes.State)
	}

	updatedJob, err := cp.InspectJob(ctx, "cancellable-job")
	if err != nil {
		t.Fatalf("failed to inspect retried job: %v", err)
	}
	if updatedJob.Job.RetryCount != 1 {
		t.Fatalf("expected retry count 1, got %d", updatedJob.Job.RetryCount)
	}
}

func TestJobSchedulerIntegration_RunJob(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	now := time.Now().UTC()

	// 1. Setup Worker in Store & Worker TaskManager
	workerID := id.NewWorkerID()
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "test-node",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	tm := worker.NewTaskManager(worker.TaskManagerOptions{
		WorkerID: workerID,
		Runtime:  run.NewNativeRuntime(),
		Logger:   logger,
	})
	defer tm.Close()

	// 2. Setup Dispatcher
	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return tm.AssignTask(ctx, worker.TaskAssignment{
			TaskID:      id.ID(req.Task.Id),
			JobID:       id.ID(req.Task.JobId),
			JobName:     "migration",
			Command:     req.Command,
			Args:        req.Args,
			Environment: req.Environment,
		})
	})

	cp, err := New(Options{
		Store:  store,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to initialize control plane: %v", err)
	}

	cmdName, cmdArgs := getEchoCmd("Running database migrations...")

	jobCfg := &spec.JobConfig{
		Name:    "migration",
		Command: cmdName,
		Args:    cmdArgs,
		Runtime: "native",
		Timeout: "1m",
		Resources: spec.ResourceConfig{
			CPU:    "500m",
			Memory: "256MiB",
		},
	}

	// 3. Execute RunJob
	res, err := cp.RunJob(ctx, jobCfg, dispatcher)
	if err != nil {
		t.Fatalf("RunJob failed: %v", err)
	}

	if res.JobID == "" || res.TaskID == "" {
		t.Fatalf("expected valid job ID and task ID, got: %+v", res)
	}
	if res.WorkerID != workerID {
		t.Fatalf("expected assigned worker %s, got %s", workerID, res.WorkerID)
	}

	// 4. Verify Job persistence in state store
	job, err := store.Jobs().Get(ctx, res.JobID)
	if err != nil {
		t.Fatalf("failed to get job from store: %v", err)
	}
	if job.Name != "migration" {
		t.Fatalf("expected job name 'migration', got '%s'", job.Name)
	}

	// 5. Verify Task was created and assigned in state store
	task, err := store.Tasks().Get(ctx, res.TaskID)
	if err != nil {
		t.Fatalf("failed to get task from store: %v", err)
	}
	if task.JobID != res.JobID {
		t.Fatalf("expected task JobID %s, got %s", res.JobID, task.JobID)
	}
	if task.WorkerID != workerID {
		t.Fatalf("expected task WorkerID %s, got %s", workerID, task.WorkerID)
	}

	// 6. Verify audit event was logged
	events, err := store.Events().ListByEntity(ctx, res.JobID)
	if err != nil || len(events) == 0 {
		t.Fatalf("expected audit events for job %s, got %d (err: %v)", res.JobID, len(events), err)
	}
}

func TestJobSchedulerIntegration_InspectAndLogs(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	now := time.Now().UTC()

	// Setup worker
	workerID := id.NewWorkerID()
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{ID: nodeID, Name: "node-1", Status: "READY", CreatedAt: now, UpdatedAt: now})
	_ = store.Workers().Create(ctx, &models.Worker{ID: workerID, NodeID: nodeID, Status: "READY", Heartbeat: now, CreatedAt: now, UpdatedAt: now})

	cp, err := New(Options{
		Store:  store,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("failed to initialize control plane: %v", err)
	}

	cmdName, cmdArgs := getEchoCmd("data-pipeline-done")
	jobCfg := &spec.JobConfig{
		Name:    "etl-batch",
		Command: cmdName,
		Args:    cmdArgs,
	}

	dispatcher := scheduler.NewInProcessDispatcher()
	dispatcher.RegisterWorkerHandler(workerID, func(ctx context.Context, req *v1.TaskAssignmentRequest) error {
		return nil
	})

	res, err := cp.RunJob(ctx, jobCfg, dispatcher)
	if err != nil {
		t.Fatalf("failed to run job: %v", err)
	}

	// Inspect Job by Name
	inspectRes, err := cp.InspectJob(ctx, "etl-batch")
	if err != nil {
		t.Fatalf("InspectJob failed: %v", err)
	}
	if inspectRes.Job.ID != res.JobID {
		t.Fatalf("expected inspect job ID %s, got %s", res.JobID, inspectRes.Job.ID)
	}
	if inspectRes.Job.AssignedTo != workerID {
		t.Fatalf("expected assigned worker %s, got %s", workerID, inspectRes.Job.AssignedTo)
	}
}
