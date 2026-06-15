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

func TestNormalizeSecretRefs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     any
		want    map[string]app.SecretRef
		wantErr bool
	}{
		{
			name: "nil input",
			raw:  nil,
			want: nil,
		},
		{
			name: "slice of strings",
			raw:  []any{"A", "B"},
			want: map[string]app.SecretRef{
				"A": {Key: "A"},
				"B": {Key: "B"},
			},
		},
		{
			name: "string map",
			raw:  map[string]any{"A": "a_key"},
			want: map[string]app.SecretRef{
				"A": {Key: "a_key"},
			},
		},
		{
			name: "external inline table",
			raw: map[string]any{
				"CLAUDE_API_KEY": map[string]any{
					"secret": "anthropic/stage",
					"key":    "CLAUDE_API_KEY",
				},
			},
			want: map[string]app.SecretRef{
				"CLAUDE_API_KEY": {Key: "CLAUDE_API_KEY", Secret: "anthropic/stage"},
			},
		},
		{
			name: "key-only inline table",
			raw: map[string]any{
				"DB_URL": map[string]any{"key": "database_url"},
			},
			want: map[string]app.SecretRef{
				"DB_URL": {Key: "database_url"},
			},
		},
		{
			name: "inline table with no key defaults to envVar",
			raw: map[string]any{
				"MY_VAR": map[string]any{"secret": "some/secret"},
			},
			want: map[string]app.SecretRef{
				"MY_VAR": {Key: "MY_VAR", Secret: "some/secret"},
			},
		},
		{
			name:    "unknown field in inline table",
			raw:     map[string]any{"X": map[string]any{"unknown": "v"}},
			wantErr: true,
		},
		{
			name:    "non-string secret field",
			raw:     map[string]any{"X": map[string]any{"secret": 42}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := app.NormalizeSecretRefs(tt.raw)
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
				t.Fatalf("NormalizeSecretRefs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeSecretsRejectsExternalRef(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"CLAUDE_API_KEY": map[string]any{
			"secret": "anthropic/stage",
			"key":    "CLAUDE_API_KEY",
		},
	}
	_, err := app.NormalizeSecrets(raw)
	if err == nil {
		t.Fatal("expected error for external ref in NormalizeSecrets, got nil")
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

func TestLoadAppConfigExternalSecretsTOML(t *testing.T) {
	t.Parallel()
	cfg, err := app.LoadAppConfig("testdata/external_secrets.toml")
	if err != nil {
		t.Fatalf("unexpected error loading external_secrets.toml: %v", err)
	}
	refs, err := app.NormalizeSecretRefs(cfg["stage"].Secrets)
	if err != nil {
		t.Fatalf("NormalizeSecretRefs: %v", err)
	}
	ref, ok := refs["CLAUDE_API_KEY"]
	if !ok {
		t.Fatal("CLAUDE_API_KEY not found in stage secrets")
	}
	if ref.Secret != "anthropic/stage" {
		t.Fatalf("CLAUDE_API_KEY.Secret = %q, want %q", ref.Secret, "anthropic/stage")
	}
	if ref.Key != "CLAUDE_API_KEY" {
		t.Fatalf("CLAUDE_API_KEY.Key = %q, want %q", ref.Key, "CLAUDE_API_KEY")
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
