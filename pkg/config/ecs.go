package config

// ECSDefaults holds cluster-wide task definition defaults applied before
// per-app config values are merged in.
type ECSDefaults struct {
	CPU      int `mapstructure:"cpu"`
	Memory   int `mapstructure:"memory"`
	Replicas int `mapstructure:"replicas"`
	// Deprecated: use Replicas instead. Kept for backward compatibility.
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
