package image

import (
	"fmt"
	"os"
	"os/exec"

	pkgapp "ops/pkg/app"
	"ops/pkg/config"
	"path/filepath"

	"charm.land/log/v2"
)

// resolveTag returns tag when explicitly provided, otherwise falls back to env.
// This keeps build/push consistent with "ops ecs deploy" which defaults to the
// env name so the workflow ops build → ops push → ops ecs deploy all agree
// on the same tag without requiring an explicit --tag on every invocation.
func resolveTag(tag, env string) string {
	if tag != "" {
		return tag
	}
	return env
}

// resolveImageURI builds the full image URI: {registryURL}/{env}/{imageName}:{tag}
func resolveImageURI(registryURL, env, imageName, tag string) string {
	return fmt.Sprintf("%s/%s/%s:%s", registryURL, env, imageName, tag)
}

// resolveImageName determines the image name to use for build/push.
// It loads the app config when available and reads the image field from the
// global section, falling back to the app name (or project in single-repo mode).
func resolveImageName(cfg *config.OpsConfig, app, appConfigPath string) string {
	if appConfigPath != "" {
		if name := imageFromConfig(appConfigPath); name != "" {
			return name
		}
	}
	if app != "" {
		return app
	}
	return cfg.Project
}

// imageFromConfig reads the image field from the global section of an app config.
// Returns empty string if the file cannot be read or has no image field.
func imageFromConfig(path string) string {
	appCfg, err := pkgapp.LoadAppConfig(path)
	if err != nil {
		return ""
	}
	if global, ok := appCfg["global"]; ok && global.Image != "" {
		return global.Image
	}
	return ""
}

// defaultDockerfile returns the default Dockerfile path for the given app.
// In mono-repo mode it is {apps_dir}/{app}/Dockerfile; in single-repo it is
// "Dockerfile" at the repo root.
func defaultDockerfile(cfg *config.OpsConfig, app string) string {
	if cfg.IsMonoRepo() && app != "" {
		return filepath.Join(cfg.AppsDirPath(), app, "Dockerfile")
	}
	return "Dockerfile"
}

// defaultBuildContext returns the default Docker build context for the given app.
//
// In mono-repo mode the context is scoped to the app directory
// ({apps_dir}/{app}/) so that Docker automatically picks up the app-level
// .dockerignore and only sends that subtree to the daemon. Users who need the
// repo root (e.g. to COPY shared code) can override with --context .
//
// In single-repo mode the context is "." (the repo root).
func defaultBuildContext(cfg *config.OpsConfig, app string) string {
	if cfg.IsMonoRepo() && app != "" {
		return filepath.Join(cfg.AppsDirPath(), app)
	}
	return "."
}

// runDockerCmd shells out to docker, streaming stdout/stderr to the terminal.
func runDockerCmd(subcmd string, extraArgs ...string) {
	dockerArgs := append([]string{subcmd}, extraArgs...)
	c := exec.Command("docker", dockerArgs...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		log.Fatal("docker command failed", "subcmd", subcmd, "err", err)
	}
}
