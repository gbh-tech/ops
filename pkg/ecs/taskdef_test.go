package ecs

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestBuildTaskDefinitionIncludesPrimaryAndAdditionalPorts(t *testing.T) {
	merged := MergedConfig{
		AppSection: AppSection{
			Name:   "gbh-odoo",
			Port:   8069,
			Ports:  []int{8069, 8072},
			CPU:    256,
			Memory: 512,
		},
	}

	input := BuildTaskDefinition(testBaseConfig(), merged, ComputeNames(merged, "production", "cluster"), "production", "sha", nil)
	got := containerMappingPorts(input.ContainerDefinitions[0].PortMappings)
	want := []int{8069, 8072}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("port mappings = %v, want %v", got, want)
	}
}

func TestBuildTaskDefinitionUsesFirstPortForHealthCheckWhenPrimaryPortOmitted(t *testing.T) {
	merged := MergedConfig{
		AppSection: AppSection{
			Name:            "api",
			Ports:           []int{8080, 9090},
			CPU:             256,
			Memory:          512,
			HealthCheckPath: "/health",
		},
	}

	input := BuildTaskDefinition(testBaseConfig(), merged, ComputeNames(merged, "stage", "cluster"), "stage", "sha", nil)
	container := input.ContainerDefinitions[0]
	got := containerMappingPorts(container.PortMappings)
	want := []int{8080, 9090}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("port mappings = %v, want %v", got, want)
	}
	if container.HealthCheck == nil {
		t.Fatal("expected health check to be configured")
	}
	if command := strings.Join(container.HealthCheck.Command, " "); !strings.Contains(command, "localhost:8080/health") {
		t.Fatalf("health check command = %q, want first configured port", command)
	}
}

func TestBuildTaskDefinitionIncludesEntrypointOverride(t *testing.T) {
	merged := MergedConfig{
		AppSection: AppSection{
			Name:       "worker",
			CPU:        256,
			Memory:     512,
			EntryPoint: []string{"/bin/sh", "-c"},
			Command:    []string{"echo ready"},
		},
	}

	input := BuildTaskDefinition(testBaseConfig(), merged, ComputeNames(merged, "stage", "cluster"), "stage", "sha", nil)
	container := input.ContainerDefinitions[0]

	if !reflect.DeepEqual(container.EntryPoint, merged.EntryPoint) {
		t.Fatalf("entrypoint = %v, want %v", container.EntryPoint, merged.EntryPoint)
	}
	if !reflect.DeepEqual(container.Command, merged.Command) {
		t.Fatalf("command = %v, want %v", container.Command, merged.Command)
	}
}

func TestBuildScheduledTaskDefinitionAddsFargateCompatibilityForTaskCapacityProvider(t *testing.T) {
	merged := MergedConfig{
		AppSection: AppSection{
			Name:   "worker",
			CPU:    1024,
			Memory: 2048,
			ScheduledTasks: []ScheduledTaskConfig{
				{
					Name:             "sync",
					Command:          []string{"sync"},
					CapacityProvider: "FARGATE",
				},
			},
		},
	}

	input := BuildScheduledTaskDefinition(testBaseConfig(), merged, ComputeNames(merged, "stage", "cluster"), "stage", "sha", nil)

	if !hasCompatibility(input.RequiresCompatibilities, ecstypes.CompatibilityEc2) {
		t.Fatalf("requiresCompatibilities = %v, want EC2 compatibility preserved", input.RequiresCompatibilities)
	}
	if !hasCompatibility(input.RequiresCompatibilities, ecstypes.CompatibilityFargate) {
		t.Fatalf("requiresCompatibilities = %v, want FARGATE compatibility", input.RequiresCompatibilities)
	}
}

func containerMappingPorts(mappings []ecstypes.PortMapping) []int {
	ports := make([]int, 0, len(mappings))
	for _, mapping := range mappings {
		ports = append(ports, int(aws.ToInt32(mapping.ContainerPort)))
	}
	return ports
}

func hasCompatibility(compatibilities []ecstypes.Compatibility, target ecstypes.Compatibility) bool {
	for _, compatibility := range compatibilities {
		if compatibility == target {
			return true
		}
	}
	return false
}

func testBaseConfig() *BaseConfig {
	return &BaseConfig{
		AWS: BaseAWS{
			ECRUrl: "123456789012.dkr.ecr.us-east-1.amazonaws.com",
			Region: "us-east-1",
		},
	}
}
