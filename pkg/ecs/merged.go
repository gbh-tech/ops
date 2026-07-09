package ecs

import (
	"fmt"
	"strings"

	"ops/pkg/app"
)

// MergedConfig is the result of merging base defaults + app global + app env.
type MergedConfig struct {
	app.AppSection
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
func ResolveConfig(base *BaseConfig, appCfg app.AppConfig, env string) (MergedConfig, error) {
	if err := validateGlobalVolumes(appCfg["global"].Volumes); err != nil {
		return MergedConfig{}, err
	}

	defaultReplicas := base.Defaults.Replicas
	merged := app.AppSection{
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
	if err := validateGPU(merged); err != nil {
		return MergedConfig{}, err
	}

	secretsName := merged.SecretsName
	if secretsName == "" {
		secretsName = merged.Name
	}

	return MergedConfig{AppSection: merged, SecretsName: secretsName}, nil
}

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
func ResolveSecrets(appCfg app.AppConfig, env, serviceName, arnPrefix string) ([]ECSSecret, error) {
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
