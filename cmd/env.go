package cmd

import (
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"ops/pkg/config"
	"ops/pkg/env"
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manages the target environment",
	Run: func(cmd *cobra.Command, args []string) {
		opts := envCommandFlags(cmd)

		log.Infof(
			"%s: %s",
			"Current environment", opts.Env,
		)
	},
}

func envCommandFlags(cmd *cobra.Command) env.CommandOptions {
	envi, _ := cmd.Flags().GetString("env")

	if envi == "" {
		envi = config.NewConfig().Env
	}

	return env.CommandOptions{
		Env: envi,
	}
}

func init() {
	rootCmd.AddCommand(envCmd)
}
