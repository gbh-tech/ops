package ecs

import (
	"context"
	"encoding/json"
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

	// CapacityProvider is the already-expanded capacity provider name.
	// When non-empty it is placed in a CapacityProviderStrategy on the ECS
	// task target, mirroring how RunMigrationTask handles this.
	// When empty, LaunchType is set instead.
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
		return nil, fmt.Errorf("service %s not found in cluster %s", service, cluster)
	}
	return out.Services[0].NetworkConfiguration, nil
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
func ReconcileSchedules(
	ctx context.Context,
	schedClient *scheduler.Client,
	cfg ReconcileConfig,
	taskDefArn string,
	desired []app.ScheduledTaskConfig,
) (created, updated, deleted []string, err error) {
	prefix := scheduleName(cfg.AppName, cfg.Env, "")

	// List all ops-managed schedules for this app/env in the group.
	existing := make(map[string]struct{})
	paginator := scheduler.NewListSchedulesPaginator(schedClient, &scheduler.ListSchedulesInput{
		GroupName:  aws.String(cfg.GroupName),
		NamePrefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, pageErr := paginator.NextPage(ctx)
		if pageErr != nil {
			return nil, nil, nil, fmt.Errorf(
				"list schedules (group=%s, prefix=%s): %w", cfg.GroupName, prefix, pageErr,
			)
		}
		for _, s := range page.Schedules {
			existing[aws.ToString(s.Name)] = struct{}{}
		}
	}

	// Index desired schedules by their full EventBridge name.
	desiredSet := make(map[string]struct{}, len(desired))
	for _, t := range desired {
		desiredSet[scheduleName(cfg.AppName, cfg.Env, t.Name)] = struct{}{}
	}

	// Delete schedules removed from the config.
	for name := range existing {
		if _, keep := desiredSet[name]; keep {
			continue
		}
		if _, delErr := schedClient.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
			Name:      aws.String(name),
			GroupName: aws.String(cfg.GroupName),
		}); delErr != nil {
			return created, updated, deleted,
				fmt.Errorf("delete schedule %s: %w", name, delErr)
		}
		log.Info("Deleted schedule", "name", name)
		deleted = append(deleted, name)
	}

	// Derive the cluster ARN used as Target.Arn.
	clusterArn := cfg.Cluster
	if !strings.HasPrefix(clusterArn, "arn:") {
		clusterArn = fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/%s",
			cfg.Region, cfg.AccountID, cfg.Cluster)
	}

	schedulerNetCfg := convertNetworkConfig(cfg.NetworkConfig)

	// Create or update each desired schedule.
	for _, t := range desired {
		name := scheduleName(cfg.AppName, cfg.Env, t.Name)

		inputJSON, buildErr := buildContainerInputJSON(cfg.AppName, t)
		if buildErr != nil {
			return created, updated, deleted,
				fmt.Errorf("build input JSON for schedule %s: %w", name, buildErr)
		}

		timezone := t.Timezone
		if timezone == "" {
			timezone = "UTC"
		}

		ecsParams := &schedulertypes.EcsParameters{
			TaskDefinitionArn:    aws.String(taskDefArn),
			TaskCount:            aws.Int32(1),
			NetworkConfiguration: schedulerNetCfg,
		}
		if cfg.CapacityProvider != "" {
			ecsParams.CapacityProviderStrategy = []schedulertypes.CapacityProviderStrategyItem{
				{
					CapacityProvider: aws.String(cfg.CapacityProvider),
					Weight:           100,
					Base:             1,
				},
			}
		} else if cfg.LaunchType != "" {
			ecsParams.LaunchType = schedulertypes.LaunchType(cfg.LaunchType)
		}

		target := &schedulertypes.Target{
			Arn:           aws.String(clusterArn),
			RoleArn:       aws.String(cfg.RoleArn),
			Input:         aws.String(inputJSON),
			EcsParameters: ecsParams,
		}

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
			if _, updateErr := schedClient.UpdateSchedule(ctx, in); updateErr != nil {
				return created, updated, deleted,
					fmt.Errorf("update schedule %s: %w", name, updateErr)
			}
			log.Info("Updated schedule", "name", name, "schedule", t.Schedule)
			updated = append(updated, name)
		} else {
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
			if _, createErr := schedClient.CreateSchedule(ctx, in); createErr != nil {
				return created, updated, deleted,
					fmt.Errorf("create schedule %s: %w", name, createErr)
			}
			log.Info("Created schedule", "name", name, "schedule", t.Schedule)
			created = append(created, name)
		}
	}

	return created, updated, deleted, nil
}

// scheduleName returns the full EventBridge Scheduler name for a task within
// an app/env. Passing an empty taskName produces the list prefix.
func scheduleName(appName, env, taskName string) string {
	return fmt.Sprintf("%s-%s-%s", appName, env, taskName)
}

// containerRunInput is the JSON payload placed in Target.Input to instruct
// EventBridge Scheduler to override the container command when invoking ECS RunTask.
type containerRunInput struct {
	ContainerOverrides []containerCommandOverride `json:"containerOverrides"`
}

type containerCommandOverride struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
	Cpu     int      `json:"cpu,omitempty"`
	Memory  int      `json:"memory,omitempty"`
}

func buildContainerInputJSON(appName string, t app.ScheduledTaskConfig) (string, error) {
	payload := containerRunInput{
		ContainerOverrides: []containerCommandOverride{
			{
				Name:    appName,
				Command: t.Command,
				Cpu:     t.CPU,
				Memory:  t.Memory,
			},
		},
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
