package config

type WerfConfig struct {
	Services     []string `mapstructure:"services"`
	ValuesPaths  []string `mapstructure:"values"`
	ValuesFiles  []string `mapstructure:"values_files"`
	SecretsPaths []string `mapstructure:"secrets"`
	SecretsFiles []string `mapstructure:"secrets_files"`
}
