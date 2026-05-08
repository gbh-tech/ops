package ecs_test

import (
	"errors"
	"testing"

	pkgecs "ops/pkg/ecs"
)

func TestInferDBPort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		service string
		want    int
		wantErr error
	}{
		{name: "postgres substring", service: "postgres-db-proxy-stage", want: 5432},
		{name: "POSTGRES case", service: "MY-POSTGRES-PROXY", want: 5432},
		{name: "mysql substring", service: "mysql-db-proxy-stage", want: 3306},
		{name: "redis substring", service: "redis-db-proxy-prod", want: 6379},
		{name: "unknown", service: "mongo-db-proxy-stage", wantErr: pkgecs.ErrUnknownDBPort},
		{name: "empty", service: "", wantErr: pkgecs.ErrUnknownDBPort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := pkgecs.InferDBPort(tt.service)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("InferDBPort(%q) err = %v, want %v", tt.service, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("InferDBPort(%q) unexpected err: %v", tt.service, err)
			}
			if got != tt.want {
				t.Fatalf("InferDBPort(%q) = %d, want %d", tt.service, got, tt.want)
			}
		})
	}
}

func TestFilterDBProxyServiceNames(t *testing.T) {
	t.Parallel()
	in := []string{
		"api-stage",
		"postgres-db-proxy-stage",
		"MySQL-DB-Proxy-stage",
		"redis-proxy-db-proxy",
	}
	got := pkgecs.FilterDBProxyServiceNames(in)
	want := []string{"postgres-db-proxy-stage", "MySQL-DB-Proxy-stage", "redis-proxy-db-proxy"}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d: got %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestServiceNameFromARN(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:ecs:us-east-1:123456789012:service/my-cluster/postgres-db-proxy-stage"
	if got := pkgecs.ServiceNameFromARN(arn); got != "postgres-db-proxy-stage" {
		t.Fatalf("got %q", got)
	}
	if got := pkgecs.ServiceNameFromARN("n-slash"); got != "n-slash" {
		t.Fatalf("no slash: got %q", got)
	}
}

func TestTaskIDFromARN(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abc123deadbeef"
	if got := pkgecs.TaskIDFromARN(arn); got != "abc123deadbeef" {
		t.Fatalf("got %q", got)
	}
}
