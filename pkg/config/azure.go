package config

import (
	"charm.land/log/v2"
	"regexp"
)

type AzureConfig struct {
	Location      string `mapstructure:"location"`
	ResourceGroup string `mapstructure:"resource_group"`
}

func CheckAzureConfig(config *AzureConfig) {
	regionRegex := `^[a-z]+[a-z0-9]*$`
	if matched, _ := regexp.MatchString(regionRegex, config.Location); !matched {
		log.Fatal(
			"Azure location provided does not seem valid.",
			"provider",
			config,
		)
	}

	resourceGroupRegex := `^[a-z0-9-]{1,90}$`
	if matched, _ := regexp.MatchString(resourceGroupRegex, config.ResourceGroup); !matched {
		log.Fatal(
			"Azure resource group provided does not seem valid.",
			"provider",
			config,
		)
	}
}
