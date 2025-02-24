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
		config := config.LoadConfig()
		clusterName := config.K8s.ClusterNamePrefix + config.Env

		opts := kubeConfigCommandFlags(cmd, clusterName)

		if config.Cloud.Provider == "aws" {
			aws.EKSLogin(clusterName, config.AWS.Region)
		} else if config.Cloud.Provider == "azure" {
			azure.AKSLogin(opts.ClusterName, opts.ResourceGroup)
		} else {
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

func kubeConfigCommandFlags(
	cmd *cobra.Command,
	clusterName string,
) kubeConfigCommandOptions {
	name, _ := cmd.Flags().GetString("cluster-name")

	if name == "" {
		name = clusterName
	}

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
