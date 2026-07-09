package werf

import (
	"ops/pkg/utils"

	"charm.land/log/v2"
)

// CommandWithSecrets runs `werf helm secret values {encrypt|decrypt}` on a
// single file, overwriting it in place.
func CommandWithSecrets(options *CommandSecretsOptions) {
	utils.RequiredFileExists(DefaultSecretKey)
	cmd := []string{
		"werf",
		"helm",
		"secret",
		"values",
		options.Command,
		options.FilePath,
		"-o",
		options.FilePath,
	}

	log.Info("Werf command", "cmd", cmd)
	execWerfCommand(cmd)

	if options.Command == "decrypt" {
		log.Info("Secret file(s) successfully decrypted!", "file", options.FilePath)
	}

	if options.Command == "encrypt" {
		log.Info("Secret file(s) successfully encrypted!", "file", options.FilePath)
	}
}
