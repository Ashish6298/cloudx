package config

import (
	"os"
	"path/filepath"
	"time"
)

const (
	DefaultNodeID            = "local-node"
	DefaultNodeName          = "local"
	DefaultControlPlaneAddr  = "127.0.0.1:7000"
	DefaultWorkerAddr        = "127.0.0.1:7001"
	DefaultRuntimeType       = "native"
	DefaultNetworkName       = "default"
	DefaultHeartbeatInterval = 5 * time.Second
	DefaultLogLevel          = "info"
)

// DefaultStoragePath returns the platform-appropriate default storage path (~/.cloudx).
func DefaultStoragePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".cloudx")
	}
	return filepath.Join(home, ".cloudx")
}

// NewDefaultConfig returns a Config struct populated with all built-in defaults.
func NewDefaultConfig() *Config {
	return &Config{
		Node: NodeConfig{
			ID:   DefaultNodeID,
			Name: DefaultNodeName,
		},
		ControlPlane: ControlPlaneConfig{
			Address: DefaultControlPlaneAddr,
		},
		Worker: WorkerConfig{
			Address: DefaultWorkerAddr,
		},
		Runtime: RuntimeConfig{
			Type: DefaultRuntimeType,
		},
		Storage: StorageConfig{
			Path: DefaultStoragePath(),
		},
		Network: NetworkConfig{
			DefaultNetwork: DefaultNetworkName,
		},
		Health: HealthConfig{
			HeartbeatInterval: DefaultHeartbeatInterval,
		},
		Logging: LoggingConfig{
			Level: DefaultLogLevel,
		},
	}
}
