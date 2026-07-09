package ecs

import (
	"reflect"
	"strings"
	"testing"

	"ops/pkg/app"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestBuildTaskDefinitionIncludesPrimaryAndAdditionalPorts(t *testing.T) {
	merged := MergedConfig{
		AppSection: app.AppSection{
			Name:   "gbh-odoo",
			Port:   8069,
			Ports:  []int{8069, 8072},
			CPU:    256,
			Memory: 512,
		},
	}

	input := BuildTaskDefinition(BuildTaskDefinitionOptions{
		Base:     testBaseConfig(),
		Merged:   merged,
		Names:    ComputeNames(merged, "production", "cluster"),
		Env:      "production",
		ImageTag: "sha",
		Secrets:  nil,
	})
	got := containerMappingPorts(input.ContainerDefinitions[0].PortMappings)
	want := []int{8069, 8072}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("port mappings = %v, want %v", got, want)
	}
}

func TestBuildTaskDefinitionUsesFirstPortForHealthCheckWhenPrimaryPortOmitted(t *testing.T) {
	merged := MergedConfig{
		AppSection: app.AppSection{
			Name:            "api",
			Ports:           []int{8080, 9090},
			CPU:             256,
			Memory:          512,
			HealthCheckPath: "/health",
		},
	}

	input := BuildTaskDefinition(BuildTaskDefinitionOptions{
		Base:     testBaseConfig(),
		Merged:   merged,
		Names:    ComputeNames(merged, "stage", "cluster"),
		Env:      "stage",
		ImageTag: "sha",
		Secrets:  nil,
	})
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

func TestBuildTaskDefinitionUsesCustomHealthCheckCommandWithoutPort(t *testing.T) {
	// A worker with no port and no health_check_path — the only way to get a
	// health check is via a custom command. This was the silent-discard bug.
	customCmd := []string{"CMD", "/bin/healthcheck"}
	merged := MergedConfig{
		AppSection: app.AppSection{
			Name:   "worker",
			CPU:    256,
			Memory: 512,
			ContainerHC: app.HealthCheckConfig{
				Command: customCmd,
			},
		},
	}

	input := BuildTaskDefinition(BuildTaskDefinitionOptions{
		Base:     testBaseConfig(),
		Merged:   merged,
		Names:    ComputeNames(merged, "stage", "cluster"),
		Env:      "stage",
		ImageTag: "sha",
		Secrets:  nil,
	})
	container := input.ContainerDefinitions[0]

	if container.HealthCheck == nil {
		t.Fatal("expected health check to be configured; custom command was silently dropped")
	}
	if !reflect.DeepEqual(container.HealthCheck.Command, customCmd) {
		t.Fatalf("health check command = %v, want %v", container.HealthCheck.Command, customCmd)
	}
}

func TestBuildTaskDefinitionUsesCustomHealthCheckCommandOverridesCurl(t *testing.T) {
	// Even when health_check_path and port are set, an explicit command wins.
	customCmd := []string{"CMD-SHELL", "wget -q -O /dev/null http://localhost:8080/health || exit 1"}
	merged := MergedConfig{
		AppSection: app.AppSection{
			Name:            "api",
			Port:            8080,
			CPU:             256,
			Memory:          512,
			HealthCheckPath: "/health",
			ContainerHC: app.HealthCheckConfig{
				Command: customCmd,
			},
		},
	}

	input := BuildTaskDefinition(BuildTaskDefinitionOptions{
		Base:     testBaseConfig(),
		Merged:   merged,
		Names:    ComputeNames(merged, "stage", "cluster"),
		Env:      "stage",
		ImageTag: "sha",
		Secrets:  nil,
	})
	container := input.ContainerDefinitions[0]

	if container.HealthCheck == nil {
		t.Fatal("expected health check to be configured")
	}
	if !reflect.DeepEqual(container.HealthCheck.Command, customCmd) {
		t.Fatalf("health check command = %v, want %v", container.HealthCheck.Command, customCmd)
	}
}

func TestBuildTaskDefinitionIncludesEntrypointOverride(t *testing.T) {
	merged := MergedConfig{
		AppSection: app.AppSection{
			Name:       "worker",
			CPU:        256,
			Memory:     512,
			EntryPoint: []string{"/bin/sh", "-c"},
			Command:    []string{"echo ready"},
		},
	}

	input := BuildTaskDefinition(BuildTaskDefinitionOptions{
		Base:     testBaseConfig(),
		Merged:   merged,
		Names:    ComputeNames(merged, "stage", "cluster"),
		Env:      "stage",
		ImageTag: "sha",
		Secrets:  nil,
	})
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
		AppSection: app.AppSection{
			Name:   "worker",
			CPU:    1024,
			Memory: 2048,
			ScheduledTasks: []app.ScheduledTaskConfig{
				{
					Name:             "sync",
					Command:          []string{"sync"},
					CapacityProvider: "FARGATE",
				},
			},
		},
	}

	input := BuildScheduledTaskDefinition(BuildTaskDefinitionOptions{
		Base:     testBaseConfig(),
		Merged:   merged,
		Names:    ComputeNames(merged, "stage", "cluster"),
		Env:      "stage",
		ImageTag: "sha",
		Secrets:  nil,
	})

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
