package client

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ops/pkg/reviewapp/infra"
	"ops/pkg/reviewapp/state"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

const testTaskARN = "arn:aws:ecs:us-east-1:1:task/cluster/abc123"

// fakeECS starts one task, reports it RUNNING for `runningPolls` describes,
// then STOPPED with the given exit code.
type fakeECS struct {
	runIn        *awsecs.RunTaskInput
	runErr       error
	failures     []ecstypes.Failure
	runningPolls int
	exitCode     *int32
	polls        int
}

func (f *fakeECS) RunTask(_ context.Context, in *awsecs.RunTaskInput, _ ...func(*awsecs.Options)) (*awsecs.RunTaskOutput, error) {
	f.runIn = in
	if f.runErr != nil {
		return nil, f.runErr
	}
	if len(f.failures) > 0 {
		return &awsecs.RunTaskOutput{Failures: f.failures}, nil
	}
	return &awsecs.RunTaskOutput{Tasks: []ecstypes.Task{{TaskArn: aws.String(testTaskARN)}}}, nil
}

func (f *fakeECS) DescribeTasks(_ context.Context, _ *awsecs.DescribeTasksInput, _ ...func(*awsecs.Options)) (*awsecs.DescribeTasksOutput, error) {
	f.polls++
	task := ecstypes.Task{TaskArn: aws.String(testTaskARN), LastStatus: aws.String("RUNNING")}
	if f.polls > f.runningPolls {
		task.LastStatus = aws.String("STOPPED")
		task.StoppedReason = aws.String("Essential container in task exited")
		task.Containers = []ecstypes.Container{{Name: aws.String(ContainerName), ExitCode: f.exitCode}}
	}
	return &awsecs.DescribeTasksOutput{Tasks: []ecstypes.Task{task}}, nil
}

// fakeLogs serves batches of events, one batch per call that follows the
// previous token, and reports a missing stream until `missingCalls` is used up.
type fakeLogs struct {
	stream       string
	batches      [][]string
	missingCalls int
	calls        int
}

func (f *fakeLogs) GetLogEvents(_ context.Context, in *cwlogs.GetLogEventsInput, _ ...func(*cwlogs.Options)) (*cwlogs.GetLogEventsOutput, error) {
	f.calls++
	f.stream = aws.ToString(in.LogStreamName)
	if f.missingCalls > 0 {
		f.missingCalls--
		return nil, &cwtypes.ResourceNotFoundException{}
	}
	i := 0
	if in.NextToken != nil {
		i = int(aws.ToString(in.NextToken)[0] - '0')
	}
	if i >= len(f.batches) {
		return &cwlogs.GetLogEventsOutput{NextForwardToken: in.NextToken}, nil
	}
	events := make([]cwtypes.OutputLogEvent, len(f.batches[i]))
	for j, m := range f.batches[i] {
		events[j] = cwtypes.OutputLogEvent{Message: aws.String(m)}
	}
	next := string(rune('0' + i + 1))
	return &cwlogs.GetLogEventsOutput{Events: events, NextForwardToken: aws.String(next)}, nil
}

type fakeManifests struct {
	got *state.Manifest
	err error
}

func (f *fakeManifests) PutManifest(_ context.Context, m *state.Manifest) (state.ManifestRef, error) {
	f.got = m
	return state.ManifestRef{VersionID: "v7", SHA256: "abc"}, f.err
}

func newTestClient(e *fakeECS, l *fakeLogs, m *fakeManifests, out *bytes.Buffer) *Client {
	inf := &infra.Infra{}
	inf.Cluster.ARN = "arn:aws:ecs:us-east-1:1:cluster/c"
	inf.Controller.TaskDefinition = "reviewapp-controller"
	inf.Network.PrivateSubnetIDs = []string{"subnet-1", "subnet-2"}
	return &Client{
		ECS:          e,
		Logs:         l,
		Manifests:    m,
		Infra:        inf,
		Controller:   &infra.Unit{SecurityGroup: "sg-c", LogGroup: "/ecs/c"},
		Settings:     Settings{Domain: "reviewapps.example.com", InfraSSM: "/reviewapp/infra", ToolsImage: "repo/tools", Region: "us-east-1", Repository: "acme/app", Actor: "octocat"},
		Out:          out,
		PollInterval: time.Millisecond,
	}
}

func int32p(v int32) *int32 { return &v }

func TestExecuteUploadsManifestAndPassesItsVersion(t *testing.T) {
	e := &fakeECS{exitCode: int32p(0)}
	m := &fakeManifests{}
	var out bytes.Buffer
	c := newTestClient(e, &fakeLogs{}, m, &out)

	manifest := &state.Manifest{PR: 7, SHA: "abc1234", Images: map[string]string{"shell": "pr-7-abc1234"}}
	res, err := c.Execute(context.Background(), Run{Command: "create", PR: 7, Manifest: manifest, StartedBy: "github-42"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.TaskARN != testTaskARN {
		t.Fatalf("result = %+v", res)
	}
	if m.got != manifest {
		t.Fatal("manifest was not uploaded")
	}

	in := e.runIn
	if aws.ToString(in.Cluster) != "arn:aws:ecs:us-east-1:1:cluster/c" || aws.ToString(in.TaskDefinition) != "reviewapp-controller" {
		t.Fatalf("cluster/task definition = %s/%s", aws.ToString(in.Cluster), aws.ToString(in.TaskDefinition))
	}
	if in.LaunchType != ecstypes.LaunchTypeFargate || aws.ToString(in.StartedBy) != "github-42" {
		t.Fatalf("launch type/started by = %s/%s", in.LaunchType, aws.ToString(in.StartedBy))
	}
	vpc := in.NetworkConfiguration.AwsvpcConfiguration
	if !slices.Equal(vpc.Subnets, []string{"subnet-1", "subnet-2"}) || !slices.Equal(vpc.SecurityGroups, []string{"sg-c"}) {
		t.Fatalf("network = %+v", vpc)
	}
	if vpc.AssignPublicIp != ecstypes.AssignPublicIpDisabled {
		t.Fatal("controller task gets a public IP")
	}

	override := in.Overrides.ContainerOverrides[0]
	if aws.ToString(override.Name) != ContainerName {
		t.Fatalf("override container = %s", aws.ToString(override.Name))
	}
	want := []string{
		"reviewapp", "create", "--pr", "7",
		"--manifest-version", "v7", "--manifest-sha256", "abc",
		"--in-cluster",
		"--domain", "reviewapps.example.com",
		"--infra-ssm", "/reviewapp/infra",
		"--tools-image", "repo/tools",
		"--region", "us-east-1",
		"--repository", "acme/app",
		"--actor", "octocat",
	}
	if !slices.Equal(override.Command, want) {
		t.Fatalf("command = %q\nwant      %q", override.Command, want)
	}
}

func TestExecuteWithoutManifest(t *testing.T) {
	e := &fakeECS{exitCode: int32p(0)}
	m := &fakeManifests{}
	c := newTestClient(e, &fakeLogs{}, m, &bytes.Buffer{})

	if _, err := c.Execute(context.Background(), Run{Command: "run", PR: 7, Args: []string{"--", "cron-alerts", "--dry-run"}}); err != nil {
		t.Fatal(err)
	}
	if m.got != nil {
		t.Fatal("uploaded a manifest for a run without one")
	}
	cmd := e.runIn.Overrides.ContainerOverrides[0].Command
	if !slices.Equal(cmd[:4], []string{"reviewapp", "run", "--pr", "7"}) || !slices.Equal(cmd[len(cmd)-3:], []string{"--", "cron-alerts", "--dry-run"}) {
		t.Fatalf("command = %q", cmd)
	}
	if slices.Contains(cmd, "--manifest-version") {
		t.Fatalf("command passes a manifest version: %q", cmd)
	}
	if aws.ToString(e.runIn.StartedBy) != "ops-reviewapp" {
		t.Fatalf("started by = %s", aws.ToString(e.runIn.StartedBy))
	}
}

func TestExecuteReturnsTheControllerExitCode(t *testing.T) {
	e := &fakeECS{runningPolls: 2, exitCode: int32p(3)}
	c := newTestClient(e, &fakeLogs{}, &fakeManifests{}, &bytes.Buffer{})

	res, err := c.Execute(context.Background(), Run{Command: "destroy", PR: 7})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", res.ExitCode)
	}
	if e.polls != 3 {
		t.Fatalf("polls = %d, want 3", e.polls)
	}
}

func TestExecutePrintsLogsOnceAsTheyArrive(t *testing.T) {
	e := &fakeECS{runningPolls: 2, exitCode: int32p(0)}
	l := &fakeLogs{missingCalls: 1, batches: [][]string{{"creating namespace", "creating database"}, {"ready"}}}
	var out bytes.Buffer
	c := newTestClient(e, l, &fakeManifests{}, &out)

	if _, err := c.Execute(context.Background(), Run{Command: "create", PR: 7}); err != nil {
		t.Fatal(err)
	}
	if l.stream != "controller/ops/abc123" {
		t.Fatalf("log stream = %s", l.stream)
	}
	text := out.String()
	for _, line := range []string{"creating namespace", "creating database", "ready"} {
		if strings.Count(text, line) != 1 {
			t.Fatalf("%q printed %d times in:\n%s", line, strings.Count(text, line), text)
		}
	}
	if strings.Index(text, "creating namespace") > strings.Index(text, "ready") {
		t.Fatalf("logs out of order:\n%s", text)
	}
}

func TestExecuteRefusesAManifestForAnotherPR(t *testing.T) {
	e := &fakeECS{}
	c := newTestClient(e, &fakeLogs{}, &fakeManifests{}, &bytes.Buffer{})

	_, err := c.Execute(context.Background(), Run{Command: "deploy", PR: 7, Manifest: &state.Manifest{PR: 8}})
	if err == nil || !strings.Contains(err.Error(), "PR 8") {
		t.Fatalf("err = %v, want a PR mismatch", err)
	}
	if e.runIn != nil {
		t.Fatal("started a task for a mismatched manifest")
	}
}

func TestExecuteStartFailures(t *testing.T) {
	for name, e := range map[string]*fakeECS{
		"api error": {runErr: errors.New("AccessDenied")},
		"failure":   {failures: []ecstypes.Failure{{Reason: aws.String("RESOURCE:MEMORY")}}},
	} {
		c := newTestClient(e, &fakeLogs{}, &fakeManifests{}, &bytes.Buffer{})
		if _, err := c.Execute(context.Background(), Run{Command: "create", PR: 7}); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
}

func TestExecuteManifestUploadFailureStartsNothing(t *testing.T) {
	e := &fakeECS{}
	c := newTestClient(e, &fakeLogs{}, &fakeManifests{err: errors.New("AccessDenied")}, &bytes.Buffer{})

	if _, err := c.Execute(context.Background(), Run{Command: "create", PR: 7, Manifest: &state.Manifest{PR: 7}}); err == nil {
		t.Fatal("want an error")
	}
	if e.runIn != nil {
		t.Fatal("started a task after the manifest upload failed")
	}
}

func TestExecuteNoExitCode(t *testing.T) {
	e := &fakeECS{exitCode: nil}
	c := newTestClient(e, &fakeLogs{}, &fakeManifests{}, &bytes.Buffer{})

	_, err := c.Execute(context.Background(), Run{Command: "create", PR: 7})
	if err == nil || !strings.Contains(err.Error(), "Essential container in task exited") {
		t.Fatalf("err = %v, want the stop reason", err)
	}
}

func TestExecuteTimeout(t *testing.T) {
	e := &fakeECS{runningPolls: 1 << 30}
	c := newTestClient(e, &fakeLogs{}, &fakeManifests{}, &bytes.Buffer{})
	c.Timeout = 20 * time.Millisecond

	_, err := c.Execute(context.Background(), Run{Command: "create", PR: 7})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline", err)
	}
}
