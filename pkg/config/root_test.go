package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestResolveAppConfigPath(t *testing.T) {
	tests := []struct {
		name     string
		repoMode string
		appsDir  string
		app      string
		override string
		files    []string
		want     string
		wantErr  string
	}{
		{
			name:     "mono default path",
			repoMode: "mono",
			appsDir:  "apps",
			app:      "api",
			override: "",
			want:     "apps/api/deploy/config.toml",
		},
		{
			name:     "single default path",
			repoMode: "single",
			app:      "api",
			override: "",
			want:     "deploy/config.toml",
		},
		{
			name:     "mono explicit extension override scoped under app",
			repoMode: "mono",
			appsDir:  "apps",
			app:      "api",
			override: "deploy/custom.toml",
			files:    []string{"apps/api/deploy/custom.toml"},
			want:     "apps/api/deploy/custom.toml",
		},
		{
			name:     "absolute override returned as-is",
			repoMode: "mono",
			appsDir:  "apps",
			app:      "api",
			override: "/abs/path/config.toml",
			want:     "/abs/path/config.toml",
		},
		{
			name:     "bare basename resolves to toml",
			repoMode: "mono",
			appsDir:  "apps",
			app:      "api",
			override: "server",
			files:    []string{"apps/api/deploy/server.toml"},
			want:     "apps/api/deploy/server.toml",
		},
		{
			name:     "bare basename resolves to yaml",
			repoMode: "mono",
			appsDir:  "apps",
			app:      "api",
			override: "worker",
			files:    []string{"apps/api/deploy/worker.yaml"},
			want:     "apps/api/deploy/worker.yaml",
		},
		{
			name:     "subpath inside deploy",
			repoMode: "mono",
			appsDir:  "apps",
			app:      "api",
			override: "worker/server",
			files:    []string{"apps/api/deploy/worker/server.yaml"},
			want:     "apps/api/deploy/worker/server.yaml",
		},
		{
			name:     "single repo bare basename",
			repoMode: "single",
			app:      "",
			override: "server",
			files:    []string{"deploy/server.toml"},
			want:     "deploy/server.toml",
		},
		{
			name:     "conflicting extensions",
			repoMode: "mono",
			appsDir:  "apps",
			app:      "api",
			override: "server",
			files:    []string{"apps/api/deploy/server.toml", "apps/api/deploy/server.yaml"},
			wantErr:  "two conflicting files with different extensions exist",
		},
		{
			name:     "missing config",
			repoMode: "mono",
			appsDir:  "apps",
			app:      "api",
			override: "missing",
			wantErr:  "no config file found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				p := filepath.Join(dir, f)
				if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(p, []byte("[global]\n"), 0o600); err != nil {
					t.Fatalf("write file: %v", err)
				}
			}
			cfg := &OpsConfig{RepoMode: tt.repoMode, AppsDir: tt.appsDir}
			app := tt.app
			if app != "" {
				app = tt.app
			}
			// Temporarily switch to the temp dir so relative paths resolve.
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("getwd: %v", err)
			}
			if err := os.Chdir(dir); err != nil {
				t.Fatalf("chdir: %v", err)
			}
			defer func() {
				if err := os.Chdir(cwd); err != nil {
					t.Fatalf("restore cwd: %v", err)
				}
			}()
			got, err := cfg.ResolveAppConfigPath(app, tt.override)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ResolveAppConfigPath() err = nil, want %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ResolveAppConfigPath() err = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveAppConfigPath() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolveAppConfigPath() = %q, want %q", got, tt.want)
			}
		})
	}
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
