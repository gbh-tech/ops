package config

import (
	"slices"

	"charm.land/log/v2"
)

// SupportedProviders lists the cloud providers ops can dispatch to. The
// active one is inferred from which provider block is defined in the config
// (`aws:`, `azure:`, …) and disambiguated by the top-level `provider:` key
// or the persistent --provider flag.
var SupportedProviders = []string{
	"aws",
	"azure",
	"gcp",
}

// SupportedDeployments mirrors SupportedProviders for deployment tools.
// The active one is inferred from which deployment block is defined
// (`ecs:`, `werf:`, …) and disambiguated by `deployment:` / --deployment.
var SupportedDeployments = []string{
	"ecs",
	"werf",
	"ansible",
}

// resolveProvider picks the active cloud provider. Precedence:
//  1. explicit `provider:` (or --provider flag, bound to the same key)
//  2. inferred from the single non-empty cloud provider block
//
// Fatals when ambiguous (multiple blocks, no explicit selection) or when
// the resolved value is not a supported provider.
func resolveProvider(c *OpsConfig) string {
	if c.Provider != "" {
		if !slices.Contains(SupportedProviders, c.Provider) {
			log.Fatal(
				"provider is not a supported cloud provider",
				"value", c.Provider,
				"supported", SupportedProviders,
			)
		}
		return c.Provider
	}

	defined := definedCloudBlocks(c)
	switch len(defined) {
	case 0:
		log.Fatal("No cloud provider block defined. Add an `aws:` (or `azure:`, `gcp:`) section to .ops/config.yaml.")
	case 1:
		return defined[0]
	default:
		log.Fatal(
			"Multiple cloud provider blocks defined; set `provider:` in .ops/config.yaml or pass --provider.",
			"defined", defined,
		)
	}
	return ""
}

// resolveDeployment mirrors resolveProvider for deployment tools.
func resolveDeployment(c *OpsConfig) string {
	if c.Deployment != "" {
		if !slices.Contains(SupportedDeployments, c.Deployment) {
			log.Fatal(
				"deployment is not a supported deployment tool",
				"value", c.Deployment,
				"supported", SupportedDeployments,
			)
		}
		return c.Deployment
	}

	defined := definedDeploymentBlocks(c)
	switch len(defined) {
	case 0:
		log.Fatal("No deployment provider block defined. Add an `ecs:` or `werf:` section to .ops/config.yaml.")
	case 1:
		return defined[0]
	default:
		log.Fatal(
			"Multiple deployment provider blocks defined; set `deployment:` in .ops/config.yaml or pass --deployment.",
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
