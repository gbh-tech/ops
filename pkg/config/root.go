package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"charm.land/log/v2"
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
	RepoMode string     `mapstructure:"repo_mode"`
	Werf     WerfConfig `mapstructure:"werf"`
	// AppsDir is the root directory containing per-app subdirectories.
	// Only relevant in mono-repo mode. Falls back to ecs.apps_dir, then "apps".
	AppsDir string `mapstructure:"apps_dir"`
}

// IsMonoRepo returns true when repo_mode is "mono" or unset (backward-compatible default).
func (c *OpsConfig) IsMonoRepo() bool {
	return c.RepoMode == "" || c.RepoMode == "mono"
}

// AppsDirPath returns the apps directory, checking the top-level apps_dir first,
// then ecs.apps_dir for backward compatibility, then defaulting to "apps".
func (c *OpsConfig) AppsDirPath() string {
	if c.AppsDir != "" {
		return c.AppsDir
	}
	if c.ECS.AppsDir != "" {
		return c.ECS.AppsDir
	}
	return "apps"
}

// ResolveAppFilePath resolves a file path relative to the current app. In
// mono-repo mode, a relative override is scoped under {apps_dir}/{app}/.
// If override is empty, defaultSubpath is used (e.g. "deploy/config.toml").
func (c *OpsConfig) ResolveAppFilePath(app, override, defaultSubpath string) string {
	if override != "" {
		if c.IsMonoRepo() && app != "" && !filepath.IsAbs(override) {
			appRoot := filepath.Join(c.AppsDirPath(), app)
			if !strings.HasPrefix(override, appRoot+string(filepath.Separator)) {
				return filepath.Join(appRoot, override)
			}
		}
		return override
	}
	if c.IsMonoRepo() {
		return filepath.Join(c.AppsDirPath(), app, defaultSubpath)
	}
	return defaultSubpath
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

	if config.AWS.Profile != "" {
		if err := os.Setenv("AWS_PROFILE", config.AWS.Profile); err != nil {
			log.Fatal("Failed to set AWS_PROFILE", "err", err)
		}
	}
	if config.AWS.Region != "" {
		if err := os.Setenv("AWS_REGION", config.AWS.Region); err != nil {
			log.Fatal("Failed to set AWS_REGION", "err", err)
		}
	}
	CheckAWSConfig(&config.AWS)

	// Enable when Azure is supported.
	//CheckAzureConfig(&config.Azure)

	return &config
}
