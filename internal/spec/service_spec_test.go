package spec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfig_FullExampleYAML(t *testing.T) {
	manifest := `
version: "v1"
services:
  api:
    command: ./api
    args: ["--port", "8080"]
    environment:
      ENV: "production"
      PORT: "8080"
    working_dir: /app
    replicas: 3
    runtime: native
    resources:
      cpu: 1
      memory: 512MB
    ports:
      - 8080
      - "9090:9090/tcp"
    restart_policy:
      type: always
      max_retries: 5
      backoff_period: "5s"
    health_check:
      type: http
      path: /healthz
      port: 8080
      interval: "10s"
      timeout: "2s"
      failure_threshold: 3
    volumes:
      - "/host/data:/app/data:ro"
      - name: shared-vol
        target: /app/shared
`

	cfg, err := ParseConfig([]byte(manifest))
	if err != nil {
		t.Fatalf("failed to parse manifest: %v", err)
	}

	if len(cfg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(cfg.Services))
	}

	apiSvc, ok := cfg.Services["api"]
	if !ok {
		t.Fatalf("expected service 'api' to exist")
	}

	if apiSvc.Command != "./api" {
		t.Errorf("expected command './api', got '%s'", apiSvc.Command)
	}
	if apiSvc.Replicas == nil || *apiSvc.Replicas != 3 {
		t.Errorf("expected replicas 3, got %v", apiSvc.Replicas)
	}

	res, err := apiSvc.Validate()
	if err != nil {
		t.Fatalf("service validation failed: %v", err)
	}

	if res.CPUCores != 1.0 {
		t.Errorf("expected 1.0 CPU cores, got %f", res.CPUCores)
	}
	// 512MB = 512 * 1000 * 1000 = 512000000 bytes
	if res.MemoryBytes != 512000000 {
		t.Errorf("expected 512000000 bytes, got %d", res.MemoryBytes)
	}

	if len(apiSvc.Ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(apiSvc.Ports))
	}
	if apiSvc.Ports[0].HostPort != 8080 || apiSvc.Ports[0].ServicePort != 8080 {
		t.Errorf("unexpected port 0: %+v", apiSvc.Ports[0])
	}
	if apiSvc.Ports[1].HostPort != 9090 || apiSvc.Ports[1].Protocol != "tcp" {
		t.Errorf("unexpected port 1: %+v", apiSvc.Ports[1])
	}

	if len(apiSvc.Volumes) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(apiSvc.Volumes))
	}
	if !apiSvc.Volumes[0].ReadOnly || apiSvc.Volumes[0].Target != "/app/data" {
		t.Errorf("unexpected volume 0: %+v", apiSvc.Volumes[0])
	}
}

func TestParseConfigFile_FromDisk(t *testing.T) {
	manifest := `
services:
  web:
    command: python3 -m http.server 8000
    replicas: 2
    resources:
      cpu: 500m
      memory: 256MiB
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "cloudx-service.yaml")
	if err := os.WriteFile(filePath, []byte(manifest), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	cfg, err := ParseConfigFile(filePath)
	if err != nil {
		t.Fatalf("failed to parse file: %v", err)
	}

	webSvc, ok := cfg.Services["web"]
	if !ok {
		t.Fatalf("expected service 'web'")
	}

	res, err := webSvc.Validate()
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	if res.CPUCores != 0.5 {
		t.Errorf("expected 0.5 CPU cores from 500m, got %f", res.CPUCores)
	}
	if res.MemoryBytes != 256*1024*1024 {
		t.Errorf("expected 256MiB in bytes (%d), got %d", 256*1024*1024, res.MemoryBytes)
	}
}

func TestServiceConfig_ValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantErr  bool
		errMatch string
	}{
		{
			name: "empty command",
			svc: ServiceConfig{
				Name:    "api",
				Command: "",
			},
			wantErr:  true,
			errMatch: "command is required",
		},
		{
			name: "negative replicas",
			svc: func() ServiceConfig {
				neg := -1
				return ServiceConfig{
					Name:     "api",
					Command:  "./api",
					Replicas: &neg,
				}
			}(),
			wantErr:  true,
			errMatch: "replicas cannot be negative",
		},
		{
			name: "invalid runtime",
			svc: ServiceConfig{
				Name:    "api",
				Command: "./api",
				Runtime: "kubernetes-wasm",
			},
			wantErr:  true,
			errMatch: "invalid runtime",
		},
		{
			name: "invalid restart policy",
			svc: ServiceConfig{
				Name:    "api",
				Command: "./api",
				RestartPolicy: &RestartPolicySpec{
					Type: "sometimes",
				},
			},
			wantErr:  true,
			errMatch: "invalid restart_policy type",
		},
		{
			name: "invalid health check interval too low",
			svc: ServiceConfig{
				Name:    "api",
				Command: "./api",
				HealthCheck: &HealthCheckConfig{
					Interval: "10ms",
				},
			},
			wantErr:  true,
			errMatch: "health_check interval must be at least 100ms",
		},
		{
			name: "invalid port range",
			svc: ServiceConfig{
				Name:    "api",
				Command: "./api",
				Ports: []PortSpec{
					{HostPort: 70000, ServicePort: 80},
				},
			},
			wantErr:  true,
			errMatch: "invalid host port 70000",
		},
		{
			name: "invalid cpu format",
			svc: ServiceConfig{
				Name:    "api",
				Command: "./api",
				Resources: ResourceConfig{
					CPU: "invalid-cpu",
				},
			},
			wantErr:  true,
			errMatch: "invalid cpu resource",
		},
		{
			name: "invalid memory format",
			svc: ServiceConfig{
				Name:    "api",
				Command: "./api",
				Resources: ResourceConfig{
					Memory: "unknown-unit-xyz",
				},
			},
			wantErr:  true,
			errMatch: "invalid memory resource",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.svc.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error containing '%s', got nil", tc.errMatch)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseCPU_And_Memory(t *testing.T) {
	// CPU Tests
	cpu, err := ParseCPU("2.5")
	if err != nil || cpu != 2.5 {
		t.Errorf("expected 2.5, got %f (err: %v)", cpu, err)
	}

	cpuMilli, err := ParseCPU("750m")
	if err != nil || cpuMilli != 0.75 {
		t.Errorf("expected 0.75, got %f (err: %v)", cpuMilli, err)
	}

	// Memory Tests
	memGB, err := ParseMemoryString("1GB")
	if err != nil || memGB != 1000*1000*1000 {
		t.Errorf("expected 1000000000 bytes, got %d (err: %v)", memGB, err)
	}

	memGiB, err := ParseMemoryString("2GiB")
	if err != nil || memGiB != 2*1024*1024*1024 {
		t.Errorf("expected 2147483648 bytes, got %d (err: %v)", memGiB, err)
	}

	memKB, err := ParseMemoryString("512k")
	if err != nil || memKB != 512*1024 {
		t.Errorf("expected 524288 bytes, got %d (err: %v)", memKB, err)
	}
}
