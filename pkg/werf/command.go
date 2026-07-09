package werf

import (
	"ops/pkg/config"

	"charm.land/log/v2"
)

// appendValuesAndSecrets appends configured werf values and secret values
// files/paths to a werf command slice. Shared by Command and CommandWithoutRepo.
func appendValuesAndSecrets(cmd []string, werfConfig config.WerfConfig) []string {
	cmd = append(cmd, GetValuesFiles(werfConfig)...)
	cmd = append(cmd, GetValuesPaths(werfConfig)...)
	cmd = append(cmd, GetSecretValuesFiles(werfConfig)...)
	cmd = append(cmd, GetSecretValuesPaths(werfConfig)...)
	return cmd
}

// Command runs a werf command that requires a repo (--repo, --env, --dev, plus
// any configured extra values and secret values files/paths).
func Command(werfConfig config.WerfConfig, options *CommandOptions) {
	cmd := []string{
		"werf",
		options.Command,
		"--env",
		options.Env,
		"--repo",
		options.Repo,
		"--dev",
	}

	cmd = appendValuesAndSecrets(cmd, werfConfig)

	log.Info("Werf command", "cmd", cmd)
	execWerfCommand(cmd)
}

// CommandWithoutRepo runs a werf command without --repo (e.g. render or
// cleanup) but still applies --env, --dev, and configured extra values/secret
// values files and paths.
func CommandWithoutRepo(werfConfig config.WerfConfig, options *CommandNoRepoOptions) {
	cmd := []string{
		"werf",
		options.Command,
		"--env",
		options.Env,
		"--dev",
	}

	cmd = appendValuesAndSecrets(cmd, werfConfig)

	log.Info("Werf command", "cmd", cmd)
	execWerfCommand(cmd)
}
