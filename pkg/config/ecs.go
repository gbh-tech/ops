package config

// ECSDefaults holds cluster-wide task definition defaults applied before
// per-app config values are merged in.
type ECSDefaults struct {
	CPU          int    `mapstructure:"cpu"`
	Memory       int    `mapstructure:"memory"`
	DesiredCount int    `mapstructure:"desired_count"`
	NetworkMode  string `mapstructure:"network_mode"`
	LaunchType   string `mapstructure:"launch_type"`
	LogDriver    string `mapstructure:"log_driver"`
}

// ECSConfig holds all ECS-related settings read from .ops/config.yaml.
// It covers both the infrastructure details (cluster, IAM roles, etc.) that
// were previously in deploy/base.toml and the app config path pointers.
type ECSConfig struct {
	// App config resolution
	// AppsDir is the root directory containing per-app subdirectories.
	// Only used in mono-repo mode (repo_mode: mono). Defaults to "apps".
	AppsDir string `mapstructure:"apps_dir"`

	// ECS cluster / IAM settings (previously in deploy/base.toml [ecs])
	Cluster          string `mapstructure:"cluster"`
	SecretArnPrefix  string `mapstructure:"secret_arn_prefix"`
	ExecutionRole    string `mapstructure:"execution_role"`
	TaskRole         string `mapstructure:"task_role"`
	CapacityProvider string `mapstructure:"capacity_provider"`

	// Task definition defaults (previously in deploy/base.toml [defaults])
	Defaults ECSDefaults `mapstructure:"defaults"`
}
