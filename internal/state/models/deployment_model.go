package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// DeploymentStatus represents the lifecycle state of an immutable deployment.
type DeploymentStatus string

const (
	DeploymentStatusPending    DeploymentStatus = "PENDING"
	DeploymentStatusInProgress DeploymentStatus = "IN_PROGRESS"
	DeploymentStatusActive     DeploymentStatus = "ACTIVE"
	DeploymentStatusSuperceded DeploymentStatus = "SUPERCEDED"
	DeploymentStatusFailed     DeploymentStatus = "FAILED"
	DeploymentStatusRolledBack DeploymentStatus = "ROLLED_BACK"
)

// UpdateStrategySpec defines rollout strategy parameters.
type UpdateStrategySpec struct {
	Type          string `json:"type" yaml:"type"` // "rolling", "recreate"
	MaxUnavailable int    `json:"max_unavailable" yaml:"max_unavailable"`
	MaxSurge       int    `json:"max_surge" yaml:"max_surge"`
}

// DeploymentConfig captures the snapshot of service configuration for a deployment.
type DeploymentConfig struct {
	Command        string               `json:"command"`
	Args           []string             `json:"args,omitempty"`
	Environment    map[string]string    `json:"environment,omitempty"`
	WorkingDir     string               `json:"working_dir,omitempty"`
	Artifact       string               `json:"artifact,omitempty"` // Artifact reference / binary path / container image
	Runtime        string               `json:"runtime"`
	Replicas       int                  `json:"replicas"`
	Resources      ResourceRequirements `json:"resources"`
	RestartPolicy  RestartPolicy        `json:"restart_policy"`
	HealthCheck    *HealthCheckSpec     `json:"health_check,omitempty"`
	UpdateStrategy *UpdateStrategySpec  `json:"update_strategy,omitempty"`
	Ports          []PortMapping        `json:"ports,omitempty"`
	Volumes        []VolumeMount        `json:"volumes,omitempty"`
	Networks       []string             `json:"networks,omitempty"`
}

// ComputeHash generates a deterministic SHA-256 fingerprint for a deployment configuration.
func (c *DeploymentConfig) ComputeHash() string {
	// Normalize map keys for deterministic JSON encoding
	keys := make([]string, 0, len(c.Environment))
	for k := range c.Environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	type sortedEnvPair struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	sortedEnv := make([]sortedEnvPair, 0, len(keys))
	for _, k := range keys {
		sortedEnv = append(sortedEnv, sortedEnvPair{Key: k, Value: c.Environment[k]})
	}

	// Sort networks deterministically
	sortedNets := make([]string, len(c.Networks))
	copy(sortedNets, c.Networks)
	sort.Strings(sortedNets)

	raw := struct {
		Command        string               `json:"command"`
		Args           []string             `json:"args,omitempty"`
		Environment    []sortedEnvPair      `json:"environment,omitempty"`
		WorkingDir     string               `json:"working_dir,omitempty"`
		Artifact       string               `json:"artifact,omitempty"`
		Runtime        string               `json:"runtime"`
		Replicas       int                  `json:"replicas"`
		Resources      ResourceRequirements `json:"resources"`
		RestartPolicy  RestartPolicy        `json:"restart_policy"`
		HealthCheck    *HealthCheckSpec     `json:"health_check,omitempty"`
		UpdateStrategy *UpdateStrategySpec  `json:"update_strategy,omitempty"`
		Ports          []PortMapping        `json:"ports,omitempty"`
		Volumes        []VolumeMount        `json:"volumes,omitempty"`
		Networks       []string             `json:"networks,omitempty"`
	}{
		Command:        c.Command,
		Args:           c.Args,
		Environment:    sortedEnv,
		WorkingDir:     c.WorkingDir,
		Artifact:       c.Artifact,
		Runtime:        c.Runtime,
		Replicas:       c.Replicas,
		Resources:      c.Resources,
		RestartPolicy:  c.RestartPolicy,
		HealthCheck:    c.HealthCheck,
		UpdateStrategy: c.UpdateStrategy,
		Ports:          c.Ports,
		Volumes:        c.Volumes,
		Networks:       sortedNets,
	}

	b, _ := json.Marshal(raw)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ImmutableDeployment represents an independently identifiable, immutable deployment record.
// A deployment contains:
// - Service (Name & ID)
// - Version
// - Configuration & Artifact reference
// - Environment & Resources & Replica count & Runtime
// - Timestamp & Config Hash (Checksum)
type ImmutableDeployment struct {
	ID          id.ID            `json:"id"`
	ServiceID   id.ID            `json:"service_id"`
	ServiceName string           `json:"service_name"`
	Version     string           `json:"version"`     // e.g. "v1", "v2", "v1.0.0"
	ConfigHash  string           `json:"config_hash"` // SHA-256 config fingerprint
	Status      DeploymentStatus `json:"status"`
	Config      DeploymentConfig `json:"config"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// Validate ensures all required deployment record fields are populated and consistent.
func (d *ImmutableDeployment) Validate() error {
	var errs []string

	if strings.TrimSpace(d.ID.String()) == "" {
		errs = append(errs, "deployment id is required")
	}
	if strings.TrimSpace(d.ServiceID.String()) == "" {
		errs = append(errs, "service id is required")
	}
	if strings.TrimSpace(d.ServiceName) == "" {
		errs = append(errs, "service name is required")
	}
	if strings.TrimSpace(d.Version) == "" {
		errs = append(errs, "version is required")
	}
	if strings.TrimSpace(d.Config.Command) == "" && strings.TrimSpace(d.Config.Artifact) == "" {
		errs = append(errs, "either command or artifact reference is required in deployment config")
	}
	if d.Config.Replicas < 0 {
		errs = append(errs, "replicas cannot be negative")
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid deployment: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Fingerprint returns the unique identity string for this deployment (e.g. "api:v1 (dep-18f45... | hash:abcd)").
func (d *ImmutableDeployment) Fingerprint() string {
	shortHash := d.ConfigHash
	if len(shortHash) > 12 {
		shortHash = shortHash[:12]
	}
	return fmt.Sprintf("%s:%s (%s | %s)", d.ServiceName, d.Version, d.ID, shortHash)
}

// ToSpecJSON marshals the DeploymentConfig into JSON string suitable for storage in spec_json.
func (d *ImmutableDeployment) ToSpecJSON() (string, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DeploymentFromModel converts a Deployment record into an ImmutableDeployment.
func DeploymentFromModel(d *Deployment) (*ImmutableDeployment, error) {
	if d == nil {
		return nil, fmt.Errorf("nil deployment model")
	}

	var imm ImmutableDeployment
	if d.SpecJSON != "" {
		if err := json.Unmarshal([]byte(d.SpecJSON), &imm); err == nil && imm.Version != "" {
			// Backfill top-level fields from DB if needed
			imm.ID = d.ID
			imm.ServiceID = d.ServiceID
			if imm.Status == "" {
				imm.Status = DeploymentStatus(d.Status)
			}
			if imm.CreatedAt.IsZero() {
				imm.CreatedAt = d.CreatedAt
			}
			imm.UpdatedAt = d.UpdatedAt
			return &imm, nil
		}
	}

	// Fallback when spec_json was raw ServiceConfig
	imm.ID = d.ID
	imm.ServiceID = d.ServiceID
	imm.Version = d.Version
	imm.Status = DeploymentStatus(d.Status)
	imm.CreatedAt = d.CreatedAt
	imm.UpdatedAt = d.UpdatedAt
	return &imm, nil
}
