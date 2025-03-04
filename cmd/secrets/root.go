package secrets

import (
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

func secretsCommandFlags(cmd *cobra.Command) SecretsCommandOptions {
	envi, _ := cmd.Flags().GetString("env")
	root, _ := cmd.Flags().GetBool("root")

	return SecretsCommandOptions{
		Env:  envi,
		Root: root,
	}
}

func init() {
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
