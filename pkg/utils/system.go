package utils

import (
	"os"

	"charm.land/log/v2"
)

func RequiredFileExists(file string) {
	_, err := os.Stat(file)

	if os.IsNotExist(err) {
		log.Fatal("A required file for this command does not exist.", "file", file)
	}
}
