package utils

import (
	"github.com/charmbracelet/log"
	"os/exec"
)

func CheckBinary(binary string) {
	cmd := exec.Command(binary, "--version", "--output", "text")
	err := cmd.Run()

	if err != nil {
		cmd = exec.Command(binary, "version")
		err = cmd.Run()
	}

	if err != nil {
		log.Fatal("Required command binary not found or cannot be executed.", "binary", binary)
	}

	log.Infof("%s is installed and executable!", binary)
}
