package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// JobConfigFile represents the top-level structure of a CloudX job manifest file.
type JobConfigFile struct {
	Version string                `yaml:"version,omitempty" json:"version,omitempty"`
	Jobs    map[string]*JobConfig `yaml:"jobs" json:"jobs"`
}

// JobRetrySpec defines the YAML specification for job retry behavior.
type JobRetrySpec struct {
	MaxRetries    int    `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`
	BackoffPeriod string `yaml:"backoff_period,omitempty" json:"backoff_period,omitempty"`
}

// JobConfig defines the declarative specification of a finite batch job workload.
type JobConfig struct {
	Name        string            `yaml:"name,omitempty" json:"name,omitempty"`
	Command     string            `yaml:"command" json:"command"`
	Args        []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty" json:"environment,omitempty"`
	WorkingDir  string            `yaml:"working_dir,omitempty" json:"working_dir,omitempty"`
	Runtime     string            `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Resources   ResourceConfig    `yaml:"resources,omitempty" json:"resources,omitempty"`
	RetryPolicy *JobRetrySpec     `yaml:"retry_policy,omitempty" json:"retry_policy,omitempty"`
	Timeout     string            `yaml:"timeout,omitempty" json:"timeout,omitempty"` // e.g. "5m", "30s", "1h"
}

// ParseJobConfigFile reads and parses a YAML job manifest file from disk.
func ParseJobConfigFile(path string) (*JobConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read job config file %s: %w", filepath.Clean(path), err)
	}
	return ParseJobConfig(data)
}

// ParseJobConfig unmarshals and normalizes YAML job manifest content.
func ParseJobConfig(data []byte) (*JobConfigFile, error) {
	var cfg JobConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML job manifest: %w", err)
	}

	if len(cfg.Jobs) == 0 {
		return nil, fmt.Errorf("manifest contains no jobs")
	}

	for name, j := range cfg.Jobs {
		if j == nil {
			return nil, fmt.Errorf("job '%s' configuration is empty", name)
		}
		if j.Name == "" {
			j.Name = name
		}
	}

	return &cfg, nil
}

// ParsedJobSettings contains strongly-typed and validated job configuration parameters.
type ParsedJobSettings struct {
	Resources    *ParsedResources
	Timeout      time.Duration
	RetryRetries int
	RetryBackoff time.Duration
}

// Validate performs in-depth validation and normalization on a JobConfig.
func (j *JobConfig) Validate() (*ParsedJobSettings, error) {
	var errs []string
	settings := &ParsedJobSettings{}

	// 1. Name validation
	if strings.TrimSpace(j.Name) == "" {
		errs = append(errs, "job name is required")
	} else if !validNameRegex.MatchString(j.Name) {
		errs = append(errs, fmt.Sprintf("invalid job name '%s': must consist of alphanumeric characters, '-', '_', or '.'", j.Name))
	}

	// 2. Command validation
	if strings.TrimSpace(j.Command) == "" {
		errs = append(errs, "job command is required")
	}

	// 3. Runtime validation
	if j.Runtime == "" {
		j.Runtime = "native"
	} else {
		rt := strings.ToLower(j.Runtime)
		if rt != "native" && rt != "docker" {
			errs = append(errs, fmt.Sprintf("invalid runtime '%s': must be 'native' or 'docker'", j.Runtime))
		}
	}

	// 4. Resources validation
	parsedRes, resErr := j.ParseResources()
	if resErr != nil {
		errs = append(errs, resErr.Error())
	}
	settings.Resources = parsedRes

	// 5. Retry Policy validation
	if j.RetryPolicy != nil {
		if j.RetryPolicy.MaxRetries < 0 {
			errs = append(errs, "retry_policy max_retries cannot be negative")
		} else {
			settings.RetryRetries = j.RetryPolicy.MaxRetries
		}

		if j.RetryPolicy.BackoffPeriod != "" {
			d, err := time.ParseDuration(j.RetryPolicy.BackoffPeriod)
			if err != nil || d < 0 {
				errs = append(errs, fmt.Sprintf("invalid retry_policy backoff_period '%s': %v", j.RetryPolicy.BackoffPeriod, err))
			} else {
				settings.RetryBackoff = d
			}
		}
	}

	// 6. Timeout validation
	if j.Timeout != "" {
		d, err := time.ParseDuration(j.Timeout)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Sprintf("invalid timeout '%s': must be a positive duration", j.Timeout))
		} else {
			settings.Timeout = d
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("job '%s' validation failed: %s", j.Name, strings.Join(errs, "; "))
	}

	return settings, nil
}

// ParseResources parses and normalizes CPU and Memory bounds for a JobConfig.
func (j *JobConfig) ParseResources() (*ParsedResources, error) {
	var parsed ParsedResources

	// 1. CPU parsing
	if j.Resources.CPU != nil {
		cpuVal, err := ParseCPU(j.Resources.CPU)
		if err != nil {
			return nil, fmt.Errorf("invalid cpu resource: %w", err)
		}
		parsed.CPUCores = cpuVal
	}

	// 2. Memory parsing
	if j.Resources.Memory != nil {
		memVal, err := ParseMemory(j.Resources.Memory)
		if err != nil {
			return nil, fmt.Errorf("invalid memory resource: %w", err)
		}
		parsed.MemoryBytes = memVal
	}

	return &parsed, nil
}
