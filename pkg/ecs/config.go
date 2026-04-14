package ecs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// HealthCheckConfig mirrors the container_health_check section.
type HealthCheckConfig struct {
	Interval    int `toml:"interval"    yaml:"interval"`
	Timeout     int `toml:"timeout"     yaml:"timeout"`
	Retries     int `toml:"retries"     yaml:"retries"`
	StartPeriod int `toml:"start_period" yaml:"start_period"`
}

// AppSection is a single named section within an app config (global, stage,
// production, etc.). Secrets can be a list of strings or a map of
// env-var → json-key; both forms normalise to a map via NormalizeSecrets.
type AppSection struct {
	Name               string            `toml:"name"                yaml:"name"`
	Image              string            `toml:"image"               yaml:"image"`
	Port               int               `toml:"port"                yaml:"port"`
	CPU                int               `toml:"cpu"                 yaml:"cpu"`
	Memory             int               `toml:"memory"              yaml:"memory"`
	DesiredCount       *int              `toml:"desired_count"       yaml:"desired_count"`
	NetworkMode        string            `toml:"network_mode"        yaml:"network_mode"`
	LaunchType         string            `toml:"launch_type"         yaml:"launch_type"`
	LogDriver          string            `toml:"log_driver"          yaml:"log_driver"`
	HealthCheckPath    string            `toml:"health_check_path"   yaml:"health_check_path"`
	ContainerHC        HealthCheckConfig `toml:"container_health_check" yaml:"container_health_check"`
	DatabaseMigrations bool              `toml:"database_migrations" yaml:"database_migrations"`
	MigrationCommand   []string          `toml:"migration_command"   yaml:"migration_command"`
	SecretsName        string            `toml:"secrets_name"        yaml:"secrets_name"`
	ExecutionRole      string            `toml:"execution_role"      yaml:"execution_role"`
	TaskRole           string            `toml:"task_role"           yaml:"task_role"`
	Command            []string          `toml:"command"             yaml:"command"`
	Environment        map[string]string `toml:"environment"         yaml:"environment"`

	// Secrets is intentionally interface{} to handle both list and map forms.
	// Use NormalizeSecrets() to get a canonical map[string]string.
	Secrets interface{} `toml:"secrets" yaml:"secrets"`
}

// AppConfig is the top-level structure of an app's config.toml / config.yaml.
// Keys are section names: "global", "stage", "production", etc.
type AppConfig map[string]AppSection

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
	CPU          int    `toml:"cpu"          yaml:"cpu"`
	Memory       int    `toml:"memory"       yaml:"memory"`
	DesiredCount int    `toml:"desired_count" yaml:"desired_count"`
	NetworkMode  string `toml:"network_mode" yaml:"network_mode"`
	LaunchType   string `toml:"launch_type"  yaml:"launch_type"`
	LogDriver    string `toml:"log_driver"   yaml:"log_driver"`
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
	Family   string
	Service  string
	LogGroup string
}

// ECSSecret is a single entry in the ECS secrets list.
type ECSSecret struct {
	Name      string
	ValueFrom string
}

// LoadFile reads path and unmarshals it into out. The file extension
// determines the parser: .toml uses BurntSushi/toml, .yaml/.yml uses
// gopkg.in/yaml.v3. Any other extension returns an error.
func LoadFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".toml":
		if err := toml.Unmarshal(data, out); err != nil {
			return fmt.Errorf("parsing TOML %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, out); err != nil {
			return fmt.Errorf("parsing YAML %s: %w", path, err)
		}
	default:
		return fmt.Errorf("unsupported config format %q (use .toml or .yaml)", ext)
	}
	return nil
}

// LoadAppConfig reads an app config file (TOML or YAML by extension).
func LoadAppConfig(path string) (AppConfig, error) {
	var app AppConfig
	if err := LoadFile(path, &app); err != nil {
		return nil, err
	}
	return app, nil
}

// ResolveConfig performs the three-layer merge:
//
//	base defaults → app [global] → app [env]
//
// Secrets are excluded here; use ResolveSecrets separately.
func ResolveConfig(base *BaseConfig, app AppConfig, env string) MergedConfig {
	defaultDesiredCount := base.Defaults.DesiredCount
	merged := AppSection{
		CPU:          base.Defaults.CPU,
		Memory:       base.Defaults.Memory,
		DesiredCount: &defaultDesiredCount,
		NetworkMode:  base.Defaults.NetworkMode,
		LaunchType:   base.Defaults.LaunchType,
		LogDriver:    base.Defaults.LogDriver,
	}

	applySection(&merged, app["global"])

	if env != "" && env != "global" {
		applySection(&merged, app[env])
	}

	secretsName := merged.SecretsName
	if secretsName == "" {
		secretsName = merged.Name
	}

	return MergedConfig{AppSection: merged, SecretsName: secretsName}
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
	// Secrets are merged separately in ResolveSecrets.
}

// NormalizeSecrets converts the secrets field (which may be a []string or
// map[string]string) into a canonical map[envVar]jsonKey.
func NormalizeSecrets(raw interface{}) map[string]string {
	if raw == nil {
		return nil
	}
	result := make(map[string]string)

	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			if key, ok := item.(string); ok {
				result[key] = key
			}
		}
	case map[string]interface{}:
		for envVar, jsonKey := range v {
			if k, ok := jsonKey.(string); ok {
				result[envVar] = k
			}
		}
	case []string:
		for _, key := range v {
			result[key] = key
		}
	case map[string]string:
		for k, vv := range v {
			result[k] = vv
		}
	}
	return result
}

// ResolveSecrets builds the ECS secrets list from the consolidated Secrets
// Manager convention:
//
//   - {serviceName}/shared  → keys from app [global].secrets
//   - {serviceName}/{env}   → keys from app [env].secrets (env-specific wins)
func ResolveSecrets(app AppConfig, env, serviceName, arnPrefix string) []ECSSecret {
	globalMap := NormalizeSecrets(app["global"].Secrets)
	envMap := NormalizeSecrets(app[env].Secrets)

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

	return secrets
}

// ComputeNames derives the ECS family name, service name, and CloudWatch log
// group from the merged config.
func ComputeNames(config MergedConfig, env, cluster string) Names {
	family := fmt.Sprintf("%s-%s", config.Name, env)
	logGroup := fmt.Sprintf("/ecs/%s/%s/%s", cluster, env, config.Name)
	return Names{Family: family, Service: family, LogGroup: logGroup}
}
