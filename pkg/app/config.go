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

// VolumeEFSConfig holds EFS-specific volume settings.
// EFS supports concurrent access from many tasks across availability zones,
// making it safe to define in the [global] app config section.
type VolumeEFSConfig struct {
	FileSystemId      string `toml:"file_system_id"    yaml:"file_system_id"`
	RootDirectory     string `toml:"root_directory"    yaml:"root_directory"`
	TransitEncryption string `toml:"transit_encryption" yaml:"transit_encryption"`
	AccessPointId     string `toml:"access_point_id"   yaml:"access_point_id"`
	// IAM controls whether to use the ECS task role for EFS authorization.
	// Valid values: "ENABLED", "DISABLED". Requires transit_encryption ENABLED.
	IAM string `toml:"iam" yaml:"iam"`
}

// VolumeHostConfig holds bind-mount settings. The volume is scoped to the
// EC2 host, so it is only safe in per-environment sections (not global).
type VolumeHostConfig struct {
	// SourcePath is the absolute path on the host. If empty, Docker assigns
	// an ephemeral directory that is not persisted after the task stops.
	SourcePath string `toml:"source_path" yaml:"source_path"`
}

// VolumeDockerConfig holds Docker-managed volume settings. Like host volumes,
// Docker volumes are host-scoped and must not be used in the global section.
type VolumeDockerConfig struct {
	Driver        string            `toml:"driver"        yaml:"driver"`
	Scope         string            `toml:"scope"         yaml:"scope"`
	Autoprovision *bool             `toml:"autoprovision" yaml:"autoprovision"`
	DriverOpts    map[string]string `toml:"driver_opts"   yaml:"driver_opts"`
	Labels        map[string]string `toml:"labels"        yaml:"labels"`
}

// VolumeConfig defines a single named volume: the task-level storage
// configuration and the container-level mount point in one place.
//
// Exactly one of EFS, Host, or Docker must be set.
//
// Multi-write safety: only EFS may appear in the [global] app config section.
// Host and Docker volumes are host-scoped and must be declared in
// per-environment sections only.
type VolumeConfig struct {
	// Name identifies the volume and must be unique within the task definition.
	Name string `toml:"name" yaml:"name"`
	// ContainerPath is the absolute path inside the container where the volume
	// is mounted.
	ContainerPath string `toml:"container_path" yaml:"container_path"`
	ReadOnly      bool   `toml:"read_only"      yaml:"read_only"`

	EFS    *VolumeEFSConfig    `toml:"efs"    yaml:"efs"`
	Host   *VolumeHostConfig   `toml:"host"   yaml:"host"`
	Docker *VolumeDockerConfig `toml:"docker" yaml:"docker"`
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

	// Volumes declares ECS volumes and their container mount points.
	// Per-environment sections may use any volume type. The [global] section
	// is restricted to multi-write-safe types (EFS, FSx Windows).
	Volumes []VolumeConfig `toml:"volumes" yaml:"volumes"`
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
