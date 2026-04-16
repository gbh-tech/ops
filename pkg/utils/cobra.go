package utils

import (
	"log"

	"github.com/spf13/cobra"
)

func MarkFlagsRequired(command *cobra.Command, flags ...string) {
	for _, flag := range flags {
		if err := command.MarkFlagRequired(flag); err != nil {
			log.Fatal("Required flag not set", "flag", flag, "err", err)
		}
	}
}
