package werf

import (
	"ops/pkg/config"
	"ops/pkg/utils"
	"os"
	"os/exec"

	"charm.land/log/v2"
)

type CommandOptions struct {
	Command, Env, Repo string
}

type CommandNoRepoOptions struct {
	Command, Env string
}

type CommandSecretsOptions struct {
	Command  string
	FilePath string
}

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

	log.Infof("Werf command: %v", cmd)
	execWerfCommand(cmd)
}

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

	log.Infof("Werf command: %v", cmd)
	execWerfCommand(cmd)
}

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

	log.Infof("Werf command: %v", cmd)
	execWerfCommand(cmd)

	if options.Command == "decrypt" {
		log.Info("Secret file(s) successfully decrypted!", "file", options.FilePath)
	}

	if options.Command == "encrypt" {
		log.Info("Secret file(s) successfully encrypted!", "file", options.FilePath)
	}
}

func execWerfCommand(args []string) {
	cmd := exec.Command(args[0], args[1:]...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()

	if err != nil {
		log.Fatalf("Failed to execute Werf command: %v", err)
	}
}
