package spec

import (
	"testing"
	"time"
)

func TestParseJobConfig(t *testing.T) {
	yamlContent := `
version: "1"
jobs:
  db-migration:
    command: "migrate"
    args: ["-path", "/migrations", "up"]
    environment:
      DB_URL: "postgres://user:pass@localhost:5432/mydb"
    working_dir: "/app"
    runtime: "native"
    resources:
      cpu: "500m"
      memory: "256MiB"
    retry_policy:
      max_retries: 3
      backoff_period: "5s"
    timeout: "10m"
`

	cfg, err := ParseJobConfig([]byte(yamlContent))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if len(cfg.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(cfg.Jobs))
	}

	job, exists := cfg.Jobs["db-migration"]
	if !exists {
		t.Fatalf("expected job 'db-migration' to exist")
	}

	settings, err := job.Validate()
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	if settings.Resources.CPUCores != 0.5 {
		t.Errorf("expected 0.5 CPU, got %f", settings.Resources.CPUCores)
	}
	if settings.Resources.MemoryBytes != 256*1024*1024 {
		t.Errorf("expected 256MiB, got %d", settings.Resources.MemoryBytes)
	}
	if settings.Timeout != 10*time.Minute {
		t.Errorf("expected 10m timeout, got %v", settings.Timeout)
	}
	if settings.RetryRetries != 3 {
		t.Errorf("expected 3 retries, got %d", settings.RetryRetries)
	}
	if settings.RetryBackoff != 5*time.Second {
		t.Errorf("expected 5s backoff, got %v", settings.RetryBackoff)
	}
}

func TestJobConfigValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "missing command",
			yaml: `
jobs:
  job1:
    runtime: "native"
`,
			wantErr: true,
		},
		{
			name: "invalid runtime",
			yaml: `
jobs:
  job1:
    command: "echo 1"
    runtime: "kubernetes"
`,
			wantErr: true,
		},
		{
			name: "invalid timeout",
			yaml: `
jobs:
  job1:
    command: "echo 1"
    timeout: "invalid-duration"
`,
			wantErr: true,
		},
		{
			name: "negative retry count",
			yaml: `
jobs:
  job1:
    command: "echo 1"
    retry_policy:
      max_retries: -1
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseJobConfig([]byte(tt.yaml))
			if err == nil {
				for _, j := range cfg.Jobs {
					_, err = j.Validate()
					if err != nil {
						break
					}
				}
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("test %s: expected error %v, got %v", tt.name, tt.wantErr, err)
			}
		})
	}
}
