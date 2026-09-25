package spec

import (
	"testing"
)

func TestParseVolumeConfig(t *testing.T) {
	yamlContent := `
version: "1"
volumes:
  pgdata:
    driver: "local"
    location: "/var/cloudx/volumes/pgdata"
    size: "10GB"
    owner_ref: "postgres-svc"
    read_only: false
`

	cfg, err := ParseVolumeConfig([]byte(yamlContent))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if len(cfg.Volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(cfg.Volumes))
	}

	vol, exists := cfg.Volumes["pgdata"]
	if !exists {
		t.Fatalf("expected volume 'pgdata' to exist")
	}

	settings, err := vol.Validate()
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	if settings.Driver != "local" {
		t.Errorf("expected driver 'local', got '%s'", settings.Driver)
	}
	if settings.SizeBytes != 10*1000*1000*1000 {
		t.Errorf("expected 10GB in bytes, got %d", settings.SizeBytes)
	}
}

func TestVolumeConfigValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "invalid driver",
			yaml: `
volumes:
  bad-vol:
    driver: "nfs-cluster"
`,
			wantErr: true,
		},
		{
			name: "invalid size string",
			yaml: `
volumes:
  bad-vol:
    size: "huge-amount"
`,
			wantErr: true,
		},
		{
			name: "invalid volume name",
			yaml: `
volumes:
  "bad name with spaces!":
    driver: "local"
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseVolumeConfig([]byte(tt.yaml))
			if err == nil {
				for _, v := range cfg.Volumes {
					_, err = v.Validate()
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
