package kube

import (
	"ops/pkg/aws"
	"ops/pkg/azure"
	"ops/pkg/config"
	"ops/pkg/k8s"

	"github.com/charmbracelet/log"
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
		cfg := config.NewConfig()

		opts := kubeConfigCommandFlags(
			cmd,
			cfg.ClusterName,
			string(cfg.CloudProvider),
		)

		if opts.CloudProvider == "aws" {
			aws.EKSLogin(opts.ClusterName)
		} else if opts.CloudProvider == "azure" {
			azure.AKSLogin(opts.ClusterName, opts.ResourceGroup)
		} else {
			log.Fatal(
				"Current cloud provider is not yet supported by ops kube-config command.",
				"cloudProvider",
				opts.CloudProvider,
			)
		}

		log.Info(
			"Current active cluster",
			"clusterName",
			k8s.GetCurrentContext(),
		)
	},
}

func kubeConfigCommandFlags(
	cmd *cobra.Command,
	clusterName string,
	cloudProvider string,
) kubeConfigCommandOptions {
	var name, provider string

	name, _ = cmd.Flags().GetString("cluster-name")
	provider, _ = cmd.Flags().GetString("cloud-provider")

	if name == "" {
		name = clusterName
	}

	if provider == "" {
		provider = cloudProvider
	}

	return kubeConfigCommandOptions{
		ClusterName:   clusterName,
		CloudProvider: cloudProvider,
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
		"cloud-provider",
		"p",
		"",
		"Cloud provider where the cluster is provisioned",
	)
	ConfigCommand.Flags().StringP(
		"resource-group",
		"r",
		"",
		"Azure Resource group where the cluster is located",
	)
}
