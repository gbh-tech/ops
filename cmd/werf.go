package cmd

import (
	"github.com/spf13/cobra"
	"ops/pkg/utils"
	"ops/pkg/werf"
	"slices"
)

var werfCmd = &cobra.Command{
	Use:   "werf",
	Short: "Encapsulates the execution of complex Werf commands for simpler usage",
	Run: func(cmd *cobra.Command, args []string) {
		opts := flags(cmd)

		if slices.Contains(werf.CommandsWithRepoList, opts.Command) {
			werf.Command(&werf.CommandOptions{
				Command: opts.Command,
				Env:     opts.Env,
				Repo:    opts.Repo,
			})
			return
		}

		if slices.Contains(werf.CommandsWithoutRepoList, opts.Command) {
			werf.CommandWithoutRepo(&werf.CommandNoRepoOptions{
				Command: opts.Command,
				Env:     opts.Env,
			})
			return
		}
	},
}

func flags(cmd *cobra.Command) werf.CommandOptions {
	command, _ := cmd.Flags().GetString("command")
	env, _ := cmd.Flags().GetString("env")
	repo, _ := cmd.Flags().GetString("repo")

	return werf.CommandOptions{
		Command: command,
		Env:     env,
		Repo:    repo,
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
	werfCmd.Flags().StringP(
		"repo",
		"r",
		"",
		"Container image registry",
	)

	utils.MarkFlagsRequired(werfCmd, "command", "env")

	rootCmd.AddCommand(werfCmd)
}
