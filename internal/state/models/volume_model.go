package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// VolumeDriver represents supported storage volume driver types.
type VolumeDriver string

const (
	VolumeDriverLocal VolumeDriver = "local"
	VolumeDriverHost  VolumeDriver = "host"
)

// VolumeState represents the lifecycle status of a persistent volume.
type VolumeState string

const (
	VolumeStatePending   VolumeState = "PENDING"
	VolumeStateAvailable VolumeState = "AVAILABLE"
	VolumeStateInUse     VolumeState = "IN_USE"
	VolumeStateDegraded  VolumeState = "DEGRADED"
	VolumeStateDeleting  VolumeState = "DELETING"
	VolumeStateDeleted   VolumeState = "DELETED"
)

// VolumeSizeMetadata captures size and capacity parameters for a persistent volume.
type VolumeSizeMetadata struct {
	SizeBytes     int64  `json:"size_bytes" yaml:"size_bytes"`         // e.g. 1073741824 (1GB)
	HumanReadable string `json:"human_readable" yaml:"human_readable"` // e.g. "10GB", "500MB"
	UsedBytes     int64  `json:"used_bytes" yaml:"used_bytes"`
}

// VolumeConfig defines the creation parameters for a persistent storage volume.
type VolumeConfig struct {
	Name     string             `json:"name" yaml:"name"`
	Driver   VolumeDriver       `json:"driver" yaml:"driver"`     // "local", "host"
	Location string             `json:"location" yaml:"location"` // host filesystem path / directory
	Size     VolumeSizeMetadata `json:"size" yaml:"size"`
	ReadOnly bool               `json:"read_only,omitempty" yaml:"read_only,omitempty"`
	OwnerRef string             `json:"owner_ref,omitempty" yaml:"owner_ref,omitempty"` // Service or Job identifier using this volume
	WorkerID id.ID              `json:"worker_id,omitempty" yaml:"worker_id,omitempty"` // Bound node/worker if local storage
	Labels   map[string]string  `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// ComputeHash generates a deterministic SHA-256 fingerprint for a volume configuration.
func (c *VolumeConfig) ComputeHash() string {
	raw := struct {
		Name     string             `json:"name"`
		Driver   VolumeDriver       `json:"driver"`
		Location string             `json:"location"`
		Size     VolumeSizeMetadata `json:"size"`
		ReadOnly bool               `json:"read_only"`
		OwnerRef string             `json:"owner_ref"`
		WorkerID string             `json:"worker_id"`
	}{
		Name:     c.Name,
		Driver:   c.Driver,
		Location: c.Location,
		Size:     c.Size,
		ReadOnly: c.ReadOnly,
		OwnerRef: c.OwnerRef,
		WorkerID: c.WorkerID.String(),
	}

	b, _ := json.Marshal(raw)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// VolumeRecord represents an independently identifiable, persistent storage volume in CloudX.
// Volume contains:
// - ID
// - Name
// - Driver (Initial driver: LocalVolume)
// - Location (filesystem directory path)
// - Size metadata
// - Owner/reference (bound service or job)
// - Created timestamp
type VolumeRecord struct {
	ID         id.ID              `json:"id"`
	Name       string             `json:"name"`
	Driver     VolumeDriver       `json:"driver"`
	Location   string             `json:"location"`
	Size       VolumeSizeMetadata `json:"size"`
	OwnerRef   string             `json:"owner_ref,omitempty"`
	WorkerID   id.ID              `json:"worker_id,omitempty"` // Bound worker node for LocalVolume
	State      VolumeState        `json:"state"`
	ConfigHash string             `json:"config_hash"`
	Config     VolumeConfig       `json:"config"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

// Validate ensures all required volume fields are populated and valid.
func (v *VolumeRecord) Validate() error {
	var errs []string

	if strings.TrimSpace(v.ID.String()) == "" {
		errs = append(errs, "volume id is required")
	}
	if strings.TrimSpace(v.Name) == "" {
		errs = append(errs, "volume name is required")
	}
	if v.Driver == "" {
		v.Driver = VolumeDriverLocal
	} else if v.Driver != VolumeDriverLocal && v.Driver != VolumeDriverHost {
		errs = append(errs, fmt.Sprintf("invalid driver '%s', must be 'local' or 'host'", v.Driver))
	}
	if strings.TrimSpace(v.Location) == "" {
		errs = append(errs, "volume location path is required")
	}
	if v.Size.SizeBytes < 0 {
		errs = append(errs, "volume size cannot be negative")
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid volume: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Fingerprint returns the unique identity string for this volume.
func (v *VolumeRecord) Fingerprint() string {
	shortHash := v.ConfigHash
	if len(shortHash) > 12 {
		shortHash = shortHash[:12]
	}
	return fmt.Sprintf("vol:%s (%s | %s | driver:%s)", v.Name, v.ID, shortHash, v.Driver)
}

// ToSpecJSON marshals VolumeRecord into JSON string suitable for storage in spec_json / DB payload.
func (v *VolumeRecord) ToSpecJSON() (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VolumeFromModel converts a basic models.Volume database record into a rich VolumeRecord.
func VolumeFromModel(m *Volume) (*VolumeRecord, error) {
	if m == nil {
		return nil, fmt.Errorf("nil volume model")
	}

	var rec VolumeRecord
	if m.SpecJSON != "" {
		if err := json.Unmarshal([]byte(m.SpecJSON), &rec); err == nil && rec.Name != "" {
			rec.ID = m.ID
			rec.Name = m.Name
			if rec.Driver == "" {
				rec.Driver = VolumeDriver(m.Driver)
			}
			if rec.Location == "" {
				rec.Location = m.Path
			}
			if rec.WorkerID == "" {
				rec.WorkerID = m.WorkerID
			}
			if rec.CreatedAt.IsZero() {
				rec.CreatedAt = m.CreatedAt
			}
			rec.UpdatedAt = m.UpdatedAt
			return &rec, nil
		}
	}

	// Fallback conversion from raw model
	driver := VolumeDriver(m.Driver)
	if driver == "" {
		driver = VolumeDriverLocal
	}
	rec = VolumeRecord{
		ID:       m.ID,
		Name:     m.Name,
		Driver:   driver,
		Location: m.Path,
		WorkerID: m.WorkerID,
		State:    VolumeStateAvailable,
		Config: VolumeConfig{
			Name:     m.Name,
			Driver:   driver,
			Location: m.Path,
			WorkerID: m.WorkerID,
		},
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
	rec.ConfigHash = rec.Config.ComputeHash()
	return &rec, nil
}
