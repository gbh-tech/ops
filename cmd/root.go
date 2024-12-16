package cmd

import (
	"ops/cmd/containerRegistry"
	"ops/cmd/env"
	"ops/cmd/git"
	"ops/cmd/kube"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ops",
	Short: "An all-purpose deployment automation tool tailored for DevOps & SRE",
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

	rootCmd.AddCommand(env.Command)
	rootCmd.AddCommand(kube.ConfigCommand)
	rootCmd.AddCommand(git.GetTicketIDCommand)
	rootCmd.AddCommand(git.TagCleanerCommand)
	rootCmd.AddCommand(containerRegistry.LoginCommand)
}
