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

// ECSSchedulerConfig holds EventBridge Scheduler settings used when an app
// declares scheduled_tasks. The IAM role and schedule group must be
// pre-provisioned by Terraform; ops-cli only creates/updates/deletes the
// individual schedule resources inside the group.
type ECSSchedulerConfig struct {
	// RoleArn is the ARN of the IAM role EventBridge Scheduler assumes to call
	// ecs:RunTask. Supports {env} and {cluster} template placeholders.
	// Example: "arn:aws:iam::123456789012:role/ecs-scheduler-{env}"
	RoleArn string `mapstructure:"role_arn"`

	// GroupName is the EventBridge Scheduler schedule group that all
	// ops-managed schedules for this cluster live in. Supports {env} and
	// {cluster} template placeholders.
	// Example: "{cluster}-{env}"
	GroupName string `mapstructure:"group_name"`
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

	// CleanupKeep is the number of task definition revisions to retain per
	// family during automatic cleanup after `ops ecs deploy` and as the
	// default for `ops ecs cleanup`. Applies to both the service family and
	// the dedicated "{app}-{env}-scheduled" family. Defaults to 5 when zero
	// or unset; the `--keep` flag on `ops ecs cleanup` overrides this.
	CleanupKeep int `mapstructure:"cleanup_keep"`

	// Scheduler holds EventBridge Scheduler settings. Required when any app
	// declares scheduled_tasks.
	Scheduler ECSSchedulerConfig `mapstructure:"scheduler"`
}

// EffectiveCleanupKeep returns the configured CleanupKeep, defaulting to 5
// when zero or negative. Centralizes the default so callers don't repeat it.
func (c ECSConfig) EffectiveCleanupKeep() int {
	if c.CleanupKeep <= 0 {
		return 5
	}
	return c.CleanupKeep
}
