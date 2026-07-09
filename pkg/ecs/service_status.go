package ecs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
)

// ServiceStatus summarizes the current state of an ECS service.
type ServiceStatus struct {
	Status       string
	RunningCount int32
	DesiredCount int32
	TaskDef      string
	LastEvent    string
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
