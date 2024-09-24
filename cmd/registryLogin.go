package cmd

import (
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"ops/pkg/config"
)

type RegistryLoginOptions struct {
	Registry string
}

var registryLoginCmd = &cobra.Command{
	Use:   "registry-login",
	Short: "Logs in to the specified container image registry (ECR, ACR, etc)",
	Run: func(cmd *cobra.Command, args []string) {
		config := config.NewConfig()

		log.Info(
			"Detected container registry type:",
			"type",
			config.ContainerRegistry.Type,
		)

		log.Info(
			"Detected container registry URL:",
			"url",
			config.ContainerRegistry.URL,
		)
	},
}

func init() {
	rootCmd.AddCommand(registryLoginCmd)
}
