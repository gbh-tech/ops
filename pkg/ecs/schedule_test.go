package ecs

import (
	"testing"

	"ops/pkg/app"
)

func TestResolveScheduledTaskCapacityProviderUsesDefaultProvider(t *testing.T) {
	task := app.ScheduledTaskConfig{Name: "sync"}

	got := ResolveScheduledTaskCapacityProvider(task, "ec2-{service}-{env}", "website", "stage")
	want := "ec2-website-stage"

	if got != want {
		t.Fatalf("capacity provider = %q, want %q", got, want)
	}
}

func TestResolveScheduledTaskCapacityProviderPrefersTaskOverride(t *testing.T) {
	task := app.ScheduledTaskConfig{
		Name:             "sync",
		CapacityProvider: "FARGATE_SPOT",
	}

	got := ResolveScheduledTaskCapacityProvider(task, "ec2-{service}-{env}", "website", "stage")
	want := "FARGATE_SPOT"

	if got != want {
		t.Fatalf("capacity provider = %q, want %q", got, want)
	}
}
