package werf

import (
	"github.com/charmbracelet/log"
	"os/exec"
)

type CommandOptions struct {
	Command, Env string
}

type CommandWithRepoOptions struct {
	CommandOptions
	Repo string
}

func execWerfCommand(args []string) {
	log.Infof("Werf command: %v", args)

	cmd := exec.Command(args[0], args[1:]...)

	_, err := cmd.Output()
	if err != nil {
		log.Fatalf("Failed to execute Werf command: %v", err)
	}
}

func BaseCommand(options *CommandOptions) {
	cmd := []string{
		"werf",
		options.Command,
		"--env",
		options.Env,
		"--dev",
	}

	log.Infof("Werf command: %v", cmd)
	//execWerfCommand(cmd)
}

func BaseCommandWithRepo(options *CommandWithRepoOptions) {
	cmd := []string{
		"werf",
		options.Command,
		"--env",
		options.Env,
		"--repo",
		options.Repo,
		"--dev",
	}

	log.Infof("Werf command: %v", cmd)
	//execWerfCommand(cmd)
}
