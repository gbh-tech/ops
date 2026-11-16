package render

import (
	"strings"
	"testing"

	"ops/pkg/app"
	pkgecs "ops/pkg/ecs"
)

const prefix = "arn:aws:secretsmanager:us-east-1:1:secret"

func TestUnit(t *testing.T) {
	cfg := app.AppConfig{
		"global": {
			Name:        "subgraph-users",
			Kind:        "subgraph",
			Port:        4003,
			Environment: map[string]string{"LOG_LEVEL": "info"},
		},
		"reviewapp": {
			Environment: map[string]string{"PUBLIC_URL": "{url}"},
			Secrets:     []any{"RLS_DATABASE_URL"},
		},
	}
	spec, err := Unit(cfg, "reviewapp", pkgecs.BaseDefaults{CPU: 256, Memory: 512}, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if spec.CPU != 256 || spec.Memory != 512 {
		t.Fatalf("cpu/memory = %d/%d", spec.CPU, spec.Memory)
	}
	if len(spec.Ports) != 1 || spec.Ports[0] != 4003 {
		t.Fatalf("ports = %v", spec.Ports)
	}
	if spec.Environment["LOG_LEVEL"] != "info" || spec.Environment["PUBLIC_URL"] != "{url}" {
		t.Fatalf("environment = %v", spec.Environment)
	}
	want := prefix + ":subgraph-users/reviewapp:RLS_DATABASE_URL::"
	if spec.Secrets["RLS_DATABASE_URL"] != want {
		t.Fatalf("secret = %q, want %q", spec.Secrets["RLS_DATABASE_URL"], want)
	}
}

func TestUnitNeedsTheSection(t *testing.T) {
	cfg := app.AppConfig{"global": {Name: "shell", Kind: "app"}, "dev": {}}
	if _, err := Unit(cfg, "reviewapp", pkgecs.BaseDefaults{}, prefix); err == nil {
		t.Fatal("want an error for a unit without a [reviewapp] section")
	}
}

func TestUnitCarriesTheFilesOfTheSection(t *testing.T) {
	cfg := app.AppConfig{
		"global":    {Name: "apollo-router", Kind: "ecs", Port: 4000},
		"reviewapp": {Files: map[string]string{"/supergraph/supergraph.graphql": "supergraph.graphql"}},
	}
	spec, err := Unit(cfg, "reviewapp", pkgecs.BaseDefaults{CPU: 256, Memory: 512}, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Files) != 1 || spec.Files["/supergraph/supergraph.graphql"] != "supergraph.graphql" {
		t.Fatalf("files = %v", spec.Files)
	}
}

func TestUnitWithoutFiles(t *testing.T) {
	cfg := app.AppConfig{"global": {Name: "shell", Kind: "ecs"}, "reviewapp": {}}
	spec, err := Unit(cfg, "reviewapp", pkgecs.BaseDefaults{}, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Files != nil {
		t.Fatalf("files = %v", spec.Files)
	}
}

func TestUnitRefusesFilesInGlobal(t *testing.T) {
	cfg := app.AppConfig{
		"global":    {Name: "apollo-router", Kind: "ecs", Files: map[string]string{"/supergraph/s.graphql": "s.graphql"}},
		"reviewapp": {},
	}
	if _, err := Unit(cfg, "reviewapp", pkgecs.BaseDefaults{}, prefix); err == nil || !strings.Contains(err.Error(), "only for review apps") {
		t.Fatalf("err = %v", err)
	}
}

func TestFilesRefusesBadPathsAndKeys(t *testing.T) {
	for _, files := range []map[string]string{
		{"supergraph.graphql": "supergraph.graphql"},        // relative path
		{"/supergraph.graphql": "supergraph.graphql"},       // directly under /
		{"/a/../supergraph.graphql": "supergraph.graphql"},  // not clean
		{"/supergraph/": "supergraph.graphql"},              // a directory
		{"/supergraph/s'g.graphql": "supergraph.graphql"},   // shell quote
		{"/supergraph/s.graphql": "../pr-8/s.graphql"},      // leaves the prefix
		{"/supergraph/s.graphql": "/pr-7/s.graphql"},        // absolute key
		{"/supergraph/s.graphql": "a/./s.graphql"},          // dot segment
		{"/supergraph/s.graphql": "s.graphql; rm -rf /tmp"}, // shell
		{"/supergraph/s.graphql": ""},                       // empty
	} {
		if _, err := Files(files); err == nil {
			t.Errorf("want an error for %v", files)
		}
	}
}

func TestFilesRefusesADestinationInsideAnother(t *testing.T) {
	for _, files := range []map[string]string{
		{"/config/schema": "a.graphql", "/config/schema/options.json": "b.json"},
		{"/config/schema/options.json": "b.json", "/config/schema": "a.graphql"},
		{"/config/schema": "a.graphql", "/config/schema/deep/options.json": "b.json"},
	} {
		if _, err := Files(files); err == nil || !strings.Contains(err.Error(), "cannot contain another") {
			t.Errorf("files %v: err = %v", files, err)
		}
	}
}

func TestFilesAllowsSiblingsWithACommonPrefix(t *testing.T) {
	files := map[string]string{"/config/schema": "a.graphql", "/config/schemas/options.json": "b.json", "/config/schema.json": "c.json"}
	if _, err := Files(files); err != nil {
		t.Fatal(err)
	}
}
