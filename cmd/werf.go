package cmd

import (
	"github.com/spf13/cobra"
	"ops/pkg/werf"
)

var werfCmd = &cobra.Command{
	Use:   "werf",
	Short: "Encapsulates the execution of complex Werf commands for simpler usage",
	Run: func(cmd *cobra.Command, args []string) {
		werf.BaseCommand(&werf.CommandOptions{
			Command: "render",
			Env:     "stage",
		})
	},
}

func init() {
	rootCmd.AddCommand(werfCmd)
}
