package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][-a-zA-Z0-9_.]*$`)

// ServiceConfigFile represents the top-level structure of a CloudX service manifest file.
type ServiceConfigFile struct {
	Version  string                    `yaml:"version,omitempty" json:"version,omitempty"`
	Services map[string]*ServiceConfig `yaml:"services" json:"services"`
}

// ServiceConfig defines the declarative specification of a single service workload.
type ServiceConfig struct {
	Name           string                `yaml:"name,omitempty" json:"name,omitempty"`
	Version        string                `yaml:"version,omitempty" json:"version,omitempty"`
	Artifact       string                `yaml:"artifact,omitempty" json:"artifact,omitempty"`
	Command        string                `yaml:"command" json:"command"`
	Args           []string              `yaml:"args,omitempty" json:"args,omitempty"`
	Environment    map[string]string     `yaml:"environment,omitempty" json:"environment,omitempty"`
	WorkingDir     string                `yaml:"working_dir,omitempty" json:"working_dir,omitempty"`
	Replicas       *int                  `yaml:"replicas,omitempty" json:"replicas,omitempty"`
	Runtime        string                `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Resources      ResourceConfig        `yaml:"resources,omitempty" json:"resources,omitempty"`
	RestartPolicy  *RestartPolicySpec    `yaml:"restart_policy,omitempty" json:"restart_policy,omitempty"`
	HealthCheck    *HealthCheckConfig    `yaml:"health_check,omitempty" json:"health_check,omitempty"`
	UpdateStrategy *UpdateStrategyConfig `yaml:"update_strategy,omitempty" json:"update_strategy,omitempty"`
	Ports          []PortSpec            `yaml:"ports,omitempty" json:"ports,omitempty"`
	Volumes        []VolumeSpec          `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	Networks       []string              `yaml:"networks,omitempty" json:"networks,omitempty"`
}

// UpdateStrategyConfig defines parameters for rollout strategies.
type UpdateStrategyConfig struct {
	Type           string `yaml:"type,omitempty" json:"type,omitempty"` // "rolling", "recreate"
	MaxUnavailable int    `yaml:"max_unavailable,omitempty" json:"max_unavailable,omitempty"`
	MaxSurge       int    `yaml:"max_surge,omitempty" json:"max_surge,omitempty"`
}

// ResourceConfig defines the CPU and Memory bounds for a service.
type ResourceConfig struct {
	CPU    any `yaml:"cpu,omitempty" json:"cpu,omitempty"`       // string "500m", "1.5", or float/int 1, 2.0
	Memory any `yaml:"memory,omitempty" json:"memory,omitempty"` // string "512MB", "1GB", "256MiB", or int bytes
}

// ParsedResources holds strongly-typed and normalized resource requirements.
type ParsedResources struct {
	CPUCores    float64 `json:"cpu_cores"`
	MemoryBytes int64   `json:"memory_bytes"`
}

// RestartPolicySpec defines the restart behavior.
type RestartPolicySpec struct {
	Type          string `yaml:"type" json:"type"` // "always", "on-failure", "never"
	MaxRetries    int    `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`
	BackoffPeriod string `yaml:"backoff_period,omitempty" json:"backoff_period,omitempty"`
}

// HealthCheckConfig defines health probe settings.
type HealthCheckConfig struct {
	Type             string `yaml:"type,omitempty" json:"type,omitempty"` // "process", "tcp", "http"
	Path             string `yaml:"path,omitempty" json:"path,omitempty"`
	Port             int    `yaml:"port,omitempty" json:"port,omitempty"`
	Interval         string `yaml:"interval,omitempty" json:"interval,omitempty"`
	Timeout          string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	FailureThreshold int    `yaml:"failure_threshold,omitempty" json:"failure_threshold,omitempty"`
	SuccessThreshold int    `yaml:"success_threshold,omitempty" json:"success_threshold,omitempty"`
}

// PortSpec represents port mappings (supports integer 8080, or string "8080", "8080:8080", "8080:80/tcp").
type PortSpec struct {
	HostPort    int    `json:"host_port"`
	ServicePort int    `json:"service_port"`
	Protocol    string `json:"protocol"` // "tcp", "udp"
}

// UnmarshalYAML custom unmarshaler to support integer ports like `8080` or mapping strings.
func (p *PortSpec) UnmarshalYAML(value *yaml.Node) error {
	var intVal int
	if err := value.Decode(&intVal); err == nil {
		p.HostPort = intVal
		p.ServicePort = intVal
		p.Protocol = "tcp"
		return nil
	}

	var strVal string
	if err := value.Decode(&strVal); err == nil {
		parsed, err := ParsePortString(strVal)
		if err != nil {
			return err
		}
		*p = parsed
		return nil
	}

	type rawPort PortSpec
	var raw rawPort
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*p = PortSpec(raw)
	return nil
}

// VolumeSpec defines a volume attachment (supports string "/host/data:/app/data:ro" or struct).
type VolumeSpec struct {
	VolumeName string `yaml:"name,omitempty" json:"name,omitempty"`
	Source     string `yaml:"source,omitempty" json:"source,omitempty"`
	Target     string `yaml:"target,omitempty" json:"target,omitempty"`
	ReadOnly   bool   `yaml:"read_only,omitempty" json:"read_only,omitempty"`
}

// UnmarshalYAML custom unmarshaler for string volume mounts or structured objects.
func (v *VolumeSpec) UnmarshalYAML(value *yaml.Node) error {
	var strVal string
	if err := value.Decode(&strVal); err == nil {
		parsed, err := ParseVolumeString(strVal)
		if err != nil {
			return err
		}
		*v = parsed
		return nil
	}

	type rawVolume VolumeSpec
	var raw rawVolume
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*v = VolumeSpec(raw)
	return nil
}

// ParseConfigFile reads and parses a YAML file from disk.
func ParseConfigFile(path string) (*ServiceConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read service config file %s: %w", filepath.Clean(path), err)
	}
	return ParseConfig(data)
}

// ParseConfig unmarshals and normalizes YAML manifest content.
func ParseConfig(data []byte) (*ServiceConfigFile, error) {
	var cfg ServiceConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML manifest: %w", err)
	}

	if len(cfg.Services) == 0 {
		return nil, fmt.Errorf("manifest contains no services")
	}

	for name, svc := range cfg.Services {
		if svc == nil {
			return nil, fmt.Errorf("service '%s' configuration is empty", name)
		}
		if svc.Name == "" {
			svc.Name = name
		}
	}

	return &cfg, nil
}

// ValidateService performs in-depth validation and normalization on a ServiceConfig.
func (s *ServiceConfig) Validate() (*ParsedResources, error) {
	var errs []string

	// 1. Name validation
	if strings.TrimSpace(s.Name) == "" {
		errs = append(errs, "service name is required")
	} else if !validNameRegex.MatchString(s.Name) {
		errs = append(errs, fmt.Sprintf("invalid service name '%s': must consist of alphanumeric characters, '-', '_', or '.'", s.Name))
	}

	// 2. Command validation
	if strings.TrimSpace(s.Command) == "" {
		errs = append(errs, "command is required")
	}

	// 3. Replicas validation
	if s.Replicas != nil && *s.Replicas < 0 {
		errs = append(errs, fmt.Sprintf("replicas cannot be negative (got %d)", *s.Replicas))
	}

	// 4. Runtime validation
	if s.Runtime == "" {
		s.Runtime = "native"
	} else {
		rt := strings.ToLower(s.Runtime)
		if rt != "native" && rt != "docker" {
			errs = append(errs, fmt.Sprintf("invalid runtime '%s': must be 'native' or 'docker'", s.Runtime))
		}
	}

	// 5. Resources validation
	parsedRes, resErr := s.ParseResources()
	if resErr != nil {
		errs = append(errs, resErr.Error())
	}

	// 6. Restart Policy validation
	if s.RestartPolicy != nil {
		if s.RestartPolicy.Type == "" {
			s.RestartPolicy.Type = "always"
		} else {
			pt := strings.ToLower(s.RestartPolicy.Type)
			if pt != "always" && pt != "on-failure" && pt != "never" {
				errs = append(errs, fmt.Sprintf("invalid restart_policy type '%s': must be 'always', 'on-failure', or 'never'", s.RestartPolicy.Type))
			}
		}
		if s.RestartPolicy.MaxRetries < 0 {
			errs = append(errs, "restart_policy max_retries cannot be negative")
		}
		if s.RestartPolicy.BackoffPeriod != "" {
			if _, err := time.ParseDuration(s.RestartPolicy.BackoffPeriod); err != nil {
				errs = append(errs, fmt.Sprintf("invalid restart_policy backoff_period '%s': %v", s.RestartPolicy.BackoffPeriod, err))
			}
		}
	}

	// 7. Health Check validation
	if s.HealthCheck != nil {
		if s.HealthCheck.Type == "" {
			s.HealthCheck.Type = "process"
		} else {
			ht := strings.ToLower(s.HealthCheck.Type)
			if ht != "process" && ht != "tcp" && ht != "http" {
				errs = append(errs, fmt.Sprintf("invalid health_check type '%s': must be 'process', 'tcp', or 'http'", s.HealthCheck.Type))
			}
		}
		if s.HealthCheck.Interval != "" {
			d, err := time.ParseDuration(s.HealthCheck.Interval)
			if err != nil {
				errs = append(errs, fmt.Sprintf("invalid health_check interval '%s': %v", s.HealthCheck.Interval, err))
			} else if d < 100*time.Millisecond {
				errs = append(errs, "health_check interval must be at least 100ms")
			}
		}
		if s.HealthCheck.Timeout != "" {
			d, err := time.ParseDuration(s.HealthCheck.Timeout)
			if err != nil {
				errs = append(errs, fmt.Sprintf("invalid health_check timeout '%s': %v", s.HealthCheck.Timeout, err))
			} else if d <= 0 {
				errs = append(errs, "health_check timeout must be positive")
			}
		}
		if s.HealthCheck.Port < 0 || s.HealthCheck.Port > 65535 {
			errs = append(errs, fmt.Sprintf("invalid health_check port %d", s.HealthCheck.Port))
		}
		if s.HealthCheck.FailureThreshold < 0 {
			errs = append(errs, "health_check failure_threshold cannot be negative")
		}
		if s.HealthCheck.SuccessThreshold < 0 {
			errs = append(errs, "health_check success_threshold cannot be negative")
		}
	}

	// 8. Update Strategy validation
	if s.UpdateStrategy != nil {
		if s.UpdateStrategy.Type == "" {
			s.UpdateStrategy.Type = "rolling"
		} else {
			st := strings.ToLower(s.UpdateStrategy.Type)
			if st != "rolling" && st != "recreate" {
				errs = append(errs, fmt.Sprintf("invalid update_strategy type '%s': must be 'rolling' or 'recreate'", s.UpdateStrategy.Type))
			}
		}
		if s.UpdateStrategy.MaxUnavailable < 0 {
			errs = append(errs, "update_strategy max_unavailable cannot be negative")
		}
		if s.UpdateStrategy.MaxSurge < 0 {
			errs = append(errs, "update_strategy max_surge cannot be negative")
		}
	}

	// 9. Ports validation
	seenHostPorts := make(map[string]bool)
	seenServicePorts := make(map[string]bool)
	for i, p := range s.Ports {
		if p.HostPort < 1 || p.HostPort > 65535 {
			errs = append(errs, fmt.Sprintf("ports[%d]: invalid host port %d (must be between 1 and 65535)", i, p.HostPort))
		}
		if p.ServicePort < 1 || p.ServicePort > 65535 {
			errs = append(errs, fmt.Sprintf("ports[%d]: invalid service port %d (must be between 1 and 65535)", i, p.ServicePort))
		}
		proto := strings.ToLower(p.Protocol)
		if proto == "" {
			proto = "tcp"
		} else if proto != "tcp" && proto != "udp" {
			errs = append(errs, fmt.Sprintf("ports[%d]: invalid protocol '%s' (must be 'tcp' or 'udp')", i, p.Protocol))
		}

		hostKey := fmt.Sprintf("%d/%s", p.HostPort, proto)
		if seenHostPorts[hostKey] {
			errs = append(errs, fmt.Sprintf("ports[%d]: duplicate host port %d/%s in service spec", i, p.HostPort, proto))
		}
		seenHostPorts[hostKey] = true

		svcKey := fmt.Sprintf("%d/%s", p.ServicePort, proto)
		if seenServicePorts[svcKey] {
			errs = append(errs, fmt.Sprintf("ports[%d]: duplicate service port %d/%s in service spec", i, p.ServicePort, proto))
		}
		seenServicePorts[svcKey] = true
	}

	// 10. Volumes validation
	for i, v := range s.Volumes {
		if v.Target == "" && v.VolumeName == "" {
			errs = append(errs, fmt.Sprintf("volumes[%d]: target mount path or volume name is required", i))
		}
	}

	// 11. Networks validation
	for i, netName := range s.Networks {
		trimmed := strings.TrimSpace(netName)
		if trimmed == "" {
			errs = append(errs, fmt.Sprintf("networks[%d]: network name cannot be empty", i))
		} else if !validNameRegex.MatchString(trimmed) {
			errs = append(errs, fmt.Sprintf("networks[%d]: invalid network name '%s': must consist of alphanumeric characters, '-', '_', or '.'", i, trimmed))
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("service '%s' validation failed: %s", s.Name, strings.Join(errs, "; "))
	}

	return parsedRes, nil
}

// ParseResources parses and normalizes CPU and Memory strings/numbers.
func (s *ServiceConfig) ParseResources() (*ParsedResources, error) {
	var parsed ParsedResources

	// 1. CPU parsing
	if s.Resources.CPU != nil {
		cpuVal, err := ParseCPU(s.Resources.CPU)
		if err != nil {
			return nil, fmt.Errorf("invalid cpu resource: %w", err)
		}
		parsed.CPUCores = cpuVal
	}

	// 2. Memory parsing
	if s.Resources.Memory != nil {
		memVal, err := ParseMemory(s.Resources.Memory)
		if err != nil {
			return nil, fmt.Errorf("invalid memory resource: %w", err)
		}
		parsed.MemoryBytes = memVal
	}

	return &parsed, nil
}

// ParseCPU parses CPU values like 1, 0.5, "1.5", "500m", "2000m".
func ParseCPU(val any) (float64, error) {
	switch v := val.(type) {
	case int:
		if v < 0 {
			return 0, fmt.Errorf("cpu cannot be negative")
		}
		return float64(v), nil
	case float64:
		if v < 0 {
			return 0, fmt.Errorf("cpu cannot be negative")
		}
		return v, nil
	case string:
		str := strings.TrimSpace(v)
		if strings.HasSuffix(str, "m") {
			milliStr := strings.TrimSuffix(str, "m")
			milli, err := strconv.ParseFloat(milliStr, 64)
			if err != nil || milli < 0 {
				return 0, fmt.Errorf("invalid millicores '%s'", str)
			}
			return milli / 1000.0, nil
		}
		flt, err := strconv.ParseFloat(str, 64)
		if err != nil || flt < 0 {
			return 0, fmt.Errorf("invalid cpu format '%s'", str)
		}
		return flt, nil
	default:
		return 0, fmt.Errorf("unsupported cpu type %T", val)
	}
}

// ParseMemory parses Memory values like 536870912, "512MB", "1GB", "256MiB", "1024k".
func ParseMemory(val any) (int64, error) {
	switch v := val.(type) {
	case int:
		if v < 0 {
			return 0, fmt.Errorf("memory cannot be negative")
		}
		return int64(v), nil
	case float64:
		if v < 0 {
			return 0, fmt.Errorf("memory cannot be negative")
		}
		return int64(v), nil
	case string:
		return ParseMemoryString(v)
	default:
		return 0, fmt.Errorf("unsupported memory type %T", val)
	}
}

// ParseMemoryString parses unit-suffixed memory strings (e.g. 512MB, 1GB, 256MiB).
func ParseMemoryString(s string) (int64, error) {
	str := strings.TrimSpace(s)
	if str == "" {
		return 0, fmt.Errorf("empty memory string")
	}

	upper := strings.ToUpper(str)
	units := []struct {
		suffix string
		mult   int64
	}{
		{"GIB", 1024 * 1024 * 1024},
		{"GB", 1000 * 1000 * 1000},
		{"MIB", 1024 * 1024},
		{"MB", 1000 * 1000},
		{"KIB", 1024},
		{"KB", 1000},
		{"B", 1},
		{"G", 1024 * 1024 * 1024},
		{"M", 1024 * 1024},
		{"K", 1024},
	}

	for _, u := range units {
		if strings.HasSuffix(upper, u.suffix) {
			numStr := strings.TrimSpace(upper[:len(upper)-len(u.suffix)])
			flt, err := strconv.ParseFloat(numStr, 64)
			if err != nil || flt < 0 {
				return 0, fmt.Errorf("invalid memory number '%s'", numStr)
			}
			return int64(flt * float64(u.mult)), nil
		}
	}

	// Plain integer bytes
	num, err := strconv.ParseInt(str, 10, 64)
	if err != nil || num < 0 {
		return 0, fmt.Errorf("invalid memory byte value '%s'", str)
	}
	return num, nil
}

// ParsePortString parses port strings like "8080", "8080:80", "8080:80/tcp".
func ParsePortString(s string) (PortSpec, error) {
	str := strings.TrimSpace(s)
	protocol := "tcp"

	if idx := strings.Index(str, "/"); idx != -1 {
		protocol = strings.ToLower(str[idx+1:])
		str = str[:idx]
	}

	parts := strings.Split(str, ":")
	if len(parts) == 1 {
		p, err := strconv.Atoi(parts[0])
		if err != nil {
			return PortSpec{}, fmt.Errorf("invalid port '%s'", str)
		}
		return PortSpec{HostPort: p, ServicePort: p, Protocol: protocol}, nil
	} else if len(parts) == 2 {
		hp, err1 := strconv.Atoi(parts[0])
		sp, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return PortSpec{}, fmt.Errorf("invalid port mapping '%s'", str)
		}
		return PortSpec{HostPort: hp, ServicePort: sp, Protocol: protocol}, nil
	}

	return PortSpec{}, fmt.Errorf("invalid port spec format '%s'", s)
}

// ParseVolumeString parses volume strings like "/host/path:/container/path", "/data:/data:ro".
func ParseVolumeString(s string) (VolumeSpec, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return VolumeSpec{Source: s, Target: s}, nil
	}

	spec := VolumeSpec{
		Source: parts[0],
		Target: parts[1],
	}

	if len(parts) >= 3 {
		if strings.ToLower(parts[2]) == "ro" {
			spec.ReadOnly = true
		}
	}

	return spec, nil
}
