package config

import "fmt"

// RegistryConfig holds optional overrides for the container image registry.
// In normal use the registry kind and URL are derived from the active cloud
// provider (e.g. AWS → ECR with URL `{account_id}.dkr.ecr.{region}.amazonaws.com`).
// Set `registry.url` only when pulling/pushing to a registry in a different
// account or region.
type RegistryConfig struct {
	URL string `mapstructure:"url"`
}

// registryTypeForCloud returns the canonical registry kind for a cloud provider.
func registryTypeForCloud(cloud string) string {
	switch cloud {
	case "aws":
		return "ecr"
	case "azure":
		return "acr"
	case "gcp":
		return "gar"
	}
	return ""
}

// deriveRegistryURL builds the default registry URL for a given cloud provider
// from the provider's own config. Returns an empty string when the required
// fields are missing; callers should validate.
func deriveRegistryURL(cloud string, config *OpsConfig) string {
	switch cloud {
	case "aws":
		if config.AWS.AccountId == "" || config.AWS.Region == "" {
			return ""
		}
		return fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", config.AWS.AccountId, config.AWS.Region)
	}
	return ""
}
