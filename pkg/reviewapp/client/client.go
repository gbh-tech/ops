// Package client is the client mode of ops reviewapp (RFC 0004, D11).
//
// RDS and MSK are private, so GitHub-hosted runners and laptops cannot change
// a review app themselves. The client uploads the manifest to the state
// bucket, starts the reviewapp-controller task with the same command and
// --in-cluster, prints the task's logs as they arrive, and returns the task's
// exit code. The manifest travels through S3 because RunTask overrides are
// limited to 8 KB.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"ops/pkg/reviewapp/infra"
	"ops/pkg/reviewapp/state"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ContainerName is the container of the reviewapp-controller task definition.
const ContainerName = "ops"

// LogStreamPrefix is the awslogs stream prefix of the controller task.
const LogStreamPrefix = "controller"

// DefaultTimeout bounds a controller run. A create waits for every service to
// reach steady state, so it is well above the 10-minute ECS waiter default.
const DefaultTimeout = 60 * time.Minute

// ECSAPI is the subset of the ECS client the client uses.
type ECSAPI interface {
	RunTask(ctx context.Context, in *awsecs.RunTaskInput, opts ...func(*awsecs.Options)) (*awsecs.RunTaskOutput, error)
	DescribeTasks(ctx context.Context, in *awsecs.DescribeTasksInput, opts ...func(*awsecs.Options)) (*awsecs.DescribeTasksOutput, error)
}

// LogsAPI is the subset of the CloudWatch Logs client the client uses.
type LogsAPI interface {
	GetLogEvents(ctx context.Context, in *cwlogs.GetLogEventsInput, opts ...func(*cwlogs.Options)) (*cwlogs.GetLogEventsOutput, error)
}

// ManifestStore uploads manifests. *state.Store implements it.
type ManifestStore interface {
	PutManifest(ctx context.Context, m *state.Manifest) (state.ManifestRef, error)
}

// Settings are the reviewapps: config values the controller needs. The
// controller task has no repository checkout, so the client passes them as
// flags.
type Settings struct {
	Domain     string
	InfraSSM   string
	ToolsImage string
	Region     string
	// Repository (owner/name) is where the controller comments.
	Repository string
	// Actor and RunURL go into the review app's history.
	Actor  string
	RunURL string
}

// Client starts controller runs.
type Client struct {
	ECS        ECSAPI
	Logs       LogsAPI
	Manifests  ManifestStore
	Infra      *infra.Infra
	Controller *infra.Unit
	Settings   Settings
	Out        io.Writer

	// PollInterval is how often the client checks the task and its logs.
	PollInterval time.Duration
	// Timeout bounds the whole run; zero means DefaultTimeout.
	Timeout time.Duration
}

// Run is one controller invocation.
type Run struct {
	// Command is the ops reviewapp subcommand, e.g. "create".
	Command string
	PR      int
	// Manifest, when set, is uploaded and passed to the controller by version.
	Manifest *state.Manifest
	// Args are extra subcommand arguments, e.g. "--", a unit and its command
	// for run. They follow every flag.
	Args []string
	// StartedBy identifies the caller in ECS, e.g. the GitHub run id.
	StartedBy string
}

// Result is the outcome of a controller run.
type Result struct {
	TaskARN  string
	ExitCode int
}

// Execute uploads the manifest, starts the controller and waits for it. A
// non-zero exit code is a Result, not an error; errors mean the run could not
// be started or followed.
func (c *Client) Execute(ctx context.Context, r Run) (*Result, error) {
	if r.Manifest != nil && r.Manifest.PR != r.PR {
		return nil, fmt.Errorf("manifest is for PR %d, not %d", r.Manifest.PR, r.PR)
	}

	timeout := c.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var ref *state.ManifestRef
	if r.Manifest != nil {
		got, err := c.Manifests.PutManifest(ctx, r.Manifest)
		if err != nil {
			return nil, fmt.Errorf("upload manifest: %w", err)
		}
		ref = &got
		c.printf("Uploaded manifest for %s (version %s)\n", state.ID(r.PR), got.VersionID)
	}

	taskARN, err := c.start(ctx, r, ref)
	if err != nil {
		return nil, err
	}
	c.printf("Started controller task %s\n", taskARN)

	code, err := c.follow(ctx, taskARN)
	if err != nil {
		return &Result{TaskARN: taskARN}, err
	}
	return &Result{TaskARN: taskARN, ExitCode: code}, nil
}

// Command returns the container command for a run: the same subcommand with
// --in-cluster and the settings the controller cannot read from a repository.
func (c *Client) Command(r Run, ref *state.ManifestRef) []string {
	cmd := []string{"reviewapp", r.Command, "--pr", strconv.Itoa(r.PR)}
	if ref != nil {
		cmd = append(cmd, "--manifest-version", ref.VersionID, "--manifest-sha256", ref.SHA256)
	}
	cmd = append(cmd,
		"--in-cluster",
		"--domain", c.Settings.Domain,
		"--infra-ssm", c.Settings.InfraSSM,
		"--tools-image", c.Settings.ToolsImage,
		"--region", c.Settings.Region,
	)
	for _, kv := range [][2]string{
		{"--repository", c.Settings.Repository},
		{"--actor", c.Settings.Actor},
		{"--run-url", c.Settings.RunURL},
	} {
		if kv[1] != "" {
			cmd = append(cmd, kv[0], kv[1])
		}
	}
	// Arguments go last, after "--" when the caller passes one, so a
	// command for a one-off task is never read as ops flags.
	return append(cmd, r.Args...)
}

func (c *Client) start(ctx context.Context, r Run, ref *state.ManifestRef) (string, error) {
	startedBy := r.StartedBy
	if startedBy == "" {
		startedBy = "ops-reviewapp"
	}
	out, err := c.ECS.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster:        aws.String(c.Infra.Cluster.ARN),
		TaskDefinition: aws.String(c.Infra.Controller.TaskDefinition),
		LaunchType:     ecstypes.LaunchTypeFargate,
		StartedBy:      aws.String(truncate(startedBy, 128)),
		NetworkConfiguration: &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
				Subnets:        c.Infra.Network.PrivateSubnetIDs,
				SecurityGroups: []string{c.Controller.SecurityGroup},
				AssignPublicIp: ecstypes.AssignPublicIpDisabled,
			},
		},
		Overrides: &ecstypes.TaskOverride{
			ContainerOverrides: []ecstypes.ContainerOverride{{
				Name:    aws.String(ContainerName),
				Command: c.Command(r, ref),
			}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("start controller task: %w", err)
	}
	if len(out.Failures) > 0 {
		reasons := make([]string, len(out.Failures))
		for i, f := range out.Failures {
			reasons[i] = aws.ToString(f.Reason)
		}
		return "", fmt.Errorf("controller task failed to start: %s", strings.Join(reasons, "; "))
	}
	if len(out.Tasks) == 0 {
		return "", errors.New("RunTask returned no task")
	}
	return aws.ToString(out.Tasks[0].TaskArn), nil
}

// follow prints the task's logs until it stops, then returns the exit code of
// the controller container.
func (c *Client) follow(ctx context.Context, taskARN string) (int, error) {
	logs := &logTail{
		client: c.Logs,
		group:  c.Controller.LogGroup,
		stream: fmt.Sprintf("%s/%s/%s", LogStreamPrefix, ContainerName, taskID(taskARN)),
		out:    c.Out,
	}
	interval := c.PollInterval
	if interval == 0 {
		interval = 5 * time.Second
	}

	// The task's exit code is the result. A log read that fails (access,
	// throttling) only costs output, so it warns once and polling goes on.
	warned := false
	drain := func() {
		if err := logs.drain(ctx); err != nil && !warned {
			c.printf("WARNING: %v; the exit code still comes from the task\n", err)
			warned = true
		}
	}

	for {
		task, err := c.describe(ctx, taskARN)
		if err != nil {
			return 0, err
		}
		drain()
		if aws.ToString(task.LastStatus) == string(ecstypes.DesiredStatusStopped) {
			// Events can reach CloudWatch shortly after the task stops, and the
			// last lines usually hold the failure reason.
			select {
			case <-ctx.Done():
			case <-time.After(interval):
			}
			drain()
			return exitCode(task)
		}

		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("waiting for controller task %s: %w", taskARN, ctx.Err())
		case <-time.After(interval):
		}
	}
}

func (c *Client) describe(ctx context.Context, taskARN string) (*ecstypes.Task, error) {
	out, err := c.ECS.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String(c.Infra.Cluster.ARN),
		Tasks:   []string{taskARN},
	})
	if err != nil {
		return nil, fmt.Errorf("describe controller task: %w", err)
	}
	if len(out.Tasks) == 0 {
		return nil, fmt.Errorf("controller task %s not found", taskARN)
	}
	return &out.Tasks[0], nil
}

// exitCode returns the exit code of the controller container in a stopped task.
func exitCode(task *ecstypes.Task) (int, error) {
	for _, ct := range task.Containers {
		if aws.ToString(ct.Name) != ContainerName {
			continue
		}
		if ct.ExitCode == nil {
			return 0, fmt.Errorf("controller container stopped without an exit code: %s", stopReason(task, &ct))
		}
		return int(*ct.ExitCode), nil
	}
	return 0, fmt.Errorf("controller container not found in task: %s", stopReason(task, nil))
}

func stopReason(task *ecstypes.Task, ct *ecstypes.Container) string {
	var reasons []string
	if ct != nil && aws.ToString(ct.Reason) != "" {
		reasons = append(reasons, aws.ToString(ct.Reason))
	}
	if r := aws.ToString(task.StoppedReason); r != "" {
		reasons = append(reasons, r)
	}
	if len(reasons) == 0 {
		return "no reason given"
	}
	return strings.Join(reasons, "; ")
}

// logTail prints new events of one log stream on each drain.
type logTail struct {
	client LogsAPI
	group  string
	stream string
	out    io.Writer
	token  *string
}

func (t *logTail) drain(ctx context.Context) error {
	for {
		out, err := t.client.GetLogEvents(ctx, &cwlogs.GetLogEventsInput{
			LogGroupName:  aws.String(t.group),
			LogStreamName: aws.String(t.stream),
			StartFromHead: aws.Bool(true),
			NextToken:     t.token,
		})
		if err != nil {
			// The stream appears once the container starts writing.
			var nf *cwtypes.ResourceNotFoundException
			if errors.As(err, &nf) {
				return nil
			}
			return fmt.Errorf("read controller logs: %w", err)
		}
		for _, e := range out.Events {
			_, _ = fmt.Fprintln(t.out, aws.ToString(e.Message))
		}
		// GetLogEvents returns the same token when there are no more events.
		next := out.NextForwardToken
		if next == nil || aws.ToString(next) == aws.ToString(t.token) {
			t.token = next
			return nil
		}
		t.token = next
	}
}

func (c *Client) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(c.Out, format, args...)
}

func taskID(taskARN string) string {
	return taskARN[strings.LastIndex(taskARN, "/")+1:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
