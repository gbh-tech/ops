package ecs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ops/pkg/config"
)

// ---- defaultDotenvPath ----

func TestDefaultDotenvPath(t *testing.T) {
	t.Parallel()

	monoDefault := &config.OpsConfig{}           // RepoMode="" → mono, AppsDir="" → "apps"
	monoCustom := &config.OpsConfig{AppsDir: "services"} // custom apps dir
	singleRepo := &config.OpsConfig{RepoMode: "single"}

	tests := []struct {
		name string
		cfg  *config.OpsConfig
		app  string
		want string
	}{
		{
			name: "mono-repo with app uses default apps dir",
			cfg:  monoDefault,
			app:  "qa-metrics",
			want: filepath.Join("apps", "qa-metrics", ".env"),
		},
		{
			name: "mono-repo with app uses custom apps dir",
			cfg:  monoCustom,
			app:  "qa-metrics",
			want: filepath.Join("services", "qa-metrics", ".env"),
		},
		{
			name: "mono-repo with nested app name",
			cfg:  monoDefault,
			app:  "platform/workers",
			want: filepath.Join("apps", "platform", "workers", ".env"),
		},
		{
			name: "mono-repo without app falls back to .env",
			cfg:  monoDefault,
			app:  "",
			want: ".env",
		},
		{
			name: "single-repo ignores app name",
			cfg:  singleRepo,
			app:  "qa-metrics",
			want: ".env",
		},
		{
			name: "single-repo no app",
			cfg:  singleRepo,
			app:  "",
			want: ".env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := defaultDotenvPath(tt.cfg, tt.app)
			if got != tt.want {
				t.Errorf("defaultDotenvPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---- writeDotenvFile ----

func TestWriteDotenvFile_CreatesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "apps", "qa-metrics", ".env")
	content := "FOO=bar\nBAZ=qux"

	if err := writeDotenvFile(path, content); err != nil {
		t.Fatalf("writeDotenvFile() unexpected error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() unexpected error: %v", err)
	}
	// writeDotenvFile appends a trailing newline
	want := content + "\n"
	if string(got) != want {
		t.Errorf("file content = %q, want %q", string(got), want)
	}
}

func TestWriteDotenvFile_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "KEY=value"

	for i := range 3 {
		if err := writeDotenvFile(path, content); err != nil {
			t.Fatalf("writeDotenvFile() call %d unexpected error: %v", i+1, err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() unexpected error: %v", err)
	}
	want := content + "\n"
	if string(got) != want {
		t.Errorf("file content after repeated writes = %q, want %q", string(got), want)
	}
}

func TestWriteDotenvFile_CreatesMissingDirectories(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "deeply", "nested", "path", ".env")

	if err := writeDotenvFile(path, "X=1"); err != nil {
		t.Fatalf("writeDotenvFile() unexpected error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file was not created at %s: %v", path, err)
	}
}

func TestWriteDotenvFile_OverwritesExistingContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	if err := writeDotenvFile(path, "OLD=value\nEXTRA=line"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	newContent := "NEW=value"
	if err := writeDotenvFile(path, newContent); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() unexpected error: %v", err)
	}
	// Old content (EXTRA=line) must be gone
	if strings.Contains(string(got), "OLD") || strings.Contains(string(got), "EXTRA") {
		t.Errorf("file still contains stale content: %q", string(got))
	}
	want := newContent + "\n"
	if string(got) != want {
		t.Errorf("file content = %q, want %q", string(got), want)
	}
}
