package utils

import "testing"

func TestGetFullRegistryRepositoryURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		registry string
		env      string
		project  string
		want     string
	}{
		{
			"123456789012.dkr.ecr.us-east-1.amazonaws.com",
			"stage",
			"my-app",
			"123456789012.dkr.ecr.us-east-1.amazonaws.com/stage/my-app",
		},
		{
			"registry.example.com",
			"production",
			"api",
			"registry.example.com/production/api",
		},
	}
	for _, tt := range tests {
		got := GetFullRegistryRepositoryURL(tt.registry, tt.env, tt.project)
		if got != tt.want {
			t.Fatalf("GetFullRegistryRepositoryURL(%q, %q, %q) = %q, want %q",
				tt.registry, tt.env, tt.project, got, tt.want)
		}
	}
}
