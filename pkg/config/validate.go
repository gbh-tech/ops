package config

func ValidateOpsConfig(config *OpsConfig) {
	ValidateContainerRegistryConfig(&config.ContainerRegistry)

}
