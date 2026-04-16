package image

import (
	"ops/pkg/config"
	"ops/pkg/utils"

	"charm.land/log/v2"
	"github.com/spf13/cobra"
)

// PushCommand is "ops push": pushes a previously built and tagged Docker image
// to the configured registry. Run "ops registry-login" first to authenticate.
//
// Two tags are always pushed:
//   - {registry}/{env}/{image}:{tag}  (the versioned tag, e.g. a git SHA or semver)
//   - {registry}/{env}/{image}:{env}  (tracks the latest image deployed to that env)
var PushCommand = &cobra.Command{
	Use:   "push",
	Short: "Push a Docker image to the configured registry",
	Long: `Push a Docker image to the registry with two tags:

  {registry}/{env}/{image}:{tag}   versioned tag (e.g. git SHA or semver)
  {registry}/{env}/{image}:{env}   environment pointer (e.g. "stage", "production")

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
		imageName := resolveImageName(cfg, app, appConfigPath)
		versionedURI := resolveImageURI(cfg.Registry.URL, env, imageName, tag)
		envURI := resolveImageURI(cfg.Registry.URL, env, imageName, env)

		utils.CheckBinary("docker")

		// Tag the local versioned image with the env pointer before pushing.
		if versionedURI != envURI {
			log.Info("Tagging image", "src", versionedURI, "dst", envURI)
			runDockerCmd("tag", versionedURI, envURI)
		}

		log.Info("Pushing image", "uri", versionedURI)
		runDockerCmd("push", versionedURI)

		log.Info("Pushing image", "uri", envURI)
		runDockerCmd("push", envURI)
	},
}

func init() {
	PushCommand.Flags().StringP("app", "a", "", "App name (required in mono-repo mode)")
	PushCommand.Flags().StringP("env", "e", "", "Target environment (required)")
	PushCommand.Flags().StringP("tag", "t", "", "Image tag (defaults to the env name, e.g. \"stage\")")
	PushCommand.Flags().String("app-config", "", "Override path to app config file")
	_ = PushCommand.MarkFlagRequired("env")
}
