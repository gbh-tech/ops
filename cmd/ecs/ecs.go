package ecs

import (
	"context"
	"fmt"
	"ops/pkg/aws"
	"ops/pkg/config"
	pkgecs "ops/pkg/ecs"
	"ops/pkg/utils"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"charm.land/log/v2"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

// Command is the "ops ecs" parent command.
var Command = &cobra.Command{
	Use:   "ecs",
	Short: "ECS deployment subcommands (deploy, render, status, wait, rollback, db-migrate, schedule-run, run, shell, cleanup, logs, vars, secrets)",
}

func init() {
	Command.AddCommand(ecsDeployCmd)
	Command.AddCommand(ecsRenderCmd)
	Command.AddCommand(ecsStatusCmd)
	Command.AddCommand(ecsWaitCmd)
	Command.AddCommand(ecsRollbackCmd)
	Command.AddCommand(ecsDbMigrateCmd)
	Command.AddCommand(ecsScheduleRunCmd)
	Command.AddCommand(ecsRunCmd)
	Command.AddCommand(ecsShellCmd)
	Command.AddCommand(ecsCleanupCmd)
	Command.AddCommand(ecsLogsCmd)
	Command.AddCommand(ecsVarsCmd)
	Command.AddCommand(ecsSecretsCmd)

	// Persistent flags are inherited by every subcommand.
	// --app is validated at runtime (required in mono-repo mode, optional in single-repo mode).
	appUsage := "App name: subdirectory in mono-repo (apps/{app}/), or ECS name override in single-repo"
	Command.PersistentFlags().StringP("app", "a", "", appUsage)
	Command.PersistentFlags().StringP("env", "e", "", "Target environment")
	Command.PersistentFlags().String("app-config", "", "Override path to app config file")
	_ = Command.MarkPersistentFlagRequired("env")

	// Subcommand-specific flags.
	ecsDeployCmd.Flags().StringP("tag", "t", "", "Container image tag (defaults to the env name, e.g. \"stage\")")

	ecsRenderCmd.Flags().StringP("tag", "t", "", "Container image tag (defaults to the env name, e.g. \"stage\")")

	ecsCleanupCmd.Flags().Int("keep", 0, "Number of task definition revisions to keep (overrides ecs.cleanup_keep; defaults to 5)")

	ecsLogsCmd.Flags().Duration("since", 10*time.Minute, "Show logs since this duration ago")

	ecsRunCmd.Flags().StringP("command", "c", "/bin/sh", "Command to execute inside the container (use 'ops shell' to open an interactive shell)")

	ecsShellCmd.Flags().StringP("shell", "s", "/bin/sh", "Shell binary to open inside the container (e.g. /bin/bash)")

	ecsVarsCmd.Flags().StringP("format", "f", "table", "Output format: table | dotenv")

	// ShellCommand is registered at the root level (ops shell) but also available
	// as ops ecs shell. It declares its own flags since it does not inherit from
	// Command's PersistentFlags.
	shellAppUsage := "App name: subdirectory in mono-repo (apps/{app}/), or ECS name override in single-repo"
	ShellCommand.Flags().StringP("app", "a", "", shellAppUsage)
	ShellCommand.Flags().StringP("env", "e", "", "Target environment")
	ShellCommand.Flags().String("app-config", "", "Override path to app config file")
	ShellCommand.Flags().StringP("shell", "s", "/bin/sh", "Shell binary to open inside the container (e.g. /bin/bash)")
	_ = ShellCommand.MarkFlagRequired("env")
}

// ecsCtx bundles the resolved config and AWS clients used by all ECS subcommands.
type ecsCtx struct {
	cfg         *config.OpsConfig
	base        *pkgecs.BaseConfig
	ecsClient   *awsecs.Client
	cwClient    *cwlogs.Client
	schedClient *scheduler.Client
}

// ecsDefaultReplicas returns the effective replica count from ECSDefaults,
// preferring the new replicas key over the deprecated desired_count fallback.
func ecsDefaultReplicas(d config.ECSDefaults) int {
	if d.Replicas != 0 {
		return d.Replicas
	}
	return d.DesiredCount
}

// buildBaseConfig assembles a pkgecs.BaseConfig from the ops config. This is
// the single place that maps OpsConfig fields to BaseConfig fields; both
// loadECSCtx and loadAppForInspect call it so additions only need one edit.
func buildBaseConfig(cfg *config.OpsConfig) *pkgecs.BaseConfig {
	return &pkgecs.BaseConfig{
		AWS: pkgecs.BaseAWS{
			AccountID: cfg.AWS.AccountId,
			Region:    cfg.AWS.Region,
			ECRUrl:    cfg.RegistryURL(),
		},
		ECS: pkgecs.BaseECS{
			Cluster:          cfg.ECS.Cluster,
			SecretArnPrefix:  cfg.ECS.ResolvedSecretArnPrefix(cfg.AWS),
			ExecutionRole:    cfg.ECS.ResolvedExecutionRole(cfg.AWS),
			TaskRole:         cfg.ECS.ResolvedTaskRole(cfg.AWS),
			CapacityProvider: cfg.ECS.CapacityProvider,
		},
		Defaults: pkgecs.BaseDefaults{
			CPU:         cfg.ECS.Defaults.CPU,
			Memory:      cfg.ECS.Defaults.Memory,
			Replicas:    ecsDefaultReplicas(cfg.ECS.Defaults),
			NetworkMode: cfg.ECS.Defaults.NetworkMode,
			LaunchType:  cfg.ECS.Defaults.LaunchType,
			LogDriver:   cfg.ECS.Defaults.LogDriver,
		},
	}
}

func ensureEcsOnAws(cfg *config.OpsConfig) {
	if cfg.DeploymentProvider() == "ecs" && cfg.CloudProvider() == "aws" {
		return
	}

	log.Fatal(
		"ops ecs commands require deployment=ecs and provider=aws (set deployment:/provider: in .ops/config.yaml or pass --deployment/--provider)",
		"expected_deployment", "ecs",
		"actual_deployment", cfg.DeploymentProvider(),
		"expected_cloud", "aws",
		"actual_cloud", cfg.CloudProvider(),
	)
}

func loadECSCtx() *ecsCtx {
	cfg := config.LoadConfig()
	ensureEcsOnAws(cfg)

	ctx := context.Background()
	awsCfg := aws.NewAWSConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)

	return &ecsCtx{
		cfg:         cfg,
		base:        buildBaseConfig(cfg),
		ecsClient:   awsecs.NewFromConfig(awsCfg),
		cwClient:    cwlogs.NewFromConfig(awsCfg),
		schedClient: scheduler.NewFromConfig(awsCfg),
	}
}

// requireAppInMonoRepo fatals when running in mono-repo mode without --app.
func requireAppInMonoRepo(cfg *config.OpsConfig, app string) {
	if cfg.IsMonoRepo() && app == "" {
		log.Fatal("--app is required in mono-repo mode (repo_mode: mono)")
	}
}

// resolveTag returns tag if non-empty, otherwise falls back to env.
// This makes "ops ecs deploy --env stage" pull the :stage image by default,
// matching the tag that "ops push" writes as the environment pointer.
func resolveTag(tag, env string) string {
	if tag != "" {
		return tag
	}
	return env
}

// loadApp loads and merges an app's config for the given environment.
func loadApp(ec *ecsCtx, app, env, appConfigOverride string) (pkgecs.AppConfig, pkgecs.MergedConfig, pkgecs.Names) {
	path := ec.cfg.ResolveAppFilePath(app, appConfigOverride, "deploy/config.toml")
	appCfg, err := pkgecs.LoadAppConfig(path)
	if err != nil {
		log.Fatal("Failed to load app config", "path", path, "err", err)
	}
	merged, err := pkgecs.ResolveConfig(ec.base, appCfg, env)
	if err != nil {
		log.Fatal("Invalid app config", "path", path, "err", err)
	}
	names := pkgecs.ComputeNames(merged, env, ec.base.ECS.Cluster)
	return appCfg, merged, names
}

// loadAppForInspect loads and merges an app config without requiring AWS
// clients or a running cluster. Used by read-only inspection commands
// (vars, secrets, render) that work purely from local config files.
func loadAppForInspect(app, env, appConfigOverride string) (pkgecs.AppConfig, pkgecs.MergedConfig) {
	cfg := config.LoadConfig()
	ensureEcsOnAws(cfg)
	requireAppInMonoRepo(cfg, app)

	path := cfg.ResolveAppFilePath(app, appConfigOverride, "deploy/config.toml")
	appCfg, err := pkgecs.LoadAppConfig(path)
	if err != nil {
		log.Fatal("Failed to load app config", "path", path, "err", err)
	}
	merged, err := pkgecs.ResolveConfig(buildBaseConfig(cfg), appCfg, env)
	if err != nil {
		log.Fatal("Invalid app config", "path", path, "err", err)
	}
	return appCfg, merged
}

// renderKeyValueTable prints a two-column lipgloss table sorted by key.
func renderKeyValueTable(header1, header2 string, rows [][]string) {
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers(header1, header2).
		StyleFunc(func(row, col int) lipgloss.Style {
			if col == 0 {
				return keyStyle
			}
			return lipgloss.NewStyle()
		}).
		Rows(rows...)
	if _, err := lipgloss.Println(t); err != nil {
		log.Fatal("Failed to render table", "err", err)
	}
}

var ecsDeployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Register task definition, run migrations if configured, and update the ECS service",
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		tag := resolveTag(cmd.Flags().Lookup("tag").Value.String(), env)
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		appCfg, merged, names := loadApp(ec, app, env, appConfigOverride)
		secrets, err := pkgecs.ResolveSecrets(appCfg, env, merged.SecretsName, ec.base.ECS.SecretArnPrefix)
		if err != nil {
			log.Fatal("Invalid secrets config", "err", err)
		}

		log.Info("Deploying", "app", merged.Name, "env", env, "tag", tag, "family", names.Family)

		input := pkgecs.BuildTaskDefinition(ec.base, merged, names, env, tag, secrets)
		ctx := context.Background()

		taskDefArn, err := pkgecs.RegisterTaskDefinition(ctx, ec.ecsClient, input)
		if err != nil {
			log.Fatal("Failed to register task definition", "err", err)
		}
		log.Info("Task definition registered", "arn", taskDefArn)

		var scheduledTaskDefArn string
		if len(merged.ScheduledTasks) > 0 {
			scheduledInput := pkgecs.BuildScheduledTaskDefinition(ec.base, merged, names, env, tag, secrets)
			scheduledTaskDefArn, err = pkgecs.RegisterTaskDefinition(ctx, ec.ecsClient, scheduledInput)
			if err != nil {
				log.Fatal("Failed to register scheduled task definition", "err", err)
			}
			log.Info("Scheduled task definition registered", "family", names.ScheduledFamily, "arn", scheduledTaskDefArn)
		}

		if merged.DatabaseMigrations && *merged.Replicas > 0 {
			if len(merged.MigrationCommand) == 0 {
				log.Fatal("database_migrations is true but migration_command is not set")
			}
			capacityProvider := pkgecs.ExpandTemplate(ec.base.ECS.CapacityProvider, merged.Name, env)
			taskArn, err := pkgecs.RunMigrationTask(ctx, ec.ecsClient, pkgecs.MigrationOpts{
				Cluster:          ec.base.ECS.Cluster,
				Service:          names.Service,
				Family:           names.Family,
				AppName:          merged.Name,
				MigrationCommand: merged.MigrationCommand,
				CapacityProvider: capacityProvider,
			})
			if err != nil {
				log.Fatal("Migration failed", "err", err)
			}
			log.Info("Migration complete, fetching logs...")
			if err := pkgecs.PrintMigrationLogs(ctx, ec.cwClient, names.LogGroup, merged.Name, taskArn); err != nil {
				log.Warn("Could not fetch migration logs", "err", err)
			}
		}

		log.Info("Updating service", "service", names.Service)
		if err := pkgecs.UpdateService(ctx, ec.ecsClient, ec.base.ECS.Cluster, names.Service, taskDefArn, int32(*merged.Replicas)); err != nil {
			log.Fatal("Failed to update service", "err", err)
		}

		keep := ec.cfg.ECS.EffectiveCleanupKeep()
		if err := pkgecs.CleanupTaskDefinitions(ctx, ec.ecsClient, names.Family, keep); err != nil {
			log.Warn("Cleanup failed (non-fatal)", "family", names.Family, "err", err)
		}
		if scheduledTaskDefArn != "" {
			if err := pkgecs.CleanupTaskDefinitions(ctx, ec.ecsClient, names.ScheduledFamily, keep); err != nil {
				log.Warn("Cleanup failed (non-fatal)", "family", names.ScheduledFamily, "err", err)
			}
		}

		if err := reconcileAppSchedules(ctx, ec, merged.ScheduledTasks, names, merged.Name, env, scheduledTaskDefArn); err != nil {
			log.Fatal("Failed to reconcile scheduled tasks", "err", err)
		}

		waitCmd := fmt.Sprintf("ops ecs wait --env %s", env)
		if app != "" {
			waitCmd = fmt.Sprintf("ops ecs wait --app %s --env %s", app, env)
		}
		if appConfigOverride != "" {
			waitCmd = fmt.Sprintf("%s --app-config %s", waitCmd, appConfigOverride)
		}
		log.Info(fmt.Sprintf("Deploy initiated. Run '%s' to wait for stability.", waitCmd))
	},
}

var ecsRenderCmd = &cobra.Command{
	Use:   "render",
	Short: "Dry-run: print the resolved task definition summary without deploying",
	Long:  "Print the resolved task definition without making any AWS API calls. Does not require AWS credentials.",
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		tag := resolveTag(cmd.Flags().Lookup("tag").Value.String(), env)
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		// render is a local-only dry-run: no AWS SDK clients are needed.
		cfg := config.LoadConfig()
		ensureEcsOnAws(cfg)
		requireAppInMonoRepo(cfg, app)
		base := buildBaseConfig(cfg)

		path := cfg.ResolveAppFilePath(app, appConfigOverride, "deploy/config.toml")
		appCfg, err := pkgecs.LoadAppConfig(path)
		if err != nil {
			log.Fatal("Failed to load app config", "path", path, "err", err)
		}
		merged, err := pkgecs.ResolveConfig(base, appCfg, env)
		if err != nil {
			log.Fatal("Invalid app config", "path", path, "err", err)
		}
		names := pkgecs.ComputeNames(merged, env, base.ECS.Cluster)
		secrets, err := pkgecs.ResolveSecrets(appCfg, env, merged.SecretsName, base.ECS.SecretArnPrefix)
		if err != nil {
			log.Fatal("Invalid secrets config", "err", err)
		}
		input := pkgecs.BuildTaskDefinition(base, merged, names, env, tag, secrets)

		ctr := input.ContainerDefinitions[0]

		keyStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
		rows := [][]string{
			{"App", merged.Name},
			{"Env", env},
			{"Family", names.Family},
			{"Image", *ctr.Image},
			{"CPU", *input.Cpu},
			{"Memory", *input.Memory},
			{"Replicas", fmt.Sprintf("%d", *merged.Replicas)},
			{"Env vars", fmt.Sprintf("%d", len(ctr.Environment))},
			{"Secrets", fmt.Sprintf("%d", len(ctr.Secrets))},
			{"Migrations", fmt.Sprintf("%v", merged.DatabaseMigrations)},
			{"Volumes", fmt.Sprintf("%d", len(input.Volumes))},
			{"Scheduled tasks", fmt.Sprintf("%d", len(merged.ScheduledTasks))},
		}
		if len(merged.ScheduledTasks) > 0 {
			rows = append(rows, []string{"Scheduled family", names.ScheduledFamily})
		}
		if merged.DatabaseMigrations {
			rows = append(rows, []string{"Migration cmd", strings.Join(merged.MigrationCommand, " ")})
		}
		for _, v := range merged.Volumes {
			volType := "host"
			switch {
			case v.EFS != nil:
				volType = fmt.Sprintf("efs:%s", v.EFS.FileSystemId)
			case v.Docker != nil:
				volType = "docker"
			}
			readOnly := ""
			if v.ReadOnly {
				readOnly = " (ro)"
			}
			rows = append(rows, []string{
				fmt.Sprintf("  Volume: %s", v.Name),
				fmt.Sprintf("%s → %s%s", volType, v.ContainerPath, readOnly),
			})
		}
		for _, st := range merged.ScheduledTasks {
			enabled := "true"
			if st.Enabled != nil && !*st.Enabled {
				enabled = "false"
			}
			rows = append(rows,
				[]string{fmt.Sprintf("  Schedule: %s", st.Name), ""},
				[]string{"    enabled", enabled},
				[]string{"    schedule", st.Schedule},
				[]string{"    command", wrapWords(strings.Join(st.Command, " "), 60)},
			)
		}

		t := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
			StyleFunc(func(row, col int) lipgloss.Style {
				if col == 0 {
					return keyStyle
				}
				return lipgloss.NewStyle()
			}).
			Rows(rows...)

		if _, err := lipgloss.Println(t); err != nil {
			log.Fatal("Failed to render task definition", "err", err)
		}
	},
}

var ecsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current ECS service status",
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, _, names := loadApp(ec, app, env, appConfigOverride)

		ctx := context.Background()
		status, err := pkgecs.GetServiceStatus(ctx, ec.ecsClient, ec.base.ECS.Cluster, names.Service)
		if err != nil {
			log.Fatal("Failed to get service status", "err", err)
		}

		fmt.Printf("Service:  %s\n", names.Service)
		fmt.Printf("Status:   %s\n", status.Status)
		fmt.Printf("Running:  %d / %d\n", status.RunningCount, status.DesiredCount)
		fmt.Printf("Task def: %s\n", status.TaskDef)
		if status.LastEvent != "" {
			fmt.Printf("Event:    %s\n", status.LastEvent)
		}
	},
}

var ecsWaitCmd = &cobra.Command{
	Use:   "wait",
	Short: "Wait for the ECS service to reach a stable state",
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, merged, names := loadApp(ec, app, env, appConfigOverride)

		ctx := context.Background()
		if merged.Replicas != nil && *merged.Replicas == 0 {
			log.Info("replicas is 0, skipping wait for service stability", "app", merged.Name)
			return
		}
		if err := pkgecs.WaitForStability(ctx, ec.ecsClient, ec.base.ECS.Cluster, names.Service); err != nil {
			log.Fatal("Service did not stabilize", "err", err)
		}
	},
}

var ecsRollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Roll back the ECS service to the previous task definition revision",
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, _, names := loadApp(ec, app, env, appConfigOverride)

		ctx := context.Background()
		if err := pkgecs.Rollback(ctx, ec.ecsClient, ec.base.ECS.Cluster, names.Service, names.Family); err != nil {
			log.Fatal("Rollback failed", "err", err)
		}
		log.Info("Rollback initiated", "service", names.Service)
	},
}

var ecsDbMigrateCmd = &cobra.Command{
	Use:   "db-migrate",
	Short: "Run a standalone database migration task via ECS",
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, merged, names := loadApp(ec, app, env, appConfigOverride)

		if !merged.DatabaseMigrations {
			log.Info("No migrations configured for this app, skipping", "app", merged.Name)
			return
		}
		if *merged.Replicas == 0 {
			log.Info("replicas is 0, skipping migrations", "app", merged.Name)
			return
		}
		if len(merged.MigrationCommand) == 0 {
			log.Fatal("database_migrations is true but migration_command is not set")
		}

		capacityProvider := pkgecs.ExpandTemplate(ec.base.ECS.CapacityProvider, merged.Name, env)
		ctx := context.Background()
		taskArn, err := pkgecs.RunMigrationTask(ctx, ec.ecsClient, pkgecs.MigrationOpts{
			Cluster:          ec.base.ECS.Cluster,
			Service:          names.Service,
			Family:           names.Family,
			AppName:          merged.Name,
			MigrationCommand: merged.MigrationCommand,
			CapacityProvider: capacityProvider,
		})
		if err != nil {
			log.Fatal("Migration failed", "err", err)
		}
		log.Info("Migration complete, fetching logs...")
		if err := pkgecs.PrintMigrationLogs(ctx, ec.cwClient, names.LogGroup, merged.Name, taskArn); err != nil {
			log.Warn("Could not fetch migration logs", "err", err)
		}
	},
}

var ecsCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove old ECS task definition revisions, keeping the latest N",
	Long: `Remove old ECS task definition revisions, keeping the latest N for both
the service family ("{app}-{env}") and the scheduled family ("{app}-{env}-scheduled").

The number kept defaults to ecs.cleanup_keep from .ops/config.yaml (or 5 when
unset). Pass --keep to override for a single invocation.`,
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, _, names := loadApp(ec, app, env, appConfigOverride)

		keep := ec.cfg.ECS.EffectiveCleanupKeep()
		if cmd.Flags().Changed("keep") {
			keep, _ = cmd.Flags().GetInt("keep")
		}

		ctx := context.Background()
		if err := pkgecs.CleanupTaskDefinitions(ctx, ec.ecsClient, names.Family, keep); err != nil {
			log.Fatal("Cleanup failed", "family", names.Family, "err", err)
		}
		if err := pkgecs.CleanupTaskDefinitions(ctx, ec.ecsClient, names.ScheduledFamily, keep); err != nil {
			log.Fatal("Cleanup failed", "family", names.ScheduledFamily, "err", err)
		}
	},
}

var ecsLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Tail recent CloudWatch logs for an ECS service",
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		since, _ := cmd.Flags().GetDuration("since")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, merged, names := loadApp(ec, app, env, appConfigOverride)
		sinceTime := time.Now().Add(-since)

		ctx := context.Background()
		if err := pkgecs.TailLogs(ctx, ec.cwClient, names.LogGroup, merged.Name, sinceTime); err != nil {
			log.Fatal("Failed to tail logs", "err", err)
		}
	},
}

var ecsVarsCmd = &cobra.Command{
	Use:   "vars",
	Short: "Pretty-print the resolved environment variables for an app and environment",
	Long: `Print the resolved environment variables for an app and environment.

Use --format to control the output:
  table  (default) human-readable two-column table
  dotenv KEY=VALUE lines suitable for piping into a .env file`,
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")
		format, _ := cmd.Flags().GetString("format")

		_, merged := loadAppForInspect(app, env, appConfigOverride)

		if len(merged.Environment) == 0 {
			fmt.Printf("No environment variables configured for app=%q env=%q\n", merged.Name, env)
			return
		}

		keys := make([]string, 0, len(merged.Environment))
		for k := range merged.Environment {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		switch format {
		case "table":
			rows := make([][]string, 0, len(keys))
			for _, k := range keys {
				rows = append(rows, []string{k, merged.Environment[k]})
			}
			renderKeyValueTable("Variable", "Value", rows)
		case "dotenv":
			out, err := godotenv.Marshal(merged.Environment)
			if err != nil {
				log.Fatal("Failed to marshal environment to dotenv format", "err", err)
			}
			fmt.Println(out)
		default:
			log.Fatal("Unknown --format value (expected: table, dotenv)", "format", format)
		}
	},
}

var ecsSecretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Pretty-print the resolved secrets and their Secrets Manager ARN references for an app and environment",
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		cfg := config.LoadConfig()
		ensureEcsOnAws(cfg)
		appCfg, merged := loadAppForInspect(app, env, appConfigOverride)
		secrets, err := pkgecs.ResolveSecrets(appCfg, env, merged.SecretsName, cfg.ECS.ResolvedSecretArnPrefix(cfg.AWS))
		if err != nil {
			log.Fatal("Invalid secrets config", "err", err)
		}
		if len(secrets) == 0 {
			fmt.Printf("No secrets configured for app=%q env=%q\n", merged.Name, env)
			return
		}

		sort.Slice(secrets, func(i, j int) bool {
			return secrets[i].Name < secrets[j].Name
		})

		rows := make([][]string, 0, len(secrets))
		for _, s := range secrets {
			rows = append(rows, []string{s.Name, s.ValueFrom})
		}

		renderKeyValueTable("Variable", "ValueFrom (ARN)", rows)
	},
}

var ecsScheduleRunCmd = &cobra.Command{
	Use:   "schedule-run <task-name>",
	Short: "Run a named scheduled task ad-hoc as a one-off ECS task",
	Long: `Run a named scheduled task immediately as a one-off ECS task.

The task must be declared in the app's deploy/config.toml under [[scheduled_tasks]].
It uses the dedicated "{app}-{env}-scheduled" task definition (no port mappings or
health checks) and inherits the service's network configuration, overriding only
the container command, CPU, and memory.

The command waits for the task to finish and exits non-zero if the task fails.
Logs are printed from CloudWatch after the task stops.

Example:
  ops ecs schedule-run daily-cleanup --app my-app --env stage`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		taskName := args[0]
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, merged, names := loadApp(ec, app, env, appConfigOverride)

		var found *pkgecs.ScheduledTaskConfig
		for i := range merged.ScheduledTasks {
			if merged.ScheduledTasks[i].Name == taskName {
				found = &merged.ScheduledTasks[i]
				break
			}
		}
		if found == nil {
			available := make([]string, len(merged.ScheduledTasks))
			for i, t := range merged.ScheduledTasks {
				available[i] = t.Name
			}
			if len(available) == 0 {
				log.Fatal("No scheduled tasks configured for this app",
					"app", merged.Name, "env", env)
			}
			log.Fatal("Scheduled task not found",
				"name", taskName,
				"available", strings.Join(available, ", "))
		}

		appName := merged.Name
		capacityProvider := pkgecs.ExpandTemplate(ec.base.ECS.CapacityProvider, appName, env)

		log.Info("Running scheduled task", "name", taskName, "app", appName, "env", env,
			"command", strings.Join(found.Command, " "))

		ctx := context.Background()
		taskArn, err := pkgecs.RunScheduledTask(ctx, ec.ecsClient, pkgecs.RunScheduledTaskOpts{
			Cluster:          ec.base.ECS.Cluster,
			Service:          names.Service,
			ScheduledFamily:  names.ScheduledFamily,
			AppName:          appName,
			CapacityProvider: capacityProvider,
			Task:             *found,
		})
		if err != nil {
			if taskArn != "" {
				if logErr := pkgecs.PrintTaskLogs(ctx, ec.cwClient, names.LogGroup, appName, appName, taskArn); logErr != nil {
					log.Warn("Could not fetch task logs", "err", logErr)
				}
			}
			log.Fatal("Scheduled task failed", "name", taskName, "err", err)
		}
		log.Info("Task complete, fetching logs...")
		if err := pkgecs.PrintTaskLogs(ctx, ec.cwClient, names.LogGroup, appName, appName, taskArn); err != nil {
			log.Warn("Could not fetch task logs", "err", err)
		}
	},
}

// reconcileAppSchedules syncs the app's scheduled_tasks from the merged config
// to EventBridge Scheduler. It is a no-op when no scheduler is configured and
// no tasks are declared.
func reconcileAppSchedules(
	ctx context.Context,
	ec *ecsCtx,
	tasks []pkgecs.ScheduledTaskConfig,
	names pkgecs.Names,
	appName, env, taskDefArn string,
) error {
	sched := ec.cfg.ECS.Scheduler

	if len(tasks) == 0 && sched.GroupName == "" {
		return nil
	}
	if len(tasks) == 0 {
		// Scheduler is configured but this app has no tasks — nothing to do.
		return nil
	}

	if sched.GroupName == "" || sched.RoleArn == "" {
		log.Fatal(
			"app declares scheduled_tasks but ecs.scheduler.{group_name,role_arn} are not set in .ops/config.yaml",
			"app", names.Service,
		)
	}

	capacityProvider := pkgecs.ExpandTemplate(ec.base.ECS.CapacityProvider, appName, env)
	groupName := pkgecs.ExpandSchedulerTemplate(sched.GroupName, ec.base.ECS.Cluster, env)
	roleArn := pkgecs.ExpandSchedulerTemplate(sched.RoleArn, ec.base.ECS.Cluster, env)

	// Fetch the service's network config once, so all schedules share the
	// same subnets/security groups as the running service.
	netCfg, err := pkgecs.FetchServiceNetworkConfig(ctx, ec.ecsClient, ec.base.ECS.Cluster, names.Service)
	if err != nil {
		log.Warn("Could not fetch service network config for scheduled tasks (proceeding without)", "err", err)
		netCfg = nil
	}

	cfg := pkgecs.ReconcileConfig{
		GroupName:        groupName,
		RoleArn:          roleArn,
		Cluster:          ec.base.ECS.Cluster,
		Region:           ec.base.AWS.Region,
		AccountID:        ec.base.AWS.AccountID,
		AppName:          appName,
		Env:              env,
		CapacityProvider: capacityProvider,
		LaunchType:       ec.base.Defaults.LaunchType,
		NetworkConfig:    netCfg,
	}

	log.Info("Reconciling scheduled tasks", "app", appName, "env", env, "count", len(tasks))
	created, updated, deleted, err := pkgecs.ReconcileSchedules(ctx, ec.schedClient, cfg, taskDefArn, tasks)
	if err != nil {
		return err
	}
	log.Info("Scheduled tasks reconciled",
		"created", len(created),
		"updated", len(updated),
		"deleted", len(deleted),
	)
	return nil
}

var ecsRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a one-off command inside a running ECS container via ECS Exec",
	Long: `Run a one-off command inside a running ECS task container using ECS Exec.

Requires both the AWS CLI and the session-manager-plugin to be installed and on PATH.
The command connects to the first running task of the service and executes the given command.
To open an interactive shell session use 'ops shell' instead.

Example:
  ops ecs run --app my-app --env stage --command "ls /app"
  ops ecs run --app my-app --env stage --command "/bin/bash -c 'echo hello'"`,
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")
		command, _ := cmd.Flags().GetString("command")

		utils.CheckBinary("aws")
		utils.CheckBinary("session-manager-plugin")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, merged, names := loadApp(ec, app, env, appConfigOverride)

		ctx := context.Background()
		out, err := ec.ecsClient.ListTasks(ctx, &awsecs.ListTasksInput{
			Cluster:       awssdk.String(ec.base.ECS.Cluster),
			ServiceName:   awssdk.String(names.Service),
			DesiredStatus: ecstypes.DesiredStatusRunning,
		})
		if err != nil {
			log.Fatal("Failed to list running tasks", "err", err)
		}
		if len(out.TaskArns) == 0 {
			log.Fatal("No running tasks found for service", "service", names.Service, "cluster", ec.base.ECS.Cluster)
		}

		taskArn := out.TaskArns[0]
		appName := merged.Name
		log.Info("Starting ECS Exec session", "task", taskArn, "container", appName, "command", command)

		execArgs := []string{"ecs", "execute-command",
			"--cluster", ec.base.ECS.Cluster,
			"--task", taskArn,
			"--container", appName,
			"--interactive",
			"--command", command,
			"--region", ec.cfg.AWS.Region,
		}
		if ec.cfg.AWS.Profile != "" {
			execArgs = append(execArgs, "--profile", ec.cfg.AWS.Profile)
		}
		execCmd := exec.Command("aws", execArgs...)
		execCmd.Stdin = os.Stdin
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr
		if err := execCmd.Run(); err != nil {
			log.Fatal("ECS Exec session ended with error", "err", err)
		}
	},
}

// openECSShell finds the first running task for the given service and opens an
// interactive shell session via ECS Exec. It is shared by both ShellCommand
// (top-level ops shell) and ecsShellCmd (ops ecs shell).
func openECSShell(ec *ecsCtx, app, env, appConfigOverride, shell string) {
	utils.CheckBinary("aws")
	utils.CheckBinary("session-manager-plugin")

	requireAppInMonoRepo(ec.cfg, app)
	_, merged, names := loadApp(ec, app, env, appConfigOverride)

	ctx := context.Background()
	out, err := ec.ecsClient.ListTasks(ctx, &awsecs.ListTasksInput{
		Cluster:       awssdk.String(ec.base.ECS.Cluster),
		ServiceName:   awssdk.String(names.Service),
		DesiredStatus: ecstypes.DesiredStatusRunning,
	})
	if err != nil {
		log.Fatal("Failed to list running tasks", "err", err)
	}
	if len(out.TaskArns) == 0 {
		log.Fatal("No running tasks found for service", "service", names.Service, "cluster", ec.base.ECS.Cluster)
	}

	taskArn := out.TaskArns[0]
	appName := merged.Name
	log.Info("Opening shell session", "task", taskArn, "container", appName, "shell", shell)

	execArgs := []string{"ecs", "execute-command",
		"--cluster", ec.base.ECS.Cluster,
		"--task", taskArn,
		"--container", appName,
		"--interactive",
		"--command", shell,
		"--region", ec.cfg.AWS.Region,
	}
	if ec.cfg.AWS.Profile != "" {
		execArgs = append(execArgs, "--profile", ec.cfg.AWS.Profile)
	}
	execCmd := exec.Command("aws", execArgs...)
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	if err := execCmd.Run(); err != nil {
		log.Fatal("Shell session ended with error", "err", err)
	}
}

// ShellCommand is the top-level "ops shell" command for opening an interactive
// shell inside a running ECS container. It is also registered as "ops ecs shell"
// via ecsShellCmd for discoverability.
var ShellCommand = &cobra.Command{
	Use:   "shell",
	Short: "Open an interactive shell session inside a running ECS container",
	Long: `Open an interactive shell inside a running ECS task container using ECS Exec.

Requires both the AWS CLI and the session-manager-plugin to be installed and on PATH.
The command connects to the first running task of the service and starts the chosen shell.

Example:
  ops shell --app my-app --env stage
  ops shell --app my-app --env stage --shell /bin/bash`,
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")
		shell, _ := cmd.Flags().GetString("shell")
		openECSShell(loadECSCtx(), app, env, appConfigOverride, shell)
	},
}

var ecsShellCmd = &cobra.Command{
	Use:   "shell",
	Short: "Open an interactive shell session inside a running ECS container",
	Long: `Open an interactive shell inside a running ECS task container using ECS Exec.

Requires both the AWS CLI and the session-manager-plugin to be installed and on PATH.
The command connects to the first running task of the service and starts the chosen shell.

Example:
  ops ecs shell --app my-app --env stage
  ops ecs shell --app my-app --env stage --shell /bin/bash`,
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")
		shell, _ := cmd.Flags().GetString("shell")
		openECSShell(loadECSCtx(), app, env, appConfigOverride, shell)
	},
}

// wrapWords wraps s onto multiple lines, breaking on whitespace, so that no
// line exceeds width characters. Words longer than width are kept whole on
// their own line. Returns s unchanged when it already fits.
func wrapWords(s string, width int) string {
	if len(s) <= width || width <= 0 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return s
	}
	var lines []string
	var current strings.Builder
	for _, w := range words {
		switch {
		case current.Len() == 0:
			current.WriteString(w)
		case current.Len()+1+len(w) > width:
			lines = append(lines, current.String())
			current.Reset()
			current.WriteString(w)
		default:
			current.WriteByte(' ')
			current.WriteString(w)
		}
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return strings.Join(lines, "\n")
}
