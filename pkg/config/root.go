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
	Company string `mapstructure:"company"`
	Project string `mapstructure:"project"`
	Env     string `mapstructure:"env"`

	// Provider is the active cloud provider tie-breaker. Optional; only
	// required when more than one cloud provider block is defined. Can also
	// be supplied via the persistent --provider flag.
	Provider string `mapstructure:"provider"`

	// Deployment is the active deployment tool tie-breaker. Optional; only
	// required when more than one deployment block is defined. Can also be
	// supplied via the persistent --deployment flag.
	Deployment string `mapstructure:"deployment"`

	// RepoMode controls how app config file paths are resolved across all providers.
	// "mono" (default): apps/{app}/deploy/config.<ext>
	// "single":         deploy/config.<ext>
	RepoMode string `mapstructure:"repo_mode"`

	// AppsDir is the root directory containing per-app subdirectories.
	// Only relevant in mono-repo mode. Falls back to ecs.apps_dir, then "apps".
	AppsDir string `mapstructure:"apps_dir"`

	Git GitConfig `mapstructure:"git"`
	K8s K8sConfig `mapstructure:"k8s"`

	// Cloud provider blocks. Defining a block implies that provider is
	// available; `provider:` (or --provider) selects the active one when
	// multiple are present.
	AWS   AWSConfig   `mapstructure:"aws"`
	Azure AzureConfig `mapstructure:"azure"`

	// Deployment provider blocks. `deployment:` (or --deployment) selects the
	// active one when multiple are present.
	ECS  ECSConfig  `mapstructure:"ecs"`
	Werf WerfConfig `mapstructure:"werf"`

	// Registry holds optional overrides; the registry kind and URL are
	// otherwise derived from the active cloud provider.
	Registry RegistryConfig `mapstructure:"registry"`
}

// GitConfig is split out only to keep the root struct readable.
type GitConfig struct {
	DefaultBranch string `mapstructure:"default_branch"`
}

// IsMonoRepo returns true when repo_mode is "mono" or unset (default).
func (c *OpsConfig) IsMonoRepo() bool {
	return c.RepoMode == "" || c.RepoMode == "mono"
}

// AppsDirPath returns the apps directory, checking the top-level apps_dir
// first, then ecs.apps_dir, then defaulting to "apps".
func (c *OpsConfig) AppsDirPath() string {
	if c.AppsDir != "" {
		return c.AppsDir
	}
	if c.ECS.AppsDir != "" {
		return c.ECS.AppsDir
	}
	return "apps"
}

// CloudProvider returns the resolved active cloud provider (e.g. "aws").
// It always reflects the value chosen during LoadConfig, so callers never
// see an empty string here.
func (c *OpsConfig) CloudProvider() string {
	return c.Provider
}

// DeploymentProvider returns the resolved active deployment provider
// (e.g. "ecs", "werf").
func (c *OpsConfig) DeploymentProvider() string {
	return c.Deployment
}

// RegistryType returns the canonical registry kind for the active cloud
// provider (e.g. "ecr" for AWS).
func (c *OpsConfig) RegistryType() string {
	return registryTypeForCloud(c.CloudProvider())
}

// RegistryURL returns the explicit `registry.url` override when set, falling
// back to the URL derived from the active cloud provider's config.
func (c *OpsConfig) RegistryURL() string {
	if c.Registry.URL != "" {
		return c.Registry.URL
	}
	return deriveRegistryURL(c.CloudProvider(), c)
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
			log.Fatal("Ops config file not found")
		} else {
			log.Fatal("Failed to read Ops config file", "err", err)
		}
	}

	if err := viper.Unmarshal(&config); err != nil {
		log.Fatal("Unable to parse Ops config file", "err", err)
	}

	// Resolve the active providers before running per-block validators so
	// downstream code can rely on CloudProvider()/DeploymentProvider().
	config.Provider = resolveProvider(&config)
	config.Deployment = resolveDeployment(&config)

	// Only validate the cloud block we actually care about.
	switch config.Provider {
	case "aws":
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
	case "azure":
		CheckAzureConfig(&config.Azure)
	}

	return &config
}
