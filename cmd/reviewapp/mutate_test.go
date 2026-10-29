package reviewapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ops/pkg/config"
)

// writeUnit creates apps/<name>/deploy/config.toml and a Dockerfile.
func writeUnit(t *testing.T, root, name, toml string) {
	t.Helper()
	dir := filepath.Join(root, "apps", name)
	if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy", "config.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testConfig(root string) *config.OpsConfig {
	cfg := &config.OpsConfig{AppDirs: []string{filepath.Join(root, "apps")}}
	cfg.ECS.Defaults.CPU = 256
	cfg.ECS.Defaults.Memory = 512
	return cfg
}

const prefix = "arn:aws:secretsmanager:us-east-1:1:secret"

func TestBuildManifest(t *testing.T) {
	root := t.TempDir()
	writeUnit(t, root, "shell", `
[global]
name = "shell"
kind = "app"
port = 3002

[reviewapp]
secrets = ["AUTH0_SECRET"]
[reviewapp.environment]
APP_BASE_URL = "{url}"
`)
	f := manifestFile{PR: 7, SHA: "abc", Tag: "pr-7-abc", Images: map[string]string{"shell": "repo/shell:pr-7-abc"}}

	m, err := buildManifest(f, testConfig(root), "repo/tools", prefix)
	if err != nil {
		t.Fatal(err)
	}
	if m.ToolsImage != "repo/tools:pr-7-abc" {
		t.Fatalf("tools image = %s", m.ToolsImage)
	}
	spec := m.Units["shell"]
	if spec.Ports[0] != 3002 || spec.CPU != 256 || spec.Environment["APP_BASE_URL"] != "{url}" {
		t.Fatalf("spec = %+v", spec)
	}
	if spec.Secrets["AUTH0_SECRET"] != prefix+":shell/reviewapp:AUTH0_SECRET::" {
		t.Fatalf("secret = %s", spec.Secrets["AUTH0_SECRET"])
	}
}

func TestBuildManifestErrors(t *testing.T) {
	root := t.TempDir()
	writeUnit(t, root, "shell", "[global]\nname = \"shell\"\nkind = \"app\"\n\n[dev]\n")

	_, err := buildManifest(manifestFile{PR: 7, Tag: "t", Images: map[string]string{"shell": "s", "ghost": "g"}}, testConfig(root), "repo/tools", prefix)
	if err == nil || !strings.Contains(err.Error(), "ghost: no deploy/config.toml") || !strings.Contains(err.Error(), "shell: no [reviewapp] section") {
		t.Fatalf("err = %v", err)
	}
	if _, err := buildManifest(manifestFile{PR: 7, Images: map[string]string{"shell": "s"}}, testConfig(root), "", prefix); err == nil {
		t.Fatal("want an error without a tools image")
	}
}

func TestMutatingCommandsAreRegistered(t *testing.T) {
	names := map[string]bool{}
	for _, c := range Command.Commands() {
		names[c.Name()] = true
	}
	for _, name := range []string{"deploy", "destroy", "sleep", "wake", "run", "gc"} {
		if !names[name] {
			t.Fatalf("%s not registered", name)
		}
	}
	if names["create"] {
		t.Fatal("create is registered; deploy creates or updates")
	}
	if deployCmd.Flags().Lookup("manifest") == nil {
		t.Fatal("deploy has no --manifest")
	}
	for _, flag := range []string{"in-cluster", "manifest-version", "manifest-sha256"} {
		if f := Command.PersistentFlags().Lookup(flag); f == nil || !f.Hidden {
			t.Fatalf("--%s missing or not hidden", flag)
		}
	}
}
