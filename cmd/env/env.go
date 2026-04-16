package env

import (
	"ops/pkg/config"

	"charm.land/log/v2"
	"github.com/spf13/cobra"
)

type CommandOptions struct {
	Env string
}

var Command = &cobra.Command{
	Use:   "env",
	Short: "Manages the target environment",
	Run: func(cmd *cobra.Command, args []string) {
		opts := flags(cmd)

		log.Info("Current environment", "env", opts.Env)
	},
}

func init() {
	Command.Flags().StringP("env", "e", "", "Environment to display (defaults to env in config)")
}

func flags(cmd *cobra.Command) CommandOptions {
	envi, _ := cmd.Flags().GetString("env")

	if envi == "" {
		envi = config.LoadConfig().Env
	}

	return CommandOptions{
		Env: envi,
	}
}
