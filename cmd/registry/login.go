package registry

import (
	"ops/pkg/aws"
	"ops/pkg/config"

	"charm.land/log/v2"
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

		registryType := config.RegistryType()
		registryURL := config.RegistryURL()

		log.Info(
			"Detected container registry type:",
			"type",
			registryType,
		)
		log.Info(
			"Detected container registry:",
			"url",
			registryURL,
		)

		switch registryType {
		case "ecr":
			url := opts.URL
			if url == "" {
				url = registryURL
			}

			if url == "" {
				log.Fatal("Container registry URL was not provided. Check your Ops config or use the --url flag.")
			}

			aws.ECRLogin(url, config.AWS.Region)
		case "acr":
			log.Fatal("Azure Container Registry is not supported by Ops.")
		case "gar":
			log.Fatal("Google Artifact Registry is not supported by Ops.")
		default:
			log.Fatal("No registry kind could be derived for the active cloud provider.", "cloud", config.CloudProvider())
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
