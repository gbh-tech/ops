package config

type WerfConfig struct {
	Services     []string `mapstructure:"services"`
	SecretsPaths []string `mapstructure:"secrets"`
	ValuesPaths  []string `mapstructure:"values"`
}
