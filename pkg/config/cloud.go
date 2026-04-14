package config

import (
	"slices"

	"charm.land/log/v2"
)

type CloudConfig struct {
	Provider string `mapstructure:"provider"`
}

var SupportedCloudProviders = []string{
	"aws",
	"gcp",
	"azure",
}

func CheckCloudConfig(config *CloudConfig) {
	if slices.Contains(SupportedCloudProviders, config.Provider) {
		return
	}

	log.Fatal(
		"Cloud provider is not supported or valid.",
		"provider",
		config,
	)
}
