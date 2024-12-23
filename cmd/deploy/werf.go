package deploy

import (
	"ops/pkg/config"
	"ops/pkg/werf"
	"slices"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

var WerfCommand = &cobra.Command{
	Use:   "werf",
	Short: "Encapsulates the execution of complex Werf commands for simpler usage",
	Run: func(cmd *cobra.Command, args []string) {
		config := config.NewConfig()
		opts := werfCommandFlags(cmd)

		if opts.Env == "" {
			opts.Env = config.Env
		}

		if config.Deployment.Provider != "werf" {
			log.Fatal(
				"Please select werf as the deployment provider first.",
				"deploymentProvider",
				config.Deployment.Provider,
			)
		}

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

func werfCommandFlags(cmd *cobra.Command) werf.CommandOptions {
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
	WerfCommand.Flags().StringP(
		"command",
		"c",
		"",
		"Werf command to execute",
	)
	WerfCommand.Flags().StringP(
		"env",
		"e",
		"",
		"Werf environment as target",
	)
	WerfCommand.Flags().StringP(
		"repo",
		"r",
		"",
		"Container image registry",
	)
}
