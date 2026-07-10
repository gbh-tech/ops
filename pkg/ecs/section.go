package ecs

import (
	"fmt"
	"strings"

	"ops/pkg/app"
)

// validateGlobalVolumes rejects host-local volume types in the [global] app
// config section. Global volumes are applied to every environment and every
// task replica; only network-attached shared file systems (EFS) support safe
// concurrent multi-writer access across hosts.
func validateGlobalVolumes(volumes []app.VolumeConfig) error {
	for _, v := range volumes {
		if v.Host != nil || v.Docker != nil {
			return fmt.Errorf(
				"volume %q uses a host-local type (host/docker) which is not safe "+
					"for the [global] config section: tasks on different EC2 instances "+
					"get independent storage with no shared access; "+
					"move this volume to a per-environment section instead",
				v.Name,
			)
		}
	}
	return nil
}

// applySection overlays non-zero fields from src onto dst.
func applySection(dst *app.AppSection, src app.AppSection) {
	if src.Name != "" {
		dst.Name = src.Name
	}
	if src.AppendEnvironment != nil {
		dst.AppendEnvironment = src.AppendEnvironment
	}
	if src.Image != "" {
		dst.Image = src.Image
	}
	if src.Port != 0 {
		dst.Port = src.Port
	}
	if len(src.Ports) > 0 {
		dst.Ports = src.Ports
	}
	if src.CPU != 0 {
		dst.CPU = src.CPU
	}
	if src.Memory != 0 {
		dst.Memory = src.Memory
	}
	if src.GPU != nil {
		dst.GPU = src.GPU
	}
	if src.Replicas != nil {
		dst.Replicas = src.Replicas
	}
	if src.NetworkMode != "" {
		dst.NetworkMode = src.NetworkMode
	}
	if src.LaunchType != "" {
		dst.LaunchType = src.LaunchType
	}
	if src.LogDriver != "" {
		dst.LogDriver = src.LogDriver
	}
	if src.HealthCheckPath != "" {
		dst.HealthCheckPath = src.HealthCheckPath
	}
	if isNonEmptyHealthCheckConfig(src.ContainerHC) {
		dst.ContainerHC = src.ContainerHC
	}
	if src.DatabaseMigrations {
		dst.DatabaseMigrations = true
	}
	if len(src.MigrationCommand) > 0 {
		dst.MigrationCommand = src.MigrationCommand
	}
	if src.SecretsName != "" {
		dst.SecretsName = src.SecretsName
	}
	if src.ExecutionRole != "" {
		dst.ExecutionRole = src.ExecutionRole
	}
	if src.TaskRole != "" {
		dst.TaskRole = src.TaskRole
	}
	if len(src.EntryPoint) > 0 {
		dst.EntryPoint = src.EntryPoint
	}
	if len(src.Command) > 0 {
		dst.Command = src.Command
	}
	if len(src.Environment) > 0 {
		if dst.Environment == nil {
			dst.Environment = make(map[string]string)
		}
		for k, v := range src.Environment {
			dst.Environment[k] = v
		}
	}
	// Secrets, BuildSecrets, and BuildArgs are resolved by their own functions
	// (ResolveSecrets, ResolveBuildSecretSpecs, ResolveBuildArgs) and are
	// intentionally omitted here.

	// Volumes replace rather than merge: the more-specific section wins entirely.
	if len(src.Volumes) > 0 {
		dst.Volumes = src.Volumes
	}

	// ScheduledTasks replace rather than merge: the more-specific section wins
	// entirely. This keeps reconciliation predictable — the config is always
	// the single source of truth for what schedules should exist.
	if len(src.ScheduledTasks) > 0 {
		dst.ScheduledTasks = src.ScheduledTasks
	}
}

func isNonEmptyHealthCheckConfig(hc app.HealthCheckConfig) bool {
	hasInterval := hc.Interval != 0
	hasTimeout := hc.Timeout != 0
	hasRetries := hc.Retries != 0
	hasStartPeriod := hc.StartPeriod != 0
	hasCommand := len(hc.Command) > 0
	return hasInterval || hasTimeout || hasRetries || hasStartPeriod || hasCommand
}

func validateHealthCheckCommand(hc app.HealthCheckConfig) error {
	if len(hc.Command) == 0 {
		return nil
	}
	switch hc.Command[0] {
	case "CMD", "CMD-SHELL":
		if len(hc.Command) < 2 {
			return fmt.Errorf(
				"container_health_check.command must include a mode token and at least one argument, got %v\n"+
					"hint: [\"CMD-SHELL\", \"curl -f http://localhost:8080/health || exit 1\"] or [\"CMD\", \"/bin/healthcheck\"]",
				hc.Command,
			)
		}
		return nil
	default:
		return fmt.Errorf(
			"container_health_check.command[0] must be \"CMD\" or \"CMD-SHELL\", got %q\n"+
				"hint: ECS requires the first element to declare the execution mode:\n"+
				"  CMD-SHELL runs via /bin/sh -c: [\"CMD-SHELL\", \"curl -f http://localhost:8080/health || exit 1\"]\n"+
				"  CMD uses exec form (no shell):  [\"CMD\", \"/bin/healthcheck\"]",
			hc.Command[0],
		)
	}
}

func validatePorts(config app.AppSection) error {
	for _, port := range append([]int{config.Port}, config.Ports...) {
		if port == 0 {
			continue
		}
		if port < 1 || port > 65535 {
			return fmt.Errorf("port %d is outside the valid TCP port range 1-65535", port)
		}
	}
	return nil
}

func validateGPU(config app.AppSection) error {
	if config.GPU == nil {
		return nil
	}
	if *config.GPU < 0 {
		return fmt.Errorf("gpu must be >= 0, got %d", *config.GPU)
	}
	if *config.GPU > 0 && strings.EqualFold(config.LaunchType, "FARGATE") {
		return fmt.Errorf("gpu is not supported with launch_type FARGATE")
	}
	return nil
}
