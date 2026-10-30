package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestReviewAppsDefaults(t *testing.T) {
	var c ReviewAppsConfig
	if c.Defined() {
		t.Fatal("empty section reports defined")
	}
	if got := c.ResolvedEnv(); got != "reviewapp" {
		t.Fatalf("env = %q, want reviewapp", got)
	}
	if got := c.ResolvedLimit(); got != 10 {
		t.Fatalf("limit = %d, want 10", got)
	}
}

func TestReviewAppsOverrides(t *testing.T) {
	c := ReviewAppsConfig{Domain: "reviewapps.example.com", Env: "preview", Limit: 3}
	if !c.Defined() {
		t.Fatal("section with a domain reports undefined")
	}
	if got := c.ResolvedEnv(); got != "preview" {
		t.Fatalf("env = %q, want preview", got)
	}
	if got := c.ResolvedLimit(); got != 3 {
		t.Fatalf("limit = %d, want 3", got)
	}
}

func writeOpsConfig(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ops"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ops", name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFindConfig(t *testing.T) {
	dir := t.TempDir()
	if got := FindConfig(dir); got != "" {
		t.Fatalf("FindConfig(empty) = %q", got)
	}
	writeOpsConfig(t, dir, "config.toml", "")
	if got := FindConfig(dir); got != filepath.Join(dir, ".ops", "config.toml") {
		t.Fatalf("FindConfig(toml only) = %q", got)
	}
	writeOpsConfig(t, dir, "config.yaml", "")
	if got := FindConfig(dir); got != filepath.Join(dir, ".ops", "config.yaml") {
		t.Fatalf("FindConfig(yaml and toml) = %q, want the YAML file", got)
	}
}

func TestReadConfigTOMLAndYAMLMatch(t *testing.T) {
	const toml = `
project = "leopard"

[aws]
region = "us-east-1"

[ecs.defaults]
cpu = 256
memory = 512

[reviewapps]
domain = "reviewapps.example.com"
infra_ssm = "/reviewapp/infra"
tools_image = "repo/reviewapp-tools"
limit = 5
idle_pause = "90m"
`
	const yaml = `
project: leopard
aws:
  region: us-east-1
ecs:
  defaults:
    cpu: 256
    memory: 512
reviewapps:
  domain: reviewapps.example.com
  infra_ssm: /reviewapp/infra
  tools_image: repo/reviewapp-tools
  limit: 5
  idle_pause: 90m
`
	var got [2]OpsConfig
	for i, f := range []struct{ name, body string }{{"config.toml", toml}, {"config.yaml", yaml}} {
		dir := t.TempDir()
		writeOpsConfig(t, dir, f.name, f.body)
		if err := readConfig(viper.New(), FindConfig(dir), &got[i]); err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
	}
	fromTOML, fromYAML := got[0], got[1]
	r := fromTOML.ReviewApps
	if r.Domain != "reviewapps.example.com" || r.InfraSSM != "/reviewapp/infra" || r.ToolsImage != "repo/reviewapp-tools" || r.ResolvedLimit() != 5 || r.IdlePause != 90*time.Minute {
		t.Fatalf("reviewapps from TOML = %+v", r)
	}
	if fromTOML.AWS.Region != "us-east-1" || fromTOML.ECS.Defaults.CPU != 256 || fromTOML.Project != "leopard" {
		t.Fatalf("other blocks from TOML = %+v", fromTOML)
	}
	if fromTOML.ReviewApps != fromYAML.ReviewApps || fromTOML.ECS.Defaults != fromYAML.ECS.Defaults || fromTOML.AWS.Region != fromYAML.AWS.Region {
		t.Fatalf("TOML and YAML differ:\n%+v\n%+v", fromTOML, fromYAML)
	}
}
