package config

// GitConfig is split out only to keep the root struct readable.
type GitConfig struct {
	DefaultBranch string `mapstructure:"default_branch"`
}
