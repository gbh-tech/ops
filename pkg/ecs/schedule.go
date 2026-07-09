package ecs

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ops/pkg/app"

	"charm.land/log/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
)

// ReconcileConfig bundles the invariant inputs needed to reconcile an app's
// scheduled tasks against EventBridge Scheduler.
type ReconcileConfig struct {
	// GroupName is the EventBridge Scheduler schedule group that holds all
	// ops-managed schedules for this cluster/env (pre-created by Terraform).
	// Already has {cluster}/{env} placeholders expanded.
	GroupName string

	// RoleArn is the IAM role EventBridge Scheduler assumes to call ecs:RunTask.
	// Already has {cluster}/{env} placeholders expanded.
	RoleArn string

	// Cluster is the ECS cluster name (not ARN). The cluster ARN is derived
	// internally using Region and AccountID.
	Cluster string

	// Region is the AWS region.
	Region string

	// AccountID is the AWS account ID.
	AccountID string

	// AppName is the ECS app / container name.
	AppName string

	// Env is the deployment environment (e.g. "stage", "production").
	Env string

	// CapacityProvider is the default capacity provider name. It may contain
	// {service}/{env} placeholders. Individual scheduled tasks may override it.
	// When the resolved provider is non-empty it is placed in a
	// CapacityProviderStrategy on the ECS task target. When empty, LaunchType is
	// set instead.
	CapacityProvider string

	// LaunchType is the ECS launch type used when CapacityProvider is empty.
	LaunchType string

	// NetworkConfig is the VPC network configuration copied from the running
	// service (fetched once via FetchServiceNetworkConfig before calling
	// ReconcileSchedules). May be nil for tasks that do not use awsvpc.
	NetworkConfig *ecstypes.NetworkConfiguration
}

// FetchServiceNetworkConfig reads the network configuration from the running
// ECS service so scheduled tasks can inherit the same subnets/security groups.
func FetchServiceNetworkConfig(ctx context.Context, ecsClient *awsecs.Client, cluster, service string) (*ecstypes.NetworkConfiguration, error) {
	out, err := ecsClient.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: []string{service},
	})
	if err != nil {
		return nil, fmt.Errorf("describe service %s: %w", service, err)
	}
	if len(out.Services) == 0 {
		return nil, fmt.Errorf("service %q not found in cluster %q", service, cluster)
	}
	return out.Services[0].NetworkConfiguration, nil
}

// ReconcileSchedulesOptions bundles the inputs for ReconcileSchedules.
type ReconcileSchedulesOptions struct {
	Client     *scheduler.Client
	Cfg        ReconcileConfig
	TaskDefArn string
	Desired    []app.ScheduledTaskConfig
}

// ReconcileSchedules reconciles the desired scheduled task list against the
// EventBridge Scheduler schedules that currently exist in the configured group
// under the "{app}-{env}-" prefix.
//
//   - Schedules absent from desired but present in the group are deleted.
//   - Schedules present in desired but absent from the group are created.
//   - Schedules present in both are updated to match the current config.
//
// All schedules reuse taskDefArn (the revision just registered by the deploy)
// and override only the container command (and optionally CPU/memory).
func ReconcileSchedules(ctx context.Context, opts ReconcileSchedulesOptions) ([]string, []string, []string, error) {
	cfg := opts.Cfg
	prefix := scheduleName(cfg.AppName, cfg.Env, "")

	existing, err := listExistingSchedules(ctx, opts.Client, cfg.GroupName, prefix)
	if err != nil {
		return nil, nil, nil, err
	}

	desiredSet := buildDesiredSet(opts.Desired, cfg.AppName, cfg.Env)

	deleted, err := deleteRemovedSchedules(ctx, opts.Client, cfg.GroupName, existing, desiredSet)
	if err != nil {
		return nil, nil, nil, err
	}

	clusterArn := clusterArn(cfg)
	schedulerNetCfg := convertNetworkConfig(cfg.NetworkConfig)

	created := []string{}
	updated := []string{}
	for _, t := range opts.Desired {
		wasCreated, wasUpdated, err := createOrUpdateSchedule(ctx, opts.Client, cfg, opts.TaskDefArn, clusterArn, schedulerNetCfg, existing, t)
		if err != nil {
			return created, updated, deleted, err
		}
		if wasCreated {
			created = append(created, scheduleName(cfg.AppName, cfg.Env, t.Name))
		}
		if wasUpdated {
			updated = append(updated, scheduleName(cfg.AppName, cfg.Env, t.Name))
		}
	}

	return created, updated, deleted, nil
}

func listExistingSchedules(ctx context.Context, client *scheduler.Client, groupName, prefix string) (map[string]struct{}, error) {
	existing := make(map[string]struct{})
	paginator := scheduler.NewListSchedulesPaginator(client, &scheduler.ListSchedulesInput{
		GroupName:  aws.String(groupName),
		NamePrefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list schedules (group=%s, prefix=%s): %w", groupName, prefix, err)
		}
		for _, s := range page.Schedules {
			existing[aws.ToString(s.Name)] = struct{}{}
		}
	}
	return existing, nil
}

func buildDesiredSet(desired []app.ScheduledTaskConfig, appName, env string) map[string]struct{} {
	desiredSet := make(map[string]struct{}, len(desired))
	for _, t := range desired {
		desiredSet[scheduleName(appName, env, t.Name)] = struct{}{}
	}
	return desiredSet
}

func deleteRemovedSchedules(ctx context.Context, client *scheduler.Client, groupName string, existing, desiredSet map[string]struct{}) ([]string, error) {
	deleted := []string{}
	for name := range existing {
		if _, keep := desiredSet[name]; keep {
			continue
		}
		if _, err := client.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
			Name:      aws.String(name),
			GroupName: aws.String(groupName),
		}); err != nil {
			return deleted, fmt.Errorf("delete schedule %s: %w", name, err)
		}
		log.Info("Deleted schedule", "name", name)
		deleted = append(deleted, name)
	}
	return deleted, nil
}

func clusterArn(cfg ReconcileConfig) string {
	clusterArn := cfg.Cluster
	if !strings.HasPrefix(clusterArn, "arn:") {
		clusterArn = fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/%s",
			cfg.Region, cfg.AccountID, cfg.Cluster)
	}
	return clusterArn
}

func createOrUpdateSchedule(
	ctx context.Context,
	client *scheduler.Client,
	cfg ReconcileConfig,
	taskDefArn string,
	clusterArn string,
	schedulerNetCfg *schedulertypes.NetworkConfiguration,
	existing map[string]struct{},
	t app.ScheduledTaskConfig,
) (bool, bool, error) {
	name := scheduleName(cfg.AppName, cfg.Env, t.Name)

	inputJSON, err := buildContainerInputJSON(cfg.AppName, t)
	if err != nil {
		return false, false, fmt.Errorf("build input JSON for schedule %s: %w", name, err)
	}

	timezone := t.Timezone
	if timezone == "" {
		timezone = "UTC"
	}

	target := buildScheduleTarget(cfg, taskDefArn, clusterArn, schedulerNetCfg, inputJSON, t)
	ftw := buildFlexibleTimeWindow(t.FlexibleWindowMinutes)
	state := scheduleState(t.Enabled)

	if _, exists := existing[name]; exists {
		in := &scheduler.UpdateScheduleInput{
			Name:                       aws.String(name),
			GroupName:                  aws.String(cfg.GroupName),
			ScheduleExpression:         aws.String(t.Schedule),
			ScheduleExpressionTimezone: aws.String(timezone),
			Target:                     target,
			FlexibleTimeWindow:         ftw,
			State:                      state,
		}
		if t.Description != "" {
			in.Description = aws.String(t.Description)
		}
		if _, err := client.UpdateSchedule(ctx, in); err != nil {
			return false, false, fmt.Errorf("update schedule %s: %w", name, err)
		}
		log.Info("Updated schedule", "name", name, "schedule", t.Schedule)
		return false, true, nil
	}

	in := &scheduler.CreateScheduleInput{
		Name:                       aws.String(name),
		GroupName:                  aws.String(cfg.GroupName),
		ScheduleExpression:         aws.String(t.Schedule),
		ScheduleExpressionTimezone: aws.String(timezone),
		Target:                     target,
		FlexibleTimeWindow:         ftw,
		State:                      state,
	}
	if t.Description != "" {
		in.Description = aws.String(t.Description)
	}
	if _, err := client.CreateSchedule(ctx, in); err != nil {
		return false, false, fmt.Errorf("create schedule %s: %w", name, err)
	}
	log.Info("Created schedule", "name", name, "schedule", t.Schedule)
	return true, false, nil
}

func buildScheduleTarget(cfg ReconcileConfig, taskDefArn, clusterArn string, schedulerNetCfg *schedulertypes.NetworkConfiguration, inputJSON string, t app.ScheduledTaskConfig) *schedulertypes.Target {
	ecsParams := &schedulertypes.EcsParameters{
		TaskDefinitionArn:    aws.String(taskDefArn),
		TaskCount:            aws.Int32(1),
		NetworkConfiguration: schedulerNetCfg,
	}
	capacityProvider := ResolveScheduledTaskCapacityProvider(t, cfg.CapacityProvider, cfg.AppName, cfg.Env)
	if capacityProvider != "" {
		ecsParams.CapacityProviderStrategy = []schedulertypes.CapacityProviderStrategyItem{
			{
				CapacityProvider: aws.String(capacityProvider),
				Weight:           100,
				Base:             1,
			},
		}
	} else if cfg.LaunchType != "" {
		ecsParams.LaunchType = schedulertypes.LaunchType(cfg.LaunchType)
	}

	return &schedulertypes.Target{
		Arn:           aws.String(clusterArn),
		RoleArn:       aws.String(cfg.RoleArn),
		Input:         aws.String(inputJSON),
		EcsParameters: ecsParams,
	}
}

// RunScheduledTaskOpts bundles the inputs for RunScheduledTask.
type RunScheduledTaskOpts struct {
	// Cluster is the ECS cluster name.
	Cluster string
	// Service is the ECS service name ("{app}-{env}"), used to inherit
	// network configuration.
	Service string
	// ScheduledFamily is the "{app}-{env}-scheduled" task definition family.
	// ECS resolves a bare family name to the latest active revision.
	ScheduledFamily string
	// AppName is the ECS container name used in the command override.
	AppName string
	// Env is the deployment environment used to expand capacity provider templates.
	Env string
	// CapacityProvider is the default capacity provider name. It may contain
	// {service}/{env} placeholders. Task.CapacityProvider overrides it.
	// When the resolved provider is empty, no capacity provider strategy is set.
	CapacityProvider string
	// Task is the scheduled task config entry to run ad-hoc.
	Task app.ScheduledTaskConfig
}

// RunScheduledTask launches a one-off ECS task for the given scheduled task
// config using the ScheduledFamily task definition (no port mappings or health
// checks) and the service's network configuration. It waits for the task to
// stop, checks the exit code, and returns the task ARN. The approach mirrors
// RunMigrationTask in deploy.go.
func RunScheduledTask(ctx context.Context, client *awsecs.Client, opts RunScheduledTaskOpts) (string, error) {
	svcOut, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster:  aws.String(opts.Cluster),
		Services: []string{opts.Service},
	})
	if err != nil {
		return "", fmt.Errorf("describe service %s: %w", opts.Service, err)
	}
	if len(svcOut.Services) == 0 {
		return "", fmt.Errorf("service %s not found in cluster %s", opts.Service, opts.Cluster)
	}
	svc := svcOut.Services[0]

	// Use the dedicated scheduled task family (no port mappings / health checks).
	// ECS resolves a bare family name to the latest active revision.
	taskDefinition := opts.ScheduledFamily
	if taskDefinition == "" {
		// Fall back to the service's current task definition when the scheduled
		// family is not set (e.g. older deployments that pre-date this feature).
		taskDefinition = aws.ToString(svc.TaskDefinition)
	}

	overrides := &ecstypes.TaskOverride{
		ContainerOverrides: []ecstypes.ContainerOverride{
			{
				Name:    aws.String(opts.AppName),
				Command: opts.Task.Command,
			},
		},
	}
	if opts.Task.CPU != 0 {
		overrides.Cpu = aws.String(strconv.Itoa(opts.Task.CPU))
	}
	if opts.Task.Memory != 0 {
		overrides.Memory = aws.String(strconv.Itoa(opts.Task.Memory))
	}

	runInput := &awsecs.RunTaskInput{
		Cluster:              aws.String(opts.Cluster),
		TaskDefinition:       aws.String(taskDefinition),
		NetworkConfiguration: svc.NetworkConfiguration,
		Overrides:            overrides,
	}
	capacityProvider := ResolveScheduledTaskCapacityProvider(opts.Task, opts.CapacityProvider, opts.AppName, opts.Env)
	if capacityProvider != "" {
		runInput.CapacityProviderStrategy = []ecstypes.CapacityProviderStrategyItem{
			{
				CapacityProvider: aws.String(capacityProvider),
				Weight:           100,
				Base:             1,
			},
		}
	}

	runOut, err := client.RunTask(ctx, runInput)
	if err != nil {
		return "", fmt.Errorf("run task: %w", err)
	}
	if len(runOut.Failures) > 0 {
		reasons := make([]string, len(runOut.Failures))
		for i, f := range runOut.Failures {
			reasons[i] = aws.ToString(f.Reason)
		}
		return "", fmt.Errorf("task failed to start: %s", strings.Join(reasons, "; "))
	}
	if len(runOut.Tasks) == 0 {
		return "", fmt.Errorf("no task returned from RunTask")
	}

	taskArn := aws.ToString(runOut.Tasks[0].TaskArn)
	log.Info("Task started", "taskArn", taskArn)
	log.Info("Waiting for task to complete...")

	waiter := awsecs.NewTasksStoppedWaiter(client, func(o *awsecs.TasksStoppedWaiterOptions) {
		o.MinDelay = 2 * time.Second
		o.MaxDelay = 15 * time.Second
	})
	if err := waiter.Wait(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String(opts.Cluster),
		Tasks:   []string{taskArn},
	}, 30*time.Minute); err != nil {
		return taskArn, fmt.Errorf("waiting for task to stop: %w", err)
	}

	descOut, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String(opts.Cluster),
		Tasks:   []string{taskArn},
	})
	if err != nil {
		return taskArn, fmt.Errorf("describe task: %w", err)
	}
	if len(descOut.Tasks) == 0 {
		return taskArn, nil
	}
	task := descOut.Tasks[0]
	for _, c := range task.Containers {
		if aws.ToString(c.Name) == opts.AppName {
			if reason := aws.ToString(c.Reason); reason != "" {
				return taskArn, fmt.Errorf("container failed: %s", reason)
			}
			if c.ExitCode != nil {
				if *c.ExitCode != 0 {
					return taskArn, fmt.Errorf("task exited with code %d", *c.ExitCode)
				}
				// ExitCode 0 is definitive success; skip task-level StoppedReason.
				return taskArn, nil
			}
			// No Reason and no ExitCode: not definitive; fall through to StoppedReason.
			break
		}
	}
	// Target container not found or produced no definitive exit status.
	if stoppedReason := aws.ToString(task.StoppedReason); stoppedReason != "" {
		return taskArn, fmt.Errorf("task stopped: %s", stoppedReason)
	}

	return taskArn, nil
}

// ResolveScheduledTaskCapacityProvider returns the capacity provider that should
// be used for one scheduled task. The task-specific value wins over the default,
// and {service}/{env} placeholders are expanded when present.
func ResolveScheduledTaskCapacityProvider(t app.ScheduledTaskConfig, defaultProvider, appName, env string) string {
	provider := defaultProvider
	if t.CapacityProvider != "" {
		provider = t.CapacityProvider
	}
	return ExpandTemplate(provider, appName, env)
}

// scheduleName returns the full EventBridge Scheduler name for a task within
// an app/env. Passing an empty taskName produces the list prefix.
func scheduleName(appName, env, taskName string) string {
	return appName + "-" + env + "-" + taskName
}

// containerRunInput is the JSON payload placed in Target.Input to instruct
// EventBridge Scheduler to override the container command when invoking ECS RunTask.
type containerRunInput struct {
	ContainerOverrides []containerCommandOverride `json:"containerOverrides"`
	Cpu                string                     `json:"cpu,omitempty"`
	Memory             string                     `json:"memory,omitempty"`
}

type containerCommandOverride struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
}

func buildContainerInputJSON(appName string, t app.ScheduledTaskConfig) (string, error) {
	payload := containerRunInput{
		ContainerOverrides: []containerCommandOverride{
			{
				Name:    appName,
				Command: t.Command,
			},
		},
	}
	if t.CPU != 0 {
		payload.Cpu = strconv.Itoa(t.CPU)
	}
	if t.Memory != 0 {
		payload.Memory = strconv.Itoa(t.Memory)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// convertNetworkConfig translates an ECS NetworkConfiguration (returned by
// DescribeServices) into the equivalent scheduler NetworkConfiguration.
func convertNetworkConfig(ecsNet *ecstypes.NetworkConfiguration) *schedulertypes.NetworkConfiguration {
	if ecsNet == nil || ecsNet.AwsvpcConfiguration == nil {
		return nil
	}
	vpc := ecsNet.AwsvpcConfiguration
	sv := &schedulertypes.AwsVpcConfiguration{
		Subnets:        vpc.Subnets,
		SecurityGroups: vpc.SecurityGroups,
	}
	if vpc.AssignPublicIp != "" {
		sv.AssignPublicIp = schedulertypes.AssignPublicIp(string(vpc.AssignPublicIp))
	}
	return &schedulertypes.NetworkConfiguration{AwsvpcConfiguration: sv}
}

// buildFlexibleTimeWindow converts a minute count to a FlexibleTimeWindow.
// 0 or negative means OFF (exact scheduled start time).
func buildFlexibleTimeWindow(minutes int) *schedulertypes.FlexibleTimeWindow {
	if minutes <= 0 {
		return &schedulertypes.FlexibleTimeWindow{Mode: schedulertypes.FlexibleTimeWindowModeOff}
	}
	return &schedulertypes.FlexibleTimeWindow{
		Mode:                   schedulertypes.FlexibleTimeWindowModeFlexible,
		MaximumWindowInMinutes: aws.Int32(int32(minutes)),
	}
}

// scheduleState converts the optional enabled pointer to an EventBridge state.
// nil is treated as true (enabled) to match the config default.
func scheduleState(enabled *bool) schedulertypes.ScheduleState {
	if enabled != nil && !*enabled {
		return schedulertypes.ScheduleStateDisabled
	}
	return schedulertypes.ScheduleStateEnabled
}
