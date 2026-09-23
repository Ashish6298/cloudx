package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfiguration(t *testing.T) {
	cfg := NewDefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default configuration failed validation: %v", err)
	}

	if cfg.Node.ID != DefaultNodeID {
		t.Errorf("expected node.id %q, got %q", DefaultNodeID, cfg.Node.ID)
	}
	if cfg.ControlPlane.Address != DefaultControlPlaneAddr {
		t.Errorf("expected control_plane.address %q, got %q", DefaultControlPlaneAddr, cfg.ControlPlane.Address)
	}
	if cfg.Runtime.Type != "native" {
		t.Errorf("expected runtime.type 'native', got %q", cfg.Runtime.Type)
	}
}

func TestValidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "valid.yaml")
	yamlContent := `
node:
  id: custom-node-1
  name: node-one
control_plane:
  address: 192.168.1.50:8000
worker:
  address: 192.168.1.51:8001
runtime:
  type: native
storage:
  path: /tmp/cloudx-storage
health:
  heartbeat_interval: 10s
logging:
  level: debug
`
	if err := os.WriteFile(confPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := Load(CLIOptions{ConfigPath: confPath})
	if err != nil {
		t.Fatalf("expected valid config to load, got error: %v", err)
	}

	if cfg.Node.ID != "custom-node-1" {
		t.Errorf("expected node.id 'custom-node-1', got %q", cfg.Node.ID)
	}
	if cfg.ControlPlane.Address != "192.168.1.50:8000" {
		t.Errorf("expected control_plane.address '192.168.1.50:8000', got %q", cfg.ControlPlane.Address)
	}
	if cfg.Health.HeartbeatInterval != 10*time.Second {
		t.Errorf("expected heartbeat_interval 10s, got %v", cfg.Health.HeartbeatInterval)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("expected logging.level 'debug', got %q", cfg.Logging.Level)
	}
}

func TestInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "invalid.yaml")
	yamlContent := `
node:
  id: [unclosed list
`
	if err := os.WriteFile(confPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := Load(CLIOptions{ConfigPath: confPath})
	if err == nil {
		t.Fatalf("expected error for invalid YAML, got nil")
	}
}

func TestMissingFields(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Node.ID = ""
	cfg.Node.Name = ""
	cfg.Storage.Path = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected validation error for empty fields, got nil")
	}

	ve, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors type, got %T", err)
	}

	foundNodeID := false
	foundStorage := false
	for _, e := range ve {
		if e.Field == "node.id" {
			foundNodeID = true
		}
		if e.Field == "storage.path" {
			foundStorage = true
		}
	}
	if !foundNodeID || !foundStorage {
		t.Errorf("expected errors on node.id and storage.path, got %v", ve)
	}
}

func TestInvalidPorts(t *testing.T) {
	tests := []string{
		"127.0.0.1",       // Missing port
		"127.0.0.1:99999", // Port too high
		"127.0.0.1:-1",    // Negative port
		"127.0.0.1:abc",   // Non-numeric port
		":7000",           // Missing host
	}

	for _, addr := range tests {
		cfg := NewDefaultConfig()
		cfg.ControlPlane.Address = addr
		err := cfg.Validate()
		if err == nil {
			t.Errorf("expected error for invalid address %q, got nil", addr)
		}
	}
}

func TestInvalidDurations(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Health.HeartbeatInterval = -5 * time.Second
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for negative heartbeat interval, got nil")
	}

	cfg.Health.HeartbeatInterval = 10 * time.Millisecond // too low
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for sub-100ms heartbeat interval, got nil")
	}
}

func TestEnvironmentOverride(t *testing.T) {
	t.Setenv("CLOUDX_NODE_ID", "env-node-99")
	t.Setenv("CLOUDX_CONTROL_PLANE_ADDRESS", "10.0.0.1:9000")
	t.Setenv("CLOUDX_LOGGING_LEVEL", "warn")

	cfg, err := Load(CLIOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Node.ID != "env-node-99" {
		t.Errorf("expected node.id 'env-node-99', got %q", cfg.Node.ID)
	}
	if cfg.ControlPlane.Address != "10.0.0.1:9000" {
		t.Errorf("expected control_plane.address '10.0.0.1:9000', got %q", cfg.ControlPlane.Address)
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("expected logging.level 'warn', got %q", cfg.Logging.Level)
	}
}

func TestCLIOverridePrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "override.yaml")
	yamlContent := `
node:
  id: yaml-node
control_plane:
  address: 127.0.0.1:7000
logging:
  level: info
`
	if err := os.WriteFile(confPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Set Env (which should override YAML, but be overridden by CLI)
	t.Setenv("CLOUDX_NODE_ID", "env-node")
	t.Setenv("CLOUDX_LOGGING_LEVEL", "warn")

	// CLI options should take top precedence
	opts := CLIOptions{
		ConfigPath:       confPath,
		NodeID:           "cli-node-winner",
		ControlPlaneAddr: "127.0.0.1:7777",
		LogLevel:         "error",
	}

	cfg, err := Load(opts)
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}

	// 1. CLI wins over Env & YAML
	if cfg.Node.ID != "cli-node-winner" {
		t.Errorf("expected CLI node.id 'cli-node-winner', got %q", cfg.Node.ID)
	}
	if cfg.ControlPlane.Address != "127.0.0.1:7777" {
		t.Errorf("expected CLI address '127.0.0.1:7777', got %q", cfg.ControlPlane.Address)
	}
	if cfg.Logging.Level != "error" {
		t.Errorf("expected CLI logging level 'error', got %q", cfg.Logging.Level)
	}
}

func TestExplicitConfigNotFound(t *testing.T) {
	_, err := Load(CLIOptions{ConfigPath: "non-existent-config-file.yaml"})
	if err == nil {
		t.Fatalf("expected error for non-existent explicit config path, got nil")
	}
	if !strings.Contains(err.Error(), "explicit configuration file not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}
