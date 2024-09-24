package config

import (
	"errors"
	"github.com/charmbracelet/log"
	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
)

type OpsConfig struct {
	ContainerRegistry ContainerRegistryConfig `yaml:"containerRegistry"`
}

var validate = validator.New()

func NewConfig() *OpsConfig {
	viper.SetConfigType("yaml")
	viper.SetConfigName("config")
	viper.AddConfigPath(".ops")
	viper.SetEnvPrefix("ops")

	if err := viper.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if errors.As(err, &configFileNotFoundError) {
			log.Fatalf("Ops config file not found!")
		}
	}

	var config OpsConfig
	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("Unable to parse config file, %v", err)
	}

	if err := validate.Struct(config); err != nil {
		log.Fatalf("Config validation failed: %s", err)
	}

	ValidateOpsConfig(&config)

	log.Info(
		"Config file loaded!",
		"configFile",
		viper.ConfigFileUsed(),
	)

	return &config
}
