package ecs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// MigrationOpts bundles the arguments needed for RunMigrationTask.
type MigrationOpts struct {
	Cluster          string
	Service          string
	Family           string
	AppName          string
	MigrationCommand []string
	// CapacityProvider is the already-expanded provider name (e.g. "ec2-lighthouse-platform-stage").
	CapacityProvider string
}

// RunMigrationTask launches a one-off ECS task with a command override, waits
// for it to stop, checks the exit code, and returns the task ARN.
func RunMigrationTask(ctx context.Context, client *awsecs.Client, opts MigrationOpts) (string, error) {
	// Fetch network config from the running service.
	svcOut, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster:  aws.String(opts.Cluster),
		Services: []string{opts.Service},
	})
	if err != nil {
		return "", fmt.Errorf("describe service %s: %w", opts.Service, err)
	}
	if len(svcOut.Services) == 0 {
		return "", fmt.Errorf("service %q not found in cluster %q", opts.Service, opts.Cluster)
	}

	runInput := &awsecs.RunTaskInput{
		Cluster:              aws.String(opts.Cluster),
		TaskDefinition:       aws.String(opts.Family),
		NetworkConfiguration: svcOut.Services[0].NetworkConfiguration,
		Overrides: &ecstypes.TaskOverride{
			ContainerOverrides: []ecstypes.ContainerOverride{
				{
					Name:    aws.String(opts.AppName),
					Command: opts.MigrationCommand,
				},
			},
		},
	}

	if opts.CapacityProvider != "" {
		runInput.CapacityProviderStrategy = []ecstypes.CapacityProviderStrategyItem{
			{
				CapacityProvider: aws.String(opts.CapacityProvider),
				Weight:           100,
				Base:             1,
			},
		}
	}

	runOut, err := client.RunTask(ctx, runInput)
	if err != nil {
		return "", fmt.Errorf("run migration task: %w", err)
	}
	if len(runOut.Failures) > 0 {
		reasons := make([]string, len(runOut.Failures))
		for i, f := range runOut.Failures {
			reasons[i] = aws.ToString(f.Reason)
		}
		return "", fmt.Errorf("migration task failed to start: %s", strings.Join(reasons, "; "))
	}
	if len(runOut.Tasks) == 0 {
		return "", fmt.Errorf("no task returned from RunTask")
	}

	taskArn := aws.ToString(runOut.Tasks[0].TaskArn)
	log.Info("Migration task started", "taskArn", taskArn)
	log.Info("Waiting for migration to complete...")

	waiter := awsecs.NewTasksStoppedWaiter(client, func(o *awsecs.TasksStoppedWaiterOptions) {
		o.MinDelay = 2 * time.Second
		o.MaxDelay = 15 * time.Second
	})
	if err := waiter.Wait(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String(opts.Cluster),
		Tasks:   []string{taskArn},
	}, 30*time.Minute); err != nil {
		return taskArn, fmt.Errorf("waiting for migration task to stop: %w", err)
	}

	descOut, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String(opts.Cluster),
		Tasks:   []string{taskArn},
	})
	if err != nil {
		return taskArn, fmt.Errorf("describe migration task: %w", err)
	}
	if err := evaluateMigrationTaskResult(descOut, opts.AppName); err != nil {
		return taskArn, err
	}

	return taskArn, nil
}

// evaluateMigrationTaskResult verifies that ECS reported a successful exit for
// the migration container. Other containers in the task do not affect the result.
func evaluateMigrationTaskResult(out *awsecs.DescribeTasksOutput, appName string) error {
	if len(out.Failures) > 0 {
		failures := make([]string, len(out.Failures))
		for i, failure := range out.Failures {
			parts := make([]string, 0, 3)
			if arn := aws.ToString(failure.Arn); arn != "" {
				parts = append(parts, arn)
			}
			if reason := aws.ToString(failure.Reason); reason != "" {
				parts = append(parts, reason)
			}
			if detail := aws.ToString(failure.Detail); detail != "" {
				parts = append(parts, detail)
			}
			if len(parts) == 0 {
				parts = append(parts, "unknown failure")
			}
			failures[i] = strings.Join(parts, ": ")
		}
		return fmt.Errorf("describe migration task failed: %s", strings.Join(failures, "; "))
	}
	if len(out.Tasks) == 0 {
		return fmt.Errorf("no migration task returned from DescribeTasks")
	}

	task := out.Tasks[0]
	stoppedReason := aws.ToString(task.StoppedReason)
	for _, container := range task.Containers {
		if aws.ToString(container.Name) != appName {
			continue
		}

		containerReason := aws.ToString(container.Reason)
		if containerReason != "" {
			return fmt.Errorf("migration container %q failed%s", appName, migrationReasonSuffix(containerReason, stoppedReason))
		}
		if container.ExitCode == nil {
			return fmt.Errorf("migration container %q has no exit code%s", appName, migrationReasonSuffix("", stoppedReason))
		}
		if *container.ExitCode != 0 {
			return fmt.Errorf("migration container %q exited with code %d%s", appName, *container.ExitCode, migrationReasonSuffix("", stoppedReason))
		}
		return nil
	}

	return fmt.Errorf("migration container %q not found in stopped task%s", appName, migrationReasonSuffix("", stoppedReason))
}

func migrationReasonSuffix(containerReason, stoppedReason string) string {
	reasons := make([]string, 0, 2)
	if containerReason != "" {
		reasons = append(reasons, "container reason: "+containerReason)
	}
	if stoppedReason != "" {
		reasons = append(reasons, "task stopped reason: "+stoppedReason)
	}
	if len(reasons) == 0 {
		return ""
	}
	return " (" + strings.Join(reasons, "; ") + ")"
}
