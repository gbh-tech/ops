// Package app provides provider-agnostic app config types and file loading.
// It is shared by all providers (ECS, future providers) and by build/push
// commands that need to read app config without depending on any specific
// deployment provider.
package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

	// BuildSecrets lists secrets to fetch from Secrets Manager at image build
	// time. Same list/map forms as Secrets. Fetched values are exposed to the
	// Dockerfile via Docker BuildKit --mount=type=secret.
	BuildSecrets any `toml:"build_secrets" yaml:"build_secrets"`

	// BuildArgs are plain key/value pairs passed as docker --build-arg at
	// build time. Env section values override matching global values; non-
	// matching global values are still included.
	BuildArgs map[string]string `toml:"build_args" yaml:"build_args"`

	// Volumes declares ECS volumes and their container mount points.
	// Per-environment sections may use any volume type. The [global] section
	// is restricted to multi-write-safe types (EFS).
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
		md, err := toml.Decode(string(data), out)
		if err != nil {
			return fmt.Errorf("parsing TOML %s: %w", path, err)
		}
		if extras := unknownTOMLKeys(md); len(extras) > 0 {
			return fmt.Errorf(
				"unknown keys in %s: %s\n"+
					"hint: in TOML, every bare key/value after a [subtable] header "+
					"belongs to that subtable, not the parent — move scalars above "+
					"subtable headers or use the [section.key] map form",
				path, strings.Join(extras, ", "),
			)
		}
	case ".yaml", ".yml":
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(out); err != nil {
			return fmt.Errorf("parsing YAML %s: %w", path, err)
		}
	default:
		return fmt.Errorf("unsupported config format %q (use .toml or .yaml)", ext)
	}
	return nil
}

// unknownTOMLKeys returns a sorted list of dotted key paths from the TOML
// MetaData that were not decoded into the target struct. Sub-keys under
// AppSection.Secrets and AppSection.BuildSecrets are excluded: those fields
// are typed as interface{} so TOML marks their children as undecoded by
// design. We identify them by checking that "secrets" or "build_secrets"
// appears at index 1 of the key path (i.e. directly under a section like
// "global" or "production", not nested inside another field).
func unknownTOMLKeys(md toml.MetaData) []string {
	var unknown []string
	for _, k := range md.Undecoded() {
		// k[0] = section name ("global", "stage", …)
		// k[1] = AppSection field name
		if len(k) >= 2 && (k[1] == "secrets" || k[1] == "build_secrets") {
			continue
		}
		unknown = append(unknown, k.String())
	}
	sort.Strings(unknown)
	return unknown
}

// LoadAppConfig reads an app config file (TOML or YAML by extension).
func LoadAppConfig(path string) (AppConfig, error) {
	var cfg AppConfig
	if err := LoadFile(path, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// NormalizeSecrets converts the secrets field (which may be a []string or
// map[string]string) into a canonical map[envVar]jsonKey. It returns an error
// for any non-string element so that malformed config entries fail fast rather
// than being silently dropped and causing missing secrets at runtime.
func NormalizeSecrets(raw any) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	result := make(map[string]string)

	switch v := raw.(type) {
	case []interface{}:
		for i, item := range v {
			key, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("secrets[%d]: expected string, got %T", i, item)
			}
			result[key] = key
		}
	case map[string]interface{}:
		for envVar, jsonKey := range v {
			k, ok := jsonKey.(string)
			if !ok {
				return nil, fmt.Errorf("secrets[%q]: expected string value, got %T", envVar, jsonKey)
			}
			result[envVar] = k
		}
	case []string:
		for _, key := range v {
			result[key] = key
		}
	case map[string]string:
		for k, vv := range v {
			result[k] = vv
		}
	default:
		return nil, fmt.Errorf("secrets: unsupported type %T", raw)
	}
	return result, nil
}

// BuildSecretSpec describes one Docker BuildKit secret that ops will fetch from
// Secrets Manager at image build time and pass as --secret id=<ID>,src=<file>.
type BuildSecretSpec struct {
	// ID is the Docker --secret id (matches the id= in --mount=type=secret,id=).
	ID string
	// ARN is the base Secrets Manager secret ARN (no JSON key suffix).
	ARN string
	// JSONKey is the key to extract from the JSON blob stored in ARN.
	JSONKey string
}

// ResolveBuildSecretSpecs resolves the build_secrets fields from the app config
// into BuildSecretSpec triples using the same merge-with-override semantics as
// ResolveSecrets:
//
//   - Global keys NOT present in the env section → fetched from {arnPrefix}:{serviceName}/shared
//   - Env-specific keys → fetched from {arnPrefix}:{serviceName}/{env}
//   - If a key appears in both, the env version wins and the global entry is dropped
//   - Keys only in global are still included
//
// The returned slice is sorted by ID for deterministic --secret flag ordering.
func ResolveBuildSecretSpecs(appCfg AppConfig, env, serviceName, arnPrefix string) ([]BuildSecretSpec, error) {
	globalMap, err := NormalizeSecrets(appCfg["global"].BuildSecrets)
	if err != nil {
		return nil, fmt.Errorf("global.build_secrets: %w", err)
	}
	envMap, err := NormalizeSecrets(appCfg[env].BuildSecrets)
	if err != nil {
		return nil, fmt.Errorf("%s.build_secrets: %w", env, err)
	}

	sharedARN := fmt.Sprintf("%s:%s/shared", arnPrefix, serviceName)
	envARN := fmt.Sprintf("%s:%s/%s", arnPrefix, serviceName, env)

	var specs []BuildSecretSpec

	for id, jsonKey := range globalMap {
		if _, overridden := envMap[id]; !overridden {
			specs = append(specs, BuildSecretSpec{ID: id, ARN: sharedARN, JSONKey: jsonKey})
		}
	}
	for id, jsonKey := range envMap {
		specs = append(specs, BuildSecretSpec{ID: id, ARN: envARN, JSONKey: jsonKey})
	}

	sort.Slice(specs, func(i, j int) bool { return specs[i].ID < specs[j].ID })
	return specs, nil
}

// ResolveBuildArgs merges global and env-specific build_args maps.
// Env values override matching global keys; non-matching global keys are still
// included. Returns nil when neither section defines any build args.
func ResolveBuildArgs(appCfg AppConfig, env string) map[string]string {
	globalArgs := appCfg["global"].BuildArgs
	envArgs := appCfg[env].BuildArgs

	if len(globalArgs) == 0 && len(envArgs) == 0 {
		return nil
	}

	merged := make(map[string]string, len(globalArgs)+len(envArgs))
	for k, v := range globalArgs {
		merged[k] = v
	}
	for k, v := range envArgs {
		merged[k] = v
	}
	return merged
}
