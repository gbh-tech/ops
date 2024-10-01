package config

import "github.com/charmbracelet/log"

type ContainerRegistryType string

var SupportedRegistryTypes = []ContainerRegistryType{
	"acr",
	"ecr",
	"gcr",
}

type ContainerRegistryConfig struct {
	Type ContainerRegistryType `mapstructure:"type"`
	URL  string                `mapstructure:"url"`
}

func ValidateContainerRegistryConfig(config *ContainerRegistryConfig) {
	for _, registryType := range SupportedRegistryTypes {
		if config.Type == registryType {
			return
		}
	}

	log.Fatal(
		"Container registry specified is not supported or valid:",
		"registry",
		config.Type,
	)
}
