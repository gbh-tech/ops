package config

// Defaults of the reviewapps: section. A project overrides them in the same
// section; leopard-platform needs none.
const (
	DefaultReviewAppsEnv   = "reviewapp"
	DefaultReviewAppsLimit = 10
)

// ReviewAppsConfig is the reviewapps: section of .ops/config.yaml (RFC 0004,
// D12). The three required keys are policy; every infrastructure fact comes
// from the SSM parameter named by InfraSSM, never from the repository.
type ReviewAppsConfig struct {
	// Domain is the parent domain of review app hosts: pr-<n>.<domain>.
	Domain string `mapstructure:"domain"`
	// InfraSSM is the SSM parameter Terraform writes with the cluster,
	// network, ALB, buckets and secret names.
	InfraSSM string `mapstructure:"infra_ssm"`
	// ToolsImage is the project's helper image (bootstrap, migrate, teardown).
	ToolsImage string `mapstructure:"tools_image"`

	// Env is the per-unit config section review apps render. Defaults to
	// "reviewapp".
	Env string `mapstructure:"env"`
	// Limit is how many review apps can run at once. Defaults to 10.
	Limit int `mapstructure:"limit"`
}

// Defined reports whether the section is present.
func (c ReviewAppsConfig) Defined() bool {
	return c.Domain != "" || c.InfraSSM != "" || c.ToolsImage != ""
}

// ResolvedEnv returns the per-unit config section, defaulting to "reviewapp".
func (c ReviewAppsConfig) ResolvedEnv() string {
	if c.Env != "" {
		return c.Env
	}
	return DefaultReviewAppsEnv
}

// ResolvedLimit returns the concurrent review app limit, defaulting to 10.
func (c ReviewAppsConfig) ResolvedLimit() int {
	if c.Limit > 0 {
		return c.Limit
	}
	return DefaultReviewAppsLimit
}
