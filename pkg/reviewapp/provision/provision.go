// Package provision is the in-cluster mode of ops reviewapp (RFC 0004, D11):
// the work that runs inside the reviewapp-controller task, where RDS, MSK and
// the private ALB targets are reachable. It creates, updates, sleeps, wakes
// and destroys the per-PR resources on top of the shared base Terraform
// builds, and keeps reviewapps/pr-<n>/state.json current after every step so
// a failed run can be resumed or destroyed.
package provision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"ops/pkg/reviewapp"
	"ops/pkg/reviewapp/infra"
	"ops/pkg/reviewapp/state"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	sd "github.com/aws/aws-sdk-go-v2/service/servicediscovery"
)

// ECSAPI is the subset of the ECS client the controller uses.
type ECSAPI interface {
	RegisterTaskDefinition(ctx context.Context, in *awsecs.RegisterTaskDefinitionInput, opts ...func(*awsecs.Options)) (*awsecs.RegisterTaskDefinitionOutput, error)
	DeregisterTaskDefinition(ctx context.Context, in *awsecs.DeregisterTaskDefinitionInput, opts ...func(*awsecs.Options)) (*awsecs.DeregisterTaskDefinitionOutput, error)
	ListTaskDefinitions(ctx context.Context, in *awsecs.ListTaskDefinitionsInput, opts ...func(*awsecs.Options)) (*awsecs.ListTaskDefinitionsOutput, error)
	CreateService(ctx context.Context, in *awsecs.CreateServiceInput, opts ...func(*awsecs.Options)) (*awsecs.CreateServiceOutput, error)
	UpdateService(ctx context.Context, in *awsecs.UpdateServiceInput, opts ...func(*awsecs.Options)) (*awsecs.UpdateServiceOutput, error)
	DeleteService(ctx context.Context, in *awsecs.DeleteServiceInput, opts ...func(*awsecs.Options)) (*awsecs.DeleteServiceOutput, error)
	DescribeServices(ctx context.Context, in *awsecs.DescribeServicesInput, opts ...func(*awsecs.Options)) (*awsecs.DescribeServicesOutput, error)
	RunTask(ctx context.Context, in *awsecs.RunTaskInput, opts ...func(*awsecs.Options)) (*awsecs.RunTaskOutput, error)
	DescribeTasks(ctx context.Context, in *awsecs.DescribeTasksInput, opts ...func(*awsecs.Options)) (*awsecs.DescribeTasksOutput, error)
}

// ELBAPI is the subset of the ELBv2 client the controller uses.
type ELBAPI interface {
	CreateTargetGroup(ctx context.Context, in *elb.CreateTargetGroupInput, opts ...func(*elb.Options)) (*elb.CreateTargetGroupOutput, error)
	DescribeTargetGroups(ctx context.Context, in *elb.DescribeTargetGroupsInput, opts ...func(*elb.Options)) (*elb.DescribeTargetGroupsOutput, error)
	ModifyTargetGroupAttributes(ctx context.Context, in *elb.ModifyTargetGroupAttributesInput, opts ...func(*elb.Options)) (*elb.ModifyTargetGroupAttributesOutput, error)
	DeleteTargetGroup(ctx context.Context, in *elb.DeleteTargetGroupInput, opts ...func(*elb.Options)) (*elb.DeleteTargetGroupOutput, error)
	DescribeRules(ctx context.Context, in *elb.DescribeRulesInput, opts ...func(*elb.Options)) (*elb.DescribeRulesOutput, error)
	CreateRule(ctx context.Context, in *elb.CreateRuleInput, opts ...func(*elb.Options)) (*elb.CreateRuleOutput, error)
	DeleteRule(ctx context.Context, in *elb.DeleteRuleInput, opts ...func(*elb.Options)) (*elb.DeleteRuleOutput, error)
}

// DiscoveryAPI is the subset of the Cloud Map client the controller uses.
type DiscoveryAPI interface {
	ListNamespaces(ctx context.Context, in *sd.ListNamespacesInput, opts ...func(*sd.Options)) (*sd.ListNamespacesOutput, error)
	CreateHttpNamespace(ctx context.Context, in *sd.CreateHttpNamespaceInput, opts ...func(*sd.Options)) (*sd.CreateHttpNamespaceOutput, error)
	GetOperation(ctx context.Context, in *sd.GetOperationInput, opts ...func(*sd.Options)) (*sd.GetOperationOutput, error)
	GetNamespace(ctx context.Context, in *sd.GetNamespaceInput, opts ...func(*sd.Options)) (*sd.GetNamespaceOutput, error)
	DeleteNamespace(ctx context.Context, in *sd.DeleteNamespaceInput, opts ...func(*sd.Options)) (*sd.DeleteNamespaceOutput, error)
}

// SecretsAPI reads secrets.
type SecretsAPI interface {
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// ECRAPI checks that images exist.
type ECRAPI interface {
	DescribeImages(ctx context.Context, in *ecr.DescribeImagesInput, opts ...func(*ecr.Options)) (*ecr.DescribeImagesOutput, error)
}

// DataAPI deletes a review app's prefix in the data bucket.
type DataAPI interface {
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObjects(ctx context.Context, in *s3.DeleteObjectsInput, opts ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

// MetricsAPI reads ALB request counts and publishes the gc heartbeat.
type MetricsAPI interface {
	GetMetricData(ctx context.Context, in *cloudwatch.GetMetricDataInput, opts ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error)
	PutMetricData(ctx context.Context, in *cloudwatch.PutMetricDataInput, opts ...func(*cloudwatch.Options)) (*cloudwatch.PutMetricDataOutput, error)
}

// LogsAPI prints helper task logs and deletes a review app's log streams.
type LogsAPI interface {
	GetLogEvents(ctx context.Context, in *cwlogs.GetLogEventsInput, opts ...func(*cwlogs.Options)) (*cwlogs.GetLogEventsOutput, error)
	DescribeLogStreams(ctx context.Context, in *cwlogs.DescribeLogStreamsInput, opts ...func(*cwlogs.Options)) (*cwlogs.DescribeLogStreamsOutput, error)
	DeleteLogStream(ctx context.Context, in *cwlogs.DeleteLogStreamInput, opts ...func(*cwlogs.Options)) (*cwlogs.DeleteLogStreamOutput, error)
}

// TaggingAPI finds resources by the reviewapp tag, so destroy also removes
// what a failed run created before it could write the state.
type TaggingAPI interface {
	GetResources(ctx context.Context, in *resourcegroupstaggingapi.GetResourcesInput, opts ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error)
}

// StateStore is the part of *state.Store the controller uses.
type StateStore interface {
	Get(ctx context.Context, pr int) (*state.State, string, error)
	Create(ctx context.Context, st *state.State) (string, error)
	Update(ctx context.Context, st *state.State, etag string) (string, error)
	List(ctx context.Context) ([]*state.State, error)
	CheckLimit(ctx context.Context, limit int) error
	GetManifest(ctx context.Context, pr int, ref state.ManifestRef) (*state.Manifest, error)
	Archive(ctx context.Context, pr int) (string, error)
}

// Commenter posts on a pull request.
type Commenter interface {
	Comment(ctx context.Context, pr int, body string) error
}

// Controller does the in-cluster work.
type Controller struct {
	ECS       ECSAPI
	ELB       ELBAPI
	Discovery DiscoveryAPI
	Secrets   SecretsAPI
	ECR       ECRAPI
	Data      DataAPI
	Metrics   MetricsAPI
	Logs      LogsAPI
	Tagging   TaggingAPI
	Store     StateStore
	// Connect opens a database connection; see database.go.
	Connect Connector
	// Comments is optional; without it no PR comments are posted.
	Comments Commenter
	// SSM reads unit facts. Facts are cached per run.
	SSM infra.API

	Infra     *infra.Infra
	Blueprint *infra.Blueprint
	Domain    string
	Region    string
	Limit     int

	// Actor and RunURL are recorded in the state history.
	Actor  string
	RunURL string

	Out io.Writer
	Now func() time.Time

	// PollInterval is how often waits poll AWS; zero means 5 seconds.
	PollInterval time.Duration
	// StableTimeout bounds the wait for services to reach steady state.
	StableTimeout time.Duration

	units map[string]*infra.Unit
}

// ToolsUnit is the unit whose roles and log group the helper tasks use.
const ToolsUnit = "reviewapp-tools"

// ToolsContainer is the container name of the helper task definition.
const ToolsContainer = "tools"

func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *Controller) poll() time.Duration {
	if c.PollInterval > 0 {
		return c.PollInterval
	}
	return 5 * time.Second
}

func (c *Controller) logf(format string, args ...any) {
	if c.Out != nil {
		_, _ = fmt.Fprintf(c.Out, format+"\n", args...)
	}
}

// unit returns the facts of a unit from SSM, once per run.
func (c *Controller) unit(ctx context.Context, name string) (*infra.Unit, error) {
	if u, ok := c.units[name]; ok {
		return u, nil
	}
	u, err := c.Infra.LoadUnit(ctx, c.SSM, name)
	if err != nil {
		return nil, err
	}
	if c.units == nil {
		c.units = map[string]*infra.Unit{}
	}
	c.units[name] = u
	return u, nil
}

// tracked is a state with the ETag of its last write.
type tracked struct {
	*state.State
	etag string
}

// save writes the state with If-Match, so a concurrent run fails instead of
// overwriting this one.
func (c *Controller) save(ctx context.Context, t *tracked) error {
	etag, err := c.Store.Update(ctx, t.State, t.etag)
	if err != nil {
		return err
	}
	t.etag = etag
	return nil
}

func (c *Controller) record(t *tracked, action string) {
	t.Record(state.HistoryEntry{Action: action, Actor: c.Actor, At: c.now(), WorkflowRunURL: c.RunURL})
}

// fail marks the review app failed and keeps the original error.
func (c *Controller) fail(ctx context.Context, t *tracked, err error) error {
	t.Status = state.StatusFailed
	c.record(t, "failed")
	if saveErr := c.save(ctx, t); saveErr != nil {
		return errors.Join(err, fmt.Errorf("save failed state: %w", saveErr))
	}
	return err
}

func (c *Controller) comment(ctx context.Context, pr int, body string) {
	if c.Comments == nil {
		return
	}
	if err := c.Comments.Comment(ctx, pr, body); err != nil {
		c.logf("WARNING: could not comment on PR %d: %v", pr, err)
	}
}

func tagValue(pr int) string {
	return state.ID(pr)
}

func ecsTags(pr int) []ecstypes.Tag {
	return []ecstypes.Tag{{Key: aws.String(reviewapp.TagKey), Value: aws.String(tagValue(pr))}}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
