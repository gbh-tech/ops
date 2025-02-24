package config

type K8sConfig struct {
	ClusterNamePrefix string `mapstructure:"cluster_name_prefix"`
}
