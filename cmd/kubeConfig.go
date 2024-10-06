package cmd

import (
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"ops/pkg/aws"
	"ops/pkg/config"
)

type kubeConfigCommandOptions struct {
	ClusterName   string
	CloudProvider string
}

var kubeConfigCmd = &cobra.Command{
	Use:   "kube-config",
	Short: "Updates local kube config file by authenticating cloud-managed k8s clusters",
	Run: func(cmd *cobra.Command, args []string) {
		opts := kubeConfigCommandFlags(cmd)

		if opts.CloudProvider == "aws" {
			aws.EKSLogin(opts.ClusterName)
		} else {
			log.Fatalf("%s config is not yet supported.", opts.CloudProvider)
		}

		log.Info(
			"Current cluster",
			"cluster",
			opts.ClusterName,
		)
	},
}

func kubeConfigCommandFlags(cmd *cobra.Command) kubeConfigCommandOptions {
	clusterName, _ := cmd.Flags().GetString("cluster-name")
	cloudProvider, _ := cmd.Flags().GetString("cloud-provider")

	if clusterName == "" {
		clusterName = config.NewConfig().ClusterName
	}

	if cloudProvider == "" {
		cloudProvider = string(config.NewConfig().CloudProvider)
	}

	return kubeConfigCommandOptions{
		ClusterName:   clusterName,
		CloudProvider: cloudProvider,
	}
}

func init() {
	kubeConfigCmd.Flags().StringP(
		"cluster-name",
		"c",
		"",
		"Cluster name used to authenticate",
	)
	kubeConfigCmd.Flags().StringP(
		"cloud-provider",
		"p",
		"",
		"Cloud provider where the cluster is provisioned",
	)

	rootCmd.AddCommand(kubeConfigCmd)
}
