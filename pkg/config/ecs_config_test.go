package config

import "testing"

func TestECSConfigResolvedCluster(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  ECSConfig
		env  string
		want string
	}{
		{
			name: "expands env placeholder",
			cfg:  ECSConfig{Cluster: "lighthouse-platform-{env}"},
			env:  "stage",
			want: "lighthouse-platform-stage",
		},
		{
			name: "returns as-is without placeholder",
			cfg:  ECSConfig{Cluster: "lighthouse-platform"},
			env:  "prod",
			want: "lighthouse-platform",
		},
		{
			name: "empty env returns configured value",
			cfg:  ECSConfig{Cluster: "lighthouse-platform-{env}"},
			env:  "",
			want: "lighthouse-platform-{env}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.cfg.ResolvedCluster(tt.env); got != tt.want {
				t.Fatalf("ResolvedCluster(%q) = %q, want %q", tt.env, got, tt.want)
			}
		})
	}
}
