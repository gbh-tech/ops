package utils

import (
	"runtime"
	"slices"
	"strings"

	"github.com/charmbracelet/log"
)

func CurrentOperatingSystem() string {
	supportedOS := []string{
		"darwin",
		"windows",
		"linux",
	}

	if !slices.Contains(supportedOS, runtime.GOOS) {
		log.Fatalf("'%s' is currently not supported by the ops CLI", runtime.GOOS)
	}

	return strings.ToLower(runtime.GOOS)
}
