package config

import (
	"errors"
	"github.com/charmbracelet/log"
	"github.com/spf13/viper"
)

type OpsConfig struct {
	Deployment        DeploymentConfig        `mapstructure:"deployment"`
	ContainerRegistry ContainerRegistryConfig `mapstructure:"container_registry"`
	Env               string                  `mapstructure:"env"`
	ClusterName       string                  `mapstructure:"cluster_name"`
	CloudProvider     CloudProvider           `mapstructure:"cloud_provider"`
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
