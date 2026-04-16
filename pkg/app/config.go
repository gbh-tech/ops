// Package app provides provider-agnostic app config types and file loading.
// It is shared by all providers (ECS, future providers) and by build/push
// commands that need to read app config without depending on any specific
// deployment provider.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// HealthCheckConfig mirrors the container_health_check section.
type HealthCheckConfig struct {
	Interval    int `toml:"interval"    yaml:"interval"`
	Timeout     int `toml:"timeout"     yaml:"timeout"`
	Retries     int `toml:"retries"     yaml:"retries"`
	StartPeriod int `toml:"start_period" yaml:"start_period"`
}

// AppSection is a single named section within an app config (global, stage,
// production, etc.). Secrets can be a list of strings or a map of
// env-var → json-key; both forms normalise to a map via NormalizeSecrets.
type AppSection struct {
	Name               string            `toml:"name"                yaml:"name"`
	Image              string            `toml:"image"               yaml:"image"`
	Port               int               `toml:"port"                yaml:"port"`
	CPU                int               `toml:"cpu"                 yaml:"cpu"`
	Memory             int               `toml:"memory"              yaml:"memory"`
	DesiredCount       *int              `toml:"desired_count"       yaml:"desired_count"`
	NetworkMode        string            `toml:"network_mode"        yaml:"network_mode"`
	LaunchType         string            `toml:"launch_type"         yaml:"launch_type"`
	LogDriver          string            `toml:"log_driver"          yaml:"log_driver"`
	HealthCheckPath    string            `toml:"health_check_path"   yaml:"health_check_path"`
	ContainerHC        HealthCheckConfig `toml:"container_health_check" yaml:"container_health_check"`
	DatabaseMigrations bool              `toml:"database_migrations" yaml:"database_migrations"`
	MigrationCommand   []string          `toml:"migration_command"   yaml:"migration_command"`
	SecretsName        string            `toml:"secrets_name"        yaml:"secrets_name"`
	ExecutionRole      string            `toml:"execution_role"      yaml:"execution_role"`
	TaskRole           string            `toml:"task_role"           yaml:"task_role"`
	Command            []string          `toml:"command"             yaml:"command"`
	Environment        map[string]string `toml:"environment"         yaml:"environment"`

	// Secrets is intentionally interface{} to handle both list and map forms.
	// Use NormalizeSecrets() to get a canonical map[string]string.
	Secrets interface{} `toml:"secrets" yaml:"secrets"`
}

// AppConfig is the top-level structure of an app's config.toml / config.yaml.
// Keys are section names: "global", "stage", "production", etc.
type AppConfig map[string]AppSection

// LoadFile reads path and unmarshals it into out. The file extension
// determines the parser: .toml uses BurntSushi/toml, .yaml/.yml uses
// gopkg.in/yaml.v3. Any other extension returns an error.
func LoadFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".toml":
		if err := toml.Unmarshal(data, out); err != nil {
			return fmt.Errorf("parsing TOML %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, out); err != nil {
			return fmt.Errorf("parsing YAML %s: %w", path, err)
		}
	default:
		return fmt.Errorf("unsupported config format %q (use .toml or .yaml)", ext)
	}
	return nil
}

// LoadAppConfig reads an app config file (TOML or YAML by extension).
func LoadAppConfig(path string) (AppConfig, error) {
	var cfg AppConfig
	if err := LoadFile(path, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
