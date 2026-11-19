package drift

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"ops/pkg/app"
	"ops/pkg/catalog"
	"ops/pkg/config"
	pkgecs "ops/pkg/ecs"
)

// ecrHost matches the account and region of an ECR registry host.
var ecrHost = regexp.MustCompile(`^(\d{12})\.dkr\.ecr\.([a-z0-9-]+)\.`)

// RegistryRegion returns the region of an ECR registry, or "" when registry is
// not an ECR host. A registry can live in another region than the workloads,
// and its images have to be looked up there.
func RegistryRegion(registry string) string {
	if match := ecrHost.FindStringSubmatch(registry); match != nil {
		return match[2]
	}
	return ""
}

// Targets resolves the catalog units that tags names into comparison targets
// for env, sorted by ID. tags maps unit IDs to image tags.
//
// A unit of a kind other than "ecs" or "lambda" has no running state and is
// left out, as is a unit tags does not name. An entry of tags that is not a
// catalog unit is ignored: an environment keeps the entry of a removed unit
// until its manifest is next updated, and there is nothing to compare for it.
func Targets(cfg *config.OpsConfig, units []catalog.Unit, tags map[string]string, env string) ([]Target, error) {
	base := pkgecs.BaseConfigFor(cfg, env)
	registry := cfg.RegistryURL()
	registryID := ""
	if match := ecrHost.FindStringSubmatch(registry); match != nil && match[1] != cfg.AWS.AccountId {
		registryID = match[1]
	}

	var targets []Target
	for _, unit := range units {
		tag, named := tags[unit.ID]
		if !named || (unit.Kind != KindECS && unit.Kind != KindLambda) {
			continue
		}
		// Deploying an empty tag falls back to another one, so it could never
		// be brought in sync.
		if tag == "" {
			return nil, fmt.Errorf("the manifest names no tag for unit %q", unit.ID)
		}
		target := Target{ID: unit.ID, Kind: unit.Kind, Tag: tag, RegistryID: registryID}
		switch unit.Kind {
		case KindECS:
			if base.ECS.Cluster == "" {
				return nil, fmt.Errorf("ecs.cluster is not set in the ops config; it names the cluster of unit %q", unit.ID)
			}
			appCfg, err := app.LoadAppConfig(unit.Config)
			if err != nil {
				return nil, err
			}
			merged, err := pkgecs.ResolveConfig(base, appCfg, env)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", unit.Config, err)
			}
			target.Image = pkgecs.ImageURI(base, merged, env, tag)
			target.Repository, target.ImageTag, target.ImageDigest = reference(target.Image, registry)
			target.Cluster = base.ECS.Cluster
			target.Service = pkgecs.ComputeNames(merged, env, base.ECS.Cluster).Service
			target.Container = merged.Name
		case KindLambda:
			target.Function = cfg.Lambda.ResolvedFunctionName(unit.ID, env)
			if target.Function == "" {
				return nil, fmt.Errorf("lambda.function_name is not set in the ops config; it names the function of unit %q", unit.ID)
			}
			// A function image lives where a service image of the same name would.
			image := pkgecs.ImageURI(base, pkgecs.MergedConfig{AppSection: app.AppSection{Name: unit.ID}}, env, tag)
			target.Repository, target.ImageTag, target.ImageDigest = reference(image, registry)
		}
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	return targets, nil
}

// reference splits an image of the registry into its repository and the tag
// or digest it names. The repository is "" when the image does not live in the
// registry. A unit's configuration can pin its image to a tag or a digest of
// its own, so these come from the image and not from the manifest tag.
func reference(image, registry string) (repository, tag, digest string) {
	path, inRegistry := strings.CutPrefix(image, registry+"/")
	if registry == "" || !inRegistry {
		return "", "", ""
	}
	// The digest goes first: it holds a colon of its own.
	path, digest, _ = strings.Cut(path, "@")
	repository, tag, _ = strings.Cut(path, ":")
	if digest != "" {
		tag = ""
	}
	return repository, tag, digest
}
