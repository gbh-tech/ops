package ecs

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"charm.land/log/v2"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	pkgecs "ops/pkg/ecs"
	"ops/pkg/utils"
)

var ecsPortForwardCmd = &cobra.Command{
	Use:   "port-forward",
	Short: "Port-forward a port from a running ECS task to localhost via SSM",
	Long: `Forward a remote port from the first RUNNING task of an ECS service to a local port using
AWS Systems Manager (AWS-StartPortForwardingSession).

Requires aws CLI and session-manager-plugin on PATH.

Provide --app and --port; the ECS service name is "{app}-{env}".`,
	Run: runPortForward,
}

var ecsDbProxyCmd = &cobra.Command{
	Use:   "db-proxy",
	Short: "Port-forward a db-proxy ECS service to localhost via SSM",
	Long: `Discover and forward a db-proxy ECS service port to localhost using
AWS Systems Manager (AWS-StartPortForwardingSession).

Requires aws CLI and session-manager-plugin on PATH.

Services whose names contain "db-proxy" are listed; if more than one exists you pick
interactively. The remote port is inferred from the service name
(postgres→5432, mysql→3306, redis→6379) unless --port is set.`,
	Run: runPortForward,
}

func initPortForwardFlags() {
	for _, cmd := range []*cobra.Command{ecsPortForwardCmd, ecsDbProxyCmd} {
		cmd.Flags().IntP("port", "p", 0, "Remote container port to forward (required for port-forward; optional for db-proxy when inferable)")
		cmd.Flags().IntP("local-port", "l", 0, "Local port (defaults to the remote port)")
		cmd.Flags().StringP("container", "c", "", "ECS container name (defaults to the first container in the task)")
	}
}

func runPortForward(cmd *cobra.Command, args []string) {
	dbProxyMode := cmd.CalledAs() == "db-proxy"

	utils.CheckBinary("aws")
	utils.CheckBinary("session-manager-plugin")

	app, _ := cmd.Flags().GetString("app")
	env, _ := cmd.Flags().GetString("env")
	container, _ := cmd.Flags().GetString("container")
	appConfigOverride, _ := cmd.Flags().GetString("app-config")

	ec := loadECSCtx(env)
	ctx := context.Background()

	service, remotePort := resolveServiceAndPort(ctx, ec, cmd, dbProxyMode, app, env, appConfigOverride)
	localPort := resolveLocalPort(cmd, remotePort)

	startPortForwardSession(ctx, ec, service, container, remotePort, localPort, dbProxyMode)
}

func resolveServiceAndPort(ctx context.Context, ec *ecsCtx, cmd *cobra.Command, dbProxyMode bool, app, env, appConfigOverride string) (string, int) {
	if dbProxyMode {
		return resolveDBProxyServiceAndPort(ctx, ec, cmd)
	}
	return resolveAppServiceAndPort(ec, cmd, app, env, appConfigOverride)
}

func resolveDBProxyServiceAndPort(ctx context.Context, ec *ecsCtx, cmd *cobra.Command) (string, int) {
	proxies, err := pkgecs.ListDBProxyServices(ctx, ec.ecsClient, ec.base.ECS.Cluster)
	if err != nil {
		log.Fatal("Failed to list db-proxy services", "err", err)
	}
	if len(proxies) == 0 {
		log.Fatal("No ECS services matching \"db-proxy\" found in cluster",
			"cluster", ec.base.ECS.Cluster)
	}
	sort.Strings(proxies)

	selected := pickDBProxyService(proxies)

	if cmd.Flags().Changed("port") {
		remotePort, err := cmd.Flags().GetInt("port")
		if err != nil || remotePort <= 0 {
			log.Fatal("Invalid --port value", "err", err)
		}
		return selected, remotePort
	}

	remotePort, err := pkgecs.InferDBPort(selected)
	if err != nil {
		if errors.Is(err, pkgecs.ErrUnknownDBPort) {
			log.Fatal("Could not infer remote port from service name; pass --port explicitly",
				"service", selected)
		}
		log.Fatal("Infer remote port", "err", err)
	}
	return selected, remotePort
}

func pickDBProxyService(proxies []string) string {
	if len(proxies) == 1 {
		return proxies[0]
	}
	opts := make([]huh.Option[string], len(proxies))
	for i, p := range proxies {
		opts[i] = huh.NewOption(p, p)
	}
	var selected string
	sel := huh.NewSelect[string]().
		Title("Select db-proxy service").
		Options(opts...).
		Value(&selected)
	form := huh.NewForm(huh.NewGroup(sel))
	if err := form.Run(); err != nil {
		log.Fatal("Selection cancelled", "err", err)
	}
	return selected
}

func resolveAppServiceAndPort(ec *ecsCtx, cmd *cobra.Command, app, env, appConfigOverride string) (string, int) {
	requireAppInMonoRepo(ec.cfg, app)
	if app == "" {
		log.Fatal("--app is required for ops ecs port-forward")
	}
	if !cmd.Flags().Changed("port") {
		log.Fatal("--port is required for ops ecs port-forward")
	}
	remotePort, err := cmd.Flags().GetInt("port")
	if err != nil || remotePort <= 0 {
		log.Fatal("Invalid --port value (must be a positive integer)", "err", err)
	}
	_, _, names := loadApp(ec, app, env, appConfigOverride)
	return names.Service, remotePort
}

func resolveLocalPort(cmd *cobra.Command, remotePort int) int {
	localPort := remotePort
	if cmd.Flags().Changed("local-port") {
		var err error
		localPort, err = cmd.Flags().GetInt("local-port")
		if err != nil || localPort <= 0 {
			log.Fatal("Invalid --local-port value (must be a positive integer)", "err", err)
		}
	}
	return localPort
}

func startPortForwardSession(ctx context.Context, ec *ecsCtx, service, container string, remotePort, localPort int, dbProxyMode bool) {
	taskArn, err := pkgecs.FindFirstRunningTaskArn(ctx, ec.ecsClient, ec.base.ECS.Cluster, service)
	if err != nil {
		log.Fatal("Could not find running task", "service", service, "err", err)
	}

	task, err := pkgecs.DescribeTask(ctx, ec.ecsClient, ec.base.ECS.Cluster, taskArn)
	if err != nil {
		log.Fatal("Describe task failed", "err", err)
	}

	resolvedContainer, runtimeID, err := pkgecs.ResolveContainerRuntimeID(task, container)
	if err != nil {
		log.Fatal("Resolve container runtime ID", "err", err)
	}

	taskID := pkgecs.TaskIDFromARN(taskArn)

	mode := "port-forward"
	if dbProxyMode {
		mode = "db-proxy"
	}
	log.Info("Starting port forwarding",
		"mode", mode,
		"service", service,
		"task_id", taskID,
		"container", resolvedContainer,
		"remote", strconv.Itoa(remotePort),
		"local", strconv.Itoa(localPort),
	)

	err = pkgecs.RunPortForwardSession(ctx, pkgecs.PortForwardSessionOpts{
		Cluster:    ec.base.ECS.Cluster,
		TaskID:     taskID,
		RuntimeID:  runtimeID,
		Region:     ec.cfg.AWS.Region,
		Profile:    ec.cfg.AWS.Profile,
		RemotePort: remotePort,
		LocalPort:  localPort,
	})
	if err != nil {
		log.Fatal("Port-forward session ended", "err", err)
	}
}
