// Package config provides the persistent --provider / --deployment flags
// and the `ops config` command for inspecting which providers and settings
// are active for the current invocation.
package config

import (
	pkgconfig "ops/pkg/config"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"charm.land/log/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// RegisterGlobalFlags wires the persistent --provider and --deployment
// flags onto the given (root) command and binds them to the matching
// top-level viper keys. When supplied at invocation time they override
// `provider:` / `deployment:` from .ops/config.yaml.
//
// Kept as an exported function so the root command (and tests) can attach
// the flags without depending on this package's command tree wiring.
func RegisterGlobalFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String(
		"provider",
		"",
		"Active cloud provider (aws|azure|gcp); overrides provider in config",
	)
	cmd.PersistentFlags().String(
		"deployment",
		"",
		"Active deployment tool (ecs|werf|ansible); overrides deployment in config",
	)
	_ = viper.BindPFlag("provider", cmd.PersistentFlags().Lookup("provider"))
	_ = viper.BindPFlag("deployment", cmd.PersistentFlags().Lookup("deployment"))
}

// Command is the "ops config" command. It loads the config (applying
// inference and any --provider / --deployment overrides) and pretty-prints
// the resolved settings so users can verify which providers are active
// before running a deploy / push / etc.
var Command = &cobra.Command{
	Use:   "config",
	Short: "Print the resolved active providers and derived settings",
	Long: `Print the resolved active settings for the current invocation.

This is useful for confirming which cloud / deployment provider is in
effect, what the derived registry URL and ARN prefixes look like, and
which environment / apps_dir will be used. Honours --provider and
--deployment overrides.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := pkgconfig.LoadConfig()

		rows := [][]string{
			{"env", cfg.Env},
			{"repo_mode", repoModeOrDefault(cfg)},
			{"apps_dir", cfg.AppsDirPath()},
			{"provider", cfg.CloudProvider()},
			{"deployment", cfg.DeploymentProvider()},
			{"registry.type", cfg.RegistryType()},
			{"registry.url", cfg.RegistryURL()},
		}

		switch cfg.CloudProvider() {
		case "aws":
			rows = append(rows,
				[]string{"aws.account_id", cfg.AWS.AccountId},
				[]string{"aws.region", cfg.AWS.Region},
				[]string{"aws.profile", orDash(cfg.AWS.Profile)},
			)
		case "azure":
			rows = append(rows,
				[]string{"azure.location", cfg.Azure.Location},
				[]string{"azure.resource_group", cfg.Azure.ResourceGroup},
			)
		}

		switch cfg.DeploymentProvider() {
		case "ecs":
			rows = append(rows,
				[]string{"ecs.cluster", cfg.ECS.Cluster},
				[]string{"ecs.capacity_provider", orDash(cfg.ECS.CapacityProvider)},
				[]string{"ecs.execution_role", orDash(cfg.ECS.ResolvedExecutionRole(cfg.AWS))},
				[]string{"ecs.task_role", orDash(cfg.ECS.ResolvedTaskRole(cfg.AWS))},
				[]string{"ecs.secret_arn_prefix", orDash(cfg.ECS.ResolvedSecretArnPrefix(cfg.AWS))},
			)
		case "werf":
			rows = append(rows,
				[]string{"werf.services", joinOrDash(cfg.Werf.Services)},
			)
		}

		keyStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
		t := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
			Headers("Setting", "Value").
			StyleFunc(func(_, col int) lipgloss.Style {
				if col == 0 {
					return keyStyle
				}
				return lipgloss.NewStyle()
			}).
			Rows(rows...)

		if _, err := lipgloss.Println(t); err != nil {
			log.Fatal("Failed to render config", "err", err)
		}
	},
}

func repoModeOrDefault(c *pkgconfig.OpsConfig) string {
	if c.RepoMode == "" {
		return "mono (default)"
	}
	return c.RepoMode
}

func orDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func joinOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	out := items[0]
	for _, s := range items[1:] {
		out += ", " + s
	}
	return out
}
