package controlplane

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// VolumeCreateResult holds the outcome of a successful volume creation.
type VolumeCreateResult struct {
	VolumeID   id.ID                `json:"volume_id"`
	VolumeName string               `json:"volume_name"`
	Location   string               `json:"location"`
	Driver     models.VolumeDriver  `json:"driver"`
	State      models.VolumeState   `json:"state"`
	CreatedAt  time.Time            `json:"created_at"`
}

// CreateVolume provisions a new persistent volume in the cluster.
//
// For LocalVolume driver, it creates the host directory if it does not exist,
// persists the VolumeRecord to the state store, and appends a VOLUME_CREATED audit event.
// The volume directory persists across process crashes and restarts — satisfying the
// Phase 44 acceptance criterion.
func (cp *ControlPlane) CreateVolume(ctx context.Context, cfg models.VolumeConfig) (*VolumeCreateResult, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("volume name is required")
	}

	// Default driver to local
	if cfg.Driver == "" {
		cfg.Driver = models.VolumeDriverLocal
	}
	if cfg.Driver != models.VolumeDriverLocal && cfg.Driver != models.VolumeDriverHost {
		return nil, fmt.Errorf("unsupported volume driver '%s': only 'local' and 'host' are supported", cfg.Driver)
	}

	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	// Check for name collision
	existing, _ := store.Volumes().List(ctx)
	for _, v := range existing {
		if strings.EqualFold(v.Name, cfg.Name) {
			return nil, fmt.Errorf("volume '%s' already exists", cfg.Name)
		}
	}

	now := time.Now().UTC()
	volID := id.NewVolumeID()

	// Resolve volume location: use provided path or default to storage base
	location := cfg.Location
	if location == "" {
		storageBase := cp.cfg.Storage.Path
		if storageBase == "" {
			storageBase = "~/.cloudx"
		}
		location = fmt.Sprintf("%s/volumes/%s", storageBase, cfg.Name)
	}

	// Provision the host directory for local/host driver
	if cfg.Driver == models.VolumeDriverLocal || cfg.Driver == models.VolumeDriverHost {
		if err := os.MkdirAll(location, 0755); err != nil {
			return nil, fmt.Errorf("failed to provision volume directory '%s': %w", location, err)
		}
	}

	// Build VolumeRecord
	rec := &models.VolumeRecord{
		ID:       volID,
		Name:     cfg.Name,
		Driver:   cfg.Driver,
		Location: location,
		Size:     cfg.Size,
		OwnerRef: cfg.OwnerRef,
		WorkerID: cfg.WorkerID,
		State:    models.VolumeStateAvailable,
		Config:   cfg,
	}
	rec.Config.Location = location
	rec.ConfigHash = rec.Config.ComputeHash()
	rec.CreatedAt = now
	rec.UpdatedAt = now

	if err := rec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid volume configuration: %w", err)
	}

	specJSON, err := rec.ToSpecJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize volume record: %w", err)
	}

	// Persist to state store
	dbVol := &models.Volume{
		ID:        volID,
		Name:      cfg.Name,
		WorkerID:  cfg.WorkerID,
		Path:      location,
		Driver:    string(cfg.Driver),
		SpecJSON:  specJSON,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Volumes().Create(ctx, dbVol); err != nil {
		return nil, fmt.Errorf("failed to persist volume '%s': %w", cfg.Name, err)
	}

	// Append VOLUME_CREATED audit event
	_ = store.Events().Append(ctx, &models.Event{
		ID:       id.NewEventID(),
		Type:     "VOLUME_CREATED",
		Source:   "controlplane",
		EntityID: volID,
		Payload: fmt.Sprintf(
			`{"name":%q,"driver":%q,"location":%q}`,
			cfg.Name, cfg.Driver, location,
		),
		CreatedAt: now,
	})

	cp.logger.Info("Volume '%s' (%s) created at %s", cfg.Name, volID, location)

	return &VolumeCreateResult{
		VolumeID:   volID,
		VolumeName: cfg.Name,
		Location:   location,
		Driver:     cfg.Driver,
		State:      models.VolumeStateAvailable,
		CreatedAt:  now,
	}, nil
}

// ListVolumes returns all volumes known to the cluster.
func (cp *ControlPlane) ListVolumes(ctx context.Context) ([]*models.VolumeRecord, error) {
	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	dbVols, err := store.Volumes().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list volumes: %w", err)
	}

	recs := make([]*models.VolumeRecord, 0, len(dbVols))
	for _, v := range dbVols {
		rec, err := models.VolumeFromModel(v)
		if err != nil {
			cp.logger.Warn("Skipping malformed volume record %s: %v", v.ID, err)
			continue
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// VolumeInspectResult holds full volume detail for inspection.
type VolumeInspectResult struct {
	Volume     *models.VolumeRecord `json:"volume"`
	DirExists  bool                 `json:"dir_exists"`
	DirSizeMsg string               `json:"dir_size_msg,omitempty"`
}

// InspectVolume retrieves detailed information about a named or ID-referenced volume.
func (cp *ControlPlane) InspectVolume(ctx context.Context, nameOrID string) (*VolumeInspectResult, error) {
	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	rec, err := cp.resolveVolume(ctx, nameOrID)
	if err != nil {
		return nil, err
	}

	// Probe filesystem for live directory status
	dirExists := false
	dirSizeMsg := ""
	if rec.Location != "" {
		if fi, statErr := os.Stat(rec.Location); statErr == nil && fi.IsDir() {
			dirExists = true
			dirSizeMsg = "directory present on disk"
		} else if statErr != nil {
			dirSizeMsg = fmt.Sprintf("directory not found: %v", statErr)
		}
	}

	return &VolumeInspectResult{
		Volume:     rec,
		DirExists:  dirExists,
		DirSizeMsg: dirSizeMsg,
	}, nil
}

// DeleteVolume removes a volume record and its host directory (if empty).
//
// Delete is refused if the volume is IN_USE by an active workload.
func (cp *ControlPlane) DeleteVolume(ctx context.Context, nameOrID string) error {
	store := cp.StateManager.Store()
	if store == nil {
		return fmt.Errorf("state store is not available")
	}

	rec, err := cp.resolveVolume(ctx, nameOrID)
	if err != nil {
		return err
	}

	if rec.State == models.VolumeStateInUse {
		return fmt.Errorf("volume '%s' is currently IN_USE and cannot be deleted", rec.Name)
	}

	// Attempt to remove host directory — ignore error if non-empty (data safety)
	if rec.Location != "" {
		if removeErr := os.Remove(rec.Location); removeErr != nil && !os.IsNotExist(removeErr) {
			cp.logger.Warn("Volume directory '%s' could not be removed (may not be empty): %v", rec.Location, removeErr)
		}
	}

	// Remove record from state store
	if err := store.Volumes().Delete(ctx, rec.ID); err != nil {
		return fmt.Errorf("failed to delete volume '%s' from state store: %w", rec.Name, err)
	}

	// Append VOLUME_DELETED audit event
	_ = store.Events().Append(ctx, &models.Event{
		ID:       id.NewEventID(),
		Type:     "VOLUME_DELETED",
		Source:   "controlplane",
		EntityID: rec.ID,
		Payload: fmt.Sprintf(
			`{"name":%q,"driver":%q,"location":%q}`,
			rec.Name, rec.Driver, rec.Location,
		),
		CreatedAt: time.Now().UTC(),
	})

	cp.logger.Info("Volume '%s' (%s) deleted", rec.Name, rec.ID)
	return nil
}

// MountVolumeEnv constructs environment variables that expose volume mount paths to a process.
// For each VolumeMount in the task spec, the location of the matching volume is exposed as:
//
//	CLOUDX_VOLUME_<NAME_UPPER>=<location>
//
// The worker/runtime layer calls this before spawning a native process so the workload
// can locate its persistent storage directory. Data written there survives process restarts.
func (cp *ControlPlane) MountVolumeEnv(ctx context.Context, volumeNames []string) (map[string]string, error) {
	env := make(map[string]string, len(volumeNames))
	for _, name := range volumeNames {
		rec, err := cp.resolveVolume(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("volume mount failed for '%s': %w", name, err)
		}
		// Ensure directory exists (idempotent)
		if err := os.MkdirAll(rec.Location, 0755); err != nil {
			return nil, fmt.Errorf("failed to ensure volume directory '%s': %w", rec.Location, err)
		}
		envKey := "CLOUDX_VOLUME_" + strings.ToUpper(strings.ReplaceAll(rec.Name, "-", "_"))
		env[envKey] = rec.Location
	}
	return env, nil
}

// resolveVolume looks up a VolumeRecord by ID or name.
func (cp *ControlPlane) resolveVolume(ctx context.Context, nameOrID string) (*models.VolumeRecord, error) {
	store := cp.StateManager.Store()

	// Try exact ID lookup first
	if dbVol, err := store.Volumes().Get(ctx, id.ID(nameOrID)); err == nil && dbVol != nil {
		return models.VolumeFromModel(dbVol)
	}

	// Fall back to name scan
	dbVols, err := store.Volumes().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list volumes: %w", err)
	}
	for _, v := range dbVols {
		if strings.EqualFold(v.Name, nameOrID) {
			return models.VolumeFromModel(v)
		}
	}

	return nil, fmt.Errorf("volume '%s' not found", nameOrID)
}
