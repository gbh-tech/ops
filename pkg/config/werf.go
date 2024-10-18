package config

type WerfConfig struct {
	Services      []string `mapstructure:"services"`
	GlobalValues  []string `mapstructure:"values"`
	GlobalSecrets []string `mapstructure:"secrets"`
}
