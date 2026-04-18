package ecs

import (
	"fmt"

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

// Re-export provider-agnostic types so existing callers of pkg/ecs that use
// AppConfig / AppSection / HealthCheckConfig / LoadFile / LoadAppConfig don't
// need to change their imports.
type AppSection = app.AppSection
type AppConfig = app.AppConfig
type HealthCheckConfig = app.HealthCheckConfig
type ScheduledTaskConfig = app.ScheduledTaskConfig

var LoadFile = app.LoadFile
var LoadAppConfig = app.LoadAppConfig

// BaseAWS holds AWS-level settings from base.toml.
type BaseAWS struct {
	AccountID string `toml:"account_id" yaml:"account_id"`
	Region    string `toml:"region"     yaml:"region"`
	ECRUrl    string `toml:"ecr_url"    yaml:"ecr_url"`
}

// BaseECS holds the ECS platform settings from base.toml.
type BaseECS struct {
	Cluster          string `toml:"cluster"           yaml:"cluster"`
	SecretArnPrefix  string `toml:"secret_arn_prefix" yaml:"secret_arn_prefix"`
	ExecutionRole    string `toml:"execution_role"    yaml:"execution_role"`
	TaskRole         string `toml:"task_role"         yaml:"task_role"`
	CapacityProvider string `toml:"capacity_provider" yaml:"capacity_provider"`
}

// BaseDefaults holds the cluster-wide defaults from base.toml.
type BaseDefaults struct {
	CPU          int    `toml:"cpu"           yaml:"cpu"`
	Memory       int    `toml:"memory"        yaml:"memory"`
	DesiredCount int    `toml:"desired_count" yaml:"desired_count"`
	NetworkMode  string `toml:"network_mode"  yaml:"network_mode"`
	LaunchType   string `toml:"launch_type"   yaml:"launch_type"`
	LogDriver    string `toml:"log_driver"    yaml:"log_driver"`
}

// BaseConfig is the top-level structure of deploy/base.toml.
type BaseConfig struct {
	AWS      BaseAWS      `toml:"aws"      yaml:"aws"`
	ECS      BaseECS      `toml:"ecs"      yaml:"ecs"`
	Defaults BaseDefaults `toml:"defaults" yaml:"defaults"`
}

// MergedConfig is the result of merging base defaults + app global + app env.
type MergedConfig struct {
	AppSection
	SecretsName string
}

// Names holds derived ECS resource names.
type Names struct {
	Family          string
	Service         string
	LogGroup        string
	ScheduledFamily string // "{app}-{env}-scheduled"
}

// ECSSecret is a single entry in the ECS secrets list.
type ECSSecret struct {
	Name      string
	ValueFrom string
}

// ResolveConfig performs the three-layer merge:
//
//	base defaults → app [global] → app [env]
//
// Secrets are excluded here; use ResolveSecrets separately.
// An error is returned when the global section contains volume types that are
// not safe for concurrent multi-host access (host, docker).
func ResolveConfig(base *BaseConfig, appCfg AppConfig, env string) (MergedConfig, error) {
	if err := validateGlobalVolumes(appCfg["global"].Volumes); err != nil {
		return MergedConfig{}, err
	}

	defaultDesiredCount := base.Defaults.DesiredCount
	merged := AppSection{
		CPU:          base.Defaults.CPU,
		Memory:       base.Defaults.Memory,
		DesiredCount: &defaultDesiredCount,
		NetworkMode:  base.Defaults.NetworkMode,
		LaunchType:   base.Defaults.LaunchType,
		LogDriver:    base.Defaults.LogDriver,
	}

	applySection(&merged, appCfg["global"])

	if env != "" && env != "global" {
		applySection(&merged, appCfg[env])
	}

	if merged.Name == "" {
		return MergedConfig{}, fmt.Errorf(
			"app config is missing a required \"name\" field; " +
				"add 'name: <your-app-name>' to the [global] section",
		)
	}

	secretsName := merged.SecretsName
	if secretsName == "" {
		secretsName = merged.Name
	}

	return MergedConfig{AppSection: merged, SecretsName: secretsName}, nil
}

// applySection overlays non-zero fields from src onto dst.
func applySection(dst *AppSection, src AppSection) {
	if src.Name != "" {
		dst.Name = src.Name
	}
	if src.Image != "" {
		dst.Image = src.Image
	}
	if src.Port != 0 {
		dst.Port = src.Port
	}
	if src.CPU != 0 {
		dst.CPU = src.CPU
	}
	if src.Memory != 0 {
		dst.Memory = src.Memory
	}
	if src.DesiredCount != nil {
		dst.DesiredCount = src.DesiredCount
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
	if src.ContainerHC != (HealthCheckConfig{}) {
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

// NormalizeSecrets is re-exported from pkg/app for callers that import pkg/ecs.
var NormalizeSecrets = app.NormalizeSecrets

// ResolveSecrets builds the ECS secrets list from the consolidated Secrets
// Manager convention:
//
//   - {serviceName}/shared  → keys from app [global].secrets
//   - {serviceName}/{env}   → keys from app [env].secrets (env-specific wins)
func ResolveSecrets(appCfg AppConfig, env, serviceName, arnPrefix string) ([]ECSSecret, error) {
	globalMap, err := app.NormalizeSecrets(appCfg["global"].Secrets)
	if err != nil {
		return nil, fmt.Errorf("global.secrets: %w", err)
	}
	envMap, err := app.NormalizeSecrets(appCfg[env].Secrets)
	if err != nil {
		return nil, fmt.Errorf("%s.secrets: %w", env, err)
	}

	sharedARN := fmt.Sprintf("%s:%s/shared", arnPrefix, serviceName)
	envARN := fmt.Sprintf("%s:%s/%s", arnPrefix, serviceName, env)

	var secrets []ECSSecret

	// Global secrets not overridden by env come from the shared secret.
	for envVar, jsonKey := range globalMap {
		if _, overridden := envMap[envVar]; !overridden {
			secrets = append(secrets, ECSSecret{
				Name:      envVar,
				ValueFrom: fmt.Sprintf("%s:%s::", sharedARN, jsonKey),
			})
		}
	}
	// Env-specific secrets always come from the env secret.
	for envVar, jsonKey := range envMap {
		secrets = append(secrets, ECSSecret{
			Name:      envVar,
			ValueFrom: fmt.Sprintf("%s:%s::", envARN, jsonKey),
		})
	}

	return secrets, nil
}

// BuildSecretSpec, ResolveBuildSecretSpecs, and ResolveBuildArgs are re-exported
// from pkg/app. They are provider-agnostic and live there to keep pkg/ecs focused
// on ECS task definitions.
type BuildSecretSpec = app.BuildSecretSpec

var ResolveBuildSecretSpecs = app.ResolveBuildSecretSpecs
var ResolveBuildArgs = app.ResolveBuildArgs

// ComputeNames derives the ECS family name, service name, CloudWatch log
// group, and scheduled task family from the merged config.
func ComputeNames(config MergedConfig, env, cluster string) Names {
	family := fmt.Sprintf("%s-%s", config.Name, env)
	logGroup := fmt.Sprintf("/ecs/%s/%s/%s", cluster, env, config.Name)
	return Names{
		Family:          family,
		Service:         family,
		LogGroup:        logGroup,
		ScheduledFamily: family + "-scheduled",
	}
}
