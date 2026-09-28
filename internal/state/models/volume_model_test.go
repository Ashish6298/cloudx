package models

import (
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

func TestVolumeRecord_Validate(t *testing.T) {
	vol := &VolumeRecord{
		ID:       id.NewVolumeID(),
		Name:     "app-cache",
		Driver:   VolumeDriverLocal,
		Location: "/data/cloudx/volumes/app-cache",
		Size: VolumeSizeMetadata{
			SizeBytes:     5368709120, // 5GB
			HumanReadable: "5GB",
		},
		OwnerRef: "api-server",
		WorkerID: id.NewWorkerID(),
		State:    VolumeStateAvailable,
		Config: VolumeConfig{
			Name:     "app-cache",
			Driver:   VolumeDriverLocal,
			Location: "/data/cloudx/volumes/app-cache",
			Size: VolumeSizeMetadata{
				SizeBytes:     5368709120,
				HumanReadable: "5GB",
			},
			OwnerRef: "api-server",
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	if err := vol.Validate(); err != nil {
		t.Fatalf("expected valid volume, got: %v", err)
	}

	hash := vol.Config.ComputeHash()
	if hash == "" {
		t.Fatalf("expected non-empty config hash")
	}

	// Deterministic fingerprint check
	if hash != vol.Config.ComputeHash() {
		t.Fatalf("expected identical hash for identical configuration")
	}

	fp := vol.Fingerprint()
	if fp == "" {
		t.Fatalf("expected non-empty fingerprint")
	}
}

func TestVolumeRecord_Serialization(t *testing.T) {
	vol := &VolumeRecord{
		ID:       id.NewVolumeID(),
		Name:     "mariadb-data",
		Driver:   VolumeDriverLocal,
		Location: "/mnt/disks/ssd/mariadb",
		Size: VolumeSizeMetadata{
			SizeBytes:     20 * 1024 * 1024 * 1024,
			HumanReadable: "20GB",
		},
		OwnerRef:  "mariadb-service",
		WorkerID:  id.NewWorkerID(),
		State:     VolumeStateInUse,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	specJSON, err := vol.ToSpecJSON()
	if err != nil {
		t.Fatalf("failed to serialize volume: %v", err)
	}

	genericModel := &Volume{
		ID:        vol.ID,
		Name:      vol.Name,
		WorkerID:  vol.WorkerID,
		Path:      vol.Location,
		Driver:    string(vol.Driver),
		SpecJSON:  specJSON,
		CreatedAt: vol.CreatedAt,
		UpdatedAt: vol.UpdatedAt,
	}

	deserialized, err := VolumeFromModel(genericModel)
	if err != nil {
		t.Fatalf("failed to deserialize volume: %v", err)
	}

	if deserialized.Name != vol.Name || deserialized.Driver != VolumeDriverLocal || deserialized.Size.SizeBytes != vol.Size.SizeBytes {
		t.Fatalf("deserialized volume does not match original: %+v", deserialized)
	}
}
