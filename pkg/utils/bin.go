package utils

import (
	"github.com/charmbracelet/log"
	"os/exec"
)

func CheckBinary(binary string) {
	var cmd *exec.Cmd

	// @TODO: kubectl does not return to stdout/stderr normally.
	if binary == "kubectl" {
		// cmd = exec.Command(binary, "--version", "--client=true")
		return
	}

	cmd = exec.Command(binary, "--version")

	err := cmd.Run()

	if err != nil {
		cmd = exec.Command(binary, "version")
		err = cmd.Run()
	}

	if err != nil {
		log.Fatal(
			"Required command binary not found or cannot be executed.",
			"binary",
			binary,
		)
	}

	log.Infof("%s is installed and executable!", binary)
}
