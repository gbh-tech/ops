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

// RegisterTaskDefinition registers a task definition and returns its ARN.
func RegisterTaskDefinition(ctx context.Context, client *awsecs.Client, input awsecs.RegisterTaskDefinitionInput) (string, error) {
	out, err := client.RegisterTaskDefinition(ctx, &input)
	if err != nil {
		return "", fmt.Errorf("register task definition: %w", err)
	}
	return aws.ToString(out.TaskDefinition.TaskDefinitionArn), nil
}

// UpdateServiceOptions bundles the inputs for UpdateService.
type UpdateServiceOptions struct {
	Client       *awsecs.Client
	Cluster      string
	Service      string
	TaskDefArn   string
	DesiredCount int32
}

// UpdateService points a service at a new task definition and triggers a
// force-new-deployment.
func UpdateService(ctx context.Context, opts UpdateServiceOptions) error {
	if _, err := opts.Client.UpdateService(ctx, &awsecs.UpdateServiceInput{
		Cluster:            aws.String(opts.Cluster),
		Service:            aws.String(opts.Service),
		TaskDefinition:     aws.String(opts.TaskDefArn),
		DesiredCount:       aws.Int32(opts.DesiredCount),
		ForceNewDeployment: true,
	}); err != nil {
		return fmt.Errorf("update service %s: %w", opts.Service, err)
	}
	return nil
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
		return fmt.Errorf("could not parse revision from ARN %q: %w", currentArn, err)
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
	arns := []string{}
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
