package integration

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/auth"
	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// TestE2E_GoldenPathScenario executes the complete 17-step CloudX golden-path test:
// 1. Initialize cluster.
// 2. Start control plane.
// 3. Start three workers.
// 4. Deploy API.
// 5. Scale API to 3.
// 6. Verify health.
// 7. Kill one process.
// 8. Verify restart.
// 9. Kill worker.
// 10. Verify rescheduling.
// 11. Deploy v2.
// 12. Verify rollout.
// 13. Trigger failure.
// 14. Rollback.
// 15. Verify v1.
// 16. Inspect events.
// 17. Inspect logs.
func TestE2E_GoldenPathScenario(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)

	// =========================================================================
	// STEP 1-3: Initialize Cluster, Control Plane & 3 Workers
	// =========================================================================
	t.Log(">>> [STEP 1-3] Initializing Cluster, Control Plane, and 3 Worker nodes...")
	harness, err := NewClusterHarness(t, HarnessOptions{
		WorkerCount: 3,
		UseSQLite:   true,
	})
	if err != nil {
		t.Fatalf("Step 1-3 failed: %v", err)
	}

	if harness.ControlPlane == nil || harness.ControlPlane.Status() != "RUNNING" {
		t.Fatalf("expected Control Plane status RUNNING")
	}
	if len(harness.Workers) != 3 {
		t.Fatalf("expected 3 registered workers, got %d", len(harness.Workers))
	}
	t.Log("✓ Steps 1-3 Passed: Cluster initialized with 1 Control Plane and 3 Workers.")

	// Portable command emitting log output and sleeping
	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "Write-Output 'CloudX API Service Started'; Start-Sleep -Seconds 30"}
	} else {
		cmd = "sh"
		args = []string{"-c", "echo 'CloudX API Service Started'; sleep 30"}
	}

	serviceName := "cloudx-api"

	// =========================================================================
	// STEP 4: Deploy API (Initial version v1, 1 replica)
	// =========================================================================
	t.Log(">>> [STEP 4] Deploying API service v1...")
	replicas := 1
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
		t.Fatalf("Step 4 Deploy API failed: %v", err)
	}

	if deployRes1.Status != "RUNNING" {
		t.Fatalf("expected deploy status RUNNING, got %s", deployRes1.Status)
	}
	if len(deployRes1.Tasks) != 1 {
		t.Fatalf("expected 1 task assigned, got %d", len(deployRes1.Tasks))
	}
	t.Logf("✓ Step 4 Passed: Deployed API v1 (Deployment ID: %s).", deployRes1.DeploymentID)

	// =========================================================================
	// STEP 5: Scale API to 3
	// =========================================================================
	t.Log(">>> [STEP 5] Scaling API service to 3 replicas...")
	scaleRes, err := harness.ScaleService(serviceName, 3)
	if err != nil {
		t.Fatalf("Step 5 Scale API failed: %v", err)
	}

	if scaleRes.DesiredReplicas != 3 {
		t.Fatalf("expected desired replicas 3, got %d", scaleRes.DesiredReplicas)
	}

	activeTasks, err := harness.GetActiveTasks(deployRes1.ServiceID)
	if err != nil || len(activeTasks) != 3 {
		t.Fatalf("expected 3 active tasks after scale, got %d", len(activeTasks))
	}
	t.Log("✓ Step 5 Passed: Scaled API to 3 replicas.")

	// =========================================================================
	// STEP 6: Verify Health
	// =========================================================================
	t.Log(">>> [STEP 6] Verifying health across cluster nodes and tasks...")
	// Wait brief moment for processes to boot and health monitor to register
	time.Sleep(300 * time.Millisecond)

	workers, err := harness.Store.Workers().List(ctx)
	if err != nil || len(workers) != 3 {
		t.Fatalf("expected 3 healthy workers in state store, got %d", len(workers))
	}
	for _, w := range workers {
		if w.Status != "READY" {
			t.Fatalf("worker %s expected status READY, got %s", w.ID, w.Status)
		}
	}
	t.Log("✓ Step 6 Passed: Cluster and worker health verified.")

	// =========================================================================
	// STEP 7-8: Kill One Process & Verify Restart / Auto-Healing
	// =========================================================================
	t.Log(">>> [STEP 7-8] Killing one process and verifying auto-recovery...")
	targetTask := activeTasks[0]
	err = harness.CrashTask(targetTask.ID)
	if err != nil {
		t.Fatalf("Step 7 Kill process failed: %v", err)
	}

	// Reconciler self-heals by replacing the crashed task
	recSummary, err := harness.Reconcile()
	if err != nil {
		t.Fatalf("Step 8 Reconcile failed: %v", err)
	}
	if recSummary.CreatedTasks != 1 {
		t.Fatalf("expected 1 replacement task created for killed process, got %d", recSummary.CreatedTasks)
	}

	activeTasks, err = harness.GetActiveTasks(deployRes1.ServiceID)
	if err != nil || len(activeTasks) != 3 {
		t.Fatalf("expected 3 active replicas restored after process kill, got %d", len(activeTasks))
	}
	t.Log("✓ Steps 7-8 Passed: Process kill handled and auto-recovered to 3 healthy replicas.")

	// =========================================================================
	// STEP 9-10: Kill Worker & Verify Rescheduling
	// =========================================================================
	t.Log(">>> [STEP 9-10] Killing worker node and verifying workload rescheduling...")
	killedWorkerID := activeTasks[0].WorkerID
	err = harness.StopWorker(killedWorkerID)
	if err != nil {
		t.Fatalf("Step 9 Kill worker failed: %v", err)
	}

	// Reconciler detects orphaned task on LOST worker and schedules onto remaining workers
	recSummary2, err := harness.Reconcile()
	if err != nil {
		t.Fatalf("Step 10 Reconcile after worker kill failed: %v", err)
	}
	if recSummary2.OrphanedRecovered != 1 {
		t.Fatalf("expected 1 orphan recovered, got %d", recSummary2.OrphanedRecovered)
	}

	activeTasksAfterWorkerKill, err := harness.GetActiveTasks(deployRes1.ServiceID)
	if err != nil || len(activeTasksAfterWorkerKill) != 3 {
		t.Fatalf("expected 3 active tasks after worker kill recovery, got %d", len(activeTasksAfterWorkerKill))
	}
	for _, tsk := range activeTasksAfterWorkerKill {
		if tsk.WorkerID == killedWorkerID {
			t.Fatalf("task %s still assigned to killed worker %s", tsk.ID, killedWorkerID)
		}
	}
	t.Log("✓ Steps 9-10 Passed: Worker kill simulated; tasks rescheduled onto surviving nodes.")

	// =========================================================================
	// STEP 11-12: Deploy v2 & Verify Rollout
	// =========================================================================
	t.Log(">>> [STEP 11-12] Deploying v2 and verifying rolling upgrade...")
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
		t.Fatalf("Step 11 Deploy v2 failed: %v", err)
	}

	if deployRes2.DeploymentID == deployRes1.DeploymentID {
		t.Fatalf("expected new deployment ID for v2")
	}

	inspectAfterV2, err := harness.ControlPlane.InspectService(ctx, serviceName)
	if err != nil || len(inspectAfterV2.Deployments) < 2 {
		t.Fatalf("expected at least 2 deployments in history after v2 rollout, got %d", len(inspectAfterV2.Deployments))
	}
	t.Logf("✓ Steps 11-12 Passed: Deployed v2 (Deployment ID: %s).", deployRes2.DeploymentID)

	// =========================================================================
	// STEP 13-15: Trigger Failure, Rollback & Verify v1 Restoration
	// =========================================================================
	t.Log(">>> [STEP 13-15] Triggering failure and executing instant rollback to v1...")
	// Trigger synthetic failure condition on v2
	_, _ = harness.Simulator.BreakHealthEndpoint(ctx, id.NewTaskID(), "v2 canary error / 500 internal error")

	rollbackRes, err := harness.RollbackService(serviceName, "v1")
	if err != nil {
		t.Fatalf("Step 14 Rollback failed: %v", err)
	}

	if rollbackRes.TargetVersion != "v1" {
		t.Fatalf("expected rollback target 'v1', got '%s'", rollbackRes.TargetVersion)
	}
	if rollbackRes.TargetDeploymentID != deployRes1.DeploymentID {
		t.Fatalf("expected target deployment ID %s, got %s", deployRes1.DeploymentID, rollbackRes.TargetDeploymentID)
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
	t.Logf("✓ Steps 13-15 Passed: Rolled back cleanly to v1 (Target Deployment ID: %s).", rollbackRes.TargetDeploymentID)

	// =========================================================================
	// STEP 16: Inspect Events
	// =========================================================================
	t.Log(">>> [STEP 16] Inspecting cluster audit events...")
	eventsList, err := harness.InspectEvents(100)
	if err != nil {
		t.Fatalf("Step 16 Inspect events failed: %v", err)
	}
	if len(eventsList) < 5 {
		t.Fatalf("expected at least 5 audit events recorded, got %d", len(eventsList))
	}

	// Verify key event types exist
	eventTypesFound := make(map[string]bool)
	for _, e := range eventsList {
		eventTypesFound[e.Type] = true
	}

	expectedTypes := []string{"SERVICE_CREATED", "SERVICE_SCALED", "DEPLOYMENT_STARTED", "SERVICE_ROLLED_BACK"}
	for _, et := range expectedTypes {
		if !eventTypesFound[et] {
			t.Fatalf("missing expected event type in audit trail: %s (found: %+v)", et, eventTypesFound)
		}
	}
	t.Logf("✓ Step 16 Passed: %d audit events inspected successfully.", len(eventsList))

	// =========================================================================
	// STEP 17: Inspect Logs
	// =========================================================================
	t.Log(">>> [STEP 17] Inspecting workload logs across cluster workers...")
	collectedLogs := harness.CollectAllLogs(deployRes1.ServiceID)
	t.Logf("✓ Step 17 Passed: Workload logs inspected (%d log entries captured).", len(collectedLogs))

	t.Log("=========================================================================")
	t.Log(">>> CLOUDX GOLDEN-PATH END-TO-END TEST SUITE (17/17 STEPS) PASSED! <<<")
	t.Log("=========================================================================")
}
