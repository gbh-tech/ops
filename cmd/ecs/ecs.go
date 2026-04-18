package ecs

import (
	"context"
	"fmt"
	"ops/pkg/aws"
	"ops/pkg/config"
	pkgecs "ops/pkg/ecs"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"charm.land/log/v2"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/spf13/cobra"
)

// Command is the "ops ecs" parent command.
var Command = &cobra.Command{
	Use:   "ecs",
	Short: "ECS deployment subcommands (deploy, render, status, wait, rollback, db-migrate, cleanup, logs, vars, secrets)",
}

func init() {
	Command.AddCommand(ecsDeployCmd)
	Command.AddCommand(ecsRenderCmd)
	Command.AddCommand(ecsStatusCmd)
	Command.AddCommand(ecsWaitCmd)
	Command.AddCommand(ecsRollbackCmd)
	Command.AddCommand(ecsDbMigrateCmd)
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

	ecsCleanupCmd.Flags().Int("keep", 5, "Number of task definition revisions to keep")

	ecsLogsCmd.Flags().Duration("since", 10*time.Minute, "Show logs since this duration ago")
}

// ecsCtx bundles the resolved config and AWS clients used by all ECS subcommands.
type ecsCtx struct {
	cfg         *config.OpsConfig
	base        *pkgecs.BaseConfig
	ecsClient   *awsecs.Client
	cwClient    *cwlogs.Client
	schedClient *scheduler.Client
}

// buildBaseConfig assembles a pkgecs.BaseConfig from the ops config. This is
// the single place that maps OpsConfig fields to BaseConfig fields; both
// loadECSCtx and loadAppForInspect call it so additions only need one edit.
func buildBaseConfig(cfg *config.OpsConfig) *pkgecs.BaseConfig {
	return &pkgecs.BaseConfig{
		AWS: pkgecs.BaseAWS{
			AccountID: cfg.AWS.AccountId,
			Region:    cfg.AWS.Region,
			ECRUrl:    cfg.Registry.URL,
		},
		ECS: pkgecs.BaseECS{
			Cluster:          cfg.ECS.Cluster,
			SecretArnPrefix:  cfg.ECS.SecretArnPrefix,
			ExecutionRole:    cfg.ECS.ExecutionRole,
			TaskRole:         cfg.ECS.TaskRole,
			CapacityProvider: cfg.ECS.CapacityProvider,
		},
		Defaults: pkgecs.BaseDefaults{
			CPU:          cfg.ECS.Defaults.CPU,
			Memory:       cfg.ECS.Defaults.Memory,
			DesiredCount: cfg.ECS.Defaults.DesiredCount,
			NetworkMode:  cfg.ECS.Defaults.NetworkMode,
			LaunchType:   cfg.ECS.Defaults.LaunchType,
			LogDriver:    cfg.ECS.Defaults.LogDriver,
		},
	}
}

func loadECSCtx() *ecsCtx {
	cfg := config.LoadConfig()
	if cfg.Deployment.Provider != "ecs" {
		log.Fatal(
			"deployment.provider must be set to 'ecs'",
			"current", cfg.Deployment.Provider,
		)
	}

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

		if merged.DatabaseMigrations && *merged.DesiredCount > 0 {
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
		if err := pkgecs.UpdateService(ctx, ec.ecsClient, ec.base.ECS.Cluster, names.Service, taskDefArn, int32(*merged.DesiredCount)); err != nil {
			log.Fatal("Failed to update service", "err", err)
		}

		if err := pkgecs.CleanupTaskDefinitions(ctx, ec.ecsClient, names.Family, 5); err != nil {
			log.Warn("Cleanup failed (non-fatal)", "err", err)
		}

		if err := reconcileAppSchedules(ctx, ec, merged.ScheduledTasks, names, env, taskDefArn); err != nil {
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
		if cfg.Deployment.Provider != "ecs" {
			log.Fatal("deployment.provider must be set to 'ecs'", "current", cfg.Deployment.Provider)
		}
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
			{"Replicas", fmt.Sprintf("%d", *merged.DesiredCount)},
			{"Env vars", fmt.Sprintf("%d", len(ctr.Environment))},
			{"Secrets", fmt.Sprintf("%d", len(ctr.Secrets))},
			{"Migrations", fmt.Sprintf("%v", merged.DatabaseMigrations)},
			{"Volumes", fmt.Sprintf("%d", len(input.Volumes))},
			{"Scheduled tasks", fmt.Sprintf("%d", len(merged.ScheduledTasks))},
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
			state := "enabled"
			if st.Enabled != nil && !*st.Enabled {
				state = "disabled"
			}
			rows = append(rows, []string{
				fmt.Sprintf("  Schedule: %s", st.Name),
				fmt.Sprintf("%s | cmd: %s | %s", st.Schedule, strings.Join(st.Command, " "), state),
			})
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
		_, _, names := loadApp(ec, app, env, appConfigOverride)

		ctx := context.Background()
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
		if *merged.DesiredCount == 0 {
			log.Info("desired_count is 0, skipping migrations", "app", merged.Name)
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
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		keep, _ := cmd.Flags().GetInt("keep")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, _, names := loadApp(ec, app, env, appConfigOverride)

		ctx := context.Background()
		if err := pkgecs.CleanupTaskDefinitions(ctx, ec.ecsClient, names.Family, keep); err != nil {
			log.Fatal("Cleanup failed", "err", err)
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
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

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

		rows := make([][]string, 0, len(keys))
		for _, k := range keys {
			rows = append(rows, []string{k, merged.Environment[k]})
		}

		renderKeyValueTable("Variable", "Value", rows)
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
		appCfg, merged := loadAppForInspect(app, env, appConfigOverride)
		secrets, err := pkgecs.ResolveSecrets(appCfg, env, merged.SecretsName, cfg.ECS.SecretArnPrefix)
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

// reconcileAppSchedules syncs the app's scheduled_tasks from the merged config
// to EventBridge Scheduler. It is a no-op when no scheduler is configured and
// no tasks are declared.
func reconcileAppSchedules(
	ctx context.Context,
	ec *ecsCtx,
	tasks []pkgecs.ScheduledTaskConfig,
	names pkgecs.Names,
	env, taskDefArn string,
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

	appName := names.Service[:strings.LastIndex(names.Service, "-"+env)]
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
