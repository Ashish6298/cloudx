package config

import (
	"fmt"
	"strings"
	"time"
)

// Config represents the complete centralized CloudX configuration.
type Config struct {
	Node         NodeConfig         `yaml:"node"`
	ControlPlane ControlPlaneConfig `yaml:"control_plane"`
	Worker       WorkerConfig       `yaml:"worker"`
	Runtime      RuntimeConfig      `yaml:"runtime"`
	Storage      StorageConfig      `yaml:"storage"`
	Network      NetworkConfig      `yaml:"network"`
	Health       HealthConfig       `yaml:"health"`
	Logging      LoggingConfig      `yaml:"logging"`
}

// NodeConfig specifies node identification and metadata.
type NodeConfig struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

// ControlPlaneConfig specifies control-plane connection properties.
type ControlPlaneConfig struct {
	Address string `yaml:"address"`
}

// WorkerConfig specifies worker endpoint parameters.
type WorkerConfig struct {
	Address        string `yaml:"address"`
	BootstrapToken string `yaml:"bootstrap_token,omitempty"`
}

// RuntimeConfig specifies runtime driver properties.
type RuntimeConfig struct {
	Type string `yaml:"type"` // "native" (default) or "docker"
}

// StorageConfig specifies root persistence path.
type StorageConfig struct {
	Path string `yaml:"path"`
}

// NetworkConfig specifies network configurations.
type NetworkConfig struct {
	DefaultNetwork string `yaml:"default_network"`
}

// HealthConfig holds heartbeat and probe timings.
type HealthConfig struct {
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
}

// LoggingConfig configures logging subsystem.
type LoggingConfig struct {
	Level string `yaml:"level"` // "debug", "info", "warn", "error"
}

// ValidationError records a structured configuration error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("config validation error on field '%s': %s", e.Field, e.Message)
}

// ValidationErrors is a slice of ValidationError.
type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d configuration error(s) found:\n", len(ve)))
	for _, e := range ve {
		sb.WriteString(fmt.Sprintf("  - [%s]: %s\n", e.Field, e.Message))
	}
	return sb.String()
}

// HasErrors returns true if validation errors exist.
func (ve ValidationErrors) HasErrors() bool {
	return len(ve) > 0
}
