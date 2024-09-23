package werf

import (
	"github.com/charmbracelet/log"
	"os/exec"
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

	log.Infof("Werf command: %v", cmd)
	execWerfCommand(cmd)
}

func execWerfCommand(args []string) {
	cmd := exec.Command(args[0], args[1:]...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf(
			"Failed to execute Werf command: %v",
			string(out),
		)
	}
}
