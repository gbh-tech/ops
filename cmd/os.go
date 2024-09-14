package cmd

import (
	"github.com/charmbracelet/log"
	. "ops/pkg/utils"

	"github.com/spf13/cobra"
)

var osCmd = &cobra.Command{
	Use:   "os",
	Short: "Prints the detected operating system to the console",
	Run: func(cmd *cobra.Command, args []string) {
		log.Infof(
			"%s: %s",
			"Current operating system:", CurrentOperatingSystem(),
		)
	},
}

func init() {
	rootCmd.AddCommand(osCmd)
}
