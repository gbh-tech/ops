package config

import (
	"errors"

	"github.com/charmbracelet/log"
	"github.com/spf13/viper"
)

type OpsConfig struct {
	AWS        AWSConfig        `mapstructure:"aws"`
	Azure      AzureConfig      `mapstructure:"azure"`
	Cloud      CloudConfig      `mapstructure:"cloud"`
	Company    string           `mapstructure:"company"`
	Deployment DeploymentConfig `mapstructure:"deployment"`
	ECS        ECSConfig        `mapstructure:"ecs"`
	Env        string           `mapstructure:"env"`
	K8s        K8sConfig        `mapstructure:"k8s"`
	Project    string           `mapstructure:"project"`
	Registry   RegistryConfig   `mapstructure:"registry"`
	// RepoMode controls how app config file paths are resolved across all providers.
	// "mono" (default): apps/{app}/deploy/config.<ext>
	// "single":         deploy/config.<ext>
	RepoMode string           `mapstructure:"repo_mode"`
	Werf     WerfConfig       `mapstructure:"werf"`
}

// IsMonoRepo returns true when repo_mode is "mono" or unset (backward-compatible default).
func (c *OpsConfig) IsMonoRepo() bool {
	return c.RepoMode == "" || c.RepoMode == "mono"
}

var config OpsConfig

func LoadConfig() *OpsConfig {
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
		log.Fatalf("Unable to parse Ops config file, %v", err)
	}

	CheckDeploymentProviderConfig(&config.Deployment)
	CheckRegistryConfig(&config.Registry)
	CheckCloudConfig(&config.Cloud)

	viper.SetDefault("AWS_PROFILE", config.AWS.Profile)
	viper.SetDefault("AWS_REGION", config.AWS.Region)
	CheckAWSConfig(&config.AWS)

	// Enable when Azure is supported.
	//CheckAzureConfig(&config.Azure)

	return &config
}
