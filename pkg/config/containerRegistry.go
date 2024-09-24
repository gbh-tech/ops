package config

import "github.com/charmbracelet/log"

type ContainerRegistryType string

var SupportedRegistryTypes = []ContainerRegistryType{
	"acr",
	"ecr",
	"gcr",
}

type ContainerRegistryConfig struct {
	Type ContainerRegistryType `yaml:"type" validate:"required"`
	URL  string                `yaml:"url" validate:"required"`
}

func ValidateContainerRegistryConfig(config *ContainerRegistryConfig) {
	for _, registryType := range SupportedRegistryTypes {
		if config.Type == registryType {
			return
		}
	}
	log.Fatalf("Invalid Container Registry type: %s", config.Type)
}
