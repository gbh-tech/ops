package client

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
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
	res, err := c.Execute(context.Background(), Run{Command: "deploy", PR: 7, Manifest: manifest, StartedBy: "github-42"})
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
		"reviewapp", "deploy", "--pr", "7",
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

	if _, err := c.Execute(context.Background(), Run{Command: "deploy", PR: 7}); err != nil {
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
		if _, err := c.Execute(context.Background(), Run{Command: "deploy", PR: 7}); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
}

func TestExecuteManifestUploadFailureStartsNothing(t *testing.T) {
	e := &fakeECS{}
	c := newTestClient(e, &fakeLogs{}, &fakeManifests{err: errors.New("AccessDenied")}, &bytes.Buffer{})

	if _, err := c.Execute(context.Background(), Run{Command: "deploy", PR: 7, Manifest: &state.Manifest{PR: 7}}); err == nil {
		t.Fatal("want an error")
	}
	if e.runIn != nil {
		t.Fatal("started a task after the manifest upload failed")
	}
}

func TestExecuteNoExitCode(t *testing.T) {
	e := &fakeECS{exitCode: nil}
	c := newTestClient(e, &fakeLogs{}, &fakeManifests{}, &bytes.Buffer{})

	_, err := c.Execute(context.Background(), Run{Command: "deploy", PR: 7})
	if err == nil || !strings.Contains(err.Error(), "Essential container in task exited") {
		t.Fatalf("err = %v, want the stop reason", err)
	}
}

func TestExecuteTimeout(t *testing.T) {
	e := &fakeECS{runningPolls: 1 << 30}
	c := newTestClient(e, &fakeLogs{}, &fakeManifests{}, &bytes.Buffer{})
	c.Timeout = 20 * time.Millisecond

	_, err := c.Execute(context.Background(), Run{Command: "deploy", PR: 7})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline", err)
	}
}

// failingLogs fails every read, as with AccessDenied or throttling.
type failingLogs struct{ calls int }

func (f *failingLogs) GetLogEvents(context.Context, *cwlogs.GetLogEventsInput, ...func(*cwlogs.Options)) (*cwlogs.GetLogEventsOutput, error) {
	f.calls++
	return nil, errors.New("AccessDeniedException")
}

func TestExecuteKeepsTheExitCodeWhenLogsFail(t *testing.T) {
	e := &fakeECS{runningPolls: 2, exitCode: int32p(4)}
	l := &failingLogs{}
	var out bytes.Buffer
	c := newTestClient(e, nil, &fakeManifests{}, &out)
	c.Logs = l

	res, err := c.Execute(context.Background(), Run{Command: "destroy", PR: 7})
	if err != nil {
		t.Fatalf("a log failure ended the run: %v", err)
	}
	if res.ExitCode != 4 {
		t.Fatalf("exit code = %d, want 4", res.ExitCode)
	}
	if strings.Count(out.String(), "WARNING") != 1 {
		t.Fatalf("want one warning, got:\n%s", out.String())
	}
	if l.calls < 4 {
		t.Fatalf("logs read %d times; polling stopped early", l.calls)
	}
}

// lateLogs returns its last line only on the second read after the task
// stopped, so only the client's final read can print it.
type lateLogs struct {
	ecs           *fakeECS
	postStopReads int
}

func (l *lateLogs) GetLogEvents(_ context.Context, in *cwlogs.GetLogEventsInput, _ ...func(*cwlogs.Options)) (*cwlogs.GetLogEventsOutput, error) {
	tok := in.NextToken
	if tok == nil {
		tok = aws.String("1")
	}
	if l.ecs.polls > l.ecs.runningPolls {
		l.postStopReads++
		if l.postStopReads == 2 {
			return &cwlogs.GetLogEventsOutput{
				Events:           []cwtypes.OutputLogEvent{{Message: aws.String("ERROR: bootstrap failed")}},
				NextForwardToken: aws.String("2"),
			}, nil
		}
	}
	return &cwlogs.GetLogEventsOutput{NextForwardToken: tok}, nil
}

func TestExecuteReadsLogsOnceMoreAfterTheTaskStops(t *testing.T) {
	e := &fakeECS{runningPolls: 1, exitCode: int32p(1)}
	var out bytes.Buffer
	c := newTestClient(e, nil, &fakeManifests{}, &out)
	c.Logs = &lateLogs{ecs: e}

	if _, err := c.Execute(context.Background(), Run{Command: "deploy", PR: 7}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ERROR: bootstrap failed") {
		t.Fatalf("the last line was lost:\n%s", out.String())
	}
}

// cwEvent is one event of a cwStream. It is readable once visible returns
// true, which lets a test make an event readable after later ones.
type cwEvent struct {
	ts      int64
	msg     string
	visible func() bool
}

// cwStream behaves like a CloudWatch log stream: it does not exist until
// exists returns true, returns readable events in timestamp order, pageSize at
// a time, and its NextForwardToken is a position in time, so an event that
// becomes readable behind it is never returned for that token.
type cwStream struct {
	ecs      *fakeECS
	exists   func() bool
	pageSize int
	events   []cwEvent
	// stoppedCalls counts the reads made after the task stopped.
	stoppedCalls int
}

func (s *cwStream) GetLogEvents(_ context.Context, in *cwlogs.GetLogEventsInput, _ ...func(*cwlogs.Options)) (*cwlogs.GetLogEventsOutput, error) {
	if s.ecs.polls > s.ecs.runningPolls {
		s.stoppedCalls++
	}
	if s.exists != nil && !s.exists() {
		return nil, &cwtypes.ResourceNotFoundException{}
	}
	var after int64 = -1
	if in.NextToken != nil {
		after, _ = strconv.ParseInt(aws.ToString(in.NextToken), 10, 64)
	} else if in.StartTime != nil {
		after = aws.ToInt64(in.StartTime) - 1
	}
	var page []cwtypes.OutputLogEvent
	for _, e := range s.events {
		if e.ts <= after || (e.visible != nil && !e.visible()) {
			continue
		}
		if len(page) == s.pageSize {
			break
		}
		page = append(page, cwtypes.OutputLogEvent{Timestamp: aws.Int64(e.ts), IngestionTime: aws.Int64(e.ts + 1), Message: aws.String(e.msg)})
	}
	if len(page) == 0 {
		return &cwlogs.GetLogEventsOutput{NextForwardToken: aws.String(strconv.FormatInt(after, 10))}, nil
	}
	last := aws.ToInt64(page[len(page)-1].Timestamp)
	return &cwlogs.GetLogEventsOutput{Events: page, NextForwardToken: aws.String(strconv.FormatInt(last, 10))}, nil
}

func assertPrintedOnce(t *testing.T, out string, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if n := strings.Count(out, line); n != 1 {
			t.Fatalf("%q printed %d times in:\n%s", line, n, out)
		}
	}
}

func TestExecutePrintsTheFirstLinesOfAStreamCreatedLate(t *testing.T) {
	e := &fakeECS{runningPolls: 4, exitCode: int32p(0)}
	// The stream appears on the third poll with two lines readable; the
	// first line becomes readable a poll later, behind them.
	l := &cwStream{ecs: e, exists: func() bool { return e.polls >= 3 }, pageSize: 1, events: []cwEvent{
		{ts: 1000, msg: "Creating pr-7", visible: func() bool { return e.polls >= 4 }},
		{ts: 2000, msg: "creating namespace"},
		{ts: 3000, msg: "creating database"},
	}}
	var out bytes.Buffer
	c := newTestClient(e, nil, &fakeManifests{}, &out)
	c.Logs = l

	if _, err := c.Execute(context.Background(), Run{Command: "deploy", PR: 7}); err != nil {
		t.Fatal(err)
	}
	assertPrintedOnce(t, out.String(), "Creating pr-7", "creating namespace", "creating database")
}

func TestExecutePrintsTheErrorWrittenJustBeforeTheTaskStops(t *testing.T) {
	e := &fakeECS{runningPolls: 2, exitCode: int32p(1)}
	// The error line becomes readable only on the third read after the task
	// stopped: the read of the poll that saw it stop takes two calls.
	l := &cwStream{ecs: e, pageSize: 10}
	l.events = []cwEvent{
		{ts: 1000, msg: "Updating pr-7 (status creating)"},
		{ts: 2000, msg: "Error: pr-7 is creating; only a ready review app can be put to sleep", visible: func() bool { return l.stoppedCalls >= 3 }},
	}
	var out bytes.Buffer
	c := newTestClient(e, nil, &fakeManifests{}, &out)
	c.Logs = l

	res, err := c.Execute(context.Background(), Run{Command: "sleep", PR: 7})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", res.ExitCode)
	}
	assertPrintedOnce(t, out.String(), "Updating pr-7", "only a ready review app can be put to sleep")
}

func TestExecuteWaitsForAStreamThatAppearsAfterTheTaskStops(t *testing.T) {
	e := &fakeECS{runningPolls: 1, exitCode: int32p(1)}
	// The container wrote its only line as it failed; the stream exists only
	// from the third read after the task stopped.
	l := &cwStream{ecs: e, pageSize: 10, events: []cwEvent{{ts: 1000, msg: "Error: manifest has no images"}}}
	l.exists = func() bool { return l.stoppedCalls >= 3 }
	var out bytes.Buffer
	c := newTestClient(e, nil, &fakeManifests{}, &out)
	c.Logs = l

	if _, err := c.Execute(context.Background(), Run{Command: "deploy", PR: 7}); err != nil {
		t.Fatal(err)
	}
	assertPrintedOnce(t, out.String(), "manifest has no images")
}

// logsFunc adapts a function to LogsAPI.
type logsFunc func(ctx context.Context, in *cwlogs.GetLogEventsInput) (*cwlogs.GetLogEventsOutput, error)

func (f logsFunc) GetLogEvents(ctx context.Context, in *cwlogs.GetLogEventsInput, _ ...func(*cwlogs.Options)) (*cwlogs.GetLogEventsOutput, error) {
	return f(ctx, in)
}

func TestLogTailRereadsOnlyTheLateEventWindow(t *testing.T) {
	window := lateEventWindow.Milliseconds()
	e := &fakeECS{}
	l := &cwStream{ecs: e, pageSize: 10, events: []cwEvent{
		{ts: 1000, msg: "first"},
		{ts: 1000 + 2*window, msg: "much later"},
	}}
	var starts []int64
	var out bytes.Buffer
	tail := &logTail{client: logsFunc(func(ctx context.Context, in *cwlogs.GetLogEventsInput) (*cwlogs.GetLogEventsOutput, error) {
		if in.NextToken == nil {
			starts = append(starts, aws.ToInt64(in.StartTime))
		}
		return l.GetLogEvents(ctx, in)
	}), out: &out}

	for range 2 {
		if _, _, err := tail.read(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(starts, []int64{0, 1000 + window}) {
		t.Fatalf("reads started at %v", starts)
	}
	if len(tail.printed) != 1 {
		t.Fatalf("keeps %d printed events, want only the one in the window", len(tail.printed))
	}
	assertPrintedOnce(t, out.String(), "first", "much later")
}

func TestLogTailPrintsARepeatedLineEachTime(t *testing.T) {
	e := &fakeECS{}
	l := &cwStream{ecs: e, pageSize: 10, events: []cwEvent{{ts: 1000, msg: "."}, {ts: 1000, msg: "."}}}
	var out bytes.Buffer
	tail := &logTail{client: l, out: &out}
	for range 3 {
		if _, _, err := tail.read(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if out.String() != ".\n.\n" {
		t.Fatalf("out = %q, want the line twice", out.String())
	}
}
