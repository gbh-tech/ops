package ecs

import (
	"strings"
	"testing"
)

func TestValidateHealthCheckCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		hc      HealthCheckConfig
		wantErr bool
	}{
		{
			name:    "empty command is ok",
			hc:      HealthCheckConfig{},
			wantErr: false,
		},
		{
			name:    "CMD form",
			hc:      HealthCheckConfig{Command: []string{"CMD", "/bin/healthcheck"}},
			wantErr: false,
		},
		{
			name:    "CMD-SHELL form",
			hc:      HealthCheckConfig{Command: []string{"CMD-SHELL", "curl -f http://localhost:8080/health || exit 1"}},
			wantErr: false,
		},
		{
			name:    "invalid first element",
			hc:      HealthCheckConfig{Command: []string{"SHELL", "curl -f http://localhost/health"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateHealthCheckCommand(tt.hc)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateHealthCheckCommand() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatePorts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     AppSection
		wantErr bool
	}{
		{
			name:    "valid single port",
			cfg:     AppSection{Port: 8080},
			wantErr: false,
		},
		{
			name:    "valid ports list",
			cfg:     AppSection{Ports: []int{8080, 9090}},
			wantErr: false,
		},
		{
			name:    "zero ports are skipped",
			cfg:     AppSection{},
			wantErr: false,
		},
		{
			name:    "port out of range high",
			cfg:     AppSection{Port: 70000},
			wantErr: true,
		},
		{
			name:    "port out of range low",
			cfg:     AppSection{Ports: []int{0, -1}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validatePorts(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePorts() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestResolveConfig(t *testing.T) {
	t.Parallel()
	base := &BaseConfig{
		Defaults: BaseDefaults{
			CPU:      256,
			Memory:   512,
			Replicas: 1,
		},
	}

	t.Run("global values merged into result", func(t *testing.T) {
		t.Parallel()
		cfg := AppConfig{
			"global": AppSection{Name: "api", Port: 8080},
		}
		merged, err := ResolveConfig(base, cfg, "stage")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if merged.Name != "api" {
			t.Fatalf("name = %q, want %q", merged.Name, "api")
		}
		if merged.CPU != 256 {
			t.Fatalf("cpu = %d, want 256 (from base defaults)", merged.CPU)
		}
	})

	t.Run("env section overrides global", func(t *testing.T) {
		t.Parallel()
		replicas := 3
		cfg := AppConfig{
			"global":     AppSection{Name: "api", CPU: 512},
			"production": AppSection{CPU: 1024, Replicas: &replicas},
		}
		merged, err := ResolveConfig(base, cfg, "production")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if merged.CPU != 1024 {
			t.Fatalf("cpu = %d, want 1024", merged.CPU)
		}
		if merged.Replicas == nil || *merged.Replicas != 3 {
			t.Fatalf("replicas = %v, want 3", merged.Replicas)
		}
	})

	t.Run("missing name returns error", func(t *testing.T) {
		t.Parallel()
		cfg := AppConfig{
			"global": AppSection{CPU: 256},
		}
		_, err := ResolveConfig(base, cfg, "stage")
		if err == nil {
			t.Fatal("expected error for missing name, got nil")
		}
	})

	t.Run("invalid health check command returns error", func(t *testing.T) {
		t.Parallel()
		cfg := AppConfig{
			"global": AppSection{
				Name:        "api",
				ContainerHC: HealthCheckConfig{Command: []string{"INVALID", "cmd"}},
			},
		}
		_, err := ResolveConfig(base, cfg, "stage")
		if err == nil {
			t.Fatal("expected error for invalid health check command, got nil")
		}
	})
}

func TestResolveSecrets(t *testing.T) {
	t.Parallel()
	cfg := AppConfig{
		"global": AppSection{Secrets: []any{"DB_URL"}},
		"stage":  AppSection{Secrets: map[string]any{"API_KEY": "api_key"}},
	}
	secrets, err := ResolveSecrets(cfg, "stage", "my-app", "arn:aws:secretsmanager:us-east-1:123456789012:secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := map[string]string{}
	for _, s := range secrets {
		found[s.Name] = s.ValueFrom
	}
	if !strings.Contains(found["DB_URL"], "my-app/shared") {
		t.Fatalf("DB_URL ValueFrom = %q, want shared path", found["DB_URL"])
	}
	if !strings.Contains(found["API_KEY"], "my-app/stage") {
		t.Fatalf("API_KEY ValueFrom = %q, want stage path", found["API_KEY"])
	}
}

func TestComputeNames(t *testing.T) {
	t.Parallel()
	merged := MergedConfig{AppSection: AppSection{Name: "api"}}
	names := ComputeNames(merged, "stage", "my-cluster")
	if names.Family != "api-stage" {
		t.Fatalf("Family = %q, want %q", names.Family, "api-stage")
	}
	if names.Service != "api-stage" {
		t.Fatalf("Service = %q, want %q", names.Service, "api-stage")
	}
	if names.LogGroup != "/ecs/my-cluster/stage/api" {
		t.Fatalf("LogGroup = %q, want %q", names.LogGroup, "/ecs/my-cluster/stage/api")
	}
	if names.ScheduledFamily != "api-stage-scheduled" {
		t.Fatalf("ScheduledFamily = %q, want %q", names.ScheduledFamily, "api-stage-scheduled")
	}
}
