package config

import (
	"slices"

	"charm.land/log/v2"
)

// CurrentConfig selects the active cloud and deployment providers when more
// than one provider block is defined in .ops/config.yaml. When only a single
// provider block is present (e.g. only `aws:` or only `ecs:`), inference fills
// these in automatically and explicit values are not required.
//
// Both fields can also be overridden at invocation time via the persistent
// `--current-cloud` and `--current-deployment` CLI flags.
type CurrentConfig struct {
	Cloud      string `mapstructure:"cloud"`
	Deployment string `mapstructure:"deployment"`
}

var SupportedCloudProviders = []string{
	"aws",
	"azure",
	"gcp",
}

var SupportedDeploymentProviders = []string{
	"ecs",
	"werf",
	"ansible",
}

// resolveCurrentCloud picks the active cloud provider. Precedence:
//  1. explicit `current.cloud` (or --current-cloud flag, bound to the same key)
//  2. inferred from the single non-empty provider block
//
// Fatals when ambiguous (multiple blocks, no explicit selection) or when the
// resolved value is not a supported provider.
func resolveCurrentCloud(c *OpsConfig) string {
	if c.Current.Cloud != "" {
		if !slices.Contains(SupportedCloudProviders, c.Current.Cloud) {
			log.Fatal(
				"current.cloud is not a supported provider",
				"value", c.Current.Cloud,
				"supported", SupportedCloudProviders,
			)
		}
		return c.Current.Cloud
	}

	defined := definedCloudBlocks(c)
	switch len(defined) {
	case 0:
		log.Fatal("No cloud provider block defined. Add an `aws:` (or `azure:`, `gcp:`) section to .ops/config.yaml.")
	case 1:
		return defined[0]
	default:
		log.Fatal(
			"Multiple cloud provider blocks defined; set `current.cloud` in .ops/config.yaml or pass --current-cloud.",
			"defined", defined,
		)
	}
	return ""
}

// resolveCurrentDeployment mirrors resolveCurrentCloud for deployment providers.
func resolveCurrentDeployment(c *OpsConfig) string {
	if c.Current.Deployment != "" {
		if !slices.Contains(SupportedDeploymentProviders, c.Current.Deployment) {
			log.Fatal(
				"current.deployment is not a supported provider",
				"value", c.Current.Deployment,
				"supported", SupportedDeploymentProviders,
			)
		}
		return c.Current.Deployment
	}

	defined := definedDeploymentBlocks(c)
	switch len(defined) {
	case 0:
		log.Fatal("No deployment provider block defined. Add an `ecs:` or `werf:` section to .ops/config.yaml.")
	case 1:
		return defined[0]
	default:
		log.Fatal(
			"Multiple deployment provider blocks defined; set `current.deployment` in .ops/config.yaml or pass --current-deployment.",
			"defined", defined,
		)
	}
	return ""
}

func definedCloudBlocks(c *OpsConfig) []string {
	var out []string
	if c.AWS.Region != "" || c.AWS.AccountId != "" {
		out = append(out, "aws")
	}
	if c.Azure.Location != "" || c.Azure.ResourceGroup != "" {
		out = append(out, "azure")
	}
	return out
}

func definedDeploymentBlocks(c *OpsConfig) []string {
	var out []string
	if c.ECS.Cluster != "" {
		out = append(out, "ecs")
	}
	if len(c.Werf.Services) > 0 || len(c.Werf.ValuesPaths) > 0 || len(c.Werf.SecretsPaths) > 0 {
		out = append(out, "werf")
	}
	return out
}
