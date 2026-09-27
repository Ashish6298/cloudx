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
	Telemetry    TelemetryConfig    `yaml:"telemetry"`
	TLS          TLSConfig          `yaml:"tls,omitempty"`
}

// TLSConfig configures Transport Layer Security (TLS/mTLS) for gRPC communication.
type TLSConfig struct {
	Enabled            bool   `yaml:"enabled,omitempty"`
	CertFile           string `yaml:"cert_file,omitempty"`
	KeyFile            string `yaml:"key_file,omitempty"`
	CAFile             string `yaml:"ca_file,omitempty"`
	ClientAuth         bool   `yaml:"client_auth,omitempty"` // requires mTLS client certificate verification
	ServerNameOverride string `yaml:"server_name_override,omitempty"`
}

// TelemetryConfig configures OpenTelemetry-compatible tracing and metrics export.
// CloudX operates completely standalone with zero external dependencies by default;
// OTLP export is optionally activated when OTLPEndpoint is set.
type TelemetryConfig struct {
	ServiceName    string `yaml:"service_name,omitempty"`
	OTLPEndpoint   string `yaml:"otlp_endpoint,omitempty"`   // e.g. "http://localhost:4318"
	BufferCapacity int    `yaml:"buffer_capacity,omitempty"` // in-memory span buffer (default 512)
	Disabled       bool   `yaml:"disabled,omitempty"`        // disables tracing
}

// NodeConfig specifies node identification and metadata.
type NodeConfig struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

// ControlPlaneConfig specifies control-plane connection properties.
type ControlPlaneConfig struct {
	Address        string    `yaml:"address"`
	ClusterID      string    `yaml:"cluster_id,omitempty"`
	BootstrapToken string    `yaml:"bootstrap_token,omitempty"`
	TLS            TLSConfig `yaml:"tls,omitempty"`
}

// WorkerConfig specifies worker endpoint parameters.
type WorkerConfig struct {
	Address        string    `yaml:"address"`
	ClusterID      string    `yaml:"cluster_id,omitempty"`
	BootstrapToken string    `yaml:"bootstrap_token,omitempty"`
	TLS            TLSConfig `yaml:"tls,omitempty"`
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
