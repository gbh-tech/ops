package drift

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

const (
	testImage  = "registry.example.com/platform/api:1.0.0-abc1234"
	testDigest = "sha256:new"
)

// fakeECS serves one service that, by default, runs testImage at testDigest
// with one stable task. Tests change the fields they care about.
type fakeECS struct {
	services     []ecstypes.Service
	servicesErr  error
	image        string
	definitionIn *awsecs.DescribeTaskDefinitionInput
	taskARNs     []string
	digests      map[string]string
	described    [][]string
	describeErr  error
	stopped      string
}

func stableService() ecstypes.Service {
	return ecstypes.Service{
		Status:         aws.String("ACTIVE"),
		TaskDefinition: aws.String("api-stage:7"),
		DesiredCount:   1,
		RunningCount:   1,
		Deployments:    []ecstypes.Deployment{{RolloutState: ecstypes.DeploymentRolloutStateCompleted}},
	}
}

func newFakeECS() *fakeECS {
	return &fakeECS{
		services: []ecstypes.Service{stableService()},
		image:    testImage,
		taskARNs: []string{"task-1"},
		digests:  map[string]string{"task-1": testDigest},
	}
}

func (f *fakeECS) DescribeServices(context.Context, *awsecs.DescribeServicesInput, ...func(*awsecs.Options)) (*awsecs.DescribeServicesOutput, error) {
	if f.servicesErr != nil {
		return nil, f.servicesErr
	}
	return &awsecs.DescribeServicesOutput{Services: f.services}, nil
}

func (f *fakeECS) DescribeTaskDefinition(_ context.Context, in *awsecs.DescribeTaskDefinitionInput, _ ...func(*awsecs.Options)) (*awsecs.DescribeTaskDefinitionOutput, error) {
	f.definitionIn = in
	return &awsecs.DescribeTaskDefinitionOutput{TaskDefinition: &ecstypes.TaskDefinition{
		ContainerDefinitions: []ecstypes.ContainerDefinition{
			{Name: aws.String("sidecar"), Image: aws.String("registry.example.com/sidecar:1")},
			{Name: aws.String("api"), Image: aws.String(f.image)},
		},
	}}, nil
}

// ListTasks pages two task ARNs at a time.
func (f *fakeECS) ListTasks(_ context.Context, in *awsecs.ListTasksInput, _ ...func(*awsecs.Options)) (*awsecs.ListTasksOutput, error) {
	start := 0
	if in.NextToken != nil {
		if _, err := fmt.Sscanf(*in.NextToken, "%d", &start); err != nil {
			return nil, err
		}
	}
	end := min(start+2, len(f.taskARNs))
	out := &awsecs.ListTasksOutput{TaskArns: f.taskARNs[start:end]}
	if end < len(f.taskARNs) {
		out.NextToken = aws.String(fmt.Sprint(end))
	}
	return out, nil
}

func (f *fakeECS) DescribeTasks(_ context.Context, in *awsecs.DescribeTasksInput, _ ...func(*awsecs.Options)) (*awsecs.DescribeTasksOutput, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	f.described = append(f.described, in.Tasks)
	out := &awsecs.DescribeTasksOutput{}
	for _, arn := range in.Tasks {
		// A task that stopped since it was listed is a failure, not a task.
		if arn == f.stopped {
			out.Failures = append(out.Failures, ecstypes.Failure{Arn: aws.String(arn), Reason: aws.String("MISSING")})
			continue
		}
		out.Tasks = append(out.Tasks, ecstypes.Task{Containers: []ecstypes.Container{
			{Name: aws.String("sidecar"), ImageDigest: aws.String("sha256:sidecar")},
			{Name: aws.String("api"), ImageDigest: aws.String(f.digests[arn])},
		}})
	}
	return out, nil
}

// fakeECR resolves every tag to testDigest unless it is told to fail.
type fakeECR struct {
	in     *awsecr.DescribeImagesInput
	err    error
	digest *string
}

func (f *fakeECR) DescribeImages(_ context.Context, in *awsecr.DescribeImagesInput, _ ...func(*awsecr.Options)) (*awsecr.DescribeImagesOutput, error) {
	f.in = in
	if f.err != nil {
		return nil, f.err
	}
	digest := aws.String(testDigest)
	if f.digest != nil {
		digest = f.digest
	}
	return &awsecr.DescribeImagesOutput{ImageDetails: []ecrtypes.ImageDetail{{ImageDigest: digest}}}, nil
}

type fakeLambda struct {
	out *awslambda.GetFunctionOutput
	err error
}

func activeFunction(imageURI string) *awslambda.GetFunctionOutput {
	return &awslambda.GetFunctionOutput{
		Configuration: &lambdatypes.FunctionConfiguration{LastUpdateStatus: lambdatypes.LastUpdateStatusSuccessful},
		Code:          &lambdatypes.FunctionCodeLocation{ResolvedImageUri: aws.String(imageURI)},
	}
}

func (f *fakeLambda) GetFunction(context.Context, *awslambda.GetFunctionInput, ...func(*awslambda.Options)) (*awslambda.GetFunctionOutput, error) {
	return f.out, f.err
}

func ecsTarget() Target {
	return Target{
		ID: "api", Kind: KindECS, Tag: "1.0.0-abc1234", Image: testImage,
		Repository: "platform/api", ImageTag: "1.0.0-abc1234",
		Cluster: "platform-stage", Service: "api", Container: "api", Replicas: 1,
	}
}

func lambdaTarget() Target {
	return Target{
		ID: "worker", Kind: KindLambda, Tag: "1.0.0-abc1234",
		Repository: "platform/worker", ImageTag: "1.0.0-abc1234", Function: "platform-worker-stage",
	}
}

func checkOne(t *testing.T, c Checker, target Target) Result {
	t.Helper()
	results := c.Check(context.Background(), []Target{target})
	if len(results) != 1 {
		t.Fatalf("Check returned %d results, want 1", len(results))
	}
	return results[0]
}

func wantStatus(t *testing.T, got Result, status Status, reason string) {
	t.Helper()
	if got.Status != status {
		t.Fatalf("status = %q (%s), want %q", got.Status, got.Reason, status)
	}
	if !strings.Contains(got.Reason, reason) {
		t.Fatalf("reason = %q, want it to contain %q", got.Reason, reason)
	}
}

func TestECSInSync(t *testing.T) {
	ecs, ecr := newFakeECS(), &fakeECR{}
	got := checkOne(t, Checker{ECS: ecs, ECR: ecr}, ecsTarget())

	wantStatus(t, got, InSync, "")
	if got.Reason != "" {
		t.Fatalf("reason = %q, want none", got.Reason)
	}
	if got.ID != "api" || got.Kind != KindECS || got.Tag != "1.0.0-abc1234" {
		t.Fatalf("result = %+v, want the target's id, kind and tag", got)
	}
	if def := aws.ToString(ecs.definitionIn.TaskDefinition); def != "api-stage:7" {
		t.Fatalf("task definition read = %q, want the service's", def)
	}
	if repo, tag := aws.ToString(ecr.in.RepositoryName), aws.ToString(ecr.in.ImageIds[0].ImageTag); repo != "platform/api" || tag != "1.0.0-abc1234" {
		t.Fatalf("image looked up = %s:%s, want platform/api:1.0.0-abc1234", repo, tag)
	}
	if ecr.in.RegistryId != nil {
		t.Fatalf("registry id = %q, want none for the caller's own registry", *ecr.in.RegistryId)
	}
}

func TestECSOutOfSync(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fakeECS, *fakeECR)
		reason string
	}{
		{
			name:   "task definition names another tag",
			change: func(e *fakeECS, _ *fakeECR) { e.image = "registry.example.com/platform/api:0.9.0-0000000" },
			reason: `task definition has image "registry.example.com/platform/api:0.9.0-0000000"`,
		},
		{
			name:   "task runs another digest",
			change: func(e *fakeECS, _ *fakeECR) { e.digests["task-1"] = "sha256:old" },
			reason: `a task runs digest "sha256:old"`,
		},
		{
			name: "rollout in progress",
			change: func(e *fakeECS, _ *fakeECR) {
				e.services[0].Deployments = append(e.services[0].Deployments, ecstypes.Deployment{})
			},
			reason: "a deployment of service api is not complete",
		},
		{
			name: "rollout failed",
			change: func(e *fakeECS, _ *fakeECR) {
				e.services[0].Deployments[0].RolloutState = ecstypes.DeploymentRolloutStateFailed
			},
			reason: "a deployment of service api is not complete",
		},
		{
			name:   "fewer tasks running than desired",
			change: func(e *fakeECS, _ *fakeECR) { e.services[0].RunningCount = 0 },
			reason: "service api is not stable: 0 of 1 tasks running",
		},
		{
			name:   "task pending",
			change: func(e *fakeECS, _ *fakeECR) { e.services[0].PendingCount = 1 },
			reason: "service api is not stable",
		},
		{
			name:   "no running task",
			change: func(e *fakeECS, _ *fakeECR) { e.taskARNs = nil },
			reason: "service api has no running task",
		},
		{
			name:   "tag is not in the registry",
			change: func(_ *fakeECS, r *fakeECR) { r.err = &ecrtypes.ImageNotFoundException{} },
			reason: "tag 1.0.0-abc1234 has no image in platform/api",
		},
		{
			name:   "registry returns no digest",
			change: func(_ *fakeECS, r *fakeECR) { r.digest = aws.String("") },
			reason: "tag 1.0.0-abc1234 has no image in platform/api",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ecs, ecr := newFakeECS(), &fakeECR{}
			tt.change(ecs, ecr)
			wantStatus(t, checkOne(t, Checker{ECS: ecs, ECR: ecr}, ecsTarget()), OutOfSync, tt.reason)
		})
	}
}

func TestECSRolloutStateOfOtherDeploymentControllersIsNotRequired(t *testing.T) {
	ecs := newFakeECS()
	ecs.services[0].Deployments[0].RolloutState = ""

	wantStatus(t, checkOne(t, Checker{ECS: ecs, ECR: &fakeECR{}}, ecsTarget()), InSync, "")
}

func TestECSMissing(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fakeECS, *fakeECR)
		reason string
	}{
		{
			name:   "service not found",
			change: func(e *fakeECS, _ *fakeECR) { e.services = nil },
			reason: "service api does not exist in cluster platform-stage",
		},
		{
			name:   "service deleted",
			change: func(e *fakeECS, _ *fakeECR) { e.services[0].Status = aws.String("INACTIVE") },
			reason: "service api does not exist in cluster platform-stage",
		},
		{
			name:   "cluster not found",
			change: func(e *fakeECS, _ *fakeECR) { e.servicesErr = &ecstypes.ClusterNotFoundException{} },
			reason: "cluster platform-stage does not exist",
		},
		{
			name:   "image repository not found",
			change: func(_ *fakeECS, r *fakeECR) { r.err = &ecrtypes.RepositoryNotFoundException{} },
			reason: "image repository platform/api does not exist",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ecs, ecr := newFakeECS(), &fakeECR{}
			tt.change(ecs, ecr)
			wantStatus(t, checkOne(t, Checker{ECS: ecs, ECR: ecr}, ecsTarget()), Missing, tt.reason)
		})
	}
}

func TestECSUnreadableKeepsTheFirstLineOfTheError(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fakeECS, *fakeECR)
	}{
		{name: "services", change: func(e *fakeECS, _ *fakeECR) { e.servicesErr = errors.New("AccessDenied: not allowed\nrequest id 1") }},
		{name: "tasks", change: func(e *fakeECS, _ *fakeECR) { e.describeErr = errors.New("AccessDenied: not allowed\nrequest id 1") }},
		{name: "registry", change: func(_ *fakeECS, r *fakeECR) { r.err = errors.New("AccessDenied: not allowed\nrequest id 1") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ecs, ecr := newFakeECS(), &fakeECR{}
			tt.change(ecs, ecr)
			got := checkOne(t, Checker{ECS: ecs, ECR: ecr}, ecsTarget())
			wantStatus(t, got, Unreadable, "AccessDenied: not allowed")
			if strings.Contains(got.Reason, "request id") {
				t.Fatalf("reason = %q, want only the first line", got.Reason)
			}
		})
	}
}

func TestECSAtZeroReplicasIsComparedByItsTaskDefinitionOnly(t *testing.T) {
	ecs := newFakeECS()
	ecs.services[0].DesiredCount, ecs.services[0].RunningCount = 0, 0
	ecs.taskARNs = nil
	target := ecsTarget()
	target.Replicas = 0

	// No registry: a unit at zero replicas has no running digest to compare.
	wantStatus(t, checkOne(t, Checker{ECS: ecs}, target), InSync, "")

	ecs.image = "registry.example.com/platform/api:0.9.0-0000000"
	wantStatus(t, checkOne(t, Checker{ECS: ecs}, target), OutOfSync, "task definition has image")
}

func TestECSImageOutsideTheRegistryIsComparedByReferenceOnly(t *testing.T) {
	ecs := newFakeECS()
	ecs.image = "docker.io/library/nginx:1.27"
	target := ecsTarget()
	target.Image, target.Repository = "docker.io/library/nginx:1.27", ""

	// No registry: the digest of an outside image is not looked up.
	wantStatus(t, checkOne(t, Checker{ECS: ecs}, target), InSync, "")
}

func TestECSTaskThatStoppedWhileItWasReadIsUnreadable(t *testing.T) {
	ecs := newFakeECS()
	ecs.taskARNs = []string{"task-1", "task-2"}
	ecs.digests["task-2"] = testDigest
	ecs.services[0].DesiredCount, ecs.services[0].RunningCount = 2, 2
	ecs.stopped = "task-2"

	// The task that was described matches, but the other was never seen.
	wantStatus(t, checkOne(t, Checker{ECS: ecs, ECR: &fakeECR{}}, ecsTarget()), Unreadable,
		"1 of 2 running tasks of service api could not be described")
}

func TestECSImagePinnedToItsOwnTagIsResolvedFromThatTag(t *testing.T) {
	ecs, ecr := newFakeECS(), &fakeECR{}
	ecs.image = "registry.example.com/platform/api:stable"
	target := ecsTarget()
	target.Image, target.ImageTag = "registry.example.com/platform/api:stable", "stable"

	wantStatus(t, checkOne(t, Checker{ECS: ecs, ECR: ecr}, target), InSync, "")
	if tag := aws.ToString(ecr.in.ImageIds[0].ImageTag); tag != "stable" {
		t.Fatalf("tag looked up = %q, want the tag the image is pinned to", tag)
	}
}

func TestECSImagePinnedToADigestNeedsNoLookup(t *testing.T) {
	ecs := newFakeECS()
	ecs.image = "registry.example.com/platform/api@" + testDigest
	target := ecsTarget()
	target.Image, target.ImageTag, target.ImageDigest = ecs.image, "", testDigest

	// No registry: the expected digest is the pinned one.
	wantStatus(t, checkOne(t, Checker{ECS: ecs}, target), InSync, "")

	ecs.digests["task-1"] = "sha256:old"
	wantStatus(t, checkOne(t, Checker{ECS: ecs}, target), OutOfSync, `a task runs digest "sha256:old", expected sha256:new`)
}

func TestECSReadsEveryPageOfTasksAndChecksEachOne(t *testing.T) {
	ecs := newFakeECS()
	ecs.taskARNs = []string{"task-1", "task-2", "task-3"}
	ecs.digests = map[string]string{"task-1": testDigest, "task-2": testDigest, "task-3": "sha256:old"}
	ecs.services[0].DesiredCount, ecs.services[0].RunningCount = 3, 3

	wantStatus(t, checkOne(t, Checker{ECS: ecs, ECR: &fakeECR{}}, ecsTarget()), OutOfSync, `a task runs digest "sha256:old"`)
	if len(ecs.described) != 1 || len(ecs.described[0]) != 3 {
		t.Fatalf("tasks described = %v, want the three tasks of both pages", ecs.described)
	}
}

func TestRegistryInAnotherAccountIsNamed(t *testing.T) {
	ecr := &fakeECR{}
	target := ecsTarget()
	target.RegistryID = "111122223333"
	checkOne(t, Checker{ECS: newFakeECS(), ECR: ecr}, target)

	if got := aws.ToString(ecr.in.RegistryId); got != "111122223333" {
		t.Fatalf("registry id = %q, want 111122223333", got)
	}
}

func TestLambda(t *testing.T) {
	tests := []struct {
		name   string
		lambda *fakeLambda
		ecr    *fakeECR
		status Status
		reason string
	}{
		{
			name:   "in sync",
			lambda: &fakeLambda{out: activeFunction("registry.example.com/platform/worker@" + testDigest)},
			status: InSync,
		},
		{
			name: "in sync while idle",
			lambda: &fakeLambda{out: &awslambda.GetFunctionOutput{
				Configuration: &lambdatypes.FunctionConfiguration{State: lambdatypes.StateInactive, LastUpdateStatus: lambdatypes.LastUpdateStatusSuccessful},
				Code:          &lambdatypes.FunctionCodeLocation{ResolvedImageUri: aws.String("registry.example.com/platform/worker@" + testDigest)},
			}},
			status: InSync,
		},
		{
			name:   "runs another digest",
			lambda: &fakeLambda{out: activeFunction("registry.example.com/platform/worker@sha256:old")},
			status: OutOfSync,
			reason: `function runs image "registry.example.com/platform/worker@sha256:old"`,
		},
		{
			name: "update in progress",
			lambda: &fakeLambda{out: &awslambda.GetFunctionOutput{
				Configuration: &lambdatypes.FunctionConfiguration{LastUpdateStatus: lambdatypes.LastUpdateStatusInProgress},
			}},
			status: OutOfSync,
			reason: "the last update of function platform-worker-stage is not complete",
		},
		{
			name:   "tag is not in the registry",
			lambda: &fakeLambda{out: activeFunction("registry.example.com/platform/worker@" + testDigest)},
			ecr:    &fakeECR{err: &ecrtypes.ImageNotFoundException{}},
			status: OutOfSync,
			reason: "tag 1.0.0-abc1234 has no image in platform/worker",
		},
		{
			name:   "function not found",
			lambda: &fakeLambda{err: &lambdatypes.ResourceNotFoundException{}},
			status: Missing,
			reason: "function platform-worker-stage does not exist",
		},
		{
			name:   "cannot be read",
			lambda: &fakeLambda{err: errors.New("ThrottlingException: Rate exceeded")},
			status: Unreadable,
			reason: "ThrottlingException: Rate exceeded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ecr := tt.ecr
			if ecr == nil {
				ecr = &fakeECR{}
			}
			wantStatus(t, checkOne(t, Checker{Lambda: tt.lambda, ECR: ecr}, lambdaTarget()), tt.status, tt.reason)
		})
	}
}

func TestUnsupportedKindIsUnreadable(t *testing.T) {
	wantStatus(t, checkOne(t, Checker{}, Target{ID: "job", Kind: "migrator"}), Unreadable, `unsupported kind "migrator"`)
}

// slowLambda counts how many calls are in flight at once.
type slowLambda struct {
	inFlight atomic.Int32
	most     atomic.Int32
}

func (s *slowLambda) GetFunction(_ context.Context, in *awslambda.GetFunctionInput, _ ...func(*awslambda.Options)) (*awslambda.GetFunctionOutput, error) {
	now := s.inFlight.Add(1)
	for {
		most := s.most.Load()
		if now <= most || s.most.CompareAndSwap(most, now) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	s.inFlight.Add(-1)
	if strings.HasSuffix(aws.ToString(in.FunctionName), "-3") {
		return nil, &lambdatypes.ResourceNotFoundException{}
	}
	return activeFunction("registry.example.com/platform/worker@" + testDigest), nil
}

// lockedECR is safe for the concurrent calls of TestCheckKeepsOrder.
type lockedECR struct {
	mu sync.Mutex
	fakeECR
}

func (l *lockedECR) DescribeImages(ctx context.Context, in *awsecr.DescribeImagesInput, opts ...func(*awsecr.Options)) (*awsecr.DescribeImagesOutput, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeECR.DescribeImages(ctx, in, opts...)
}

func TestCheckKeepsOrderWhileItReadsUnitsSideBySide(t *testing.T) {
	var targets []Target
	for i := range 20 {
		target := lambdaTarget()
		target.ID = fmt.Sprintf("worker-%d", i)
		target.Function = fmt.Sprintf("platform-worker-%d", i)
		targets = append(targets, target)
	}
	lambda := &slowLambda{}

	results := Checker{Lambda: lambda, ECR: &lockedECR{}, Concurrency: 4}.Check(context.Background(), targets)

	for i, result := range results {
		want := InSync
		if i == 3 {
			want = Missing
		}
		if result.ID != targets[i].ID || result.Status != want {
			t.Fatalf("result %d = %s %s, want %s %s", i, result.ID, result.Status, targets[i].ID, want)
		}
	}
	if most := lambda.most.Load(); most < 2 || most > 4 {
		t.Fatalf("most units read at once = %d, want between 2 and 4", most)
	}
}

func TestCheckWithNoTargets(t *testing.T) {
	if results := (Checker{}).Check(context.Background(), nil); len(results) != 0 {
		t.Fatalf("results = %v, want none", results)
	}
}
