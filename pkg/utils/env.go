package utils

import (
	"os"

	"github.com/charmbracelet/log"
)

func GetEnvironment(key string) string {
	value := os.Getenv(key)

	if value == "" {
		log.Fatal(
			"A required env var is not set.",
			"environmentVariable",
			key,
		)
	}

	return value
}
