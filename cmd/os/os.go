package os

import (
	"ops/pkg/utils"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

var Command = &cobra.Command{
	Use:   "os",
	Short: "Prints the detected operating system to the console",
	Run: func(cmd *cobra.Command, args []string) {
		log.Infof(
			"%s: %s",
			"Current operating system:", utils.CurrentOperatingSystem(),
		)
	},
}
