package registry

import (
	"ops/pkg/aws"
	"ops/pkg/config"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

type RegistryLoginOptions struct {
	Registry string
}

var LoginCommand = &cobra.Command{
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

		if config.ContainerRegistry.Type == "ecr" {
			aws.ECRLogin()
		}

		if config.ContainerRegistry.Type == "acr" {
			log.Fatalf("Azure Container Registry is not supported by Ops.")
		}

		if config.ContainerRegistry.Type == "gcr" {
			log.Fatalf("Google Cloud Container Registry is not supported by Ops.")
		}
	},
}
