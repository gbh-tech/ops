package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestCommandWritesEmptyJSONArray(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ops"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := "provider: aws\ndeployment: ecs\napp_dirs:\n  - apps\naws:\n  region: us-east-1\n  account_id: '123456789012'\n"
	if err := os.WriteFile(filepath.Join(root, ".ops", "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
		viper.Reset()
	})

	viper.Reset()
	oldFormat, oldAppID, oldDeployableOnly := format, appID, deployableOnly
	format, appID, deployableOnly = "json", "", false
	var output bytes.Buffer
	Command.SetOut(&output)
	t.Cleanup(func() {
		format, appID, deployableOnly = oldFormat, oldAppID, oldDeployableOnly
		Command.SetOut(nil)
	})

	if err := Command.RunE(Command, nil); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "[]\n" {
		t.Fatalf("output = %q, want %q", got, "[]\n")
	}
}
