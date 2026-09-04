package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"ops/pkg/app"
	"ops/pkg/config"
)

type Unit struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Root         string            `json:"root"`
	Config       string            `json:"config"`
	Dockerfile   string            `json:"dockerfile"`
	Package      string            `json:"package,omitempty"`
	Environments []string          `json:"environments"`
	Subgraph     *SubgraphMetadata `json:"subgraph,omitempty"`
}

type SubgraphMetadata struct {
	Name   string `json:"name"`
	Port   int    `json:"port"`
	Schema string `json:"schema"`
}

func Discover(cfg *config.OpsConfig) ([]Unit, error) {
	var units []Unit
	seen := map[string]string{}
	for _, root := range cfg.AppsDirPaths() {
		matches, err := filepath.Glob(filepath.Join(root, "*", "deploy", "config.toml"))
		if err != nil {
			return nil, fmt.Errorf("discovering app configs under %s: %w", root, err)
		}
		for _, configPath := range matches {
			appRoot := filepath.Dir(filepath.Dir(configPath))
			appCfg, err := app.LoadAppConfig(configPath)
			if err != nil {
				return nil, err
			}
			global, ok := appCfg["global"]
			if !ok || global.Name == "" || global.Kind == "" {
				return nil, fmt.Errorf("%s requires global.name and global.kind", configPath)
			}
			if prior, exists := seen[global.Name]; exists {
				return nil, fmt.Errorf("duplicate app ID %q in %s and %s", global.Name, prior, configPath)
			}
			seen[global.Name] = configPath
			dockerfile := filepath.Join(appRoot, "Dockerfile")
			info, err := os.Stat(dockerfile)
			if err != nil {
				return nil, fmt.Errorf("dockerfile for %s: %w", global.Name, err)
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("dockerfile for %s must be a regular file", global.Name)
			}
			var packageName string
			if data, err := os.ReadFile(filepath.Join(appRoot, "package.json")); err == nil {
				var pkg struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(data, &pkg); err != nil {
					return nil, fmt.Errorf("parsing package for %s: %w", global.Name, err)
				}
				packageName = pkg.Name
			}
			var environments []string
			for section := range appCfg {
				if section != "global" {
					environments = append(environments, section)
				}
			}
			sort.Strings(environments)
			var subgraph *SubgraphMetadata
			if global.Subgraph != nil {
				subgraph = &SubgraphMetadata{
					Name:   global.Subgraph.Name,
					Port:   global.Port,
					Schema: filepath.Join(appRoot, global.Subgraph.Schema),
				}
			}
			units = append(units, Unit{ID: global.Name, Kind: global.Kind, Root: appRoot, Config: configPath, Dockerfile: dockerfile, Package: packageName, Environments: environments, Subgraph: subgraph})
		}
	}
	sort.Slice(units, func(i, j int) bool { return units[i].ID < units[j].ID })
	return units, nil
}
