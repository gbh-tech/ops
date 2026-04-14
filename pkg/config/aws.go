package config

import (
	"charm.land/log/v2"
	"regexp"
)

type AWSConfig struct {
	Region    string `mapstructure:"region"`
	AccountId string `mapstructure:"account_id"`
	Profile   string `mapstructure:"profile"`
}

func CheckAWSConfig(config *AWSConfig) {
	regionRegex := `^([a-z]{2}-[a-z]+-\d{1})$`
	if matched, _ := regexp.MatchString(regionRegex, config.Region); !matched {
		log.Fatal(
			"AWS region provided does not seem valid.",
			"provider",
			config,
		)
	}

	accountIdRegex := `^\d{12}$`
	if matched, _ := regexp.MatchString(accountIdRegex, config.AccountId); !matched {
		log.Fatal(
			"AWS account ID provided does not seem valid.",
			"provider",
			config,
		)
	}
}
