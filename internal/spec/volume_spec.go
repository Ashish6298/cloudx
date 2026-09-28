package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// VolumeConfigFile represents the top-level structure of a CloudX volume manifest file.
type VolumeConfigFile struct {
	Version string                   `yaml:"version,omitempty" json:"version,omitempty"`
	Volumes map[string]*VolumeConfig `yaml:"volumes" json:"volumes"`
}

// VolumeConfig defines the declarative specification of a persistent storage volume.
type VolumeConfig struct {
	Name     string            `yaml:"name,omitempty" json:"name,omitempty"`
	Driver   string            `yaml:"driver,omitempty" json:"driver,omitempty"`     // "local" (default)
	Location string            `yaml:"location,omitempty" json:"location,omitempty"` // Directory path on worker/host
	Size     string            `yaml:"size,omitempty" json:"size,omitempty"`         // e.g. "10GB", "500MB"
	ReadOnly bool              `yaml:"read_only,omitempty" json:"read_only,omitempty"`
	OwnerRef string            `yaml:"owner_ref,omitempty" json:"owner_ref,omitempty"`
	WorkerID string            `yaml:"worker_id,omitempty" json:"worker_id,omitempty"`
	Labels   map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
}

// ParseVolumeConfigFile reads and parses a YAML volume manifest file from disk.
func ParseVolumeConfigFile(path string) (*VolumeConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read volume config file %s: %w", filepath.Clean(path), err)
	}
	return ParseVolumeConfig(data)
}

// ParseVolumeConfig unmarshals and normalizes YAML volume manifest content.
func ParseVolumeConfig(data []byte) (*VolumeConfigFile, error) {
	var cfg VolumeConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML volume manifest: %w", err)
	}

	if len(cfg.Volumes) == 0 {
		return nil, fmt.Errorf("manifest contains no volumes")
	}

	for name, v := range cfg.Volumes {
		if v == nil {
			return nil, fmt.Errorf("volume '%s' configuration is empty", name)
		}
		if v.Name == "" {
			v.Name = name
		}
	}

	return &cfg, nil
}

// ParsedVolumeSettings contains validated and parsed volume parameters.
type ParsedVolumeSettings struct {
	SizeBytes int64
	Driver    string
}

// Validate performs in-depth validation and normalization on a VolumeConfig.
func (v *VolumeConfig) Validate() (*ParsedVolumeSettings, error) {
	var errs []string
	settings := &ParsedVolumeSettings{
		Driver: "local",
	}

	// 1. Name validation
	if strings.TrimSpace(v.Name) == "" {
		errs = append(errs, "volume name is required")
	} else if !validNameRegex.MatchString(v.Name) {
		errs = append(errs, fmt.Sprintf("invalid volume name '%s': must consist of alphanumeric characters, '-', '_', or '.'", v.Name))
	}

	// 2. Driver validation
	if v.Driver != "" {
		d := strings.ToLower(v.Driver)
		if d != "local" && d != "host" {
			errs = append(errs, fmt.Sprintf("invalid volume driver '%s': must be 'local' or 'host'", v.Driver))
		} else {
			settings.Driver = d
		}
	}

	// 3. Location validation
	if strings.TrimSpace(v.Location) == "" {
		// Provide default location based on name
		v.Location = filepath.Join("volumes", v.Name)
	}

	// 4. Size validation
	if v.Size != "" {
		bytes, err := ParseMemoryString(v.Size)
		if err != nil {
			errs = append(errs, fmt.Sprintf("invalid volume size '%s': %v", v.Size, err))
		} else {
			settings.SizeBytes = bytes
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("volume '%s' validation failed: %s", v.Name, strings.Join(errs, "; "))
	}

	return settings, nil
}
