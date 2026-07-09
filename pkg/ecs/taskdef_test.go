package ecs

import (
	"strings"
	"testing"

	"ops/pkg/app"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/google/go-cmp/cmp"
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

	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("port mappings mismatch (-got +want):\n%s", diff)
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

	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("port mappings mismatch (-got +want):\n%s", diff)
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
	if diff := cmp.Diff(container.HealthCheck.Command, customCmd); diff != "" {
		t.Fatalf("health check command mismatch (-got +want):\n%s", diff)
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
	if diff := cmp.Diff(container.HealthCheck.Command, customCmd); diff != "" {
		t.Fatalf("health check command mismatch (-got +want):\n%s", diff)
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

	if diff := cmp.Diff(container.EntryPoint, merged.EntryPoint); diff != "" {
		t.Fatalf("entrypoint mismatch (-got +want):\n%s", diff)
	}
	if diff := cmp.Diff(container.Command, merged.Command); diff != "" {
		t.Fatalf("command mismatch (-got +want):\n%s", diff)
	}
}

func TestBuildTaskDefinitionIncludesGPUResourceRequirement(t *testing.T) {
	merged := MergedConfig{
		AppSection: app.AppSection{
			Name:   "vllm",
			CPU:    4096,
			Memory: 14336,
			GPU:    1,
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
	reqs := input.ContainerDefinitions[0].ResourceRequirements
	if len(reqs) != 1 {
		t.Fatalf("resourceRequirements len = %d, want 1", len(reqs))
	}
	if reqs[0].Type != ecstypes.ResourceTypeGpu {
		t.Fatalf("resourceRequirements[0].Type = %v, want GPU", reqs[0].Type)
	}
	if aws.ToString(reqs[0].Value) != "1" {
		t.Fatalf("resourceRequirements[0].Value = %q, want 1", aws.ToString(reqs[0].Value))
	}
}

func TestBuildTaskDefinitionOmitsGPUResourceRequirementWhenZero(t *testing.T) {
	merged := MergedConfig{
		AppSection: app.AppSection{
			Name:   "api",
			CPU:    256,
			Memory: 512,
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
	if len(input.ContainerDefinitions[0].ResourceRequirements) != 0 {
		t.Fatalf("resourceRequirements = %v, want empty", input.ContainerDefinitions[0].ResourceRequirements)
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
