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
		config := config.LoadConfig()
		opts := loginCommandFlags(cmd)

		log.Info(
			"Detected container registry type:",
			"type",
			config.Registry.Type,
		)

		log.Info(
			"Detected container registry:",
			"url",
			config.Registry.URL,
		)

		switch config.Registry.Type {
		case "ecr":
			url := opts.URL
			if url == "" {
				url = config.Registry.URL
			}

			if url == "" {
				log.Fatal("Container registry URL was not provided. Check your Ops config or use the --url flag.")
			}

			aws.ECRLogin(url, config.AWS.Region)
		case "acr":
			log.Fatal("Azure Container Registry is not supported by Ops.")
		case "gcr":
			log.Fatal("Google Cloud Container Registry is not supported by Ops.")
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
