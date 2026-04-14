package kube

import (
	"ops/pkg/aws"
	"ops/pkg/azure"
	"ops/pkg/config"
	"ops/pkg/k8s"

	"charm.land/log/v2"
	"github.com/spf13/cobra"
)

type kubeConfigCommandOptions struct {
	ClusterName   string
	CloudProvider string
	ResourceGroup string
}

var ConfigCommand = &cobra.Command{
	Use:   "kube-config",
	Short: "Updates local kube config file by authenticating cloud-managed k8s clusters",
	Run: func(cmd *cobra.Command, args []string) {
		config := config.LoadConfig()
		opts := kubeConfigCommandFlags(cmd)

		if opts.ClusterName == "" {
			opts.ClusterName = config.K8s.ClusterNamePrefix + config.Env
		}

		switch config.Cloud.Provider {
		case "aws":
			aws.EKSLogin(opts.ClusterName, config.AWS.Region)
		case "azure":
			azure.AKSLogin(opts.ClusterName, opts.ResourceGroup)
		default:
			log.Fatal(
				"Current cloud provider is not yet supported by Op.",
				"cloudProvider",
				config.Cloud.Provider,
			)
		}

		log.Info(
			"Current active cluster",
			"clusterName",
			k8s.GetCurrentContext(),
		)
	},
}

func kubeConfigCommandFlags(cmd *cobra.Command) kubeConfigCommandOptions {
	name, _ := cmd.Flags().GetString("cluster-name")

	return kubeConfigCommandOptions{
		ClusterName: name,
	}
}

func init() {
	ConfigCommand.Flags().StringP(
		"cluster-name",
		"c",
		"",
		"Cluster name used to authenticate",
	)

	ConfigCommand.Flags().StringP(
		"resource-group",
		"r",
		"",
		"Azure Resource group where the cluster is located",
	)
}
