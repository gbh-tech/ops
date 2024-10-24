package config

import (
	"errors"
	"github.com/charmbracelet/log"
	"github.com/spf13/viper"
)

type OpsConfig struct {
	CloudProvider     CloudProvider           `mapstructure:"cloud_provider"`
	ClusterName       string                  `mapstructure:"cluster_name"`
	ContainerRegistry ContainerRegistryConfig `mapstructure:"container_registry"`
	Deployment        DeploymentConfig        `mapstructure:"deployment"`
	Env               string                  `mapstructure:"env"`
	Werf              WerfConfig              `mapstructure:"werf"`
}

var config OpsConfig

func NewConfig() *OpsConfig {
	viper.SetConfigType("yaml")
	viper.SetConfigName("config")
	viper.AddConfigPath(".ops")
	viper.SetEnvPrefix("ops")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if errors.As(err, &configFileNotFoundError) {
			log.Fatalf("Ops config file not found!")
		}
	}

	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("Unable to parse config file, %v", err)
	}

	ValidateOpsConfig(&config)

	log.Info(
		"Config file loaded!",
		"configFile",
		viper.ConfigFileUsed(),
	)

	return &config
}

func NewWerfConfig() *WerfConfig {
	viper.SetConfigType("yaml")
	viper.SetConfigName("config")
	viper.AddConfigPath(".ops")
	viper.SetEnvPrefix("ops")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if errors.As(err, &configFileNotFoundError) {
			log.Fatalf("Ops config file not found!")
		}
	}

	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("Unable to parse config file, %v", err)
	}

	return &config.Werf
}
