package deploy

import "github.com/spf13/cobra"

// Command is the "ops deploy" parent command. Provider-specific subcommands
// (ecs, werf) are registered under it.
var Command = &cobra.Command{
	Use:   "deploy",
	Short: "Deployment commands for supported providers (ecs, werf)",
}

func init() {
	Command.AddCommand(WerfCommand)
}
