package cmd

import (
	"github.com/spf13/cobra"
	"ops/pkg/utils"
	"ops/pkg/werf"
)

type WerfCommandOptions struct {
	Command, Env string
}

var werfCmd = &cobra.Command{
	Use:   "werf",
	Short: "Encapsulates the execution of complex Werf commands for simpler usage",
	Run: func(cmd *cobra.Command, args []string) {
		opts := flags(cmd)

		werf.BaseCommand(&werf.CommandOptions{
			Command: opts.Command,
			Env:     opts.Env,
		})
	},
}

func flags(cmd *cobra.Command) WerfCommandOptions {
	command, _ := cmd.Flags().GetString("command")
	env, _ := cmd.Flags().GetString("env")

	return WerfCommandOptions{
		Command: command,
		Env:     env,
	}
}

func init() {
	werfCmd.Flags().StringP(
		"command",
		"c",
		"",
		"Werf command to execute",
	)
	werfCmd.Flags().StringP(
		"env",
		"e",
		"",
		"Werf environment as target",
	)

	utils.MarkFlagsRequired(werfCmd, "command", "env")

	rootCmd.AddCommand(werfCmd)
}
