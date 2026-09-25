package controlplane_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

func newTestCPForVolume(t *testing.T) (*controlplane.ControlPlane, string, func()) {
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

	cleanup := func() {
		_ = store.Close()
	}
	return cp, dir, cleanup
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeCreate — CreateVolume provisions directory and persists record.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeCreate(t *testing.T) {
	cp, baseDir, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()

	result, err := cp.CreateVolume(ctx, models.VolumeConfig{
		Name:   "test-data",
		Driver: models.VolumeDriverLocal,
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	// Validate returned result
	if result.VolumeName != "test-data" {
		t.Errorf("expected name 'test-data', got %q", result.VolumeName)
	}
	if result.Driver != models.VolumeDriverLocal {
		t.Errorf("expected driver 'local', got %q", result.Driver)
	}
	if result.State != models.VolumeStateAvailable {
		t.Errorf("expected state AVAILABLE, got %q", result.State)
	}

	// Validate host directory was created
	expectedDir := filepath.Join(baseDir, "volumes", "test-data")
	if fi, err := os.Stat(expectedDir); err != nil || !fi.IsDir() {
		t.Errorf("expected volume directory to exist at %s, got err=%v", expectedDir, err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeCreateWithExplicitLocation — Custom path is respected.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeCreateWithExplicitLocation(t *testing.T) {
	cp, _, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()
	customDir := filepath.Join(t.TempDir(), "custom-vol")

	result, err := cp.CreateVolume(ctx, models.VolumeConfig{
		Name:     "custom",
		Driver:   models.VolumeDriverLocal,
		Location: customDir,
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	if result.Location != customDir {
		t.Errorf("expected location %q, got %q", customDir, result.Location)
	}
	if fi, err := os.Stat(customDir); err != nil || !fi.IsDir() {
		t.Errorf("expected custom directory to exist at %s", customDir)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeCreateDuplicateName — Duplicate name is rejected.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeCreateDuplicateName(t *testing.T) {
	cp, _, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()

	_, err := cp.CreateVolume(ctx, models.VolumeConfig{Name: "dupe"})
	if err != nil {
		t.Fatalf("first CreateVolume failed unexpectedly: %v", err)
	}

	_, err = cp.CreateVolume(ctx, models.VolumeConfig{Name: "dupe"})
	if err == nil {
		t.Fatal("expected error creating duplicate volume, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeList — All created volumes appear in ListVolumes.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeList(t *testing.T) {
	cp, _, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()

	names := []string{"alpha", "beta", "gamma"}
	for _, n := range names {
		if _, err := cp.CreateVolume(ctx, models.VolumeConfig{Name: n}); err != nil {
			t.Fatalf("CreateVolume(%q): %v", n, err)
		}
	}

	vols, err := cp.ListVolumes(ctx)
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}

	if len(vols) != len(names) {
		t.Errorf("expected %d volumes, got %d", len(names), len(vols))
	}

	found := make(map[string]bool)
	for _, v := range vols {
		found[v.Name] = true
	}
	for _, n := range names {
		if !found[n] {
			t.Errorf("volume %q not found in ListVolumes result", n)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeInspect — InspectVolume returns correct details.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeInspect(t *testing.T) {
	cp, baseDir, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()

	_, err := cp.CreateVolume(ctx, models.VolumeConfig{Name: "inspect-me"})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	result, err := cp.InspectVolume(ctx, "inspect-me")
	if err != nil {
		t.Fatalf("InspectVolume: %v", err)
	}

	if result.Volume.Name != "inspect-me" {
		t.Errorf("expected name 'inspect-me', got %q", result.Volume.Name)
	}
	if !result.DirExists {
		t.Error("expected DirExists=true after CreateVolume, got false")
	}

	_ = baseDir
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeInspectByID — InspectVolume resolves by raw ID string.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeInspectByID(t *testing.T) {
	cp, _, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()

	created, err := cp.CreateVolume(ctx, models.VolumeConfig{Name: "id-lookup"})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	result, err := cp.InspectVolume(ctx, created.VolumeID.String())
	if err != nil {
		t.Fatalf("InspectVolume by ID: %v", err)
	}

	if result.Volume.ID != created.VolumeID {
		t.Errorf("expected volume ID %s, got %s", created.VolumeID, result.Volume.ID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeDelete — DeleteVolume removes record from state.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeDelete(t *testing.T) {
	cp, _, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()

	_, err := cp.CreateVolume(ctx, models.VolumeConfig{Name: "to-delete"})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	if err := cp.DeleteVolume(ctx, "to-delete"); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}

	vols, err := cp.ListVolumes(ctx)
	if err != nil {
		t.Fatalf("ListVolumes after delete: %v", err)
	}
	for _, v := range vols {
		if v.Name == "to-delete" {
			t.Error("expected volume 'to-delete' to be removed, but it still appears in ListVolumes")
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeDeleteNotFound — Deleting non-existent volume returns an error.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeDeleteNotFound(t *testing.T) {
	cp, _, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()

	err := cp.DeleteVolume(ctx, "does-not-exist")
	if err == nil {
		t.Fatal("expected error deleting non-existent volume, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumePersistenceAcrossRestarts — Data written to volume directory
// survives a simulated process crash (control plane re-open).
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumePersistenceAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cloudx_test.db")
	ctx := context.Background()

	// --- First lifecycle: create volume and write data ---
	store1, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open (first): %v", err)
	}
	cfg := config.NewDefaultConfig()
	cfg.Storage.Path = dir

	cp1, err := controlplane.New(controlplane.Options{Config: cfg, Store: store1, Logger: logging.NewDefaultLogger()})
	if err != nil {
		t.Fatalf("controlplane.New (first): %v", err)
	}

	result, err := cp1.CreateVolume(ctx, models.VolumeConfig{Name: "persistent-vol"})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	volLocation := result.Location

	// Simulate a process writing data to the volume directory
	testFile := filepath.Join(volLocation, "important_data.txt")
	if err := os.WriteFile(testFile, []byte("critical data"), 0644); err != nil {
		t.Fatalf("WriteFile to volume: %v", err)
	}

	_ = store1.Close() // simulate "crash"

	// --- Second lifecycle: re-open and verify data persists ---
	store2, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open (second): %v", err)
	}
	defer store2.Close()

	cp2, err := controlplane.New(controlplane.Options{Config: cfg, Store: store2, Logger: logging.NewDefaultLogger()})
	if err != nil {
		t.Fatalf("controlplane.New (second): %v", err)
	}

	// Volume record still accessible
	inspected, err := cp2.InspectVolume(ctx, "persistent-vol")
	if err != nil {
		t.Fatalf("InspectVolume after restart: %v", err)
	}
	if inspected.Volume.Name != "persistent-vol" {
		t.Errorf("expected 'persistent-vol', got %q", inspected.Volume.Name)
	}
	if !inspected.DirExists {
		t.Error("expected volume directory to still exist after restart")
	}

	// Data still on disk
	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("ReadFile after restart: %v", err)
	}
	if string(data) != "critical data" {
		t.Errorf("expected 'critical data', got %q", string(data))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeMountEnv — MountVolumeEnv returns correct env var mapping.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeMountEnv(t *testing.T) {
	cp, _, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()

	_, err := cp.CreateVolume(ctx, models.VolumeConfig{Name: "my-data"})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	env, err := cp.MountVolumeEnv(ctx, []string{"my-data"})
	if err != nil {
		t.Fatalf("MountVolumeEnv: %v", err)
	}

	key := "CLOUDX_VOLUME_MY_DATA"
	if _, ok := env[key]; !ok {
		t.Errorf("expected env key %q to be present, got: %v", key, env)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeMountEnvNotFound — MountVolumeEnv errors on unknown volume.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeMountEnvNotFound(t *testing.T) {
	cp, _, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()

	_, err := cp.MountVolumeEnv(ctx, []string{"ghost-volume"})
	if err == nil {
		t.Fatal("expected error for unknown volume, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeInvalidDriver — Unsupported driver is rejected.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeInvalidDriver(t *testing.T) {
	cp, _, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()

	_, err := cp.CreateVolume(ctx, models.VolumeConfig{
		Name:   "bad-driver",
		Driver: "nfs",
	})
	if err == nil {
		t.Fatal("expected error for unsupported driver 'nfs', got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestVolumeCreateEmptyName — Empty name is rejected.
// ─────────────────────────────────────────────────────────────────────────────

func TestVolumeCreateEmptyName(t *testing.T) {
	cp, _, cleanup := newTestCPForVolume(t)
	defer cleanup()

	ctx := context.Background()

	_, err := cp.CreateVolume(ctx, models.VolumeConfig{Name: ""})
	if err == nil {
		t.Fatal("expected error for empty volume name, got nil")
	}
}
