package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/api"
	"github.com/cloudx-org/cloudx/internal/auth"
	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/cloudx-org/cloudx/internal/health"
	"github.com/cloudx-org/cloudx/internal/simulation"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Helper for native OS sleep commands
func testSleepCommand(durationSec int) (string, []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-Command", fmt.Sprintf("Start-Sleep -Seconds %d", durationSec)}
	}
	return "sh", []string{"-c", fmt.Sprintf("sleep %d", durationSec)}
}

// ============================================================================
// 1. Worker Crash Scenario
// ============================================================================
func TestFailure_WorkerCrash(t *testing.T) {
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
		Name:     "worker-crash-app",
		Version:  "v1",
		Runtime:  "native",
		Command:  cmd,
		Args:     args,
		Replicas: &replicas,
	}

	deployRes, err := harness.DeployService(svcConfig)
	if err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	activeTasks, err := harness.GetActiveTasks(deployRes.ServiceID)
	if err != nil || len(activeTasks) != 3 {
		t.Fatalf("expected 3 active tasks, got %d", len(activeTasks))
	}

	// Abruptly terminate worker 1
	crashedWorkerID := activeTasks[0].WorkerID
	t.Logf("Simulating crash of worker node: %s", crashedWorkerID)
	err = harness.StopWorker(crashedWorkerID)
	if err != nil {
		t.Fatalf("failed to stop worker: %v", err)
	}

	// Reconcile pass: recognizes orphan on crashed worker, migrates to remaining 2 workers
	summary, err := harness.Reconcile()
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if summary.OrphanedRecovered != 1 {
		t.Fatalf("expected 1 orphaned task recovered, got %d", summary.OrphanedRecovered)
	}

	// Verify all 3 desired replicas are active and none on crashed worker
	survivingTasks, err := harness.GetActiveTasks(deployRes.ServiceID)
	if err != nil || len(survivingTasks) != 3 {
		t.Fatalf("expected 3 active tasks after worker crash recovery, got %d", len(survivingTasks))
	}

	for _, task := range survivingTasks {
		if task.WorkerID == crashedWorkerID {
			t.Fatalf("task %s is still assigned to crashed worker %s", task.ID, crashedWorkerID)
		}
	}
	t.Log("✓ Worker crash recovery verified successfully.")
}

// ============================================================================
// 2. Process Crash Scenario
// ============================================================================
func TestFailure_ProcessCrash(t *testing.T) {
	harness, err := NewClusterHarness(t, HarnessOptions{
		WorkerCount: 2,
		UseSQLite:   true,
	})
	if err != nil {
		t.Fatalf("failed to create harness: %v", err)
	}

	cmd, args := testSleepCommand(20)
	replicas := 2
	svcConfig := &spec.ServiceConfig{
		Name:     "proc-crash-app",
		Version:  "v1",
		Runtime:  "native",
		Command:  cmd,
		Args:     args,
		Replicas: &replicas,
	}

	deployRes, err := harness.DeployService(svcConfig)
	if err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	activeTasks, err := harness.GetActiveTasks(deployRes.ServiceID)
	if err != nil || len(activeTasks) != 2 {
		t.Fatalf("expected 2 active tasks, got %d", len(activeTasks))
	}

	// Terminate process abruptly (SIGKILL)
	crashedTaskID := activeTasks[0].ID
	t.Logf("Simulating SIGKILL process crash on task: %s", crashedTaskID)
	err = harness.CrashTask(crashedTaskID)
	if err != nil {
		t.Fatalf("failed to crash task: %v", err)
	}

	// Reconcile pass: self-heals by replacing failed process
	summary, err := harness.Reconcile()
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if summary.CreatedTasks != 1 {
		t.Fatalf("expected 1 replacement task created for crashed process, got %d", summary.CreatedTasks)
	}

	recoveredTasks, err := harness.GetActiveTasks(deployRes.ServiceID)
	if err != nil || len(recoveredTasks) != 2 {
		t.Fatalf("expected 2 active healthy replicas after process crash, got %d", len(recoveredTasks))
	}
	t.Log("✓ Process crash and auto-healing verified successfully.")
}

// ============================================================================
// 3. Control-Plane Restart Scenario
// ============================================================================
func TestFailure_ControlPlaneRestart(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "state.db")
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)
	logger := logging.NewDefaultLogger()

	// 1. First Control Plane Instance
	store1, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}

	cpConfig := config.NewDefaultConfig()
	cpConfig.Storage.Path = filepath.Join(tempDir, "cp1")
	cp1, err := controlplane.New(controlplane.Options{Config: cpConfig, Store: store1, Logger: logger})
	if err != nil {
		t.Fatalf("failed to create cp1: %v", err)
	}
	_ = cp1.Start(ctx)

	// Deploy a service in cp1
	cmd, args := testSleepCommand(30)
	replicas := 2
	svcConfig := &spec.ServiceConfig{
		Name:     "persistent-service",
		Version:  "v1",
		Runtime:  "native",
		Command:  cmd,
		Args:     args,
		Replicas: &replicas,
	}

	deployRes1, err := cp1.DeployService(ctx, svcConfig, nil)
	if err != nil {
		t.Fatalf("cp1 deploy failed: %v", err)
	}

	// 2. Abruptly Stop CP1
	t.Log("Simulating Control Plane abrupt shutdown...")
	_ = cp1.Stop(ctx)
	_ = store1.Close()

	// 3. Start New Control Plane Instance against the SAME database
	t.Log("Restarting Control Plane against persisted state store...")
	store2, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store on restart: %v", err)
	}
	defer store2.Close()

	cp2, err := controlplane.New(controlplane.Options{Config: cpConfig, Store: store2, Logger: logger})
	if err != nil {
		t.Fatalf("failed to create cp2: %v", err)
	}
	_ = cp2.Start(ctx)
	defer func() { _ = cp2.Stop(ctx) }()

	// Verify service and deployment survived restart in database
	inspectRes, err := cp2.InspectService(ctx, "persistent-service")
	if err != nil {
		t.Fatalf("failed to inspect service after restart: %v", err)
	}

	if inspectRes.Service.ID != deployRes1.ServiceID {
		t.Fatalf("service ID mismatch across restart: expected %s, got %s", deployRes1.ServiceID, inspectRes.Service.ID)
	}
	if len(inspectRes.Deployments) == 0 {
		t.Fatalf("expected persisted deployment records after restart, got 0")
	}
	t.Log("✓ Control-plane restart and state persistence verified successfully.")
}

// ============================================================================
// 4. SQLite Interruption / Transient Lock Scenario
// ============================================================================
func TestFailure_SQLiteInterruption(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "locked_state.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	// 1. Verify Transaction rollback on failure
	err = store.Transaction(ctx, func(txStore state.Store) error {
		now := time.Now().UTC()
		_ = txStore.Services().Create(ctx, &models.Service{
			ID:        id.NewServiceID(),
			Name:      "transient-service",
			Replicas:  1,
			Command:   "echo",
			CreatedAt: now,
			UpdatedAt: now,
		})
		// Force synthetic abort / interruption
		return fmt.Errorf("synthetic database error / transaction aborted")
	})

	if err == nil {
		t.Fatalf("expected error from aborted transaction, got nil")
	}

	// Verify uncommitted record was completely rolled back
	services, err := store.Services().List(ctx)
	if err != nil {
		t.Fatalf("failed to list services: %v", err)
	}
	for _, s := range services {
		if s.Name == "transient-service" {
			t.Fatalf("uncommitted service record should not exist in database after rollback")
		}
	}
	t.Log("✓ SQLite transaction interruption and rollback verified successfully.")
}

// ============================================================================
// 5. RPC Timeout Scenario
// ============================================================================
func TestFailure_RPCTimeout(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)
	memStore, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory store: %v", err)
	}
	defer memStore.Close()

	srv, err := api.NewServer(api.ServerOptions{
		Address: "127.0.0.1:0",
		Store:   memStore,
		Logger:  logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create api server: %v", err)
	}
	_ = srv.Start()
	defer srv.Stop()

	// Connect gRPC client
	conn, err := grpc.Dial(srv.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial server: %v", err)
	}
	defer conn.Close()

	client := v1.NewControlPlaneServiceClient(conn)

	// Invoke RPC with an already-expired / ultra-short deadline
	timeoutCtx, cancel := context.WithTimeout(ctx, 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond) // Ensure deadline is exceeded

	_, err = client.Heartbeat(timeoutCtx, &v1.HeartbeatRequest{
		WorkerId: id.NewWorkerID().String(),
	})

	if err == nil {
		t.Fatalf("expected deadline exceeded error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || (st.Code() != codes.DeadlineExceeded && st.Code() != codes.Canceled) {
		t.Fatalf("expected DeadlineExceeded or Canceled gRPC code, got %v (%v)", st.Code(), err)
	}
	t.Logf("✓ RPC Timeout handled deterministically: %v", err)
}

// ============================================================================
// 6. Duplicate Messages & Idempotency Scenario
// ============================================================================
func TestFailure_DuplicateMessages(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)
	memStore, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer memStore.Close()

	srv, err := api.NewServer(api.ServerOptions{
		Address: "127.0.0.1:0",
		Store:   memStore,
		Logger:  logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create api server: %v", err)
	}
	_ = srv.Start()
	defer srv.Stop()

	conn, _ := grpc.Dial(srv.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	defer conn.Close()
	client := v1.NewControlPlaneServiceClient(conn)

	workerID := id.NewWorkerID().String()
	nodeID := id.NewNodeID().String()

	// 1. Initial Worker Registration
	regReq := &v1.RegisterWorkerRequest{
		WorkerId: workerID,
		NodeId:   nodeID,
		Address:  "127.0.0.1:9090",
		Hostname: "node-1",
	}

	resp1, err := client.RegisterWorker(ctx, regReq)
	if err != nil || !resp1.Accepted {
		t.Fatalf("initial registration failed: %v", err)
	}

	// 2. Duplicate Registration with identical parameters -> must be idempotent and accepted
	resp2, err := client.RegisterWorker(ctx, regReq)
	if err != nil || !resp2.Accepted {
		t.Fatalf("duplicate registration should be accepted idempotently, got err: %v, resp: %+v", err, resp2)
	}

	// 3. Duplicate Task Assignment Request
	taskID := id.NewTaskID().String()
	assignReq := &v1.TaskAssignmentRequest{
		WorkerId: workerID,
		Task: &v1.Task{
			Id:       taskID,
			WorkerId: workerID,
		},
		Command: "echo hello",
	}

	assignResp1, err := client.AssignTask(ctx, assignReq)
	if err != nil || !assignResp1.Accepted {
		t.Fatalf("initial task assignment failed: %v", err)
	}

	// Duplicate Task Assignment -> should acknowledge idempotently
	assignResp2, err := client.AssignTask(ctx, assignReq)
	if err != nil || !assignResp2.Accepted {
		t.Fatalf("duplicate task assignment failed: %v", err)
	}

	t.Log("✓ Duplicate messages and idempotent retry behavior verified successfully.")
}

// ============================================================================
// 7. Delayed Messages & Failure Detector Status Progression
// ============================================================================
func TestFailure_DelayedMessages(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	workerID := id.NewWorkerID()
	nodeID := id.NewNodeID()

	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "delayed-node",
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

	detectorCfg := health.FailureDetectorConfig{
		CheckInterval:    50 * time.Millisecond,
		SuspectedTimeout: 200 * time.Millisecond,
		UnhealthyTimeout: 400 * time.Millisecond,
		LostTimeout:      600 * time.Millisecond,
	}
	fd := health.NewFailureDetector(detectorCfg, store, logging.NewDefaultLogger())

	// Advance time to 250ms past last heartbeat -> SUSPECTED
	t.Log("Testing delayed heartbeat -> SUSPECTED transition...")
	fd.EvaluateWorkers(ctx, now.Add(250*time.Millisecond))
	w, _ := store.Workers().Get(ctx, workerID)
	if w.Status != string(health.StatusSuspected) {
		t.Fatalf("expected SUSPECTED after 250ms delay, got %s", w.Status)
	}

	// Advance time to 450ms past last heartbeat -> UNHEALTHY
	t.Log("Testing delayed heartbeat -> UNHEALTHY transition...")
	fd.EvaluateWorkers(ctx, now.Add(450*time.Millisecond))
	w, _ = store.Workers().Get(ctx, workerID)
	if w.Status != string(health.StatusUnhealthy) {
		t.Fatalf("expected UNHEALTHY after 450ms delay, got %s", w.Status)
	}

	// Advance time to 700ms past last heartbeat -> LOST
	t.Log("Testing delayed heartbeat -> LOST transition...")
	fd.EvaluateWorkers(ctx, now.Add(700*time.Millisecond))
	w, _ = store.Workers().Get(ctx, workerID)
	if w.Status != string(health.StatusLost) {
		t.Fatalf("expected LOST after 700ms delay, got %s", w.Status)
	}

	t.Log("✓ Delayed message handling and health degradation verified successfully.")
}

// ============================================================================
// 8. Health Failure & Probe Interception Scenario
// ============================================================================
func TestFailure_HealthFailure(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)
	store, _ := sqlite.Open(ctx, ":memory:")
	defer store.Close()

	logger := logging.NewDefaultLogger()
	prober := simulation.NewSimulatedProber(health.NewDefaultProber())
	sim := simulation.NewSimulator(store, nil, prober, logger)

	taskID := id.NewTaskID()
	cfg := health.ProbeConfig{
		Type: health.CheckTypeProcess,
		PID:  1234,
	}

	// 1. Break health endpoint artificially
	t.Logf("Injecting probe failure into task %s...", taskID)
	res, err := sim.BreakHealthEndpoint(ctx, taskID, "HTTP 503 Service Unavailable / simulated outage")
	if err != nil || !res.Success {
		t.Fatalf("failed to break health endpoint: %v", err)
	}

	// 2. Health probe should return failure
	probeRes := prober.Check(ctx, cfg)
	if probeRes.Healthy {
		t.Fatalf("expected probe to fail under broken health simulation")
	}
	if !strings.Contains(probeRes.Error, "simulated outage") {
		t.Fatalf("unexpected probe error message: %s", probeRes.Error)
	}

	// 3. Restore health endpoint
	t.Logf("Restoring normal health probing for task %s...", taskID)
	resRestore, err := sim.RestoreHealthEndpoint(ctx, taskID)
	if err != nil || !resRestore.Success {
		t.Fatalf("failed to restore health endpoint: %v", err)
	}

	t.Log("✓ Health check failure injection and recovery verified successfully.")
}

// ============================================================================
// 9. Resource Exhaustion Scenario
// ============================================================================
func TestFailure_ResourceExhaustion(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)
	store, _ := sqlite.Open(ctx, ":memory:")
	defer store.Close()

	logger := logging.NewDefaultLogger()
	sim := simulation.NewSimulator(store, nil, nil, logger)

	taskID := id.NewTaskID()
	t.Logf("Simulating safe resource exhaustion / memory pressure on task %s...", taskID)
	res, err := sim.ExhaustResources(ctx, taskID, 64)
	if err != nil {
		t.Fatalf("failed to execute resource exhaustion simulation: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected successful resource exhaustion simulation")
	}

	// Verify simulation event logged in store
	events, err := store.Events().List(ctx, 10)
	if err != nil || len(events) == 0 {
		t.Fatalf("expected simulation event logged, got %d", len(events))
	}
	if events[0].Type != "SIMULATION_EXHAUST_RESOURCES" {
		t.Fatalf("expected event SIMULATION_EXHAUST_RESOURCES, got %s", events[0].Type)
	}
	t.Log("✓ Resource exhaustion simulation verified successfully.")
}

// ============================================================================
// 10. Worker Reconnection & Re-Registration Scenario
// ============================================================================
func TestFailure_WorkerReconnection(t *testing.T) {
	ctx := auth.WithPermissionScope(context.Background(), auth.ScopeControlPlane)
	memStore, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer memStore.Close()

	srv, err := api.NewServer(api.ServerOptions{
		Address: "127.0.0.1:0",
		Store:   memStore,
		Logger:  logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create api server: %v", err)
	}
	_ = srv.Start()
	defer srv.Stop()

	tempDir := t.TempDir()
	cfg := config.NewDefaultConfig()
	cfg.Storage.Path = tempDir
	cfg.ControlPlane.Address = srv.Address()
	cfg.Health.HeartbeatInterval = 100 * time.Millisecond

	// 1. Initial Worker Daemon boot and connection
	daemon1, err := worker.NewDaemon(worker.Options{
		Config: cfg,
		Logger: logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create daemon: %v", err)
	}

	err = daemon1.Start(ctx)
	if err != nil || daemon1.Status() != worker.StatusReady {
		t.Fatalf("initial daemon startup failed: %v", err)
	}

	stableWorkerID := daemon1.ID()

	// 2. Abruptly stop daemon (network partition / worker crash)
	t.Logf("Simulating sudden worker disconnect for ID: %s", stableWorkerID)
	_ = daemon1.Stop(ctx)

	// 3. Reconnect / Reboot Worker Daemon with same identity storage
	t.Log("Simulating worker reconnect and identity restoration...")
	daemon2, err := worker.NewDaemon(worker.Options{
		Config: cfg,
		Logger: logging.NewDefaultLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create second daemon: %v", err)
	}

	if daemon2.ID() != stableWorkerID {
		t.Fatalf("worker identity failed to persist across reconnect: expected %s, got %s", stableWorkerID, daemon2.ID())
	}

	err = daemon2.Start(ctx)
	if err != nil || daemon2.Status() != worker.StatusReady {
		t.Fatalf("worker reconnection failed: %v", err)
	}
	defer func() { _ = daemon2.Stop(ctx) }()

	// Verify worker status in control plane state store is restored to READY
	wRecord, err := memStore.Workers().Get(ctx, stableWorkerID)
	if err != nil || wRecord.Status != "READY" {
		t.Fatalf("expected worker state in store to be READY, got %s (err: %v)", wRecord.Status, err)
	}

	t.Log("✓ Worker reconnection and state restoration verified successfully.")
}
