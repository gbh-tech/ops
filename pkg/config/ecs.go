package config

import (
	"fmt"
	"strings"
)

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

// ECSConfig holds all ECS-related settings read from .ops/config.yaml.
//
// Fields that previously had to be repeated verbatim (full ARN prefixes,
// per-account secret prefixes) are now optional: leave them unset and they
// will be derived from the active AWS config. Provide an explicit value only
// when you need to point at a different account or region.
type ECSConfig struct {
	// AppsDir is the root directory containing per-app subdirectories.
	// Only used in mono-repo mode (repo_mode: mono). Defaults to "apps".
	// Top-level `apps_dir` wins over this when both are set.
	AppsDir string `mapstructure:"apps_dir"`

	Cluster          string `mapstructure:"cluster"`
	CapacityProvider string `mapstructure:"capacity_provider"`

	// SecretArnPrefix overrides the derived secrets manager ARN prefix.
	// When empty, defaults to `arn:aws:secretsmanager:{region}:{account}:secret`.
	SecretArnPrefix string `mapstructure:"secret_arn_prefix"`

	// ExecutionRole / TaskRole accept either:
	//   - a full ARN: "arn:aws:iam::123456789012:role/api-stage-task-exec"
	//   - a short role name: "api-stage-task-exec" (the ARN prefix is added)
	// Both support {service} and {env} template placeholders.
	ExecutionRole string `mapstructure:"execution_role"`
	TaskRole      string `mapstructure:"task_role"`

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

// ResolvedSecretArnPrefix returns the explicit prefix when set, otherwise
// derives one from the AWS account and region. Returns an empty string when
// neither is available.
func (e *ECSConfig) ResolvedSecretArnPrefix(aws AWSConfig) string {
	if e.SecretArnPrefix != "" {
		return e.SecretArnPrefix
	}
	if aws.Region == "" || aws.AccountId == "" {
		return ""
	}
	return fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret", aws.Region, aws.AccountId)
}

// ResolvedExecutionRole returns the execution role with the IAM ARN prefix
// added when the configured value is a bare role name.
func (e *ECSConfig) ResolvedExecutionRole(aws AWSConfig) string {
	return ensureIAMRoleARN(e.ExecutionRole, aws.AccountId)
}

// ResolvedTaskRole returns the task role with the IAM ARN prefix added when
// the configured value is a bare role name.
func (e *ECSConfig) ResolvedTaskRole(aws AWSConfig) string {
	return ensureIAMRoleARN(e.TaskRole, aws.AccountId)
}

// ensureIAMRoleARN prepends `arn:aws:iam::{account}:role/` to v unless v is
// empty or already a full ARN. Template placeholders like {service}/{env} are
// preserved untouched and expanded later by the ECS layer.
func ensureIAMRoleARN(v, accountID string) string {
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "arn:") {
		return v
	}
	if accountID == "" {
		return v
	}
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, v)
}
