package config

import "testing"

func TestRegistryTypeForCloud(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cloud string
		want  string
	}{
		{"aws", "ecr"},
		{"azure", "acr"},
		{"gcp", "gar"},
		{"unknown", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := registryTypeForCloud(tt.cloud)
		if got != tt.want {
			t.Fatalf("registryTypeForCloud(%q) = %q, want %q", tt.cloud, got, tt.want)
		}
	}
}

func TestDeriveRegistryURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cloud string
		cfg   OpsConfig
		want  string
	}{
		{
			name:  "aws with account and region",
			cloud: "aws",
			cfg:   OpsConfig{AWS: AWSConfig{AccountId: "123456789012", Region: "us-east-1"}},
			want:  "123456789012.dkr.ecr.us-east-1.amazonaws.com",
		},
		{
			name:  "aws missing region",
			cloud: "aws",
			cfg:   OpsConfig{AWS: AWSConfig{AccountId: "123456789012"}},
			want:  "",
		},
		{
			name:  "aws missing account",
			cloud: "aws",
			cfg:   OpsConfig{AWS: AWSConfig{Region: "us-east-1"}},
			want:  "",
		},
		{
			name:  "azure returns empty",
			cloud: "azure",
			cfg:   OpsConfig{},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := deriveRegistryURL(tt.cloud, &tt.cfg)
			if got != tt.want {
				t.Fatalf("deriveRegistryURL(%q) = %q, want %q", tt.cloud, got, tt.want)
			}
		})
	}
}
