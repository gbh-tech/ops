package ecs

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
