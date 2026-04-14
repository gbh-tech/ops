package config

import "charm.land/log/v2"

type DeploymentProvider string

var SupportedDeploymentProviders = []DeploymentProvider{
	"werf",
	"ansible",
	"ecs",
}

type DeploymentConfig struct {
	Provider DeploymentProvider `mapstructure:"provider"`
}

func CheckDeploymentProviderConfig(config *DeploymentConfig) {
	for _, provider := range SupportedDeploymentProviders {
		if config.Provider == provider {
			return
		}
	}

	log.Fatal(
		"Deployment provider specified is not supported or valid:",
		"provider",
		config.Provider,
	)
}
