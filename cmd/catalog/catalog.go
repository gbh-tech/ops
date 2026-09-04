package catalog

import (
	"encoding/json"
	"fmt"

	"ops/pkg/catalog"
	"ops/pkg/config"

	"github.com/spf13/cobra"
)

var (
	format         string
	environment    string
	appID          string
	deployableOnly bool
)

var Command = &cobra.Command{
	Use:   "catalog",
	Short: "Discover application manifests in configured app directories",
	RunE: func(cmd *cobra.Command, args []string) error {
		units, err := catalog.Discover(config.LoadConfig())
		if err != nil {
			return err
		}
		filtered := units[:0]
		for _, unit := range units {
			if appID != "" && unit.ID != appID {
				continue
			}
			if deployableOnly && unit.Kind == "image-only" {
				continue
			}
			if environment != "" && len(unit.Environments) > 0 && !contains(unit.Environments, environment) {
				continue
			}
			filtered = append(filtered, unit)
		}
		units = filtered
		if appID != "" && len(units) == 0 {
			return fmt.Errorf("unknown or ineligible application %q", appID)
		}
		if format == "json" {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(units)
		}
		if format != "table" {
			return fmt.Errorf("unsupported format %q; expected table or json", format)
		}
		for _, unit := range units {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", unit.ID, unit.Kind, unit.Config)
		}
		return nil
	},
}

func init() {
	Command.Flags().StringVar(&format, "format", "table", "Output format: table or json")
	Command.Flags().StringVar(&environment, "env", "", "Include applications configured for this environment")
	Command.Flags().StringVar(&appID, "app", "", "Include only this application ID")
	Command.Flags().BoolVar(&deployableOnly, "deployable", false, "Exclude image-only applications")
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
