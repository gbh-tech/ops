package config

import "github.com/charmbracelet/log"

type RegistryType string

var SupportedRegistries = []RegistryType{
	"acr",
	"ecr",
	"gcr",
}

type RegistryConfig struct {
	Type RegistryType `mapstructure:"type"`
	URL  string       `mapstructure:"url"`
}

func ValidateContainerRegistryConfig(config *RegistryConfig) {
	for _, registryType := range SupportedRegistries {
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
