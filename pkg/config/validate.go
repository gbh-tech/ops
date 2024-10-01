package config

func ValidateOpsConfig(config *OpsConfig) {
	ValidateDeploymentProviderConfig(&config.Deployment)
	ValidateContainerRegistryConfig(&config.ContainerRegistry)
}
