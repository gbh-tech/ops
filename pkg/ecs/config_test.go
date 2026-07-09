package ecs

import (
	"strings"
	"testing"

	"ops/pkg/app"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestValidateHealthCheckCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		hc      app.HealthCheckConfig
		wantErr bool
	}{
		{
			name:    "empty command is ok",
			hc:      app.HealthCheckConfig{},
			wantErr: false,
		},
		{
			name:    "CMD form",
			hc:      app.HealthCheckConfig{Command: []string{"CMD", "/bin/healthcheck"}},
			wantErr: false,
		},
		{
			name:    "CMD-SHELL form",
			hc:      app.HealthCheckConfig{Command: []string{"CMD-SHELL", "curl -f http://localhost:8080/health || exit 1"}},
			wantErr: false,
		},
		{
			name:    "CMD with no argument",
			hc:      app.HealthCheckConfig{Command: []string{"CMD"}},
			wantErr: true,
		},
		{
			name:    "CMD-SHELL with no argument",
			hc:      app.HealthCheckConfig{Command: []string{"CMD-SHELL"}},
			wantErr: true,
		},
		{
			name:    "invalid first element",
			hc:      app.HealthCheckConfig{Command: []string{"SHELL", "curl -f http://localhost/health"}},
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
		cfg     app.AppSection
		wantErr bool
	}{
		{
			name:    "valid single port",
			cfg:     app.AppSection{Port: 8080},
			wantErr: false,
		},
		{
			name:    "valid ports list",
			cfg:     app.AppSection{Ports: []int{8080, 9090}},
			wantErr: false,
		},
		{
			name:    "zero ports are skipped",
			cfg:     app.AppSection{},
			wantErr: false,
		},
		{
			name:    "port out of range high",
			cfg:     app.AppSection{Port: 70000},
			wantErr: true,
		},
		{
			name:    "port out of range low",
			cfg:     app.AppSection{Ports: []int{0, -1}},
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
		cfg := app.AppConfig{
			"global": app.AppSection{Name: "api", Port: 8080},
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
		cfg := app.AppConfig{
			"global":     app.AppSection{Name: "api", CPU: 512},
			"production": app.AppSection{CPU: 1024, Replicas: &replicas},
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

	t.Run("global append environment is inherited", func(t *testing.T) {
		t.Parallel()
		cfg := app.AppConfig{
			"global": app.AppSection{Name: "api", AppendEnvironment: boolPtr(true)},
		}
		merged, err := ResolveConfig(base, cfg, "stage")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !merged.AppendsEnvironment() {
			t.Fatal("append_environment = false, want true")
		}
	})

	t.Run("env append environment overrides global", func(t *testing.T) {
		t.Parallel()
		cfg := app.AppConfig{
			"global": app.AppSection{Name: "api", AppendEnvironment: boolPtr(true)},
			"stage":  app.AppSection{AppendEnvironment: boolPtr(false)},
		}
		merged, err := ResolveConfig(base, cfg, "stage")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if merged.AppendsEnvironment() {
			t.Fatal("append_environment = true, want false")
		}
	})

	t.Run("missing name returns error", func(t *testing.T) {
		t.Parallel()
		cfg := app.AppConfig{
			"global": app.AppSection{CPU: 256},
		}
		_, err := ResolveConfig(base, cfg, "stage")
		if err == nil {
			t.Fatal("expected error for missing name, got nil")
		}
	})

	t.Run("invalid health check command returns error", func(t *testing.T) {
		t.Parallel()
		cfg := app.AppConfig{
			"global": app.AppSection{
				Name:        "api",
				ContainerHC: app.HealthCheckConfig{Command: []string{"INVALID", "cmd"}},
			},
		}
		_, err := ResolveConfig(base, cfg, "stage")
		if err == nil {
			t.Fatal("expected error for invalid health check command, got nil")
		}
	})

	t.Run("env gpu overrides global", func(t *testing.T) {
		t.Parallel()
		cfg := app.AppConfig{
			"global":     app.AppSection{Name: "vllm", GPU: 1},
			"production": app.AppSection{GPU: 2},
		}
		merged, err := ResolveConfig(base, cfg, "production")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if merged.GPU != 2 {
			t.Fatalf("gpu = %d, want 2", merged.GPU)
		}
	})

	t.Run("negative gpu returns error", func(t *testing.T) {
		t.Parallel()
		cfg := app.AppConfig{
			"global": app.AppSection{Name: "vllm", GPU: -1},
		}
		_, err := ResolveConfig(base, cfg, "production")
		if err == nil {
			t.Fatal("expected error for negative gpu, got nil")
		}
	})
}

func TestResolveSecrets(t *testing.T) {
	t.Parallel()
	cfg := app.AppConfig{
		"global": app.AppSection{Secrets: []any{"DB_URL"}},
		"stage":  app.AppSection{Secrets: map[string]any{"API_KEY": "api_key"}},
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

func TestResolveSecretsExternalCombined(t *testing.T) {
	t.Parallel()
	const arnPrefix = "arn:aws:secretsmanager:us-east-1:123456789012:secret"

	cfg := app.AppConfig{
		"global": app.AppSection{Secrets: map[string]any{"DB_URL": "db_url"}},
		"stage": app.AppSection{Secrets: map[string]any{
			"PLAIN_KEY": "plain_key",
			"CLAUDE_API_KEY": map[string]any{
				"secret": "anthropic/stage",
				"key":    "CLAUDE_API_KEY",
			},
			"ARN_KEY": map[string]any{
				"secret": "arn:aws:secretsmanager:us-east-1:999:secret:ext",
				"key":    "K",
			},
		}},
	}

	secrets, err := ResolveSecrets(cfg, "stage", "my-app", arnPrefix)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := map[string]string{}
	for _, s := range secrets {
		found[s.Name] = s.ValueFrom
	}

	const wantClaude = "arn:aws:secretsmanager:us-east-1:123456789012:secret:anthropic/stage:CLAUDE_API_KEY::"
	if found["CLAUDE_API_KEY"] != wantClaude {
		t.Fatalf("CLAUDE_API_KEY ValueFrom = %q, want %q", found["CLAUDE_API_KEY"], wantClaude)
	}

	if !strings.Contains(found["PLAIN_KEY"], "my-app/stage") {
		t.Fatalf("PLAIN_KEY ValueFrom = %q, want my-app/stage path", found["PLAIN_KEY"])
	}

	const wantARN = "arn:aws:secretsmanager:us-east-1:999:secret:ext:K::"
	if found["ARN_KEY"] != wantARN {
		t.Fatalf("ARN_KEY ValueFrom = %q, want %q", found["ARN_KEY"], wantARN)
	}

	if !strings.Contains(found["DB_URL"], "my-app/shared") {
		t.Fatalf("DB_URL ValueFrom = %q, want shared path", found["DB_URL"])
	}
}

func TestResolveSecretsExternalSubtests(t *testing.T) {
	t.Parallel()
	arnPrefix := "arn:aws:secretsmanager:us-east-1:123456789012:secret"

	t.Run("external secret alongside implicit key", func(t *testing.T) {
		t.Parallel()
		cfg := app.AppConfig{
			"global": app.AppSection{},
			"stage": app.AppSection{Secrets: map[string]any{
				"CLAUDE_API_KEY": map[string]any{
					"secret": "anthropic/stage",
					"key":    "CLAUDE_API_KEY",
				},
				"DB_URL": "db_url",
			}},
		}
		secrets, err := ResolveSecrets(cfg, "stage", "my-app", arnPrefix)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := map[string]string{}
		for _, s := range secrets {
			found[s.Name] = s.ValueFrom
		}
		wantClaude := arnPrefix + ":anthropic/stage:CLAUDE_API_KEY::"
		if found["CLAUDE_API_KEY"] != wantClaude {
			t.Fatalf("CLAUDE_API_KEY ValueFrom = %q, want %q", found["CLAUDE_API_KEY"], wantClaude)
		}
		wantDB := arnPrefix + ":my-app/stage:db_url::"
		if found["DB_URL"] != wantDB {
			t.Fatalf("DB_URL ValueFrom = %q, want %q", found["DB_URL"], wantDB)
		}
	})

	t.Run("full ARN passthrough", func(t *testing.T) {
		t.Parallel()
		fullARN := "arn:aws:secretsmanager:us-east-1:123456789012:secret:custom-XyZ"
		cfg := app.AppConfig{
			"global": app.AppSection{},
			"stage": app.AppSection{Secrets: map[string]any{
				"CUSTOM": map[string]any{
					"secret": fullARN,
					"key":    "K",
				},
			}},
		}
		secrets, err := ResolveSecrets(cfg, "stage", "my-app", arnPrefix)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := map[string]string{}
		for _, s := range secrets {
			found[s.Name] = s.ValueFrom
		}
		want := fullARN + ":K::"
		if found["CUSTOM"] != want {
			t.Fatalf("CUSTOM ValueFrom = %q, want %q", found["CUSTOM"], want)
		}
	})
}

func TestComputeNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		merged      MergedConfig
		wantFamily  string
		wantService string
	}{
		{
			name:        "defaults service to name",
			merged:      MergedConfig{AppSection: app.AppSection{Name: "api"}},
			wantFamily:  "api-stage",
			wantService: "api",
		},
		{
			name:        "appends environment when enabled",
			merged:      MergedConfig{AppSection: app.AppSection{Name: "api", AppendEnvironment: boolPtr(true)}},
			wantFamily:  "api-stage",
			wantService: "api-stage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			names := ComputeNames(tt.merged, "stage", "my-cluster")
			if names.Family != tt.wantFamily {
				t.Fatalf("Family = %q, want %q", names.Family, tt.wantFamily)
			}
			if names.Service != tt.wantService {
				t.Fatalf("Service = %q, want %q", names.Service, tt.wantService)
			}
			if names.LogGroup != "/ecs/my-cluster/stage/api" {
				t.Fatalf("LogGroup = %q, want %q", names.LogGroup, "/ecs/my-cluster/stage/api")
			}
			if names.ScheduledFamily != "api-stage-scheduled" {
				t.Fatalf("ScheduledFamily = %q, want %q", names.ScheduledFamily, "api-stage-scheduled")
			}
		})
	}
}
