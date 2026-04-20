package image

import (
	pkgapp "ops/pkg/app"
	"ops/pkg/config"
	"ops/pkg/utils"

	"charm.land/log/v2"
	"github.com/spf13/cobra"
)

// PushCommand is "ops push": pushes a previously built and tagged Docker image
// to the configured registry. Run "ops registry-login" first to authenticate.
//
// When --tag is provided (or resolves to a value different from --env), two
// tags are pushed:
//   - {registry}/{env}/{image}:{tag}  (the versioned tag, e.g. a git SHA or semver)
//   - {registry}/{env}/{image}:{env}  (environment pointer, e.g. "stage")
//
// When --tag is omitted, the tag defaults to the env name so both URIs are
// identical; only one push is performed in that case.
var PushCommand = &cobra.Command{
	Use:   "push",
	Short: "Push a Docker image to the configured registry",
	Long: `Push a Docker image to the registry.

When --tag differs from --env, two tags are pushed:

  {registry}/{env}/{image}:{tag}   versioned tag (e.g. git SHA or semver)
  {registry}/{env}/{image}:{env}   environment pointer (e.g. "stage", "production")

When --tag is omitted it defaults to the env name, so both URIs are identical
and only one push is performed.

Authenticate first with: ops registry-login

In mono-repo mode (repo_mode: mono) --app is required unless --app-config
points directly to an app config that defines the image name.`,
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		tag := resolveTag(cmd.Flags().Lookup("tag").Value.String(), env)
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		cfg := config.LoadConfig()

		if cfg.IsMonoRepo() && app == "" {
			log.Fatal("--app is required in mono-repo mode (repo_mode: mono)")
		}

		appConfigPath := cfg.ResolveAppFilePath(app, appConfigOverride, "deploy/config.toml")
		appCfg, err := pkgapp.LoadAppConfig(appConfigPath)
		if err != nil {
			log.Fatal("Failed to load app config", "path", appConfigPath, "err", err)
		}
		imageName := resolveImageName(cfg, app, appCfg)
		registryURL := cfg.RegistryURL()
		versionedURI := resolveImageURI(registryURL, env, imageName, tag)
		envURI := resolveImageURI(registryURL, env, imageName, env)

		utils.CheckBinary("docker")

		// Tag the local versioned image with the env pointer before pushing.
		if versionedURI != envURI {
			log.Info("Tagging image", "src", versionedURI, "dst", envURI)
			runDockerCmd("tag", versionedURI, envURI)
		}

		log.Info("Pushing image", "uri", versionedURI)
		runDockerCmd("push", versionedURI)

		if versionedURI != envURI {
			log.Info("Pushing image", "uri", envURI)
			runDockerCmd("push", envURI)
		}
	},
}

func init() {
	PushCommand.Flags().StringP("app", "a", "", "App name (required in mono-repo mode)")
	PushCommand.Flags().StringP("env", "e", "", "Target environment (required)")
	PushCommand.Flags().StringP("tag", "t", "", "Image tag (defaults to the env name, e.g. \"stage\")")
	PushCommand.Flags().String("app-config", "", "Override path to app config file")
	_ = PushCommand.MarkFlagRequired("env")
}
