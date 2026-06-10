package app_test

import (
	"reflect"
	"testing"

	"ops/pkg/app"
)

func TestNormalizeSecrets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     any
		want    map[string]string
		wantErr bool
	}{
		{
			name: "nil input",
			raw:  nil,
			want: nil,
		},
		{
			name: "string slice",
			raw:  []any{"DB_PASSWORD", "API_KEY"},
			want: map[string]string{"DB_PASSWORD": "DB_PASSWORD", "API_KEY": "API_KEY"},
		},
		{
			name: "string map",
			raw:  map[string]any{"DB_PASSWORD": "db_password", "API_KEY": "api_key"},
			want: map[string]string{"DB_PASSWORD": "db_password", "API_KEY": "api_key"},
		},
		{
			name: "typed string slice",
			raw:  []string{"FOO", "BAR"},
			want: map[string]string{"FOO": "FOO", "BAR": "BAR"},
		},
		{
			name: "typed string map",
			raw:  map[string]string{"X": "y"},
			want: map[string]string{"X": "y"},
		},
		{
			name:    "non-string element in slice",
			raw:     []any{42},
			wantErr: true,
		},
		{
			name:    "unsupported type",
			raw:     12345,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := app.NormalizeSecrets(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("NormalizeSecrets() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveBuildArgs(t *testing.T) {
	t.Parallel()
	t.Run("env overrides global", func(t *testing.T) {
		t.Parallel()
		cfg := app.AppConfig{
			"global": app.AppSection{BuildArgs: map[string]string{"FOO": "global", "BAR": "baz"}},
			"stage":  app.AppSection{BuildArgs: map[string]string{"FOO": "stage"}},
		}
		got := app.ResolveBuildArgs(cfg, "stage")
		if got["FOO"] != "stage" {
			t.Fatalf("FOO = %q, want %q", got["FOO"], "stage")
		}
		if got["BAR"] != "baz" {
			t.Fatalf("BAR = %q, want %q", got["BAR"], "baz")
		}
	})
	t.Run("nil when both empty", func(t *testing.T) {
		t.Parallel()
		cfg := app.AppConfig{
			"global": app.AppSection{},
			"stage":  app.AppSection{},
		}
		if got := app.ResolveBuildArgs(cfg, "stage"); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})
}

func TestLoadAppConfigTOML(t *testing.T) {
	t.Parallel()
	cfg, err := app.LoadAppConfig("testdata/valid.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	global := cfg["global"]
	if global.Name != "my-app" {
		t.Fatalf("name = %q, want %q", global.Name, "my-app")
	}
	if global.Port != 8080 {
		t.Fatalf("port = %d, want 8080", global.Port)
	}
	if global.ContainerHC.Interval != 30 {
		t.Fatalf("hc interval = %d, want 30", global.ContainerHC.Interval)
	}
	if cfg["stage"].AppendEnvironment == nil || !*cfg["stage"].AppendEnvironment {
		t.Fatalf("stage append_environment = %v, want true", cfg["stage"].AppendEnvironment)
	}
}

func TestLoadAppConfigYAML(t *testing.T) {
	t.Parallel()
	cfg, err := app.LoadAppConfig("testdata/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg["global"].Name != "my-app" {
		t.Fatalf("name = %q, want %q", cfg["global"].Name, "my-app")
	}
	if cfg["stage"].AppendEnvironment == nil || !*cfg["stage"].AppendEnvironment {
		t.Fatalf("stage append_environment = %v, want true", cfg["stage"].AppendEnvironment)
	}
}

func TestLoadAppConfigDesiredCountPromotedToReplicas(t *testing.T) {
	t.Parallel()
	cfg, err := app.LoadAppConfig("testdata/desired_count.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	replicas := cfg["global"].Replicas
	if replicas == nil || *replicas != 2 {
		t.Fatalf("replicas = %v, want 2", replicas)
	}
}

func TestLoadAppConfigUnknownKeyError(t *testing.T) {
	t.Parallel()
	_, err := app.LoadAppConfig("testdata/unknown_key.toml")
	if err == nil {
		t.Fatal("expected error for unknown TOML key, got nil")
	}
}

func TestLoadFileUnsupportedExtension(t *testing.T) {
	t.Parallel()
	var out any
	err := app.LoadFile("config.json", &out)
	if err == nil {
		t.Fatal("expected error for unsupported extension, got nil")
	}
}
