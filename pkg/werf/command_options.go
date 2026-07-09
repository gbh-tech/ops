package werf

import (
	"os"
	"os/exec"

	"charm.land/log/v2"
)

// CommandOptions bundles the inputs for Command.
type CommandOptions struct {
	Command, Env, Repo string
}

// CommandNoRepoOptions bundles the inputs for CommandWithoutRepo.
type CommandNoRepoOptions struct {
	Command, Env string
}

// CommandSecretsOptions bundles the inputs for CommandWithSecrets.
type CommandSecretsOptions struct {
	Command  string
	FilePath string
}

// execWerfCommand runs a prepared werf command, wiring stdout/stderr to the
// terminal. It fatal-logs on non-zero exit.
func execWerfCommand(args []string) {
	cmd := exec.Command(args[0], args[1:]...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()

	if err != nil {
		log.Fatal("Failed to execute Werf command", "err", err)
	}
}
