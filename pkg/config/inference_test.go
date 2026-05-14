package config

import (
	"reflect"
	"testing"
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
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := definedCloudBlocks(&tt.cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("definedCloudBlocks() = %v, want %v", got, tt.want)
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
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := definedDeploymentBlocks(&tt.cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("definedDeploymentBlocks() = %v, want %v", got, tt.want)
			}
		})
	}
}
