package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CLIOptions encapsulates CLI flag overrides with high precedence.
type CLIOptions struct {
	ConfigPath        string
	NodeID            string
	NodeName          string
	ControlPlaneAddr  string
	WorkerAddr        string
	RuntimeType       string
	StoragePath       string
	HeartbeatInterval time.Duration
	LogLevel          string
}

// DiscoverConfigFile searches standard locations for a cloudx configuration file:
// 1. Explicit path (if passed)
// 2. ./cloudx.yaml or ./cloudx.yml in current directory
// 3. ~/.cloudx/cloudx.yaml or ~/.cloudx/cloudx.yml
func DiscoverConfigFile(explicitPath string) (string, bool) {
	if explicitPath != "" {
		if _, err := os.Stat(explicitPath); err == nil {
			return explicitPath, true
		}
		return explicitPath, false
	}

	// Current directory candidates
	cwdCandidates := []string{"cloudx.yaml", "cloudx.yml", "configs/cloudx.yaml", "configs/cloudx.yml"}
	for _, cand := range cwdCandidates {
		if _, err := os.Stat(cand); err == nil {
			return cand, true
		}
	}

	// User home directory candidates
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		homeCandidates := []string{
			filepath.Join(home, ".cloudx", "cloudx.yaml"),
			filepath.Join(home, ".cloudx", "cloudx.yml"),
		}
		for _, cand := range homeCandidates {
			if _, err := os.Stat(cand); err == nil {
				return cand, true
			}
		}
	}

	return "", false
}

// Load loads, parses, applies environment/CLI overrides, and validates configuration.
// Precedence order:
// 1. Built-in defaults
// 2. Configuration file (YAML)
// 3. Environment variables (CLOUDX_*)
// 4. CLI flags (via CLIOptions)
func Load(opts CLIOptions) (*Config, error) {
	// 1. Start with built-in defaults
	cfg := NewDefaultConfig()

	// 2. Load from YAML file if discovered or explicitly provided
	path, found := DiscoverConfigFile(opts.ConfigPath)
	if opts.ConfigPath != "" && !found {
		return nil, fmt.Errorf("explicit configuration file not found at: %s", opts.ConfigPath)
	}

	if found {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read configuration file %s: %w", path, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse YAML configuration %s: %w", path, err)
		}
	}

	// 3. Apply Environment Variable overrides (CLOUDX_*)
	applyEnvOverrides(cfg)

	// 4. Apply CLI Flag overrides
	applyCLIOverrides(cfg, opts)

	// 5. Semantic validation
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if val := os.Getenv("CLOUDX_NODE_ID"); val != "" {
		cfg.Node.ID = val
	}
	if val := os.Getenv("CLOUDX_NODE_NAME"); val != "" {
		cfg.Node.Name = val
	}
	if val := os.Getenv("CLOUDX_CONTROL_PLANE_ADDRESS"); val != "" {
		cfg.ControlPlane.Address = val
	}
	if val := os.Getenv("CLOUDX_WORKER_ADDRESS"); val != "" {
		cfg.Worker.Address = val
	}
	if val := os.Getenv("CLOUDX_RUNTIME_TYPE"); val != "" {
		cfg.Runtime.Type = val
	}
	if val := os.Getenv("CLOUDX_STORAGE_PATH"); val != "" {
		cfg.Storage.Path = val
	}
	if val := os.Getenv("CLOUDX_NETWORK_DEFAULT_NETWORK"); val != "" {
		cfg.Network.DefaultNetwork = val
	}
	if val := os.Getenv("CLOUDX_HEALTH_HEARTBEAT_INTERVAL"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.Health.HeartbeatInterval = d
		}
	}
	if val := os.Getenv("CLOUDX_LOGGING_LEVEL"); val != "" {
		cfg.Logging.Level = strings.ToLower(val)
	}
}

func applyCLIOverrides(cfg *Config, opts CLIOptions) {
	if opts.NodeID != "" {
		cfg.Node.ID = opts.NodeID
	}
	if opts.NodeName != "" {
		cfg.Node.Name = opts.NodeName
	}
	if opts.ControlPlaneAddr != "" {
		cfg.ControlPlane.Address = opts.ControlPlaneAddr
	}
	if opts.WorkerAddr != "" {
		cfg.Worker.Address = opts.WorkerAddr
	}
	if opts.RuntimeType != "" {
		cfg.Runtime.Type = opts.RuntimeType
	}
	if opts.StoragePath != "" {
		cfg.Storage.Path = opts.StoragePath
	}
	if opts.HeartbeatInterval > 0 {
		cfg.Health.HeartbeatInterval = opts.HeartbeatInterval
	}
	if opts.LogLevel != "" {
		cfg.Logging.Level = strings.ToLower(opts.LogLevel)
	}
}
