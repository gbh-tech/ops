package render

import (
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

func TestUnitRunOnly(t *testing.T) {
	zero, one := 0, 1
	cases := []struct {
		name string
		cfg  app.AppConfig
		want bool
	}{
		{"replicas = 0 in the section", app.AppConfig{"global": {Name: "job"}, "reviewapp": {Replicas: &zero}}, true},
		{"replicas = 0 in [global]", app.AppConfig{"global": {Name: "job", Replicas: &zero}, "reviewapp": {}}, true},
		{"the section overrides [global]", app.AppConfig{"global": {Name: "job", Replicas: &zero}, "reviewapp": {Replicas: &one}}, false},
		{"replicas = 1", app.AppConfig{"global": {Name: "web"}, "reviewapp": {Replicas: &one}}, false},
		{"no replicas anywhere", app.AppConfig{"global": {Name: "web"}, "reviewapp": {}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := Unit(tc.cfg, "reviewapp", pkgecs.BaseDefaults{CPU: 256, Memory: 512}, prefix)
			if err != nil {
				t.Fatal(err)
			}
			if spec.RunOnly != tc.want {
				t.Fatalf("RunOnly = %v, want %v", spec.RunOnly, tc.want)
			}
		})
	}
}
