package config

import "testing"

func TestIsMonoRepo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		repoMode string
		want     bool
	}{
		{"", true},
		{"mono", true},
		{"single", false},
		{"other", false},
	}
	for _, tt := range tests {
		cfg := &OpsConfig{RepoMode: tt.repoMode}
		if got := cfg.IsMonoRepo(); got != tt.want {
			t.Fatalf("IsMonoRepo() with repo_mode=%q = %v, want %v", tt.repoMode, got, tt.want)
		}
	}
}

func TestAppsDirPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		appsDir string
		ecsDir  string
		want    string
	}{
		{"top-level wins", "myapps", "ecsapps", "myapps"},
		{"ecs fallback", "", "ecsapps", "ecsapps"},
		{"default fallback", "", "", "apps"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &OpsConfig{
				AppsDir: tt.appsDir,
				ECS:     ECSConfig{AppsDir: tt.ecsDir},
			}
			if got := cfg.AppsDirPath(); got != tt.want {
				t.Fatalf("AppsDirPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegistryType(t *testing.T) {
	t.Parallel()
	cfg := &OpsConfig{Provider: "aws"}
	if got := cfg.RegistryType(); got != "ecr" {
		t.Fatalf("RegistryType() = %q, want %q", got, "ecr")
	}
}

func TestRegistryURL(t *testing.T) {
	t.Parallel()
	t.Run("explicit override", func(t *testing.T) {
		t.Parallel()
		cfg := &OpsConfig{
			Provider: "aws",
			Registry: RegistryConfig{URL: "custom.registry.example.com"},
			AWS:      AWSConfig{AccountId: "123456789012", Region: "us-east-1"},
		}
		want := "custom.registry.example.com"
		if got := cfg.RegistryURL(); got != want {
			t.Fatalf("RegistryURL() = %q, want %q", got, want)
		}
	})
	t.Run("derived from aws", func(t *testing.T) {
		t.Parallel()
		cfg := &OpsConfig{
			Provider: "aws",
			AWS:      AWSConfig{AccountId: "123456789012", Region: "us-east-1"},
		}
		want := "123456789012.dkr.ecr.us-east-1.amazonaws.com"
		if got := cfg.RegistryURL(); got != want {
			t.Fatalf("RegistryURL() = %q, want %q", got, want)
		}
	})
}

func TestResolveAppFilePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		repoMode    string
		appsDir     string
		app         string
		override    string
		defaultPath string
		want        string
	}{
		{
			name:        "mono default path",
			repoMode:    "mono",
			appsDir:     "apps",
			app:         "api",
			override:    "",
			defaultPath: "deploy/config.toml",
			want:        "apps/api/deploy/config.toml",
		},
		{
			name:        "single default path",
			repoMode:    "single",
			app:         "api",
			override:    "",
			defaultPath: "deploy/config.toml",
			want:        "deploy/config.toml",
		},
		{
			name:        "mono relative override scoped under app",
			repoMode:    "mono",
			appsDir:     "apps",
			app:         "api",
			override:    "deploy/custom.toml",
			defaultPath: "deploy/config.toml",
			want:        "apps/api/deploy/custom.toml",
		},
		{
			name:        "absolute override returned as-is",
			repoMode:    "mono",
			appsDir:     "apps",
			app:         "api",
			override:    "/abs/path/config.toml",
			defaultPath: "deploy/config.toml",
			want:        "/abs/path/config.toml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &OpsConfig{RepoMode: tt.repoMode, AppsDir: tt.appsDir}
			got := cfg.ResolveAppFilePath(tt.app, tt.override, tt.defaultPath)
			if got != tt.want {
				t.Fatalf("ResolveAppFilePath() = %q, want %q", got, tt.want)
			}
		})
	}
}
