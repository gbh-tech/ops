package werf

import (
	"os"
	"os/exec"

	"github.com/charmbracelet/log"
)

type CommandOptions struct {
	Command, Env, Repo string
}

type CommandNoRepoOptions struct {
	Command, Env string
}

func Command(options *CommandOptions) {
	cmd := []string{
		"werf",
		options.Command,
		"--env",
		options.Env,
		"--repo",
		options.Repo,
		"--dev",
	}

	additionalValuesFiles := GetValuesPaths()
	cmd = append(cmd, additionalValuesFiles...)

	log.Infof("Werf command: %v", cmd)
	execWerfCommand(cmd)
}

func CommandWithoutRepo(options *CommandNoRepoOptions) {
	cmd := []string{
		"werf",
		options.Command,
		"--env",
		options.Env,
		"--dev",
	}

	additionalValuesFiles := GetValuesPaths()
	cmd = append(cmd, additionalValuesFiles...)

	log.Infof("Werf command: %v", cmd)
	execWerfCommand(cmd)
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
