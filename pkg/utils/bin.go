package utils

import (
	"os/exec"

	"charm.land/log/v2"
)

func CheckBinary(binary string) {
	var err error

	if binary == "kubectl" {
		err = exec.Command(binary, "version", "--client=true").Run()
	} else {
		err = exec.Command(binary, "--version").Run()
		if err != nil {
			err = exec.Command(binary, "version").Run()
		}
	}

	if err != nil {
		log.Fatal(
			"Required command binary not found or cannot be executed.",
			"binary", binary, "err", err,
		)
	}
}
