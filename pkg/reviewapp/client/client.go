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
	// IdleTimeout, when set, is passed on to the controller as
	// --idle-timeout. A controller image from an older ops release has no
	// such flag, so the project sets it only after updating that image; ops
	// does not check the controller's version.
	IdleTimeout time.Duration
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
	// Command is the ops reviewapp subcommand, e.g. "deploy".
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
	if c.Settings.IdleTimeout > 0 {
		cmd = append(cmd, "--idle-timeout", c.Settings.IdleTimeout.String())
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
	read := func() (bool, int, bool) {
		found, printed, err := logs.read(ctx)
		if err != nil && !warned {
			c.printf("WARNING: %v; the exit code still comes from the task\n", err)
			warned = true
		}
		return found, printed, err != nil
	}

	for {
		task, err := c.describe(ctx, taskARN)
		if err != nil {
			return 0, err
		}
		read()
		if aws.ToString(task.LastStatus) == string(ecstypes.DesiredStatusStopped) {
			c.drain(ctx, interval, read)
			return exitCode(task)
		}

		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("waiting for controller task %s: %w", taskARN, ctx.Err())
		case <-time.After(interval):
		}
	}
}

// finalQuietReads is how many reads in a row must find the stream and no new
// line before the client stops reading a stopped task's logs.
const finalQuietReads = 2

// finalMaxReads bounds the reads after the task stops, for a stream that
// never appears or keeps growing.
const finalMaxReads = 6

// drain reads a stopped task's logs until they settle. Events reach
// CloudWatch shortly after the task stops, and the last lines usually hold
// the failure reason, so one read is not enough: it waits a poll interval
// between reads and stops after finalQuietReads reads with nothing new. A
// stream that does not exist yet is not quiet. A failing read is, since
// waiting does not fix it, unless it printed lines before it failed.
func (c *Client) drain(ctx context.Context, interval time.Duration, read func() (bool, int, bool)) {
	quiet := 0
	for i := 0; i < finalMaxReads && quiet < finalQuietReads; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		found, printed, failed := read()
		if printed == 0 && (failed || found) {
			quiet++
		} else {
			quiet = 0
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

// lateEventWindow is how far before the newest printed event each read
// starts. CloudWatch can make an event readable after events with later
// timestamps, so a reader that only moves forward (a NextForwardToken kept
// between reads) skips it. Re-reading this window and skipping what was
// printed catches such events without reading the whole stream each time.
const lateEventWindow = 5 * time.Minute

// logTail prints the events of one log stream, each exactly once.
type logTail struct {
	client LogsAPI
	group  string
	stream string
	out    io.Writer

	// newest is the timestamp (ms) of the newest event printed.
	newest int64
	// printed counts the events printed per key, so a line logged twice in
	// the same millisecond is printed twice, and a re-read one never.
	printed map[eventKey]int
}

// eventKey identifies a log event: GetLogEvents returns no event id.
type eventKey struct {
	timestamp int64
	ingested  int64
	message   string
}

// read prints the events it has not printed yet. It reads from the head of
// the stream, which only holds this task's events, until it has printed an
// event, then from lateEventWindow before the newest one. found reports
// whether the stream exists: it appears once the container writes its first
// line, so a missing stream means nothing to print yet, not an error.
func (t *logTail) read(ctx context.Context) (found bool, printed int, err error) {
	if t.printed == nil {
		t.printed = map[eventKey]int{}
	}
	in := &cwlogs.GetLogEventsInput{
		LogGroupName:  aws.String(t.group),
		LogStreamName: aws.String(t.stream),
		StartFromHead: aws.Bool(true),
	}
	start := t.newest - lateEventWindow.Milliseconds()
	if start > 0 {
		in.StartTime = aws.Int64(start)
	}

	seen := map[eventKey]int{}
	for {
		out, err := t.client.GetLogEvents(ctx, in)
		if err != nil {
			var nf *cwtypes.ResourceNotFoundException
			if errors.As(err, &nf) {
				return false, printed, nil
			}
			return true, printed, fmt.Errorf("read controller logs: %w", err)
		}
		for _, e := range out.Events {
			k := eventKey{aws.ToInt64(e.Timestamp), aws.ToInt64(e.IngestionTime), aws.ToString(e.Message)}
			seen[k]++
			if seen[k] <= t.printed[k] {
				continue
			}
			t.printed[k] = seen[k]
			_, _ = fmt.Fprintln(t.out, k.message)
			printed++
			t.newest = max(t.newest, k.timestamp)
		}
		// GetLogEvents returns the token it was given at the end of the stream.
		next := out.NextForwardToken
		if next == nil || aws.ToString(next) == aws.ToString(in.NextToken) {
			break
		}
		in.NextToken = next
	}

	// Events before the next read's window are never returned again.
	if next := t.newest - lateEventWindow.Milliseconds(); next > 0 {
		for k := range t.printed {
			if k.timestamp < next {
				delete(t.printed, k)
			}
		}
	}
	return true, printed, nil
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
