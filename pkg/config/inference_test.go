package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDefinedCloudBlocks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  OpsConfig
		want []string
	}{
		{
			name: "aws only",
			cfg:  OpsConfig{AWS: AWSConfig{Region: "us-east-1"}},
			want: []string{"aws"},
		},
		{
			name: "azure only",
			cfg:  OpsConfig{Azure: AzureConfig{Location: "eastus"}},
			want: []string{"azure"},
		},
		{
			name: "aws and azure",
			cfg: OpsConfig{
				AWS:   AWSConfig{AccountId: "123456789012"},
				Azure: AzureConfig{ResourceGroup: "rg-prod"},
			},
			want: []string{"aws", "azure"},
		},
		{
			name: "none",
			cfg:  OpsConfig{},
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := definedCloudBlocks(&tt.cfg)
			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Fatalf("definedCloudBlocks() mismatch (-got +want):\n%s", diff)
			}
		})
	}
}

func TestDefinedDeploymentBlocks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  OpsConfig
		want []string
	}{
		{
			name: "ecs only",
			cfg:  OpsConfig{ECS: ECSConfig{Cluster: "my-cluster"}},
			want: []string{"ecs"},
		},
		{
			name: "werf only via services",
			cfg:  OpsConfig{Werf: WerfConfig{Services: []string{"app"}}},
			want: []string{"werf"},
		},
		{
			name: "none",
			cfg:  OpsConfig{},
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := definedDeploymentBlocks(&tt.cfg)
			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Fatalf("definedDeploymentBlocks() mismatch (-got +want):\n%s", diff)
			}
		})
	}
}
