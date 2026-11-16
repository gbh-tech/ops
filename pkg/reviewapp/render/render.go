// Package render turns a unit's deploy config into the UnitSpec that travels
// in the review app manifest. It runs on the client, which has the
// repository; the controller only reads the result.
package render

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"ops/pkg/app"
	pkgecs "ops/pkg/ecs"
	"ops/pkg/reviewapp/state"
)

// Unit renders one unit for the review app section env (e.g. "reviewapp").
// The unit must declare that section, so its secrets resolve to
// <secrets name>/<env> rather than another environment's.
func Unit(appCfg app.AppConfig, env string, defaults pkgecs.BaseDefaults, secretARNPrefix string) (state.UnitSpec, error) {
	if _, ok := appCfg[env]; !ok {
		return state.UnitSpec{}, fmt.Errorf("no [%s] section", env)
	}
	merged, err := pkgecs.ResolveConfig(&pkgecs.BaseConfig{Defaults: defaults}, appCfg, env)
	if err != nil {
		return state.UnitSpec{}, err
	}
	secrets, err := pkgecs.ResolveSecrets(appCfg, env, merged.SecretsName, secretARNPrefix)
	if err != nil {
		return state.UnitSpec{}, err
	}

	spec := state.UnitSpec{
		CPU:         merged.CPU,
		Memory:      merged.Memory,
		Command:     merged.Command,
		EntryPoint:  merged.EntryPoint,
		Environment: merged.Environment,
		HealthCheck: merged.ContainerHC.Command,
	}
	for _, p := range append([]int{merged.Port}, merged.Ports...) {
		if p != 0 && !slices.Contains(spec.Ports, p) {
			spec.Ports = append(spec.Ports, p)
		}
	}
	files, err := Files(appCfg[env].Files)
	if err != nil {
		return state.UnitSpec{}, err
	}
	spec.Files = files
	if len(secrets) > 0 {
		spec.Secrets = make(map[string]string, len(secrets))
		for _, s := range secrets {
			spec.Secrets[s.Name] = s.ValueFrom
		}
	}
	return spec, nil
}

// fileKey is an object key under the data prefix. The charset keeps the key
// safe in the init container's shell command.
var fileKey = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// filePath is an absolute container path, with the same charset.
var filePath = regexp.MustCompile(`^(/[A-Za-z0-9._-]+)+$`)

// Files checks the [<env>.files] entries: absolute, clean container paths
// below a directory, and keys that stay inside the review app's data prefix.
func Files(files map[string]string) (map[string]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(files))
	for dst, key := range files {
		if !filePath.MatchString(dst) || path.Clean(dst) != dst || path.Dir(dst) == "/" || slices.Contains([]string{".", ".."}, path.Base(dst)) {
			return nil, fmt.Errorf("files: %q is not an absolute file path below a directory", dst)
		}
		if !fileKey.MatchString(key) || slices.ContainsFunc(strings.Split(key, "/"), func(p string) bool { return p == "." || p == ".." }) {
			return nil, fmt.Errorf("files: key %q of %s is not a key under the data prefix", key, dst)
		}
		out[dst] = key
	}
	// A destination inside another would be hidden by, or mount over, the
	// other's directory. Whole components only: /a/schema and /a/schemas are
	// fine.
	dsts := slices.Sorted(maps.Keys(out))
	for _, a := range dsts {
		for _, b := range dsts {
			if strings.HasPrefix(b, a+"/") {
				return nil, fmt.Errorf("files: %s is inside %s; a destination cannot contain another", b, a)
			}
		}
	}
	return out, nil
}
