package ecs

import (
	"context"
	"fmt"
	"ops/pkg/aws"
	"ops/pkg/config"
	pkgecs "ops/pkg/ecs"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"charm.land/log/v2"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/spf13/cobra"
)

// Command is the "ops ecs" parent command.
var Command = &cobra.Command{
	Use:   "ecs",
	Short: "ECS deployment subcommands (deploy, render, status, wait, rollback, db-migrate, cleanup, logs)",
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

	// --app is required in mono-repo mode (validated at runtime); optional in single-repo mode.
	appUsage := "App name: subdirectory in mono-repo (apps/{app}/), or ECS name override in single-repo"

	ecsDeployCmd.Flags().StringP("app", "a", "", appUsage)
	ecsDeployCmd.Flags().StringP("env", "e", "", "Target environment (stage, production, etc.)")
	ecsDeployCmd.Flags().StringP("tag", "t", "latest", "Container image tag")
	ecsDeployCmd.Flags().String("app-config", "", "Override path to app config file")
	_ = ecsDeployCmd.MarkFlagRequired("env")

	ecsRenderCmd.Flags().StringP("app", "a", "", appUsage)
	ecsRenderCmd.Flags().StringP("env", "e", "", "Target environment")
	ecsRenderCmd.Flags().StringP("tag", "t", "latest", "Container image tag")
	ecsRenderCmd.Flags().String("app-config", "", "Override path to app config file")
	_ = ecsRenderCmd.MarkFlagRequired("env")

	ecsStatusCmd.Flags().StringP("app", "a", "", appUsage)
	ecsStatusCmd.Flags().StringP("env", "e", "", "Target environment")
	_ = ecsStatusCmd.MarkFlagRequired("env")

	ecsWaitCmd.Flags().StringP("app", "a", "", appUsage)
	ecsWaitCmd.Flags().StringP("env", "e", "", "Target environment")
	_ = ecsWaitCmd.MarkFlagRequired("env")

	ecsRollbackCmd.Flags().StringP("app", "a", "", appUsage)
	ecsRollbackCmd.Flags().StringP("env", "e", "", "Target environment")
	_ = ecsRollbackCmd.MarkFlagRequired("env")

	ecsDbMigrateCmd.Flags().StringP("app", "a", "", appUsage)
	ecsDbMigrateCmd.Flags().StringP("env", "e", "", "Target environment")
	ecsDbMigrateCmd.Flags().String("app-config", "", "Override path to app config file")
	_ = ecsDbMigrateCmd.MarkFlagRequired("env")

	ecsCleanupCmd.Flags().StringP("app", "a", "", appUsage)
	ecsCleanupCmd.Flags().StringP("env", "e", "", "Target environment")
	ecsCleanupCmd.Flags().Int("keep", 5, "Number of task definition revisions to keep")
	_ = ecsCleanupCmd.MarkFlagRequired("env")

	ecsLogsCmd.Flags().StringP("app", "a", "", appUsage)
	ecsLogsCmd.Flags().StringP("env", "e", "", "Target environment")
	ecsLogsCmd.Flags().Duration("since", 10*time.Minute, "Show logs since this duration ago")
	_ = ecsLogsCmd.MarkFlagRequired("env")
}

// ecsCtx bundles the resolved config and AWS clients used by all ECS subcommands.
type ecsCtx struct {
	cfg       *config.OpsConfig
	base      *pkgecs.BaseConfig
	ecsClient *awsecs.Client
	cwClient  *cwlogs.Client
}

// loadECSCtx validates the deployment provider, assembles the ECS base config
// from .ops/config.yaml, and initialises AWS SDK clients.
func loadECSCtx() *ecsCtx {
	cfg := config.LoadConfig()
	if cfg.Deployment.Provider != "ecs" {
		log.Fatal(
			"deployment.provider must be set to 'ecs'",
			"current", cfg.Deployment.Provider,
		)
	}

	base := &pkgecs.BaseConfig{
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

	ctx := context.Background()
	awsCfg := aws.NewAWSConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)

	return &ecsCtx{
		cfg:       cfg,
		base:      base,
		ecsClient: awsecs.NewFromConfig(awsCfg),
		cwClient:  cwlogs.NewFromConfig(awsCfg),
	}
}

// requireAppInMonoRepo fatals when running in mono-repo mode without --app.
func requireAppInMonoRepo(cfg *config.OpsConfig, app string) {
	if cfg.IsMonoRepo() && app == "" {
		log.Fatal("--app is required in mono-repo mode (repo_mode: mono)")
	}
}

// resolveAppConfig returns the app config file path, respecting an explicit
// override flag before falling back to the convention for the active repo mode:
//
//	mono-repo:   {apps_dir}/{app}/deploy/config.toml
//	single-repo: deploy/config.toml
func resolveAppConfig(cfg *config.OpsConfig, app, override string) string {
	if override != "" {
		return override
	}
	if cfg.IsMonoRepo() {
		return filepath.Join(cfg.ECS.AppsDirPath(), app, "deploy", "config.toml")
	}
	return "deploy/config.toml"
}

// loadApp loads and merges an app's config for the given environment.
func loadApp(ec *ecsCtx, app, env, appConfigOverride string) (pkgecs.AppConfig, pkgecs.MergedConfig, pkgecs.Names) {
	path := resolveAppConfig(ec.cfg, app, appConfigOverride)
	appCfg, err := pkgecs.LoadAppConfig(path)
	if err != nil {
		log.Fatal("Failed to load app config", "path", path, "err", err)
	}
	merged := pkgecs.ResolveConfig(ec.base, appCfg, env)
	names := pkgecs.ComputeNames(merged, env, ec.base.ECS.Cluster)
	return appCfg, merged, names
}

var ecsDeployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Register task definition, run migrations if configured, and update the ECS service",
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		tag, _ := cmd.Flags().GetString("tag")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		appCfg, merged, names := loadApp(ec, app, env, appConfigOverride)
		secrets := pkgecs.ResolveSecrets(appCfg, env, merged.SecretsName, ec.base.ECS.SecretArnPrefix)

		log.Info("Deploying", "app", merged.Name, "env", env, "tag", tag, "family", names.Family)

		input := pkgecs.BuildTaskDefinition(ec.base, merged, names, env, tag, secrets)
		ctx := context.Background()

		taskDefArn, err := pkgecs.RegisterTaskDefinition(ctx, ec.ecsClient, input)
		if err != nil {
			log.Fatal("Failed to register task definition", "err", err)
		}
		log.Info("Task definition registered", "arn", taskDefArn)

		if merged.DatabaseMigrations {
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

		log.Info(fmt.Sprintf("Deploy initiated. Run 'ops ecs wait --app %s --env %s' to wait for stability.", app, env))
	},
}

var ecsRenderCmd = &cobra.Command{
	Use:   "render",
	Short: "Dry-run: print the resolved task definition summary without deploying",
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")
		tag, _ := cmd.Flags().GetString("tag")
		appConfigOverride, _ := cmd.Flags().GetString("app-config")

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		appCfg, merged, names := loadApp(ec, app, env, appConfigOverride)
		secrets := pkgecs.ResolveSecrets(appCfg, env, merged.SecretsName, ec.base.ECS.SecretArnPrefix)
		input := pkgecs.BuildTaskDefinition(ec.base, merged, names, env, tag, secrets)

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
		}
		if merged.DatabaseMigrations {
			rows = append(rows, []string{"Migration cmd", strings.Join(merged.MigrationCommand, " ")})
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

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, _, names := loadApp(ec, app, env, "")

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

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, _, names := loadApp(ec, app, env, "")

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

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, _, names := loadApp(ec, app, env, "")

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

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, _, names := loadApp(ec, app, env, "")

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

		ec := loadECSCtx()
		requireAppInMonoRepo(ec.cfg, app)
		_, merged, names := loadApp(ec, app, env, "")
		sinceTime := time.Now().Add(-since)

		ctx := context.Background()
		if err := pkgecs.TailLogs(ctx, ec.cwClient, names.LogGroup, merged.Name, sinceTime); err != nil {
			log.Fatal("Failed to tail logs", "err", err)
		}
	},
}
