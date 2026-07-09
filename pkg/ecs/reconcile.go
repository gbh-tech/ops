package ecs

import (
	"context"
	"fmt"
	"strings"

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
