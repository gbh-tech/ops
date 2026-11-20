// Package drift compares what an environment runs with the image tags a
// manifest names for its units. It only reads: deciding what to deploy, and
// deploying it, stay with the caller.
package drift

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// Status is the state of one unit against its manifest tag.
type Status string

const (
	// InSync: the unit runs the image the manifest names.
	InSync Status = "in-sync"
	// OutOfSync: the unit exists and runs another image, or a rollout is not
	// complete. A deployment at the manifest tag is what brings it in sync.
	OutOfSync Status = "out-of-sync"
	// Missing: the service, function or image repository does not exist in
	// this environment. Deploying cannot fix that; its infrastructure must be
	// provisioned first.
	Missing Status = "missing"
	// Unreadable: the state could not be read, so nothing is known about it.
	Unreadable Status = "unreadable"
)

// Kinds of unit that have a running state to compare.
const (
	KindECS    = "ecs"
	KindLambda = "lambda"
)

// DefaultConcurrency is how many units are read side by side.
const DefaultConcurrency = 8

// describeTasksLimit is the most tasks one DescribeTasks call accepts.
const describeTasksLimit = 100

// Target is one unit to compare, with everything already resolved from the
// repository configuration.
type Target struct {
	ID   string
	Kind string
	// Tag is the image tag the manifest names.
	Tag string
	// Image is the full image reference a deployment at Tag puts in the ECS
	// task definition. Unused for Lambda.
	Image string
	// Repository is the ECR repository that holds the unit's image, and
	// RegistryID the account that owns it ("" for the caller's own). An empty
	// Repository means the image lives outside the registry and is compared by
	// its reference only, with no digest.
	Repository string
	RegistryID string
	// ImageTag is the tag the running digest is resolved from. It is Tag,
	// unless the unit's configuration pins its image to a tag of its own.
	// ImageDigest is set instead when the configuration pins a digest.
	ImageTag    string
	ImageDigest string

	// ECS units.
	Cluster   string
	Service   string
	Container string

	// Lambda units.
	Function string
}

// Result is the state of one unit.
type Result struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Tag    string `json:"tag"`
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// ECSAPI is the subset of the ECS client the comparison uses.
type ECSAPI interface {
	DescribeServices(ctx context.Context, in *awsecs.DescribeServicesInput, opts ...func(*awsecs.Options)) (*awsecs.DescribeServicesOutput, error)
	DescribeTaskDefinition(ctx context.Context, in *awsecs.DescribeTaskDefinitionInput, opts ...func(*awsecs.Options)) (*awsecs.DescribeTaskDefinitionOutput, error)
	ListTasks(ctx context.Context, in *awsecs.ListTasksInput, opts ...func(*awsecs.Options)) (*awsecs.ListTasksOutput, error)
	DescribeTasks(ctx context.Context, in *awsecs.DescribeTasksInput, opts ...func(*awsecs.Options)) (*awsecs.DescribeTasksOutput, error)
}

// ECRAPI is the subset of the ECR client the comparison uses.
type ECRAPI interface {
	DescribeImages(ctx context.Context, in *awsecr.DescribeImagesInput, opts ...func(*awsecr.Options)) (*awsecr.DescribeImagesOutput, error)
	BatchGetImage(ctx context.Context, in *awsecr.BatchGetImageInput, opts ...func(*awsecr.Options)) (*awsecr.BatchGetImageOutput, error)
}

// Media types of an image index, which names one image per platform.
var indexMediaTypes = []string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
}

// LambdaAPI is the subset of the Lambda client the comparison uses.
type LambdaAPI interface {
	GetFunction(ctx context.Context, in *awslambda.GetFunctionInput, opts ...func(*awslambda.Options)) (*awslambda.GetFunctionOutput, error)
}

// Checker reads the state of units from AWS.
type Checker struct {
	ECS    ECSAPI
	ECR    ECRAPI
	Lambda LambdaAPI
	// Concurrency is how many units are read side by side.
	// DefaultConcurrency when zero.
	Concurrency int
}

// verdict is the outcome of one comparison. The zero value is not valid: use
// the constructors below.
type verdict struct {
	status Status
	reason string
}

func inSync() verdict { return verdict{status: InSync} }

func outOfSync(format string, args ...any) verdict {
	return verdict{status: OutOfSync, reason: fmt.Sprintf(format, args...)}
}

func missing(format string, args ...any) verdict {
	return verdict{status: Missing, reason: fmt.Sprintf(format, args...)}
}

func unreadable(err error) verdict {
	return verdict{status: Unreadable, reason: err.Error()}
}

// Check compares every target and returns one result per target, in the order
// given. It never fails as a whole: a unit whose state cannot be read is
// reported as Unreadable, so a caller that deploys what is not in sync never
// skips a unit it could not check.
func (c Checker) Check(ctx context.Context, targets []Target) []Result {
	concurrency := c.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}

	results := make([]Result, len(targets))
	slots := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			v := c.check(ctx, target)
			results[i] = Result{ID: target.ID, Kind: target.Kind, Tag: target.Tag, Status: v.status, Reason: v.reason}
		}()
	}
	wg.Wait()
	return results
}

func (c Checker) check(ctx context.Context, target Target) verdict {
	switch target.Kind {
	case KindECS:
		return c.checkECS(ctx, target)
	case KindLambda:
		return c.checkLambda(ctx, target)
	default:
		return unreadable(fmt.Errorf("unsupported kind %q", target.Kind))
	}
}

// expectedDigests returns the digests the unit may run: the digest of the
// image its configuration pins, or of its image tag. A multi-platform image is
// an index, and a workload runs one of the images the index lists, so those
// count too. The second value reports whether the digests were resolved; when
// it is false the verdict says why.
func (c Checker) expectedDigests(ctx context.Context, target Target) ([]string, bool, verdict) {
	name := "tag " + target.ImageTag
	imageID := ecrtypes.ImageIdentifier{ImageTag: aws.String(target.ImageTag)}
	if target.ImageDigest != "" {
		name = "digest " + target.ImageDigest
		imageID = ecrtypes.ImageIdentifier{ImageDigest: aws.String(target.ImageDigest)}
	}
	var registryID *string
	if target.RegistryID != "" {
		registryID = aws.String(target.RegistryID)
	}
	noImage := outOfSync("%s has no image in %s", name, target.Repository)

	out, err := c.ECR.DescribeImages(ctx, &awsecr.DescribeImagesInput{
		RegistryId:     registryID,
		RepositoryName: aws.String(target.Repository),
		ImageIds:       []ecrtypes.ImageIdentifier{imageID},
	})
	if err != nil {
		var noRepository *ecrtypes.RepositoryNotFoundException
		if errors.As(err, &noRepository) {
			return nil, false, missing("image repository %s does not exist", target.Repository)
		}
		var notFound *ecrtypes.ImageNotFoundException
		if errors.As(err, &notFound) {
			return nil, false, noImage
		}
		return nil, false, unreadable(err)
	}
	if len(out.ImageDetails) == 0 || aws.ToString(out.ImageDetails[0].ImageDigest) == "" {
		return nil, false, noImage
	}
	digest := aws.ToString(out.ImageDetails[0].ImageDigest)
	if !slices.Contains(indexMediaTypes, aws.ToString(out.ImageDetails[0].ImageManifestMediaType)) {
		return []string{digest}, true, verdict{}
	}

	// By the digest just resolved, not by the tag again: a tag that moved in
	// between would mix the images of two versions.
	index, err := c.ECR.BatchGetImage(ctx, &awsecr.BatchGetImageInput{
		RegistryId:         registryID,
		RepositoryName:     aws.String(target.Repository),
		ImageIds:           []ecrtypes.ImageIdentifier{{ImageDigest: aws.String(digest)}},
		AcceptedMediaTypes: indexMediaTypes,
	})
	if err != nil {
		return nil, false, unreadable(err)
	}
	if len(index.Images) == 0 {
		return nil, false, noImage
	}
	var listed struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal([]byte(aws.ToString(index.Images[0].ImageManifest)), &listed); err != nil {
		return nil, false, unreadable(fmt.Errorf("parsing the image index %s of %s: %w", digest, target.Repository, err))
	}
	digests := []string{digest}
	for _, image := range listed.Manifests {
		digests = append(digests, image.Digest)
	}
	return digests, true, verdict{}
}

func (c Checker) checkECS(ctx context.Context, target Target) verdict {
	services, err := c.ECS.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster:  aws.String(target.Cluster),
		Services: []string{target.Service},
	})
	if err != nil {
		var noCluster *ecstypes.ClusterNotFoundException
		if errors.As(err, &noCluster) {
			return missing("cluster %s does not exist", target.Cluster)
		}
		return unreadable(err)
	}
	// A deleted service stays listed as INACTIVE for a while.
	if len(services.Services) == 0 || aws.ToString(services.Services[0].Status) != "ACTIVE" {
		return missing("service %s does not exist in cluster %s", target.Service, target.Cluster)
	}
	service := services.Services[0]

	// The task definition must name the image a deployment at this tag would:
	// a tag is a deployment request even when it resolves to the digest that
	// is already running, because its rollout applies the unit's configuration.
	definition, err := c.ECS.DescribeTaskDefinition(ctx, &awsecs.DescribeTaskDefinitionInput{
		TaskDefinition: service.TaskDefinition,
	})
	if err != nil {
		return unreadable(err)
	}
	deployed := ""
	for _, container := range definition.TaskDefinition.ContainerDefinitions {
		if aws.ToString(container.Name) == target.Container {
			deployed = aws.ToString(container.Image)
		}
	}
	if deployed != target.Image {
		return outOfSync("task definition has image %q, expected %q", deployed, target.Image)
	}
	// A service scaled to zero, by its configuration or by anything else, has
	// no task to compare, and its task definition is what its next task runs.
	if service.DesiredCount == 0 && service.RunningCount == 0 && service.PendingCount == 0 {
		return inSync()
	}

	if len(service.Deployments) != 1 || service.Deployments[0].RolloutState == ecstypes.DeploymentRolloutStateInProgress || service.Deployments[0].RolloutState == ecstypes.DeploymentRolloutStateFailed {
		return outOfSync("a deployment of service %s is not complete", target.Service)
	}
	if service.RunningCount != service.DesiredCount || service.PendingCount != 0 {
		return outOfSync("service %s is not stable: %d of %d tasks running", target.Service, service.RunningCount, service.DesiredCount)
	}

	var taskARNs []string
	pages := awsecs.NewListTasksPaginator(c.ECS, &awsecs.ListTasksInput{
		Cluster:       aws.String(target.Cluster),
		ServiceName:   aws.String(target.Service),
		DesiredStatus: ecstypes.DesiredStatusRunning,
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return unreadable(err)
		}
		taskARNs = append(taskARNs, page.TaskArns...)
	}
	if len(taskARNs) == 0 {
		return outOfSync("service %s has no running task", target.Service)
	}
	if target.Repository == "" {
		return inSync()
	}

	digests, ok, v := c.expectedDigests(ctx, target)
	if !ok {
		return v
	}
	for start := 0; start < len(taskARNs); start += describeTasksLimit {
		batch := taskARNs[start:min(start+describeTasksLimit, len(taskARNs))]
		tasks, err := c.ECS.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
			Cluster: aws.String(target.Cluster),
			Tasks:   batch,
		})
		if err != nil {
			return unreadable(err)
		}
		// A task that stopped since it was listed comes back as a failure, not
		// as a task, and its digest was not seen.
		if len(tasks.Tasks) != len(batch) {
			return unreadable(fmt.Errorf("%d of %d running tasks of service %s could not be described", len(batch)-len(tasks.Tasks), len(batch), target.Service))
		}
		for _, task := range tasks.Tasks {
			running := ""
			for _, container := range task.Containers {
				if aws.ToString(container.Name) == target.Container {
					running = aws.ToString(container.ImageDigest)
				}
			}
			if !slices.Contains(digests, running) {
				return outOfSync("a task runs digest %q, expected %s", running, digests[0])
			}
		}
	}
	return inSync()
}

func (c Checker) checkLambda(ctx context.Context, target Target) verdict {
	function, err := c.Lambda.GetFunction(ctx, &awslambda.GetFunctionInput{
		FunctionName: aws.String(target.Function),
	})
	if err != nil {
		var noFunction *lambdatypes.ResourceNotFoundException
		if errors.As(err, &noFunction) {
			return missing("function %s does not exist", target.Function)
		}
		return unreadable(err)
	}
	if function.Configuration == nil || function.Configuration.LastUpdateStatus != lambdatypes.LastUpdateStatusSuccessful {
		return outOfSync("the last update of function %s is not complete", target.Function)
	}

	digests, ok, v := c.expectedDigests(ctx, target)
	if !ok {
		return v
	}
	deployed := ""
	if function.Code != nil {
		deployed = aws.ToString(function.Code.ResolvedImageUri)
	}
	_, running, _ := strings.Cut(deployed, "@")
	if !slices.Contains(digests, running) {
		return outOfSync("function runs image %q, expected digest %s", deployed, digests[0])
	}
	return inSync()
}
