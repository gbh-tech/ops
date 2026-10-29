package provision

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ops/pkg/reviewapp"
	"ops/pkg/reviewapp/state"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	sd "github.com/aws/aws-sdk-go-v2/service/servicediscovery"
)

// Destroy deletes everything a review app has, in reverse order of create.
// It finds resources from the state, with the reviewapp=pr-<n> tag and the
// naming conventions as a fallback, so a half-created review app is also
// deleted. Every step runs even when an earlier one fails; the state is
// archived only when all of them succeed, so destroy can be run again.
func (c *Controller) Destroy(ctx context.Context, pr int) error {
	st, etag, err := c.Store.Get(ctx, pr)
	var t *tracked
	switch {
	case err == nil:
		t = &tracked{State: st, etag: etag}
		t.Status = state.StatusDestroying
		c.record(t, "destroy")
		if err := c.save(ctx, t); err != nil {
			return err
		}
	case errors.Is(err, state.ErrNotFound):
		c.logf("No state for %s; deleting what the tags and names find", state.ID(pr))
	default:
		return err
	}

	tagged, err := c.taggedResources(ctx, pr)
	if err != nil {
		return err
	}

	var errs []error
	step := func(name string, fn func() error) {
		if err := fn(); err != nil {
			c.logf("ERROR: %s: %v", name, err)
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	services := c.servicesOf(t, tagged)
	step("delete services", func() error { return c.deleteServices(ctx, services) })
	step("delete listener rules", func() error { return c.deleteRules(ctx, pr, t) })
	step("delete target groups", func() error { return c.deleteTargetGroups(ctx, t, tagged) })
	step("delete namespace", func() error { return c.deleteNamespace(ctx, pr) })
	step("tear down topics", func() error { return c.teardown(ctx, pr) })
	step("drop database", func() error { return c.dropDatabase(ctx, pr) })
	step("deregister task definitions", func() error { return c.deregister(ctx, pr) })
	step("delete data", func() error { return c.deleteData(ctx, pr) })
	step("delete log streams", func() error { return c.deleteLogStreams(ctx, pr, t) })

	if len(errs) > 0 {
		return fmt.Errorf("destroy %s left resources; run destroy again: %w", state.ID(pr), errors.Join(errs...))
	}
	if t != nil {
		key, err := c.Store.Archive(ctx, pr)
		if err != nil {
			return err
		}
		c.logf("Archived state to %s", key)
	}
	c.comment(ctx, pr, fmt.Sprintf("Review app %s destroyed.", state.ID(pr)))
	return nil
}

// taggedResources returns the ARNs carrying reviewapp=pr-<n>.
func (c *Controller) taggedResources(ctx context.Context, pr int) ([]string, error) {
	var arns []string
	var token *string
	for {
		out, err := c.Tagging.GetResources(ctx, &resourcegroupstaggingapi.GetResourcesInput{
			TagFilters:      []tagtypes.TagFilter{{Key: aws.String(reviewapp.TagKey), Values: []string{tagValue(pr)}}},
			PaginationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("find tagged resources: %w", err)
		}
		for _, r := range out.ResourceTagMappingList {
			arns = append(arns, aws.ToString(r.ResourceARN))
		}
		if aws.ToString(out.PaginationToken) == "" {
			return arns, nil
		}
		token = out.PaginationToken
	}
}

// servicesOf lists service names from the state and the tagged ECS services.
func (c *Controller) servicesOf(t *tracked, tagged []string) []string {
	var names []string
	if t != nil {
		names = append(names, t.Resources.Services...)
	}
	for _, arn := range tagged {
		if strings.Contains(arn, ":service/") {
			names = appendUnique(names, arn[strings.LastIndex(arn, "/")+1:])
		}
	}
	return names
}

func (c *Controller) deleteServices(ctx context.Context, names []string) error {
	var errs []error
	var deleted []string
	for _, name := range names {
		svc, err := c.describeService(ctx, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if svc == nil {
			continue
		}
		c.logf("Deleting service %s", name)
		if _, err := c.ECS.DeleteService(ctx, &awsecs.DeleteServiceInput{
			Cluster: aws.String(c.Infra.Cluster.ARN),
			Service: aws.String(name),
			Force:   aws.Bool(true),
		}); err != nil {
			errs = append(errs, fmt.Errorf("delete service %s: %w", name, err))
			continue
		}
		deleted = append(deleted, name)
	}
	// The namespace and target groups can only go once the services are gone.
	for _, name := range deleted {
		if err := c.waitGone(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Controller) waitGone(ctx context.Context, name string) error {
	for {
		out, err := c.ECS.DescribeServices(ctx, &awsecs.DescribeServicesInput{
			Cluster:  aws.String(c.Infra.Cluster.ARN),
			Services: []string{name},
		})
		if err != nil {
			return fmt.Errorf("describe service %s: %w", name, err)
		}
		if len(out.Services) == 0 || aws.ToString(out.Services[0].Status) == "INACTIVE" {
			return nil
		}
		if err := sleep(ctx, c.poll()); err != nil {
			return fmt.Errorf("waiting for service %s to drain: %w", name, err)
		}
	}
}

func (c *Controller) deleteRules(ctx context.Context, pr int, t *tracked) error {
	arns, err := c.hostRules(ctx, pr)
	if err != nil {
		return err
	}
	if t != nil {
		for _, r := range t.Resources.ListenerRules {
			arns = appendUnique(arns, r.ARN)
		}
	}
	var errs []error
	for _, arn := range arns {
		_, err := c.ELB.DeleteRule(ctx, &elb.DeleteRuleInput{RuleArn: aws.String(arn)})
		if err != nil && !isNotFound(err) {
			errs = append(errs, fmt.Errorf("delete rule %s: %w", arn, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Controller) deleteTargetGroups(ctx context.Context, t *tracked, tagged []string) error {
	var arns []string
	if t != nil {
		arns = append(arns, t.Resources.TargetGroups...)
	}
	for _, arn := range tagged {
		if strings.Contains(arn, ":targetgroup/") {
			arns = appendUnique(arns, arn)
		}
	}
	var errs []error
	for _, arn := range arns {
		_, err := c.ELB.DeleteTargetGroup(ctx, &elb.DeleteTargetGroupInput{TargetGroupArn: aws.String(arn)})
		if err != nil && !isNotFound(err) {
			errs = append(errs, fmt.Errorf("delete target group %s: %w", arn, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Controller) deleteNamespace(ctx context.Context, pr int) error {
	_, id, err := c.findNamespace(ctx, pr)
	if err != nil || id == "" {
		return err
	}
	c.logf("Deleting namespace %s", reviewapp.Namespace(pr))
	out, err := c.Discovery.DeleteNamespace(ctx, &sd.DeleteNamespaceInput{Id: aws.String(id)})
	if err != nil {
		return fmt.Errorf("delete namespace: %w", err)
	}
	_, err = c.waitOperation(ctx, aws.ToString(out.OperationId))
	return err
}

// teardown runs the helper task's teardown (topics and consumer groups) when
// the review app got as far as registering it.
func (c *Controller) teardown(ctx context.Context, pr int) error {
	family := reviewapp.ToolsFamily(pr)
	arns, err := c.taskDefinitions(ctx, family)
	if err != nil {
		return err
	}
	if len(arns) == 0 {
		c.logf("No helper task definition; skipping teardown")
		return nil
	}
	return c.runOneOff(ctx, oneOff{PR: pr, Unit: ToolsUnit, Family: arns[len(arns)-1], Container: ToolsContainer, Command: []string{"teardown"}})
}

// taskDefinitions lists the active revisions of families starting with
// prefix, oldest first.
func (c *Controller) taskDefinitions(ctx context.Context, prefix string) ([]string, error) {
	var arns []string
	var token *string
	for {
		out, err := c.ECS.ListTaskDefinitions(ctx, &awsecs.ListTaskDefinitionsInput{
			FamilyPrefix: aws.String(prefix),
			NextToken:    token,
		})
		if err != nil {
			return nil, fmt.Errorf("list task definitions %s: %w", prefix, err)
		}
		arns = append(arns, out.TaskDefinitionArns...)
		if out.NextToken == nil {
			return arns, nil
		}
		token = out.NextToken
	}
}

// deregister deregisters every revision of every reviewapp-pr-<n>-* family.
// FamilyPrefix is a plain prefix, so the trailing hyphen keeps pr-1 from
// matching pr-10.
func (c *Controller) deregister(ctx context.Context, pr int) error {
	prefix := reviewapp.Family(pr, "")
	arns, err := c.taskDefinitions(ctx, prefix)
	if err != nil {
		return err
	}
	var errs []error
	for _, arn := range arns {
		family := arn[strings.LastIndex(arn, "/")+1:]
		if !strings.HasPrefix(family, prefix) {
			continue
		}
		if _, err := c.ECS.DeregisterTaskDefinition(ctx, &awsecs.DeregisterTaskDefinitionInput{TaskDefinition: aws.String(arn)}); err != nil {
			errs = append(errs, fmt.Errorf("deregister %s: %w", arn, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Controller) deleteData(ctx context.Context, pr int) error {
	if c.Infra.Buckets.Data == "" {
		return nil
	}
	prefix := reviewapp.DataPrefix(pr)
	var token *string
	for {
		out, err := c.Data.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(c.Infra.Buckets.Data),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return fmt.Errorf("list %s: %w", prefix, err)
		}
		if len(out.Contents) > 0 {
			ids := make([]s3types.ObjectIdentifier, len(out.Contents))
			for i, o := range out.Contents {
				ids[i] = s3types.ObjectIdentifier{Key: o.Key}
			}
			if _, err := c.Data.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(c.Infra.Buckets.Data),
				Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)},
			}); err != nil {
				return fmt.Errorf("delete %s: %w", prefix, err)
			}
		}
		if !aws.ToBool(out.IsTruncated) {
			return nil
		}
		token = out.NextContinuationToken
	}
}

// deleteLogStreams removes the review app's streams from each unit's log
// group. The groups are shared with the other review apps and stay.
func (c *Controller) deleteLogStreams(ctx context.Context, pr int, t *tracked) error {
	units := []string{ToolsUnit}
	if t != nil {
		for _, u := range sortedKeys(t.Config) {
			units = appendUnique(units, u)
		}
	}
	var errs []error
	groups := map[string]bool{}
	for _, u := range units {
		facts, err := c.unit(ctx, u)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if groups[facts.LogGroup] {
			continue
		}
		groups[facts.LogGroup] = true
		if err := c.deleteStreams(ctx, facts.LogGroup, reviewapp.LogStreamPrefix(pr)+"/"); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Controller) deleteStreams(ctx context.Context, group, prefix string) error {
	var names []string
	var token *string
	for {
		out, err := c.Logs.DescribeLogStreams(ctx, &cwlogs.DescribeLogStreamsInput{
			LogGroupName:        aws.String(group),
			LogStreamNamePrefix: aws.String(prefix),
			NextToken:           token,
		})
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			return fmt.Errorf("list log streams of %s: %w", group, err)
		}
		for _, s := range out.LogStreams {
			names = append(names, aws.ToString(s.LogStreamName))
		}
		if out.NextToken == nil {
			break
		}
		token = out.NextToken
	}
	var errs []error
	for _, name := range names {
		if _, err := c.Logs.DeleteLogStream(ctx, &cwlogs.DeleteLogStreamInput{
			LogGroupName:  aws.String(group),
			LogStreamName: aws.String(name),
		}); err != nil && !isNotFound(err) {
			errs = append(errs, fmt.Errorf("delete log stream %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// isNotFound reports whether an AWS error means the resource is already gone.
func isNotFound(err error) bool {
	var code interface{ ErrorCode() string }
	if !errors.As(err, &code) {
		return false
	}
	return slices.Contains([]string{
		"RuleNotFound", "TargetGroupNotFound", "ResourceNotFoundException", "NamespaceNotFound",
	}, code.ErrorCode())
}
