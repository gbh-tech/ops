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

// ServiceStatus summarizes the current state of an ECS service.
type ServiceStatus struct {
	Status       string
	RunningCount int32
	DesiredCount int32
	TaskDef      string
	LastEvent    string
}

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

// RegisterTaskDefinition registers a task definition and returns its ARN.
func RegisterTaskDefinition(ctx context.Context, client *awsecs.Client, input awsecs.RegisterTaskDefinitionInput) (string, error) {
	out, err := client.RegisterTaskDefinition(ctx, &input)
	if err != nil {
		return "", fmt.Errorf("register task definition: %w", err)
	}
	return aws.ToString(out.TaskDefinition.TaskDefinitionArn), nil
}

// UpdateService points a service at a new task definition and triggers a
// force-new-deployment.
func UpdateService(ctx context.Context, client *awsecs.Client, cluster, service, taskDefArn string, desiredCount int32) error {
	_, err := client.UpdateService(ctx, &awsecs.UpdateServiceInput{
		Cluster:            aws.String(cluster),
		Service:            aws.String(service),
		TaskDefinition:     aws.String(taskDefArn),
		DesiredCount:       aws.Int32(desiredCount),
		ForceNewDeployment: true,
	})
	if err != nil {
		return fmt.Errorf("update service %s: %w", service, err)
	}
	return nil
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
		return "", fmt.Errorf("service %s not found in cluster %s", opts.Service, opts.Cluster)
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

	if len(descOut.Tasks) > 0 {
		for _, c := range descOut.Tasks[0].Containers {
			if aws.ToString(c.Name) == opts.AppName && c.ExitCode != nil && *c.ExitCode != 0 {
				return taskArn, fmt.Errorf("migration task exited with code %d", *c.ExitCode)
			}
		}
	}

	return taskArn, nil
}

// WaitForStability blocks until the service reaches a stable state.
func WaitForStability(ctx context.Context, client *awsecs.Client, cluster, service string) error {
	log.Info("Waiting for service stability...", "service", service)
	waiter := awsecs.NewServicesStableWaiter(client, func(o *awsecs.ServicesStableWaiterOptions) {
		o.MinDelay = 10 * time.Second
		o.MaxDelay = 10 * time.Second
	})
	if err := waiter.Wait(ctx, &awsecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: []string{service},
	}, 30*time.Minute); err != nil {
		return fmt.Errorf("waiting for service stability: %w", err)
	}
	log.Info("Service is stable", "service", service)
	return nil
}

// GetServiceStatus returns the current status of an ECS service.
func GetServiceStatus(ctx context.Context, client *awsecs.Client, cluster, service string) (ServiceStatus, error) {
	out, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: []string{service},
	})
	if err != nil {
		return ServiceStatus{}, fmt.Errorf("describe service %s: %w", service, err)
	}
	if len(out.Services) == 0 {
		return ServiceStatus{}, fmt.Errorf("service %s not found", service)
	}

	svc := out.Services[0]
	status := ServiceStatus{
		Status:       aws.ToString(svc.Status),
		RunningCount: svc.RunningCount,
		DesiredCount: svc.DesiredCount,
		TaskDef:      aws.ToString(svc.TaskDefinition),
	}
	if len(svc.Events) > 0 {
		status.LastEvent = aws.ToString(svc.Events[0].Message)
	}
	return status, nil
}

// Rollback re-deploys the previous task definition revision for a service.
func Rollback(ctx context.Context, client *awsecs.Client, cluster, service, family string) error {
	svcOut, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: []string{service},
	})
	if err != nil {
		return fmt.Errorf("describe service %s: %w", service, err)
	}
	if len(svcOut.Services) == 0 {
		return fmt.Errorf("service %s not found", service)
	}

	currentArn := aws.ToString(svcOut.Services[0].TaskDefinition)
	// ARN format: arn:aws:ecs:region:account:task-definition/family:revision
	parts := strings.Split(currentArn, ":")
	var revision int
	if _, err := fmt.Sscanf(parts[len(parts)-1], "%d", &revision); err != nil {
		return fmt.Errorf("could not parse revision from ARN %s: %w", currentArn, err)
	}
	if revision <= 1 {
		return fmt.Errorf("no previous revision to roll back to (current: %d)", revision)
	}

	previous := fmt.Sprintf("%s:%d", family, revision-1)
	log.Info("Rolling back", "service", service, "from", revision, "to", revision-1)

	_, err = client.UpdateService(ctx, &awsecs.UpdateServiceInput{
		Cluster:            aws.String(cluster),
		Service:            aws.String(service),
		TaskDefinition:     aws.String(previous),
		ForceNewDeployment: true,
	})
	if err != nil {
		return fmt.Errorf("update service for rollback: %w", err)
	}
	return nil
}

// CleanupTaskDefinitions deregisters and deletes old task definition revisions
// for a family, keeping the latest `keep` revisions.
func CleanupTaskDefinitions(ctx context.Context, client *awsecs.Client, family string, keep int) error {
	var arns []string
	paginator := awsecs.NewListTaskDefinitionsPaginator(client, &awsecs.ListTaskDefinitionsInput{
		FamilyPrefix: aws.String(family),
		Status:       ecstypes.TaskDefinitionStatusActive,
		Sort:         ecstypes.SortOrderAsc,
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list task definitions for %s: %w", family, err)
		}
		arns = append(arns, page.TaskDefinitionArns...)
	}

	total := len(arns)
	if total <= keep {
		log.Info("Nothing to clean up", "family", family, "revisions", total, "keep", keep)
		return nil
	}

	toDelete := arns[:total-keep]
	log.Info("Cleaning up old revisions", "family", family, "deleting", len(toDelete), "keeping", keep)

	for _, arn := range toDelete {
		if _, err := client.DeregisterTaskDefinition(ctx, &awsecs.DeregisterTaskDefinitionInput{
			TaskDefinition: aws.String(arn),
		}); err != nil {
			return fmt.Errorf("deregister %s: %w", arn, err)
		}
	}

	// delete-task-definitions accepts up to 10 ARNs at a time.
	for i := 0; i < len(toDelete); i += 10 {
		end := i + 10
		if end > len(toDelete) {
			end = len(toDelete)
		}
		if _, err := client.DeleteTaskDefinitions(ctx, &awsecs.DeleteTaskDefinitionsInput{
			TaskDefinitions: toDelete[i:end],
		}); err != nil {
			return fmt.Errorf("delete task definitions batch: %w", err)
		}
	}

	log.Info("Cleanup complete", "family", family)
	return nil
}
