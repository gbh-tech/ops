package config

import "charm.land/log/v2"

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

func CheckRegistryConfig(config *RegistryConfig) {
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
