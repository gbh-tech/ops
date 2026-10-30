package reviewapp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second: "30s",
		5 * time.Minute:  "5m",
		47 * time.Hour:   "47h",
	} {
		if got := shortDuration(d); got != want {
			t.Fatalf("shortDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestUntil(t *testing.T) {
	if got := until(time.Now().Add(-time.Minute)); got != "expired" {
		t.Fatalf("until(past) = %q, want expired", got)
	}
	if got := until(time.Now().Add(3*time.Hour + time.Minute)); got != "in 3h" {
		t.Fatalf("until(+3h) = %q, want in 3h", got)
	}
}

func TestCommandTree(t *testing.T) {
	want := map[string]bool{"list": false, "status": false, "logs": false}
	for _, c := range Command.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("subcommand %s not registered", name)
		}
	}
	for _, flag := range []string{"domain", "infra-ssm", "tools-image", "region"} {
		if Command.PersistentFlags().Lookup(flag) == nil {
			t.Fatalf("persistent flag --%s missing", flag)
		}
	}
	if statusCmd.Flags().Lookup("pr") == nil || logsCmd.Flags().Lookup("pr") == nil {
		t.Fatal("status and logs need --pr")
	}
}

func TestResolveSettingsLetsFlagsCoverAnIncompleteConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ops"), 0o750); err != nil {
		t.Fatal(err)
	}
	// No aws block: LoadConfig would refuse this file.
	body := "[reviewapps]\ndomain = \"reviewapps.example.com\"\ninfra_ssm = \"/reviewapp/infra\"\n"
	if err := os.WriteFile(filepath.Join(dir, ".ops", "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cmd := &cobra.Command{}
	for _, name := range []string{"domain", "infra-ssm", "tools-image", "region"} {
		cmd.Flags().String(name, "", "")
	}
	if err := cmd.Flags().Set("region", "us-west-2"); err != nil {
		t.Fatal(err)
	}
	s := resolveSettings(cmd)
	if s.region != "us-west-2" || s.domain != "reviewapps.example.com" || s.infraSSM != "/reviewapp/infra" {
		t.Fatalf("settings = %+v", s)
	}
}
