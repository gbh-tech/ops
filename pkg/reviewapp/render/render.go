// Package render turns a unit's deploy config into the UnitSpec that travels
// in the review app manifest. It runs on the client, which has the
// repository; the controller only reads the result.
package render

import (
	"fmt"
	"slices"

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
	if len(secrets) > 0 {
		spec.Secrets = make(map[string]string, len(secrets))
		for _, s := range secrets {
			spec.Secrets[s.Name] = s.ValueFrom
		}
	}
	return spec, nil
}
