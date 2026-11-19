package ecs

import "ops/pkg/config"

// BaseAWS holds AWS-level settings from base.toml.
type BaseAWS struct {
	AccountID          string `toml:"account_id"          yaml:"account_id"`
	Region             string `toml:"region"              yaml:"region"`
	ECRUrl             string `toml:"ecr_url"             yaml:"ecr_url"`
	RegistryRepository string `toml:"registry_repository" yaml:"registry_repository"`
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

// baseReplicas returns the effective replica count from ECSDefaults,
// preferring the new replicas key over the deprecated desired_count fallback.
func baseReplicas(d config.ECSDefaults) int {
	if d.Replicas != 0 {
		return d.Replicas
	}
	return d.DesiredCount
}

// BaseConfigFor assembles a BaseConfig from the ops config. This is the single
// place that maps OpsConfig fields to BaseConfig fields, so additions only
// need one edit.
func BaseConfigFor(cfg *config.OpsConfig, env string) *BaseConfig {
	return &BaseConfig{
		AWS: BaseAWS{
			AccountID:          cfg.AWS.AccountId,
			Region:             cfg.AWS.Region,
			ECRUrl:             cfg.RegistryURL(),
			RegistryRepository: cfg.RegistryRepository(),
		},
		ECS: BaseECS{
			Cluster:          cfg.ECS.ResolvedCluster(env),
			SecretArnPrefix:  cfg.ECS.ResolvedSecretArnPrefix(cfg.AWS),
			ExecutionRole:    cfg.ECS.ResolvedExecutionRole(cfg.AWS),
			TaskRole:         cfg.ECS.ResolvedTaskRole(cfg.AWS),
			CapacityProvider: cfg.ECS.CapacityProvider,
		},
		Defaults: BaseDefaults{
			CPU:         cfg.ECS.Defaults.CPU,
			Memory:      cfg.ECS.Defaults.Memory,
			Replicas:    baseReplicas(cfg.ECS.Defaults),
			NetworkMode: cfg.ECS.Defaults.NetworkMode,
			LaunchType:  cfg.ECS.Defaults.LaunchType,
			LogDriver:   cfg.ECS.Defaults.LogDriver,
		},
	}
}
