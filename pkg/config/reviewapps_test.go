package config

import "testing"

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
