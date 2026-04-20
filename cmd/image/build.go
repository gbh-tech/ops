package image

import (
	"context"
	"fmt"
	"os"
	"sort"

	pkgapp "ops/pkg/app"
	pkgaws "ops/pkg/aws"
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

		// Load AppConfig once; shared by image name resolution and build config.
		// Fail immediately if the path resolves but the file cannot be read or
		// parsed — silently falling back to a nil config would hide mistyped
		// --app-config paths and produce unexpected image names.
		appCfg, err := pkgapp.LoadAppConfig(appConfigPath)
		if err != nil {
			log.Fatal("Failed to load app config", "path", appConfigPath, "err", err)
		}

		imageName := resolveImageName(cfg, app, appCfg)
		imageURI := resolveImageURI(cfg.RegistryURL(), env, imageName, tag)

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
		if appCfg != nil {
			var cleanup func()
			secretArgs, buildArgArgs, cleanup = resolveBuildConfig(cmd.Context(), cfg, appCfg, app, env)
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

// resolveBuildConfig resolves build_secrets and build_args from an already-loaded
// AppConfig and returns the corresponding docker CLI argument slices plus a cleanup
// function that removes any temp files written for secrets. The caller must invoke
// cleanup() after docker build completes (success or failure).
func resolveBuildConfig(ctx context.Context, cfg *config.OpsConfig, appCfg pkgapp.AppConfig, app, env string) (secretArgs, buildArgArgs []string, cleanup func()) {
	cleanup = func() {} // no-op default

	serviceName := resolveServiceName(appCfg, app, cfg)

	// --- build_secrets ---
	specs, err := pkgapp.ResolveBuildSecretSpecs(appCfg, env, serviceName, cfg.ECS.ResolvedSecretArnPrefix(cfg.AWS))
	if err != nil {
		log.Fatal("Invalid build_secrets config", "err", err)
	}
	if len(specs) > 0 {
		secretArgs, cleanup = fetchAndWriteSecrets(ctx, cfg, specs)
	}

	// --- build_args ---
	buildArgs := pkgapp.ResolveBuildArgs(appCfg, env)
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
// writes each plaintext value into a dedicated temp directory, and returns the
// accumulated --secret flag args along with a cleanup function. The caller must
// invoke cleanup() after docker build completes (success or failure); it removes
// the entire temp directory in one call.
func fetchAndWriteSecrets(ctx context.Context, cfg *config.OpsConfig, specs []pkgapp.BuildSecretSpec) (args []string, cleanup func()) {
	awsCfg := pkgaws.NewAWSConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	smClient := pkgaws.NewSecretsManagerClient(awsCfg)

	// Create a single temp directory for all secret files. One RemoveAll in
	// cleanup handles partial failures cleanly.
	tmpDir, err := os.MkdirTemp("", "ops-build-secrets-*")
	if err != nil {
		log.Fatal("Failed to create temp directory for build secrets", "err", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }

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

	// Fetch and write one file per secret key inside tmpDir.
	// os.CreateTemp gives each file an OS-assigned name inside tmpDir so
	// user-controlled id values cannot escape the directory via path traversal.
	// id is only used as the Docker secret name in the --secret flag, never as
	// a filesystem path component.
	idToFile := map[string]string{}
	for _, g := range byARN {
		values, ferr := pkgaws.FetchSecretKeys(ctx, smClient, g.arn, g.keys)
		if ferr != nil {
			log.Fatal("Failed to fetch build secrets", "arn", g.arn, "err", ferr)
		}
		for i, id := range g.ids {
			key := g.keys[i]
			val := values[key] // FetchSecretKeys errors on missing keys
			f, cerr := os.CreateTemp(tmpDir, "secret-*")
			if cerr != nil {
				log.Fatal("Failed to create temp file for build secret", "id", id, "err", cerr)
			}
			if _, werr := f.Write([]byte(val)); werr != nil {
				_ = f.Close()
				log.Fatal("Failed to write build secret file", "id", id, "err", werr)
			}
			if cerr := f.Close(); cerr != nil {
				log.Fatal("Failed to close build secret file", "id", id, "err", cerr)
			}
			idToFile[id] = f.Name()
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
