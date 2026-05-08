package ecs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

const dbProxySubstring = "db-proxy"

// ErrUnknownDBPort is returned when InferDBPort cannot derive a port from the service name.
var ErrUnknownDBPort = errors.New("cannot infer database port from service name (expected postgres, mysql, or redis in name); pass --port explicitly")

// InferDBPort maps common proxy naming conventions to default listening ports.
func InferDBPort(serviceName string) (int, error) {
	lower := strings.ToLower(serviceName)
	switch {
	case strings.Contains(lower, "postgres"):
		return 5432, nil
	case strings.Contains(lower, "mysql"):
		return 3306, nil
	case strings.Contains(lower, "redis"):
		return 6379, nil
	default:
		return 0, ErrUnknownDBPort
	}
}

// FilterDBProxyServiceNames returns names whose lowercase form contains "db-proxy".
func FilterDBProxyServiceNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), dbProxySubstring) {
			out = append(out, n)
		}
	}
	return out
}

// ServiceNameFromARN extracts the ECS service short name from a ListServices ARN.
// ARN format: arn:aws:ecs:region:account:service/cluster/service-name
func ServiceNameFromARN(arn string) string {
	if i := strings.LastIndex(arn, "/"); i >= 0 && i < len(arn)-1 {
		return arn[i+1:]
	}
	return arn
}

// ListDBProxyServices lists all service ARNs in the cluster and returns short names
// that match FilterDBProxyServiceNames.
func ListDBProxyServices(ctx context.Context, client *awsecs.Client, cluster string) ([]string, error) {
	var names []string
	var next *string
	for {
		out, err := client.ListServices(ctx, &awsecs.ListServicesInput{
			Cluster:    awssdk.String(cluster),
			NextToken:  next,
			MaxResults: awssdk.Int32(100),
		})
		if err != nil {
			return nil, fmt.Errorf("list services: %w", err)
		}
		for _, arn := range out.ServiceArns {
			names = append(names, ServiceNameFromARN(arn))
		}
		next = out.NextToken
		if next == nil || awssdk.ToString(next) == "" {
			break
		}
	}
	filtered := FilterDBProxyServiceNames(names)
	return filtered, nil
}

// FindFirstRunningTaskArn returns the ARN of the first RUNNING task for the service.
func FindFirstRunningTaskArn(ctx context.Context, client *awsecs.Client, cluster, service string) (string, error) {
	out, err := client.ListTasks(ctx, &awsecs.ListTasksInput{
		Cluster:       awssdk.String(cluster),
		ServiceName:   awssdk.String(service),
		DesiredStatus: ecstypes.DesiredStatusRunning,
		MaxResults:    awssdk.Int32(100),
	})
	if err != nil {
		return "", fmt.Errorf("list running tasks: %w", err)
	}
	if len(out.TaskArns) == 0 {
		return "", fmt.Errorf("no RUNNING tasks for service %q in cluster %q", service, cluster)
	}
	return out.TaskArns[0], nil
}

// TaskIDFromARN returns the ECS task ID segment (last path component of the ARN).
func TaskIDFromARN(taskArn string) string {
	if i := strings.LastIndex(taskArn, "/"); i >= 0 && i < len(taskArn)-1 {
		return taskArn[i+1:]
	}
	return taskArn
}

// ResolveContainerRuntimeID picks the target container and returns its runtime ID for SSM.
// When containerName is empty, the first container in the task is used.
func ResolveContainerRuntimeID(task *ecstypes.Task, containerName string) (resolvedName, runtimeID string, err error) {
	containers := task.Containers
	if len(containers) == 0 {
		return "", "", errors.New("task has no containers")
	}
	var c *ecstypes.Container
	if containerName != "" {
		for i := range containers {
			if awssdk.ToString(containers[i].Name) == containerName {
				c = &containers[i]
				break
			}
		}
		if c == nil {
			return "", "", fmt.Errorf("container %q not found on task", containerName)
		}
	} else {
		c = &containers[0]
	}
	rid := awssdk.ToString(c.RuntimeId)
	if rid == "" {
		return "", "", fmt.Errorf("runtimeId not available for container %q (task may still be starting)", awssdk.ToString(c.Name))
	}
	return awssdk.ToString(c.Name), rid, nil
}

// DescribeTask loads a single task by ARN.
func DescribeTask(ctx context.Context, client *awsecs.Client, cluster, taskArn string) (*ecstypes.Task, error) {
	out, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: awssdk.String(cluster),
		Tasks:   []string{taskArn},
	})
	if err != nil {
		return nil, fmt.Errorf("describe task: %w", err)
	}
	if len(out.Tasks) == 0 {
		return nil, fmt.Errorf("task %q not found", taskArn)
	}
	return &out.Tasks[0], nil
}

// PortForwardSessionOpts configures aws ssm start-session for ECS port forwarding.
type PortForwardSessionOpts struct {
	Cluster    string
	TaskID     string
	RuntimeID  string
	Region     string
	Profile    string
	RemotePort int
	LocalPort  int
}

// ECSExecTarget builds the SSM target string for ECS Exec / port forwarding.
func ECSExecTarget(cluster, taskID, runtimeID string) string {
	return fmt.Sprintf("ecs:%s_%s_%s", cluster, taskID, runtimeID)
}

// RunPortForwardSession shells out to `aws ssm start-session` with AWS-StartPortForwardingSession.
func RunPortForwardSession(ctx context.Context, opts PortForwardSessionOpts) error {
	if opts.RemotePort <= 0 || opts.RemotePort > 65535 {
		return fmt.Errorf("invalid remote port %d", opts.RemotePort)
	}
	if opts.LocalPort <= 0 || opts.LocalPort > 65535 {
		return fmt.Errorf("invalid local port %d", opts.LocalPort)
	}

	params := map[string][]string{
		"portNumber":      {fmt.Sprintf("%d", opts.RemotePort)},
		"localPortNumber": {fmt.Sprintf("%d", opts.LocalPort)},
	}
	paramJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal SSM parameters: %w", err)
	}

	target := ECSExecTarget(opts.Cluster, opts.TaskID, opts.RuntimeID)
	args := []string{
		"ssm", "start-session",
		"--target", target,
		"--document-name", "AWS-StartPortForwardingSession",
		"--parameters", string(paramJSON),
		"--region", opts.Region,
	}
	if opts.Profile != "" {
		args = append(args, "--profile", opts.Profile)
	}

	cmd := exec.CommandContext(ctx, "aws", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
