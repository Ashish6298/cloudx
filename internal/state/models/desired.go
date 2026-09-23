package models

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// RestartPolicyType defines supported restart policies.
type RestartPolicyType string

const (
	RestartPolicyNever     RestartPolicyType = "never"
	RestartPolicyOnFailure RestartPolicyType = "on-failure"
	RestartPolicyAlways    RestartPolicyType = "always"
)

// HealthCheckType defines supported health check probe types.
type HealthCheckType string

const (
	HealthCheckProcess HealthCheckType = "process"
	HealthCheckTCP     HealthCheckType = "tcp"
	HealthCheckHTTP    HealthCheckType = "http"
)

// ResourceRequirements specifies requested/allocated compute resources.
type ResourceRequirements struct {
	CPU    float64 `json:"cpu" yaml:"cpu"`       // e.g. 0.5, 1.0, 2.0 cores
	Memory int64   `json:"memory" yaml:"memory"` // memory in bytes (e.g. 512MB = 536870912)
}

// RestartPolicy defines process restart behavior.
type RestartPolicy struct {
	Type          RestartPolicyType `json:"type" yaml:"type"`
	MaxRetries    int               `json:"max_retries,omitempty" yaml:"max_retries,omitempty"`
	BackoffPeriod time.Duration     `json:"backoff_period,omitempty" yaml:"backoff_period,omitempty"`
}

// HealthCheckSpec defines health probe criteria.
type HealthCheckSpec struct {
	Type             HealthCheckType `json:"type" yaml:"type"`
	Path             string          `json:"path,omitempty" yaml:"path,omitempty"`
	Port             int             `json:"port,omitempty" yaml:"port,omitempty"`
	Interval         time.Duration   `json:"interval" yaml:"interval"`
	Timeout          time.Duration   `json:"timeout" yaml:"timeout"`
	FailureThreshold int             `json:"failure_threshold" yaml:"failure_threshold"`
	SuccessThreshold int             `json:"success_threshold" yaml:"success_threshold"`
}

// PortMapping defines host to container/service port mapping.
type PortMapping struct {
	HostPort    int    `json:"host_port" yaml:"host_port"`
	ServicePort int    `json:"service_port" yaml:"service_port"`
	Protocol    string `json:"protocol" yaml:"protocol"` // "tcp", "udp"
}

// VolumeMount defines a volume attachment.
type VolumeMount struct {
	VolumeName string `json:"volume_name" yaml:"volume_name"`
	MountPath  string `json:"mount_path" yaml:"mount_path"`
	ReadOnly   bool   `json:"read_only" yaml:"read_only"`
}

// ServiceDesiredState represents what a service SHOULD look like in the cluster.
type ServiceDesiredState struct {
	ID            id.ID                `json:"id" yaml:"id"`
	Name          string               `json:"name" yaml:"name"`
	Version       string               `json:"version" yaml:"version"`
	Replicas      int                  `json:"replicas" yaml:"replicas"`
	Runtime       string               `json:"runtime" yaml:"runtime"`
	Command       string               `json:"command" yaml:"command"`
	Args          []string             `json:"args,omitempty" yaml:"args,omitempty"`
	Environment   map[string]string    `json:"environment,omitempty" yaml:"environment,omitempty"`
	Ports         []PortMapping        `json:"ports,omitempty" yaml:"ports,omitempty"`
	Resources     ResourceRequirements `json:"resources" yaml:"resources"`
	RestartPolicy RestartPolicy        `json:"restart_policy" yaml:"restart_policy"`
	HealthCheck   *HealthCheckSpec     `json:"health_check,omitempty" yaml:"health_check,omitempty"`
	Volumes       []VolumeMount        `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	CreatedAt     time.Time            `json:"created_at" yaml:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at" yaml:"updated_at"`
}

// Validate performs strict validation on ServiceDesiredState.
func (s *ServiceDesiredState) Validate() error {
	var errs []string

	if strings.TrimSpace(s.Name) == "" {
		errs = append(errs, "name must not be empty")
	}
	if strings.TrimSpace(s.Version) == "" {
		errs = append(errs, "version must not be empty")
	}
	if s.Replicas < 0 {
		errs = append(errs, "replicas cannot be negative")
	}
	if strings.TrimSpace(s.Command) == "" {
		errs = append(errs, "command must not be empty")
	}
	runtimeType := strings.ToLower(s.Runtime)
	if runtimeType == "" {
		s.Runtime = "native"
	} else if runtimeType != "native" && runtimeType != "docker" {
		errs = append(errs, fmt.Sprintf("invalid runtime '%s', must be 'native' or 'docker'", s.Runtime))
	}

	if s.RestartPolicy.Type != "" {
		policy := strings.ToLower(string(s.RestartPolicy.Type))
		if policy != "never" && policy != "on-failure" && policy != "always" {
			errs = append(errs, fmt.Sprintf("invalid restart policy '%s'", s.RestartPolicy.Type))
		}
	} else {
		s.RestartPolicy.Type = RestartPolicyAlways
	}

	for i, p := range s.Ports {
		if p.HostPort < 1 || p.HostPort > 65535 {
			errs = append(errs, fmt.Sprintf("port[%d]: invalid host port %d", i, p.HostPort))
		}
		if p.ServicePort < 1 || p.ServicePort > 65535 {
			errs = append(errs, fmt.Sprintf("port[%d]: invalid service port %d", i, p.ServicePort))
		}
	}

	if s.HealthCheck != nil {
		hcType := strings.ToLower(string(s.HealthCheck.Type))
		if hcType != "process" && hcType != "tcp" && hcType != "http" {
			errs = append(errs, fmt.Sprintf("invalid health check type '%s'", s.HealthCheck.Type))
		}
		if s.HealthCheck.Interval < 100*time.Millisecond {
			errs = append(errs, "health check interval must be at least 100ms")
		}
		if s.HealthCheck.Timeout <= 0 {
			errs = append(errs, "health check timeout must be positive")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid desired state: %s", strings.Join(errs, "; "))
	}
	return nil
}

// ToJSON serializes the desired state to a JSON string.
func (s *ServiceDesiredState) ToJSON() (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// FromJSON deserializes a JSON string into ServiceDesiredState.
func FromJSON(data string) (*ServiceDesiredState, error) {
	var s ServiceDesiredState
	if err := json.Unmarshal([]byte(data), &s); err != nil {
		return nil, err
	}
	return &s, nil
}
