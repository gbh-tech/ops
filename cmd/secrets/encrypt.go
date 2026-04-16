package secrets

import (
	"fmt"
	"ops/pkg/utils"
	"ops/pkg/werf"

	"charm.land/log/v2"
	"github.com/spf13/cobra"
)

var encryptCommand = &cobra.Command{
	Use:   "encrypt",
	Short: "Encrypts secrets",
	Run: func(cmd *cobra.Command, args []string) {
		requireWerfProvider()
		opts := secretsCommandFlags(cmd)

		if !opts.Root && opts.Env == "" {
			log.Fatal("Either '--root' or '--env' are needed secrets encrypt command.")
		}

		if opts.Root {
			log.Info("Encrypting secrets...", "file", werf.DefaultSecretValuesFile)
			utils.RequiredFileExists(werf.DefaultSecretValuesFile)
			werf.CommandWithSecrets(&werf.CommandSecretsOptions{
				Command:  "encrypt",
				FilePath: werf.DefaultSecretValuesFile,
			})
		}

		if opts.Env != "" {
			log.Info("Encrypting secrets for selected environment.", "env", opts.Env)
			var secretFile = fmt.Sprintf("%s/%s.yaml", ".helm/secrets", opts.Env)

			utils.RequiredFileExists(secretFile)
			werf.CommandWithSecrets(&werf.CommandSecretsOptions{
				Command:  "encrypt",
				FilePath: secretFile,
			})
		}
	},
}
