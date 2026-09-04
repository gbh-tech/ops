package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"ops/pkg/config"
)

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	writeApp := func(dir, body, packageName string) {
		t.Helper()
		appRoot := filepath.Join(root, dir)
		if err := os.MkdirAll(filepath.Join(appRoot, "deploy"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(appRoot, "deploy", "config.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(appRoot, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if packageName != "" {
			if err := os.WriteFile(filepath.Join(appRoot, "package.json"), []byte(`{"name":"`+packageName+`"}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeApp("services/policies", `[global]
name = "subgraph-policies"
kind = "ecs"
port = 4001
[global.subgraph]
name = "policies"
schema = "src/schema.graphql"
[dev]
`, "@leopard/subgraph-policies")
	writeApp("functions/worker", `[global]
name = "worker"
kind = "lambda"
[stage]
`, "@leopard/worker")

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	units, err := Discover(&config.OpsConfig{AppDirs: []string{"services", "functions"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("len = %d, want 2", len(units))
	}
	if units[0].ID != "subgraph-policies" || units[0].Subgraph == nil {
		t.Fatalf("unexpected first unit: %+v", units[0])
	}
	if units[1].ID != "worker" || units[1].Package != "@leopard/worker" {
		t.Fatalf("unexpected second unit: %+v", units[1])
	}
}
