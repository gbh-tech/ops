package secrets

import (
	"fmt"
	"ops/pkg/utils"
	"ops/pkg/werf"

	"charm.land/log/v2"
	"github.com/spf13/cobra"
)

var decryptCommand = &cobra.Command{
	Use:   "decrypt",
	Short: "Decrypts secrets",
	Run: func(cmd *cobra.Command, args []string) {
		requireWerfProvider()
		opts := secretsCommandFlags(cmd)

		if !opts.Root && opts.Env == "" {
			log.Fatal("Either '--root' or '--env' are needed secrets decrypt command.")
		}

		if opts.Root {
			log.Info("Decrypting secrets...", "file", werf.DefaultSecretValuesFile)
			utils.RequiredFileExists(werf.DefaultSecretValuesFile)
			werf.CommandWithSecrets(&werf.CommandSecretsOptions{
				Command:  "decrypt",
				FilePath: werf.DefaultSecretValuesFile,
			})
		}

		if opts.Env != "" {
			log.Info("Decrypting secrets for selected environment.", "env", opts.Env)
			var secretFile = fmt.Sprintf("%s/%s.yaml", ".helm/secrets", opts.Env)

			utils.RequiredFileExists(secretFile)
			werf.CommandWithSecrets(&werf.CommandSecretsOptions{
				Command:  "decrypt",
				FilePath: secretFile,
			})
		}
	},
}
