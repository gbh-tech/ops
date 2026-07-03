package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func walkCommands(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, sub := range cmd.Commands() {
		walkCommands(sub, fn)
	}
}

func ancestorPersistentShorthands(cmd *cobra.Command) map[string]string {
	out := make(map[string]string)
	for parent := cmd.Parent(); parent != nil; parent = parent.Parent() {
		parent.PersistentFlags().VisitAll(func(f *pflag.Flag) {
			if f.Shorthand != "" {
				out[f.Shorthand] = f.Name
			}
		})
	}
	return out
}

func TestNoShorthandFlagConflicts(t *testing.T) {
	t.Parallel()

	var conflicts []string

	walkCommands(rootCmd, func(cmd *cobra.Command) {
		inherited := ancestorPersistentShorthands(cmd)
		if len(inherited) == 0 {
			return
		}

		check := func(fs *pflag.FlagSet) {
			fs.VisitAll(func(f *pflag.Flag) {
				if f.Shorthand == "" {
					return
				}
				if parentFlagName, ok := inherited[f.Shorthand]; ok {
					conflicts = append(conflicts, fmt.Sprintf(
						"  %s: local -%s (--%s) conflicts with ancestor persistent --%s",
						cmd.CommandPath(), f.Shorthand, f.Name, parentFlagName,
					))
				}
			})
		}

		check(cmd.LocalNonPersistentFlags())
		check(cmd.PersistentFlags())
	})

	if len(conflicts) > 0 {
		t.Errorf("shorthand flag conflicts detected (would panic at runtime):\n%s",
			strings.Join(conflicts, "\n"))
	}
}
