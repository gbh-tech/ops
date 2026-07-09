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

	if len(descOut.Tasks) == 0 {
		return taskArn, nil
	}
	task := descOut.Tasks[0]
	for _, c := range task.Containers {
		isTargetContainer := aws.ToString(c.Name) == opts.AppName
		exitedNonZero := c.ExitCode != nil && *c.ExitCode != 0
		if isTargetContainer && exitedNonZero {
			return taskArn, fmt.Errorf("migration task exited with code %d", *c.ExitCode)
		}
	}

	return taskArn, nil
}
