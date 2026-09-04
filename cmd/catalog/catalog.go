package catalog

import (
	"encoding/json"
	"fmt"

	"ops/pkg/catalog"
	"ops/pkg/config"

	"github.com/spf13/cobra"
)

var format string

var Command = &cobra.Command{
	Use:   "catalog",
	Short: "Discover application manifests in configured app directories",
	RunE: func(cmd *cobra.Command, args []string) error {
		units, err := catalog.Discover(config.LoadConfig())
		if err != nil {
			return err
		}
		if format == "json" {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(units)
		}
		for _, unit := range units {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", unit.ID, unit.Kind, unit.Config)
		}
		return nil
	},
}

func init() {
	Command.Flags().StringVar(&format, "format", "table", "Output format: table or json")
}
