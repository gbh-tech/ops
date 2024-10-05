package utils

import (
	"github.com/spf13/cobra"
	"log"
)

func MarkFlagsRequired(command *cobra.Command, flags ...string) {
	for _, flag := range flags {
		if err := command.MarkFlagRequired(flag); err != nil {
			log.Fatalf("Required '%s' flag not set: %v", flag, err)
		}
	}
}
