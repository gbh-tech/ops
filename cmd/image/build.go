package image

import (
	"context"
	"fmt"
	"os"
	"sort"

	pkgapp "ops/pkg/app"
	pkgaws "ops/pkg/aws"
	"ops/pkg/config"
	pkgecs "ops/pkg/ecs"
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
to widen the context to the repo root when the Dockerfile COPYs shared code.

build_secrets and build_args declared in deploy/config.toml are automatically
applied. Use --secret and --build-arg for additional values (e.g. local dev).`,
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		tag := resolveTag(cmd.Flags().Lookup("tag").Value.String(), env)
		appConfigOverride, _ := cmd.Flags().GetString("app-config")
		dockerfile, _ := cmd.Flags().GetString("dockerfile")
		buildContext, _ := cmd.Flags().GetString("context")
		extraSecrets, _ := cmd.Flags().GetStringArray("secret")
		extraBuildArgs, _ := cmd.Flags().GetStringArray("build-arg")

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

		// Resolve build_secrets and build_args from the app config.
		var secretArgs []string
		var buildArgArgs []string
		if appConfigPath != "" {
			var cleanup func()
			secretArgs, buildArgArgs, cleanup = resolveBuildConfig(cmd.Context(), cfg, appConfigPath, app, env)
			defer cleanup()
		}

		// Append CLI-provided --secret and --build-arg values after config-derived ones.
		for _, s := range extraSecrets {
			secretArgs = append(secretArgs, "--secret", s)
		}
		for _, a := range extraBuildArgs {
			buildArgArgs = append(buildArgArgs, "--build-arg", a)
		}

		log.Info("Building image",
			"uri", imageURI,
			"dockerfile", dockerfile,
			"context", buildContext,
			"platform", platform,
			"no-cache", noCache,
			"build_secrets", len(secretArgs)/2,
			"build_args", len(buildArgArgs)/2,
		)

		dockerArgs := append([]string{"--platform", platform, "-t", imageURI, "-f", dockerfile}, secretArgs...)
		dockerArgs = append(dockerArgs, buildArgArgs...)
		if noCache {
			dockerArgs = append(dockerArgs, "--no-cache")
		}
		dockerArgs = append(dockerArgs, buildContext)
		runDockerCmd("build", dockerArgs...)
	},
}

// resolveBuildConfig loads the app config, resolves build_secrets and
// build_args, and returns the corresponding docker CLI argument slices plus a
// cleanup function that removes any temp files written for secrets. The caller
// must invoke cleanup() after docker build completes (success or failure).
func resolveBuildConfig(ctx context.Context, cfg *config.OpsConfig, appConfigPath, app, env string) (secretArgs, buildArgArgs []string, cleanup func()) {
	cleanup = func() {} // no-op default

	appCfg, err := pkgapp.LoadAppConfig(appConfigPath)
	if err != nil {
		log.Fatal("Failed to load app config", "path", appConfigPath, "err", err)
	}

	serviceName := resolveServiceName(appCfg, app, cfg)

	// --- build_secrets ---
	specs := pkgecs.ResolveBuildSecretSpecs(appCfg, env, serviceName, cfg.ECS.SecretArnPrefix)
	if len(specs) > 0 {
		secretArgs, cleanup = fetchAndWriteSecrets(ctx, cfg, specs)
	}

	// --- build_args ---
	buildArgs := pkgecs.ResolveBuildArgs(appCfg, env)
	keys := make([]string, 0, len(buildArgs))
	for k := range buildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic order
	for _, k := range keys {
		buildArgArgs = append(buildArgArgs, "--build-arg", fmt.Sprintf("%s=%s", k, buildArgs[k]))
	}

	return secretArgs, buildArgArgs, cleanup
}

// resolveServiceName determines the Secrets Manager service name using the
// same fallback chain as ecsSecretsCmd: global.secrets_name → global.name →
// app flag → cfg.Project.
func resolveServiceName(appCfg pkgapp.AppConfig, app string, cfg *config.OpsConfig) string {
	if global, ok := appCfg["global"]; ok {
		if global.SecretsName != "" {
			return global.SecretsName
		}
		if global.Name != "" {
			return global.Name
		}
	}
	if app != "" {
		return app
	}
	return cfg.Project
}

// fetchAndWriteSecrets fetches all build secret specs from Secrets Manager,
// writes each plaintext value to a temp file, and returns the accumulated
// --secret flag args along with a cleanup function that removes all temp files.
// The caller must invoke cleanup() after docker build completes.
func fetchAndWriteSecrets(ctx context.Context, cfg *config.OpsConfig, specs []pkgecs.BuildSecretSpec) (args []string, cleanup func()) {
	awsCfg := pkgaws.NewAWSConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	smClient := pkgaws.NewSecretsManagerClient(awsCfg)

	// Group specs by ARN to minimise Secrets Manager API calls.
	type specGroup struct {
		arn  string
		ids  []string
		keys []string
	}
	byARN := map[string]*specGroup{}
	for _, spec := range specs {
		g, ok := byARN[spec.ARN]
		if !ok {
			g = &specGroup{arn: spec.ARN}
			byARN[spec.ARN] = g
		}
		g.ids = append(g.ids, spec.ID)
		g.keys = append(g.keys, spec.JSONKey)
	}

	// Fetch and write one temp file per secret key.
	var tmpFiles []string
	idToFile := map[string]string{}
	for _, g := range byARN {
		values, err := pkgaws.FetchSecretKeys(ctx, smClient, g.arn, g.keys)
		if err != nil {
			log.Fatal("Failed to fetch build secrets", "arn", g.arn, "err", err)
		}
		for i, id := range g.ids {
			key := g.keys[i]
			val, ok := values[key]
			if !ok {
				log.Fatal("Secret key not found in fetched secret", "key", key, "arn", g.arn)
			}
			f, ferr := os.CreateTemp("", "ops-build-secret-*")
			if ferr != nil {
				log.Fatal("Failed to create temp file for build secret", "id", id, "err", ferr)
			}
			tmpPath := f.Name()
			if _, werr := f.WriteString(val); werr != nil {
				_ = f.Close()
				_ = os.Remove(tmpPath)
				log.Fatal("Failed to write build secret to temp file", "id", id, "err", werr)
			}
			_ = f.Close()
			tmpFiles = append(tmpFiles, tmpPath)
			idToFile[id] = tmpPath
		}
	}

	cleanup = func() {
		for _, p := range tmpFiles {
			_ = os.Remove(p)
		}
	}

	// Build --secret args in the original spec order for determinism.
	for _, spec := range specs {
		args = append(args, "--secret", fmt.Sprintf("id=%s,src=%s", spec.ID, idToFile[spec.ID]))
	}
	return args, cleanup
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
	BuildCommand.Flags().StringArray("secret", nil, "Additional Docker BuildKit secret (id=<name>,src=<file> or env=<var>); appended after config build_secrets")
	BuildCommand.Flags().StringArray("build-arg", nil, "Additional Docker build argument (KEY=VALUE); appended after config build_args")
	_ = BuildCommand.MarkFlagRequired("env")
}
