package image

import (
	"ops/pkg/config"
	"ops/pkg/utils"

	"charm.land/log/v2"
	"github.com/spf13/cobra"
)

// BuildCommand is "ops build": builds a Docker image and tags it for the
// configured registry using the resolved image URI.
var BuildCommand = &cobra.Command{
	Use:   "build",
	Short: "Build a Docker image tagged for the configured registry",
	Long: `Build a Docker image tagged as {registry}/{env}/{image}:{tag}.

In mono-repo mode (repo_mode: mono) --app is required unless --app-config
points directly to an app config that defines the image name.

The default build context is {apps_dir}/{app}/ in mono-repo mode so that
Docker picks up the app's own .dockerignore automatically. Use --context .
to widen the context to the repo root when the Dockerfile COPYs shared code.`,
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		tag := resolveTag(cmd.Flags().Lookup("tag").Value.String(), env)
		appConfigOverride, _ := cmd.Flags().GetString("app-config")
		dockerfile, _ := cmd.Flags().GetString("dockerfile")
		buildContext, _ := cmd.Flags().GetString("context")

		cfg := config.LoadConfig()

		if cfg.IsMonoRepo() && app == "" {
			log.Fatal("--app is required in mono-repo mode (repo_mode: mono)")
		}

		platform, _ := cmd.Flags().GetString("platform")

		appConfigPath := cfg.ResolveAppFilePath(app, appConfigOverride, "deploy/config.toml")
		imageName := resolveImageName(cfg, app, appConfigPath)
		imageURI := resolveImageURI(cfg.Registry.URL, env, imageName, tag)

		if dockerfile == "" {
			dockerfile = defaultDockerfile(cfg, app)
		}
		if buildContext == "" {
			buildContext = defaultBuildContext(cfg, app)
		}

		noCache, _ := cmd.Flags().GetBool("no-cache")

		utils.CheckBinary("docker")
		log.Info("Building image", "uri", imageURI, "dockerfile", dockerfile, "context", buildContext, "platform", platform, "no-cache", noCache)

		buildArgs := []string{"--platform", platform, "-t", imageURI, "-f", dockerfile}
		if noCache {
			buildArgs = append(buildArgs, "--no-cache")
		}
		buildArgs = append(buildArgs, buildContext)
		runDockerCmd("build", buildArgs...)
	},
}

func init() {
	BuildCommand.Flags().StringP("app", "a", "", "App name (required in mono-repo mode)")
	BuildCommand.Flags().StringP("env", "e", "", "Target environment (required)")
	BuildCommand.Flags().StringP("tag", "t", "", "Image tag (defaults to the env name, e.g. \"stage\")")
	BuildCommand.Flags().String("app-config", "", "Override path to app config file")
	BuildCommand.Flags().String("dockerfile", "", "Path to Dockerfile (defaults to {apps_dir}/{app}/Dockerfile in mono-repo, Dockerfile otherwise)")
	BuildCommand.Flags().String("context", "", "Docker build context (defaults to {apps_dir}/{app}/ in mono-repo, \".\" otherwise)")
	BuildCommand.Flags().String("platform", "linux/amd64", "Target platform for the build (passed to docker --platform)")
	BuildCommand.Flags().Bool("no-cache", false, "Do not use cache when building the image")
	_ = BuildCommand.MarkFlagRequired("env")
}
