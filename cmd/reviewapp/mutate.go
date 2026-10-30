package reviewapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"ops/pkg/app"
	"ops/pkg/catalog"
	"ops/pkg/config"
	pkgecs "ops/pkg/ecs"
	"ops/pkg/reviewapp/client"
	"ops/pkg/reviewapp/infra"
	"ops/pkg/reviewapp/provision"
	"ops/pkg/reviewapp/render"
	"ops/pkg/reviewapp/state"

	"charm.land/log/v2"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	sd "github.com/aws/aws-sdk-go-v2/service/servicediscovery"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
)

// Commands that change a review app. Outside the cluster each one uploads
// what the controller needs and runs it in the reviewapp-controller task
// (client mode); with --in-cluster it does the work (D11).

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Create the review app of a pull request, or update it to a new commit",
	Long: `Deploy brings the review app of a pull request to the images in --manifest.

The first run creates it: namespace, database, bootstrap (topics, migrations,
seed), target groups, listener rules and services. Later runs find its state
and update it: migrations and new task definitions. A failed run resumes
where it stopped, and a paused review app is unpaused.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		pr := prFlag(cmd)
		if inCluster(cmd) {
			c := controller(cmd)
			must(c.Deploy(c.ctx, pr, manifestRef(cmd)))
			return
		}
		runClient(cmd, client.Run{Command: "deploy", PR: pr, Manifest: clientManifest(cmd, pr)})
	},
}

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Delete a review app and everything it created",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		pr := prFlag(cmd)
		if inCluster(cmd) {
			c := controller(cmd)
			must(c.Destroy(c.ctx, pr))
			return
		}
		runClient(cmd, client.Run{Command: "destroy", PR: pr})
	},
}

var pauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Scale a review app to zero and keep its data; --all pauses every ready one",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		all, _ := cmd.Flags().GetBool("all")
		if all {
			if !inCluster(cmd) {
				log.Fatal("pause --all runs only in the controller task (--in-cluster)")
			}
			c := controller(cmd)
			must(c.PauseAll(c.ctx))
			return
		}
		pr := prFlag(cmd)
		if inCluster(cmd) {
			c := controller(cmd)
			must(c.Pause(c.ctx, pr))
			return
		}
		runClient(cmd, client.Run{Command: "pause", PR: pr})
	},
}

var unpauseCmd = &cobra.Command{
	Use:   "unpause",
	Short: "Scale a paused review app back up",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		pr := prFlag(cmd)
		if inCluster(cmd) {
			c := controller(cmd)
			must(c.Unpause(c.ctx, pr))
			return
		}
		runClient(cmd, client.Run{Command: "unpause", PR: pr})
	},
}

var runCmd = &cobra.Command{
	Use:   "run <unit> [-- command...]",
	Short: "Run a one-off task of a unit, e.g. a scheduled function",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pr := prFlag(cmd)
		if inCluster(cmd) {
			c := controller(cmd)
			must(c.Run(c.ctx, pr, args[0], args[1:]))
			return
		}
		runClient(cmd, client.Run{Command: "run", PR: pr, Args: append([]string{"--"}, args...)})
	},
}

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Destroy expired review apps and pause idle ones (scheduled)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		if !inCluster(cmd) {
			log.Fatal("gc runs only in the controller task (--in-cluster)")
		}
		c := controller(cmd)
		must(c.GC(c.ctx))
	},
}

func init() {
	pf := Command.PersistentFlags()
	pf.Bool("in-cluster", false, "Do the work here instead of in the controller task (set by the client)")
	pf.Bool("allow-local", false, "Allow --in-cluster outside ECS, for a sandbox account")
	pf.String("manifest-version", "", "S3 version of the manifest to deploy (set by the client)")
	pf.String("manifest-sha256", "", "sha256 of that manifest version (set by the client)")
	pf.String("repository", "", "owner/name of the repository (default: GITHUB_REPOSITORY)")
	pf.String("actor", "", "Who started the action (default: GITHUB_ACTOR, then USER)")
	pf.String("run-url", "", "Workflow run URL for the history (default: from GITHUB_* variables)")
	for _, name := range []string{"in-cluster", "allow-local", "manifest-version", "manifest-sha256"} {
		_ = pf.MarkHidden(name)
	}

	for _, c := range []*cobra.Command{deployCmd, destroyCmd, pauseCmd, unpauseCmd, runCmd} {
		c.Flags().Int("pr", 0, "Pull request number")
	}
	deployCmd.Flags().String("manifest", "", "Manifest file: {pr, sha, tag, images: {unit: image}} (client mode)")
	pauseCmd.Flags().Bool("all", false, "Pause every ready review app (in-cluster only)")

	Command.AddCommand(deployCmd, destroyCmd, pauseCmd, unpauseCmd, runCmd, gcCmd)
}

func must(err error) {
	if err != nil {
		log.Fatal("Review app command failed", "err", err)
	}
}

// inCluster reports whether this run does the work. It refuses to do it
// outside an ECS task unless --allow-local is given.
func inCluster(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("in-cluster")
	if !v {
		return false
	}
	local, _ := cmd.Flags().GetBool("allow-local")
	if os.Getenv("ECS_CONTAINER_METADATA_URI_V4") == "" && !local {
		log.Fatal("--in-cluster runs only inside the reviewapp-controller task; pass --allow-local for a sandbox account")
	}
	return true
}

func manifestRef(cmd *cobra.Command) state.ManifestRef {
	version, _ := cmd.Flags().GetString("manifest-version")
	sum, _ := cmd.Flags().GetString("manifest-sha256")
	if version == "" || sum == "" {
		log.Fatal("--manifest-version and --manifest-sha256 are required in the controller")
	}
	return state.ManifestRef{VersionID: version, SHA256: sum}
}

// identity fills repository, actor and run URL from flags or GitHub Actions.
func identity(cmd *cobra.Command) (repository, actor, runURL string) {
	repository, _ = cmd.Flags().GetString("repository")
	actor, _ = cmd.Flags().GetString("actor")
	runURL, _ = cmd.Flags().GetString("run-url")
	if repository == "" {
		repository = os.Getenv("GITHUB_REPOSITORY")
	}
	if actor == "" {
		actor = os.Getenv("GITHUB_ACTOR")
	}
	if actor == "" {
		actor = os.Getenv("USER")
	}
	if runURL == "" && os.Getenv("GITHUB_RUN_ID") != "" {
		runURL = fmt.Sprintf("%s/%s/actions/runs/%s", os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_RUN_ID"))
	}
	return repository, actor, runURL
}

// runClient starts the controller task and exits with its exit code.
func runClient(cmd *cobra.Command, run client.Run) {
	rc := load(cmd)
	controllerUnit, err := rc.infra.LoadUnit(rc.ctx, ssm.NewFromConfig(rc.aws), infra.ControllerUnit)
	if err != nil {
		log.Fatal("Failed to read the controller unit", "err", err)
	}
	repository, actor, runURL := identity(cmd)
	if os.Getenv("GITHUB_RUN_ID") != "" {
		run.StartedBy = "github-" + os.Getenv("GITHUB_RUN_ID")
	}
	c := &client.Client{
		ECS:        awsecs.NewFromConfig(rc.aws),
		Logs:       cwlogs.NewFromConfig(rc.aws),
		Manifests:  rc.store(),
		Infra:      rc.infra,
		Controller: controllerUnit,
		Settings: client.Settings{
			Domain:     rc.settings.domain,
			InfraSSM:   rc.settings.infraSSM,
			ToolsImage: rc.settings.toolsImage,
			Region:     rc.settings.region,
			Repository: repository,
			Actor:      actor,
			RunURL:     runURL,
			IdlePause:  rc.settings.idlePause,
		},
		Out: os.Stdout,
	}
	res, err := c.Execute(rc.ctx, run)
	if err != nil {
		log.Fatal("Controller run failed", "err", err)
	}
	if res.ExitCode != 0 {
		log.Error("Controller task failed", "task", res.TaskARN, "exit_code", res.ExitCode)
	}
	os.Exit(res.ExitCode)
}

// manifestFile is what the workflow writes: the images it built or reuses.
type manifestFile struct {
	PR  int    `json:"pr"`
	SHA string `json:"sha"`
	// Tag is the tag of the images built for the PR; the helper image uses it.
	Tag        string            `json:"tag"`
	ToolsImage string            `json:"tools_image"`
	Images     map[string]string `json:"images"`
}

// clientManifest reads --manifest and renders each unit's config from the
// repository, so the controller deploys exactly this commit's config.
func clientManifest(cmd *cobra.Command, pr int) *state.Manifest {
	path, _ := cmd.Flags().GetString("manifest")
	if path == "" {
		log.Fatal("--manifest is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal("Failed to read manifest", "path", path, "err", err)
	}
	var f manifestFile
	if err := json.Unmarshal(data, &f); err != nil {
		log.Fatal("Failed to parse manifest", "path", path, "err", err)
	}
	if f.PR != pr {
		log.Fatal("Manifest is for another PR", "manifest_pr", f.PR, "pr", pr)
	}
	if len(f.Images) == 0 {
		log.Fatal("Manifest has no images", "path", path)
	}

	cfg := config.LoadConfig()
	settings := resolveSettings(cmd)
	rc := load(cmd)
	m, err := buildManifest(f, cfg, settings.toolsImage, rc.infra.SecretARNPrefix())
	if err != nil {
		log.Fatal("Failed to build the manifest", "err", err)
	}
	return m
}

// buildManifest renders each unit of the manifest file from its
// deploy/config.toml in the review app section.
func buildManifest(f manifestFile, cfg *config.OpsConfig, toolsImage, secretPrefix string) (*state.Manifest, error) {
	tools := f.ToolsImage
	if tools == "" {
		if toolsImage == "" || f.Tag == "" {
			return nil, fmt.Errorf("the manifest needs tools_image, or a tag with reviewapps.tools_image set")
		}
		tools = toolsImage + ":" + f.Tag
	}

	units, err := catalog.Discover(cfg)
	if err != nil {
		return nil, fmt.Errorf("discover units: %w", err)
	}
	configs := map[string]string{}
	for _, u := range units {
		configs[u.ID] = u.Config
	}

	env := cfg.ReviewApps.ResolvedEnv()
	defaults := pkgecs.BaseDefaults{
		CPU:         cfg.ECS.Defaults.CPU,
		Memory:      cfg.ECS.Defaults.Memory,
		NetworkMode: cfg.ECS.Defaults.NetworkMode,
		LaunchType:  cfg.ECS.Defaults.LaunchType,
		LogDriver:   cfg.ECS.Defaults.LogDriver,
	}
	m := &state.Manifest{PR: f.PR, SHA: f.SHA, Images: f.Images, ToolsImage: tools, Units: map[string]state.UnitSpec{}}
	var errs []string
	for _, unit := range sortedUnits(f.Images) {
		path, ok := configs[unit]
		if !ok {
			errs = append(errs, unit+": no deploy/config.toml")
			continue
		}
		appCfg, err := app.LoadAppConfig(path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", unit, err))
			continue
		}
		spec, err := render.Unit(appCfg, env, defaults, secretPrefix)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", unit, err))
			continue
		}
		m.Units[unit] = spec
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return m, nil
}

func sortedUnits(images map[string]string) []string {
	units := make([]string, 0, len(images))
	for u := range images {
		units = append(units, u)
	}
	sort.Strings(units)
	return units
}

// ctlContext is a controller with the context it runs in.
type ctlContext struct {
	*provision.Controller
	ctx context.Context
}

// controller builds the in-cluster controller from the flags the client
// passed and the infrastructure parameters.
func controller(cmd *cobra.Command) *ctlContext {
	rc := load(cmd)
	ssmClient := ssm.NewFromConfig(rc.aws)
	bp, err := rc.infra.LoadBlueprint(rc.ctx, ssmClient)
	if err != nil {
		log.Fatal("Failed to read the review app blueprint", "err", err)
	}
	if rc.settings.domain == "" {
		log.Fatal("--domain is required")
	}
	repository, actor, runURL := identity(cmd)
	secrets := secretsmanager.NewFromConfig(rc.aws)

	c := &provision.Controller{
		ECS:       awsecs.NewFromConfig(rc.aws),
		ELB:       elb.NewFromConfig(rc.aws),
		Discovery: sd.NewFromConfig(rc.aws),
		Secrets:   secrets,
		ECR:       ecr.NewFromConfig(rc.aws),
		Data:      s3.NewFromConfig(rc.aws),
		Metrics:   cloudwatch.NewFromConfig(rc.aws),
		Logs:      cwlogs.NewFromConfig(rc.aws),
		Tagging:   resourcegroupstaggingapi.NewFromConfig(rc.aws),
		SSM:       ssmClient,
		Store:     rc.store(),
		Connect:   provision.PgxConnect,
		Infra:     rc.infra,
		Blueprint: bp,
		Domain:    rc.settings.domain,
		Region:    rc.settings.region,
		Limit:     rc.settings.limit,
		IdlePause: rc.settings.idlePause,
		Actor:     actor,
		RunURL:    runURL,
		Out:       os.Stdout,
	}
	if repository != "" && rc.infra.Secrets.GitHubApp != "" {
		commenter, err := githubCommenter(rc, secrets, repository)
		if err != nil {
			log.Warn("PR comments are off", "err", err)
		} else {
			c.Comments = commenter
		}
	}
	return &ctlContext{Controller: c, ctx: rc.ctx}
}

// githubCommenter reads the token from the GitHub App secret named in the
// infra parameter. The secret holds JSON with a "token" key.
func githubCommenter(rc *runContext, secrets *secretsmanager.Client, repository string) (*provision.GitHubCommenter, error) {
	out, err := secrets.GetSecretValue(rc.ctx, &secretsmanager.GetSecretValueInput{SecretId: awssdk.String(rc.infra.Secrets.GitHubApp)})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rc.infra.Secrets.GitHubApp, err)
	}
	var v struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(awssdk.ToString(out.SecretString)), &v); err != nil || v.Token == "" {
		return nil, fmt.Errorf("secret %s has no token", rc.infra.Secrets.GitHubApp)
	}
	return provision.NewGitHubCommenter(repository, v.Token)
}
