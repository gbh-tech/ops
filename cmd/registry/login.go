package registry

import (
	"ops/pkg/aws"
	"ops/pkg/config"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

type LoginCommandOptions struct {
	URL string
}

var LoginCommand = &cobra.Command{
	Use:   "registry-login",
	Short: "Logs in to the specified container image registry",
	Run: func(cmd *cobra.Command, args []string) {
		config := config.NewConfig()
		opts := loginCommandFlags(cmd)

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
			if opts.URL != "" {
				aws.ECRLogin(opts.URL)
			} else {
				aws.ECRLogin(config.ContainerRegistry.URL)
			}
		}

		if config.ContainerRegistry.Type == "acr" {
			log.Fatalf("Azure Container Registry is not supported by Ops.")
		}

		if config.ContainerRegistry.Type == "gcr" {
			log.Fatalf("Google Cloud Container Registry is not supported by Ops.")
		}
	},
}

func loginCommandFlags(cmd *cobra.Command) LoginCommandOptions {
	url, _ := cmd.Flags().GetString("url")

	return LoginCommandOptions{
		URL: url,
	}
}

func init() {
	LoginCommand.Flags().StringP(
		"url",
		"u",
		"",
		"Container registry URL",
	)
}
