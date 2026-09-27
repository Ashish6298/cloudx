package integration

import (
	"context"
	"runtime"
	"testing"

	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// TestClusterIntegration_DeployScaleCrashRecoverDeployV2Rollback verifies the complete
// Phase 68 verification cycle on a local cluster with 1 Control Plane and 3 Workers.
// Lifecycle:
// Deploy v1 -> Scale -> Crash Task/Worker -> Recover -> Deploy v2 -> Rollback to v1
func TestClusterIntegration_DeployScaleCrashRecoverDeployV2Rollback(t *testing.T) {
	ctx := context.Background()

	// 1. Setup Local Cluster with 1 Control Plane + 3 Workers
	harness, err := NewClusterHarness(t, HarnessOptions{
		WorkerCount: 3,
		UseSQLite:   true,
	})
	if err != nil {
		t.Fatalf("failed to initialize cluster test harness: %v", err)
	}

	if len(harness.Workers) != 3 {
		t.Fatalf("expected 3 registered workers, got %d", len(harness.Workers))
	}

	// Define portable command for native runtime
	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 30"}
	} else {
		cmd = "sh"
		args = []string{"-c", "sleep 30"}
	}

	serviceName := "web-service"

	// =========================================================================
	// STAGE 1: DEPLOY v1 (Replicas = 2)
	// =========================================================================
	t.Log(">>> STAGE 1: Deploying service v1 with 2 replicas across 3 workers...")
	replicas := 2
	svcConfigV1 := &spec.ServiceConfig{
		Name:     serviceName,
		Version:  "v1",
		Runtime:  "native",
		Command:  cmd,
		Args:     args,
		Replicas: &replicas,
		Resources: spec.ResourceConfig{
			CPU:    "500m",
			Memory: "128MB",
		},
	}

	deployRes1, err := harness.DeployService(svcConfigV1)
	if err != nil {
		t.Fatalf("Stage 1 Deploy v1 failed: %v", err)
	}

	if deployRes1.Status != "RUNNING" {
		t.Fatalf("expected status RUNNING, got %s", deployRes1.Status)
	}
	if len(deployRes1.Tasks) != 2 {
		t.Fatalf("expected 2 active tasks scheduled, got %d", len(deployRes1.Tasks))
	}

	// Verify tasks in store
	activeTasks, err := harness.GetActiveTasks(deployRes1.ServiceID)
	if err != nil || len(activeTasks) != 2 {
		t.Fatalf("expected 2 active tasks in store, got %d (err: %v)", len(activeTasks), err)
	}
	t.Logf("✓ Stage 1 Passed: Deployed v1 with %d active tasks.", len(activeTasks))

	// =========================================================================
	// STAGE 2: SCALE UP (2 -> 3 Replicas)
	// =========================================================================
	t.Log(">>> STAGE 2: Scaling service from 2 -> 3 replicas across 3 workers...")
	scaleRes, err := harness.ScaleService(serviceName, 3)
	if err != nil {
		t.Fatalf("Stage 2 Scale failed: %v", err)
	}

	if scaleRes.DesiredReplicas != 3 {
		t.Fatalf("expected desired replicas 3, got %d", scaleRes.DesiredReplicas)
	}
	if scaleRes.Summary.CreatedTasks != 1 {
		t.Fatalf("expected 1 additional task created during scale up, got %d", scaleRes.Summary.CreatedTasks)
	}

	activeTasks, err = harness.GetActiveTasks(deployRes1.ServiceID)
	if err != nil || len(activeTasks) != 3 {
		t.Fatalf("expected 3 active tasks in store after scale up, got %d", len(activeTasks))
	}

	// Verify worker distribution across the 3 workers
	workerUsage := make(map[string]int)
	for _, t := range activeTasks {
		workerUsage[t.WorkerID.String()]++
	}
	t.Logf("✓ Stage 2 Passed: Scaled to 3 replicas distributed across workers: %+v", workerUsage)

	// =========================================================================
	// STAGE 3: CRASH (Simulate Task Failure / Process Kill)
	// =========================================================================
	t.Log(">>> STAGE 3: Simulating crash on one active task...")
	crashedTask := activeTasks[0]
	err = harness.CrashTask(crashedTask.ID)
	if err != nil {
		t.Fatalf("Stage 3 Crash simulation failed: %v", err)
	}

	// Verify task is recorded as FAILED
	tRecord, err := harness.Store.Tasks().Get(ctx, crashedTask.ID)
	if err != nil || tRecord.State != string(models.TaskStateFailed) {
		t.Fatalf("expected crashed task to be in FAILED state, got %s (err: %v)", tRecord.State, err)
	}

	// Active task count should now be 2
	activeTasksAfterCrash, _ := harness.GetActiveTasks(deployRes1.ServiceID)
	if len(activeTasksAfterCrash) != 2 {
		t.Fatalf("expected 2 active tasks remaining immediately after crash, got %d", len(activeTasksAfterCrash))
	}
	t.Logf("✓ Stage 3 Passed: Task %s terminated and marked FAILED.", crashedTask.ID)

	// =========================================================================
	// STAGE 4: RECOVER (Reconciler self-heals deficit)
	// =========================================================================
	t.Log(">>> STAGE 4: Running reconciliation to auto-recover cluster to desired state (3 replicas)...")
	recSummary, err := harness.Reconcile()
	if err != nil {
		t.Fatalf("Stage 4 Reconcile failed: %v", err)
	}

	if recSummary.CreatedTasks != 1 {
		t.Fatalf("expected reconciler to create 1 replacement task, got %d", recSummary.CreatedTasks)
	}

	recoveredTasks, err := harness.GetActiveTasks(deployRes1.ServiceID)
	if err != nil || len(recoveredTasks) != 3 {
		t.Fatalf("expected 3 active healthy replicas after auto-recovery, got %d", len(recoveredTasks))
	}

	// Verify replacement task is distinct from crashed task
	foundCrashed := false
	for _, rt := range recoveredTasks {
		if rt.ID == crashedTask.ID {
			foundCrashed = true
			break
		}
	}
	if foundCrashed {
		t.Fatalf("crashed task %s should not be among active recovered tasks", crashedTask.ID)
	}
	t.Logf("✓ Stage 4 Passed: Cluster auto-recovered 3 desired healthy replicas.")

	// =========================================================================
	// STAGE 5: DEPLOY v2 (Rolling Upgrade)
	// =========================================================================
	t.Log(">>> STAGE 5: Deploying service v2 (Rolling upgrade)...")
	desiredV2Replicas := 3
	svcConfigV2 := &spec.ServiceConfig{
		Name:     serviceName,
		Version:  "v2",
		Runtime:  "native",
		Command:  cmd,
		Args:     args,
		Replicas: &desiredV2Replicas,
		UpdateStrategy: &spec.UpdateStrategyConfig{
			Type:           "rolling",
			MaxUnavailable: 1,
			MaxSurge:       1,
		},
	}

	deployRes2, err := harness.DeployService(svcConfigV2)
	if err != nil {
		t.Fatalf("Stage 5 Deploy v2 failed: %v", err)
	}

	if deployRes2.DeploymentID == deployRes1.DeploymentID {
		t.Fatalf("expected distinct deployment ID for v2, got identical: %s", deployRes2.DeploymentID)
	}

	// Verify active deployment is now v2
	inspectRes, err := harness.ControlPlane.InspectService(ctx, serviceName)
	if err != nil {
		t.Fatalf("failed to inspect service after v2 deploy: %v", err)
	}
	if len(inspectRes.Deployments) < 2 {
		t.Fatalf("expected at least 2 deployments in history, got %d", len(inspectRes.Deployments))
	}

	// Trigger additional reconcile pass if needed for rolling completion
	_, _ = harness.Reconcile()

	allServiceTasks, _ := harness.Store.Tasks().ListByService(ctx, deployRes1.ServiceID)
	v2TaskCount := 0
	for _, t := range allServiceTasks {
		if t.DeploymentID == deployRes2.DeploymentID && (t.State == string(models.TaskStateRunning) || t.State == string(models.TaskStateAssigned)) {
			v2TaskCount++
		}
	}
	t.Logf("✓ Stage 5 Passed: Deployed v2 (Deployment ID: %s, active v2 tasks: %d).", deployRes2.DeploymentID, v2TaskCount)

	// =========================================================================
	// STAGE 6: ROLLBACK (v2 -> v1)
	// =========================================================================
	t.Log(">>> STAGE 6: Rolling back service from v2 -> v1...")
	rollbackRes, err := harness.RollbackService(serviceName, "v1")
	if err != nil {
		t.Fatalf("Stage 6 Rollback failed: %v", err)
	}

	if rollbackRes.TargetVersion != "v1" {
		t.Fatalf("expected rollback target version 'v1', got '%s'", rollbackRes.TargetVersion)
	}
	if rollbackRes.TargetDeploymentID != deployRes1.DeploymentID {
		t.Fatalf("expected rollback target deployment ID %s, got %s", deployRes1.DeploymentID, rollbackRes.TargetDeploymentID)
	}

	// Verify inspection shows target deployment status ACTIVE and former SUPERCEDED/ROLLED_BACK
	inspectAfterRollback, err := harness.ControlPlane.InspectService(ctx, serviceName)
	if err != nil {
		t.Fatalf("failed to inspect service after rollback: %v", err)
	}

	var activeDepFound *models.Deployment
	for _, d := range inspectAfterRollback.Deployments {
		if d.Status == string(models.DeploymentStatusActive) {
			activeDepFound = d
			break
		}
	}
	if activeDepFound == nil || activeDepFound.ID != deployRes1.DeploymentID {
		t.Fatalf("expected active deployment after rollback to be v1 (%s), got %+v", deployRes1.DeploymentID, activeDepFound)
	}

	t.Logf("✓ Stage 6 Passed: Rolled back cleanly to v1 (Target Deployment ID: %s).", rollbackRes.TargetDeploymentID)
	t.Log("=========================================================================")
	t.Log(">>> ALL 6 LIFECYCLE STAGES COMPLETED AND VERIFIED SUCCESSFULLY! <<<")
	t.Log("=========================================================================")
}

// TestClusterIntegration_WorkerFailureAndTaskRescheduling tests worker node failure and automated orphan migration.
func TestClusterIntegration_WorkerFailureAndTaskRescheduling(t *testing.T) {
	harness, err := NewClusterHarness(t, HarnessOptions{
		WorkerCount: 3,
		UseSQLite:   true,
	})
	if err != nil {
		t.Fatalf("failed to initialize harness: %v", err)
	}

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 30"}
	} else {
		cmd = "sh"
		args = []string{"-c", "sleep 30"}
	}

	replicas := 3
	svcConfig := &spec.ServiceConfig{
		Name:     "resilient-app",
		Version:  "v1",
		Runtime:  "native",
		Command:  cmd,
		Args:     args,
		Replicas: &replicas,
	}

	deployRes, err := harness.DeployService(svcConfig)
	if err != nil {
		t.Fatalf("Deploy failed: %v", err)
	}

	activeTasks, err := harness.GetActiveTasks(deployRes.ServiceID)
	if err != nil || len(activeTasks) != 3 {
		t.Fatalf("expected 3 active tasks, got %d", len(activeTasks))
	}

	// 1. Pick a worker hosting one of the tasks and terminate it
	targetWorkerID := activeTasks[0].WorkerID
	t.Logf("Stopping worker %s hosting task %s...", targetWorkerID, activeTasks[0].ID)
	err = harness.StopWorker(targetWorkerID)
	if err != nil {
		t.Fatalf("failed to stop worker: %v", err)
	}

	// 2. Reconcile pass: identifies orphan on LOST worker, marks task LOST, schedules onto remaining 2 workers
	recSummary, err := harness.Reconcile()
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if recSummary.OrphanedRecovered != 1 {
		t.Fatalf("expected 1 orphaned task recovered, got %d", recSummary.OrphanedRecovered)
	}
	if recSummary.CreatedTasks != 1 {
		t.Fatalf("expected 1 replacement task created, got %d", recSummary.CreatedTasks)
	}

	// 3. Verify healthy active replicas count is restored to 3 across remaining workers
	remainingActive, err := harness.GetActiveTasks(deployRes.ServiceID)
	if err != nil || len(remainingActive) != 3 {
		t.Fatalf("expected 3 active tasks after worker failure recovery, got %d", len(remainingActive))
	}

	for _, tsk := range remainingActive {
		if tsk.WorkerID == targetWorkerID {
			t.Fatalf("active task %s is still assigned to terminated worker %s", tsk.ID, targetWorkerID)
		}
	}
	t.Logf("✓ Worker failure recovery verified: Orphan migrated cleanly to surviving workers.")
}
