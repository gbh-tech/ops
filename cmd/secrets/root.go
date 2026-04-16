package secrets

import (
	"ops/pkg/config"

	"charm.land/log/v2"
	"github.com/spf13/cobra"
)

type SecretsCommandOptions struct {
	Env  string
	Root bool
}

var Command = &cobra.Command{
	Use:   "secrets",
	Short: "Manage deployment/applications secrets stored in version control by the deployment provider (Werf, Ansible Vault, etc)",
}

// requireWerfProvider fatals with a clear message when the deployment provider
// is not werf, since the secrets commands invoke the werf CLI directly.
func requireWerfProvider() {
	cfg := config.LoadConfig()
	if cfg.Deployment.Provider != "werf" {
		log.Fatal(
			"ops secrets requires deployment.provider = \"werf\"",
			"current", cfg.Deployment.Provider,
		)
	}
}

func secretsCommandFlags(cmd *cobra.Command) SecretsCommandOptions {
	envi, _ := cmd.Flags().GetString("env")
	root, _ := cmd.Flags().GetBool("root")

	return SecretsCommandOptions{
		Env:  envi,
		Root: root,
	}
}

func init() {
	Command.PersistentFlags().StringP("env", "e", "", "Target environment")
	Command.PersistentFlags().BoolP(
		"root",
		"r",
		false,
		"Encrypt/Decrypt the root credential file (.helm/secret-values.yaml)",
	)
	Command.PersistentFlags().BoolP(
		"custom",
		"c",
		false,
		"Encrypt/Decrypt additional custom credential files",
	)

	Command.AddCommand(encryptCommand)
	Command.AddCommand(decryptCommand)
}
