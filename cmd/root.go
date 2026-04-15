package cmd

import (
	"ops/cmd/deploy"
	ecscmd "ops/cmd/ecs"
	"ops/cmd/env"
	"ops/cmd/git"
	imagecmd "ops/cmd/image"
	"ops/cmd/kube"
	"ops/cmd/registry"
	"ops/cmd/secrets"
	"os"

	"github.com/spf13/cobra"
)

var Version string = "dev"

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

	rootCmd.AddCommand(env.Command)
	rootCmd.AddCommand(kube.ConfigCommand)
	rootCmd.AddCommand(git.GetTicketIDCommand)
	rootCmd.AddCommand(git.TagCleanerCommand)
	rootCmd.AddCommand(registry.LoginCommand)
	rootCmd.AddCommand(deploy.Command)
	rootCmd.AddCommand(ecscmd.Command)
	rootCmd.AddCommand(secrets.Command)
	rootCmd.AddCommand(imagecmd.BuildCommand)
	rootCmd.AddCommand(imagecmd.PushCommand)
}
