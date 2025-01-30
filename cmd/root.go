package cmd

import (
	"ops/cmd/deploy"
	"ops/cmd/env"
	"ops/cmd/git"
	"ops/cmd/kube"
	osCmd "ops/cmd/os"
	"ops/cmd/registry"
	"os"

	"github.com/spf13/cobra"
)

var Version string = "development"

var rootCmd = &cobra.Command{
	Use:     "ops",
	Short:   "An all-purpose deployment automation tool tailored for DevOps & SRE",
	Version: Version,
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringP(
		"env",
		"e",
		"",
		"Environment as target",
	)

	rootCmd.AddCommand(osCmd.Command)
	rootCmd.AddCommand(env.Command)
	rootCmd.AddCommand(kube.ConfigCommand)
	rootCmd.AddCommand(git.GetTicketIDCommand)
	rootCmd.AddCommand(git.TagCleanerCommand)
	rootCmd.AddCommand(registry.LoginCommand)
	rootCmd.AddCommand(deploy.WerfCommand)
}
