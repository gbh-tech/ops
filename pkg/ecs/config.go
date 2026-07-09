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
	CPU         int    `toml:"cpu"           yaml:"cpu"`
	Memory      int    `toml:"memory"        yaml:"memory"`
	Replicas    int    `toml:"replicas" yaml:"replicas"`
	NetworkMode string `toml:"network_mode"  yaml:"network_mode"`
	LaunchType  string `toml:"launch_type"   yaml:"launch_type"`
	LogDriver   string `toml:"log_driver"    yaml:"log_driver"`
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

	defaultReplicas := base.Defaults.Replicas
	merged := AppSection{
		CPU:         base.Defaults.CPU,
		Memory:      base.Defaults.Memory,
		Replicas:    &defaultReplicas,
		NetworkMode: base.Defaults.NetworkMode,
		LaunchType:  base.Defaults.LaunchType,
		LogDriver:   base.Defaults.LogDriver,
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
	if err := validatePorts(merged); err != nil {
		return MergedConfig{}, err
	}
	if err := validateHealthCheckCommand(merged.ContainerHC); err != nil {
		return MergedConfig{}, err
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

func isNonEmptyHealthCheckConfig(hc HealthCheckConfig) bool {
	hasInterval := hc.Interval != 0
	hasTimeout := hc.Timeout != 0
	hasRetries := hc.Retries != 0
	hasStartPeriod := hc.StartPeriod != 0
	hasCommand := len(hc.Command) > 0
	return hasInterval || hasTimeout || hasRetries || hasStartPeriod || hasCommand
}

func validateHealthCheckCommand(hc HealthCheckConfig) error {
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

func validatePorts(config AppSection) error {
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

// NormalizeSecrets is re-exported from pkg/app for callers that import pkg/ecs.
var NormalizeSecrets = app.NormalizeSecrets

// secretValueFrom builds the ECS ValueFrom string for a single secret ref.
// arnPrefix is the cluster-level prefix (e.g. "arn:aws:secretsmanager:us-east-1:123456789012:secret").
// implicitBase is the full ARN for the service's implicit secret (shared or env path).
// When ref.Secret is non-empty it overrides the implicit base; a bare name is
// appended to arnPrefix, and a full arn:... value is used as-is.
func secretValueFrom(arnPrefix, implicitBase string, ref app.SecretRef) string {
	base := implicitBase
	if ref.Secret != "" {
		if strings.HasPrefix(ref.Secret, "arn:") {
			base = ref.Secret
		} else {
			base = arnPrefix + ":" + ref.Secret
		}
	}
	return base + ":" + ref.Key + "::"
}

// ResolveSecrets builds the ECS secrets list from the consolidated Secrets
// Manager convention:
//
//   - {serviceName}/shared  → keys from app [global].secrets
//   - {serviceName}/{env}   → keys from app [env].secrets (env-specific wins)
//
// Both global and env secrets support external Secrets Manager references via
// inline-table entries: { secret = "other/secret", key = "JSON_KEY" }.
func ResolveSecrets(appCfg AppConfig, env, serviceName, arnPrefix string) ([]ECSSecret, error) {
	globalMap, err := app.NormalizeSecretRefs(appCfg["global"].Secrets)
	if err != nil {
		return nil, fmt.Errorf("global.secrets: %w", err)
	}
	envMap, err := app.NormalizeSecretRefs(appCfg[env].Secrets)
	if err != nil {
		return nil, fmt.Errorf("%s.secrets: %w", env, err)
	}

	sharedARN := arnPrefix + ":" + serviceName + "/shared"
	envARN := arnPrefix + ":" + serviceName + "/" + env

	secrets := []ECSSecret{}

	// Global secrets not overridden by env come from the shared secret.
	for envVar, ref := range globalMap {
		if _, overridden := envMap[envVar]; !overridden {
			secrets = append(secrets, ECSSecret{
				Name:      envVar,
				ValueFrom: secretValueFrom(arnPrefix, sharedARN, ref),
			})
		}
	}
	// Env-specific secrets always come from the env secret.
	for envVar, ref := range envMap {
		secrets = append(secrets, ECSSecret{
			Name:      envVar,
			ValueFrom: secretValueFrom(arnPrefix, envARN, ref),
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

// AppendsEnvironment reports whether ECS service operations target the legacy
// "{name}-{env}" service name instead of the default bare "name".
func (config MergedConfig) AppendsEnvironment() bool {
	return config.AppendEnvironment != nil && *config.AppendEnvironment
}

// ComputeNames derives the ECS family name, service name, CloudWatch log
// group, and scheduled task family from the merged config.
func ComputeNames(config MergedConfig, env, cluster string) Names {
	family := config.Name + "-" + env
	service := config.Name
	if config.AppendsEnvironment() {
		service = family
	}
	logGroup := fmt.Sprintf("/ecs/%s/%s/%s", cluster, env, config.Name)
	return Names{
		Family:          family,
		Service:         service,
		LogGroup:        logGroup,
		ScheduledFamily: family + "-scheduled",
	}
}
