package werf

import (
	"ops/pkg/config"

	"charm.land/log/v2"
)

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

	additionalValuesFiles := GetValuesFiles(werfConfig)
	cmd = append(cmd, additionalValuesFiles...)

	additionalValuesPath := GetValuesPaths(werfConfig)
	cmd = append(cmd, additionalValuesPath...)

	additionalSecretValuesFiles := GetSecretValuesFiles(werfConfig)
	cmd = append(cmd, additionalSecretValuesFiles...)

	additionalSecretValuesPath := GetSecretValuesPaths(werfConfig)
	cmd = append(cmd, additionalSecretValuesPath...)

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

	additionalValuesFiles := GetValuesFiles(werfConfig)
	cmd = append(cmd, additionalValuesFiles...)

	additionalValuesPath := GetValuesPaths(werfConfig)
	cmd = append(cmd, additionalValuesPath...)

	additionalSecretValuesFiles := GetSecretValuesFiles(werfConfig)
	cmd = append(cmd, additionalSecretValuesFiles...)

	additionalSecretValuesPath := GetSecretValuesPaths(werfConfig)
	cmd = append(cmd, additionalSecretValuesPath...)

	log.Info("Werf command", "cmd", cmd)
	execWerfCommand(cmd)
}
