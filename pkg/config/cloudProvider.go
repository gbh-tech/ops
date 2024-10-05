package config

import (
	"github.com/charmbracelet/log"
	"slices"
)

type CloudProvider string

var SupportedCloudProviders = []CloudProvider{
	"aws",
	"gcp",
	"azure",
}

func ValidateCloudProvider(provider *CloudProvider) {
	if slices.Contains(SupportedCloudProviders, *provider) {
		return
	}

	log.Fatal(
		"Cloud provider is not supported or valid:",
		"provider",
		provider,
	)
}
