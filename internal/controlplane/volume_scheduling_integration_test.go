package controlplane_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

type integTestEnv struct {
	cp      *controlplane.ControlPlane
	store   *sqlite.Store
	baseDir string
	cleanup func()
	// Pre-registered workers
	worker1ID id.ID
	worker2ID id.ID
}

func newIntegTestEnv(t *testing.T) *integTestEnv {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cloudx_test.db")
	ctx := context.Background()

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	cfg := config.NewDefaultConfig()
	cfg.Storage.Path = dir

	cp, err := controlplane.New(controlplane.Options{
		Config: cfg,
		Store:  store,
		Logger: logging.NewDefaultLogger(),
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("controlplane.New: %v", err)
	}

	// Register two workers: worker-1 and worker-2
	now := time.Now().UTC()
	w1ID := id.NewWorkerID()
	w2ID := id.NewWorkerID()
	nodeID := id.NewNodeID()

	// Nodes are required by FK constraint on workers table
	if err := store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "test-node",
		Address:   "127.0.0.1",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create test node: %v", err)
	}

	if err := store.Workers().Create(ctx, &models.Worker{
		ID:        w1ID,
		NodeID:    nodeID,
		Address:   "worker-1:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create worker-1: %v", err)
	}
	if err := store.Workers().Create(ctx, &models.Worker{
		ID:        w2ID,
		NodeID:    nodeID,
		Address:   "worker-2:7002",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create worker-2: %v", err)
	}

	return &integTestEnv{
		cp:        cp,
		store:     store,
		baseDir:   dir,
		worker1ID: w1ID,
		worker2ID: w2ID,
		cleanup:   func() { _ = store.Close() },
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Integration tests via AssignmentCoordinator (end-to-end volume affinity)
// ─────────────────────────────────────────────────────────────────────────────

// TestVolumeSchedulingIntegration_PinsToVolumeOwner verifies that the
// AssignmentCoordinator correctly routes a volume-dependent task to the
// worker that owns the volume, even when another worker is less loaded.
func TestVolumeSchedulingIntegration_PinsToVolumeOwner(t *testing.T) {
	env := newIntegTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()

	// Create volume bound to worker-2
	now := time.Now().UTC()
	volID := id.NewVolumeID()
	if err := env.store.Volumes().Create(ctx, &models.Volume{
		ID:        volID,
		Name:      "db-data",
		WorkerID:  env.worker2ID, // pinned to worker-2
		Path:      filepath.Join(env.baseDir, "volumes", "db-data"),
		Driver:    "local",
		SpecJSON:  `{}`,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create volume: %v", err)
	}

	// Run scheduling with volume requirement
	taskID := id.NewTaskID()
	coord := scheduler.NewAssignmentCoordinator(
		env.store,
		scheduler.NewBasicScheduler(),
		nil, // no real dispatcher in unit test
		logging.NewDefaultLogger(),
	)

	result, err := coord.Assign(ctx, scheduler.AssignOptions{
		TaskID: taskID,
		Requirements: &scheduler.TaskRequirements{
			TaskID:          taskID,
			CPU:             0.5,
			Memory:          256 * 1024 * 1024,
			RequiredRuntime: "native",
			RequiredVolumes: []string{"db-data"},
		},
		Spec: scheduler.TaskSpec{
			Command: "echo hello",
			Runtime: "native",
		},
	})
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}

	if result.WorkerID != env.worker2ID {
		t.Errorf("expected task assigned to worker-2 (volume owner), got worker %s", result.WorkerID)
	}
	t.Logf("Task correctly pinned to worker-2 (volume owner): %s", result.WorkerID)
}

// TestVolumeSchedulingIntegration_UnboundVolumeAccessibleToAll verifies that
// a volume created without a worker binding (WorkerID="") is accessible from
// all workers — this is the single-node case where the directory is on the
// same machine as the control plane.
func TestVolumeSchedulingIntegration_UnboundVolumeAccessibleToAll(t *testing.T) {
	env := newIntegTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()

	// Volume with no worker binding
	now := time.Now().UTC()
	if err := env.store.Volumes().Create(ctx, &models.Volume{
		ID:        id.NewVolumeID(),
		Name:      "shared-data",
		WorkerID:  "", // unbound
		Path:      filepath.Join(env.baseDir, "volumes", "shared-data"),
		Driver:    "local",
		SpecJSON:  `{}`,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create volume: %v", err)
	}

	taskID := id.NewTaskID()
	coord := scheduler.NewAssignmentCoordinator(
		env.store,
		scheduler.NewBasicScheduler(),
		nil,
		logging.NewDefaultLogger(),
	)

	result, err := coord.Assign(ctx, scheduler.AssignOptions{
		TaskID: taskID,
		Requirements: &scheduler.TaskRequirements{
			TaskID:          taskID,
			CPU:             0.5,
			Memory:          256 * 1024 * 1024,
			RequiredRuntime: "native",
			RequiredVolumes: []string{"shared-data"},
		},
		Spec: scheduler.TaskSpec{
			Command: "echo hello",
			Runtime: "native",
		},
	})
	if err != nil {
		t.Fatalf("Assign with unbound volume: %v", err)
	}

	// Both workers should have been feasible; either could have been chosen
	if result.WorkerID != env.worker1ID && result.WorkerID != env.worker2ID {
		t.Errorf("expected scheduling on one of the two workers, got %s", result.WorkerID)
	}
	t.Logf("Unbound volume accessible on both workers: assigned to %s", result.WorkerID)
}

// TestVolumeSchedulingIntegration_NoCompatibleWorkerFails verifies that when
// no registered worker owns the required volume, scheduling fails explicitly.
func TestVolumeSchedulingIntegration_NoCompatibleWorkerFails(t *testing.T) {
	env := newIntegTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()

	// No volume record created — task asking for "missing-vol" should fail.
	taskID := id.NewTaskID()
	coord := scheduler.NewAssignmentCoordinator(
		env.store,
		scheduler.NewBasicScheduler(),
		nil,
		logging.NewDefaultLogger(),
	)

	_, err := coord.Assign(ctx, scheduler.AssignOptions{
		TaskID: taskID,
		Requirements: &scheduler.TaskRequirements{
			TaskID:          taskID,
			CPU:             0.5,
			Memory:          256 * 1024 * 1024,
			RequiredRuntime: "native",
			RequiredVolumes: []string{"missing-vol"},
		},
		Spec: scheduler.TaskSpec{
			Command: "echo hello",
			Runtime: "native",
		},
	})
	if err == nil {
		t.Fatal("expected scheduling to fail when no worker owns the required volume, got nil")
	}
	t.Logf("Correctly rejected with: %v", err)
}

// TestVolumeSchedulingIntegration_SpecRequiredVolumesPathway verifies that
// RequiredVolumes in TaskSpec (not Requirements) are propagated correctly.
func TestVolumeSchedulingIntegration_SpecRequiredVolumesPathway(t *testing.T) {
	env := newIntegTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()

	now := time.Now().UTC()
	if err := env.store.Volumes().Create(ctx, &models.Volume{
		ID:        id.NewVolumeID(),
		Name:      "spec-vol",
		WorkerID:  env.worker1ID, // pinned to worker-1
		Path:      filepath.Join(env.baseDir, "volumes", "spec-vol"),
		Driver:    "local",
		SpecJSON:  `{}`,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create volume: %v", err)
	}

	taskID := id.NewTaskID()
	coord := scheduler.NewAssignmentCoordinator(
		env.store,
		scheduler.NewBasicScheduler(),
		nil,
		logging.NewDefaultLogger(),
	)

	// Pass volumes via Spec.RequiredVolumes (not Requirements.RequiredVolumes)
	result, err := coord.Assign(ctx, scheduler.AssignOptions{
		TaskID: taskID,
		Spec: scheduler.TaskSpec{
			Command:         "echo hello",
			Runtime:         "native",
			RequiredVolumes: []string{"spec-vol"}, // propagated pathway
		},
		// Requirements is nil — will be auto-created and merged with Spec volumes
	})
	if err != nil {
		t.Fatalf("Assign via Spec.RequiredVolumes: %v", err)
	}

	if result.WorkerID != env.worker1ID {
		t.Errorf("expected task pinned to worker-1 (spec-vol owner), got %s", result.WorkerID)
	}
	t.Logf("Spec.RequiredVolumes pathway correctly pins to worker-1")
}

// TestVolumeSchedulingIntegration_VolumeCreatedViaControlPlane exercises the
// full path: CreateVolume → volume persisted with WorkerID → scheduler assigns
// task to that worker.
func TestVolumeSchedulingIntegration_VolumeCreatedViaControlPlane(t *testing.T) {
	env := newIntegTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()

	// Create volume using the control plane API, binding to worker-2
	volCfg := models.VolumeConfig{
		Name:     "cp-volume",
		Driver:   models.VolumeDriverLocal,
		WorkerID: env.worker2ID, // explicit worker binding
	}
	_, err := env.cp.CreateVolume(ctx, volCfg)
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	// Now schedule a task requiring that volume
	taskID := id.NewTaskID()
	coord := scheduler.NewAssignmentCoordinator(
		env.store,
		scheduler.NewBasicScheduler(),
		nil,
		logging.NewDefaultLogger(),
	)

	result, err := coord.Assign(ctx, scheduler.AssignOptions{
		TaskID: taskID,
		Requirements: &scheduler.TaskRequirements{
			TaskID:          taskID,
			CPU:             0.5,
			Memory:          256 * 1024 * 1024,
			RequiredRuntime: "native",
			RequiredVolumes: []string{"cp-volume"},
		},
		Spec: scheduler.TaskSpec{
			Command: "echo hello",
			Runtime: "native",
		},
	})
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}

	if result.WorkerID != env.worker2ID {
		t.Errorf("expected task pinned to worker-2 (cp-volume owner), got %s", result.WorkerID)
	}
	t.Logf("Full CP lifecycle correctly pins to worker-2")
}

// TestVolumeSchedulingIntegration_MultiVolumeAllOnSameWorker verifies multi-volume
// tasks are only assigned if ALL volumes are on the same worker.
func TestVolumeSchedulingIntegration_MultiVolumeAllOnSameWorker(t *testing.T) {
	env := newIntegTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()

	now := time.Now().UTC()
	// Both volumes pinned to worker-2
	for _, name := range []string{"vol-a", "vol-b"} {
		if err := env.store.Volumes().Create(ctx, &models.Volume{
			ID:        id.NewVolumeID(),
			Name:      name,
			WorkerID:  env.worker2ID,
			Path:      filepath.Join(env.baseDir, "volumes", name),
			Driver:    "local",
			SpecJSON:  `{}`,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create volume %s: %v", name, err)
		}
	}

	taskID := id.NewTaskID()
	coord := scheduler.NewAssignmentCoordinator(
		env.store, scheduler.NewBasicScheduler(), nil, logging.NewDefaultLogger(),
	)

	result, err := coord.Assign(ctx, scheduler.AssignOptions{
		TaskID: taskID,
		Requirements: &scheduler.TaskRequirements{
			TaskID:          taskID,
			CPU:             0.5,
			Memory:          256 * 1024 * 1024,
			RequiredRuntime: "native",
			RequiredVolumes: []string{"vol-a", "vol-b"}, // both on worker-2
		},
		Spec: scheduler.TaskSpec{Command: "echo hello", Runtime: "native"},
	})
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if result.WorkerID != env.worker2ID {
		t.Errorf("expected worker-2 (owns both volumes), got %s", result.WorkerID)
	}
}

// TestVolumeSchedulingIntegration_VolumeSplitAcrossWorkersFails verifies that
// when vol-a is on worker-1 and vol-b is on worker-2, a task needing both fails.
func TestVolumeSchedulingIntegration_VolumeSplitAcrossWorkersFails(t *testing.T) {
	env := newIntegTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()

	now := time.Now().UTC()
	// vol-a on worker-1, vol-b on worker-2 (split)
	volumes := []struct {
		name     string
		workerID id.ID
	}{
		{"vol-a", env.worker1ID},
		{"vol-b", env.worker2ID},
	}
	for _, v := range volumes {
		if err := env.store.Volumes().Create(ctx, &models.Volume{
			ID:        id.NewVolumeID(),
			Name:      v.name,
			WorkerID:  v.workerID,
			Path:      filepath.Join(env.baseDir, "volumes", v.name),
			Driver:    "local",
			SpecJSON:  `{}`,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create volume %s: %v", v.name, err)
		}
	}

	taskID := id.NewTaskID()
	coord := scheduler.NewAssignmentCoordinator(
		env.store, scheduler.NewBasicScheduler(), nil, logging.NewDefaultLogger(),
	)

	_, err := coord.Assign(ctx, scheduler.AssignOptions{
		TaskID: taskID,
		Requirements: &scheduler.TaskRequirements{
			TaskID:          taskID,
			CPU:             0.5,
			Memory:          256 * 1024 * 1024,
			RequiredRuntime: "native",
			RequiredVolumes: []string{"vol-a", "vol-b"}, // split across workers
		},
		Spec: scheduler.TaskSpec{Command: "echo hello", Runtime: "native"},
	})
	if err == nil {
		t.Fatal("expected failure when required volumes are split across workers, got nil")
	}
	t.Logf("Correctly rejected split-volume scenario: %v", err)
}
