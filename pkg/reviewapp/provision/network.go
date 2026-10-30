package provision

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"ops/pkg/reviewapp"
	"ops/pkg/reviewapp/infra"
	"ops/pkg/reviewapp/state"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	sd "github.com/aws/aws-sdk-go-v2/service/servicediscovery"
	sdtypes "github.com/aws/aws-sdk-go-v2/service/servicediscovery/types"
)

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// findNamespace returns the ARN and id of a review app's namespace, or empty
// strings when it does not exist.
func (c *Controller) findNamespace(ctx context.Context, pr int) (arn, id string, err error) {
	out, err := c.Discovery.ListNamespaces(ctx, &sd.ListNamespacesInput{Filters: []sdtypes.NamespaceFilter{{
		Name:      sdtypes.NamespaceFilterNameName,
		Values:    []string{reviewapp.Namespace(pr)},
		Condition: sdtypes.FilterConditionEq,
	}}})
	if err != nil {
		return "", "", fmt.Errorf("list namespaces: %w", err)
	}
	for _, ns := range out.Namespaces {
		if aws.ToString(ns.Name) == reviewapp.Namespace(pr) {
			return aws.ToString(ns.Arn), aws.ToString(ns.Id), nil
		}
	}
	return "", "", nil
}

// ensureNamespace creates the review app's Service Connect namespace.
func (c *Controller) ensureNamespace(ctx context.Context, pr int) (string, error) {
	arn, _, err := c.findNamespace(ctx, pr)
	if err != nil || arn != "" {
		return arn, err
	}
	c.logf("Creating namespace %s", reviewapp.Namespace(pr))
	out, err := c.Discovery.CreateHttpNamespace(ctx, &sd.CreateHttpNamespaceInput{
		Name:        aws.String(reviewapp.Namespace(pr)),
		Description: aws.String("Review app " + state.ID(pr)),
		Tags:        []sdtypes.Tag{{Key: aws.String(reviewapp.TagKey), Value: aws.String(tagValue(pr))}},
	})
	if err != nil {
		return "", fmt.Errorf("create namespace: %w", err)
	}
	id, err := c.waitOperation(ctx, aws.ToString(out.OperationId))
	if err != nil {
		return "", err
	}
	ns, err := c.Discovery.GetNamespace(ctx, &sd.GetNamespaceInput{Id: aws.String(id)})
	if err != nil {
		return "", fmt.Errorf("get namespace %s: %w", id, err)
	}
	return aws.ToString(ns.Namespace.Arn), nil
}

// waitOperation waits for a Cloud Map operation and returns its namespace id.
func (c *Controller) waitOperation(ctx context.Context, opID string) (string, error) {
	for {
		out, err := c.Discovery.GetOperation(ctx, &sd.GetOperationInput{OperationId: aws.String(opID)})
		if err != nil {
			return "", fmt.Errorf("get operation %s: %w", opID, err)
		}
		switch out.Operation.Status {
		case sdtypes.OperationStatusSuccess:
			return out.Operation.Targets[string(sdtypes.OperationTargetTypeNamespace)], nil
		case sdtypes.OperationStatusFail:
			return "", fmt.Errorf("operation %s failed: %s", opID, aws.ToString(out.Operation.ErrorMessage))
		}
		if err := sleep(ctx, c.poll()); err != nil {
			return "", fmt.Errorf("waiting for operation %s: %w", opID, err)
		}
	}
}

// ensureTargetGroup creates the target group of a public unit.
func (c *Controller) ensureTargetGroup(ctx context.Context, pr int, unit string, route infra.Route) (string, error) {
	name := reviewapp.TargetGroup(pr, unit)
	out, err := c.ELB.DescribeTargetGroups(ctx, &elb.DescribeTargetGroupsInput{Names: []string{name}})
	var nf *elbtypes.TargetGroupNotFoundException
	switch {
	case err == nil && len(out.TargetGroups) > 0:
		return aws.ToString(out.TargetGroups[0].TargetGroupArn), nil
	case err != nil && !errors.As(err, &nf):
		return "", fmt.Errorf("describe target group %s: %w", name, err)
	}

	hcPort := "traffic-port"
	if route.HealthCheckPort != 0 {
		hcPort = strconv.Itoa(route.HealthCheckPort)
	}
	created, err := c.ELB.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:            aws.String(name),
		Port:            aws.Int32(int32(route.Port)),
		Protocol:        elbtypes.ProtocolEnumHttp,
		VpcId:           aws.String(c.Infra.Network.VPCID),
		TargetType:      elbtypes.TargetTypeEnumIp,
		HealthCheckPath: aws.String(route.HealthCheckPath),
		HealthCheckPort: aws.String(hcPort),
		Matcher:         &elbtypes.Matcher{HttpCode: aws.String("200-399")},
		Tags:            []elbtypes.Tag{{Key: aws.String(reviewapp.TagKey), Value: aws.String(tagValue(pr))}},
	})
	if err != nil {
		return "", fmt.Errorf("create target group %s: %w", name, err)
	}
	arn := aws.ToString(created.TargetGroups[0].TargetGroupArn)
	// Review apps redeploy often and hold no long requests.
	if _, err := c.ELB.ModifyTargetGroupAttributes(ctx, &elb.ModifyTargetGroupAttributesInput{
		TargetGroupArn: aws.String(arn),
		Attributes:     []elbtypes.TargetGroupAttribute{{Key: aws.String("deregistration_delay.timeout_seconds"), Value: aws.String("10")}},
	}); err != nil {
		return "", fmt.Errorf("set target group %s attributes: %w", name, err)
	}
	return arn, nil
}

// listenerRules returns every rule of the HTTPS listener.
func (c *Controller) listenerRules(ctx context.Context) ([]elbtypes.Rule, error) {
	var rules []elbtypes.Rule
	var marker *string
	for {
		out, err := c.ELB.DescribeRules(ctx, &elb.DescribeRulesInput{
			ListenerArn: aws.String(c.Infra.ALB.HTTPSListenerARN),
			Marker:      marker,
			PageSize:    aws.Int32(400),
		})
		if err != nil {
			return nil, fmt.Errorf("describe listener rules: %w", err)
		}
		rules = append(rules, out.Rules...)
		if out.NextMarker == nil {
			return rules, nil
		}
		marker = out.NextMarker
	}
}

func ruleValues(r elbtypes.Rule, field string) []string {
	for _, cond := range r.Conditions {
		if aws.ToString(cond.Field) != field {
			continue
		}
		switch {
		case cond.HostHeaderConfig != nil:
			return cond.HostHeaderConfig.Values
		case cond.PathPatternConfig != nil:
			return cond.PathPatternConfig.Values
		}
		return cond.Values
	}
	return nil
}

// plannedRule is one listener rule a review app needs.
type plannedRule struct {
	unit  string
	tgARN string
	paths []string
}

// planRules lists the rules of every public unit, with catch-all rules last
// so more specific paths win.
func (c *Controller) planRules(tgs map[string]string) []plannedRule {
	var specific, catchAll []plannedRule
	for _, unit := range sortedKeys(tgs) {
		for _, paths := range c.Blueprint.Routes[unit].Rules {
			r := plannedRule{unit: unit, tgARN: tgs[unit], paths: paths}
			if slices.Contains(paths, "/*") {
				catchAll = append(catchAll, r)
			} else {
				specific = append(specific, r)
			}
		}
	}
	return append(specific, catchAll...)
}

// ensureRules creates the listener rules for the review app host. Rules that
// already exist for the host and paths are kept. New rules take the lowest
// free priorities in plan order, so catch-alls get the highest numbers.
func (c *Controller) ensureRules(ctx context.Context, pr int, tgs map[string]string) ([]state.ListenerRule, error) {
	host := reviewapp.Host(pr, c.Domain)
	existing, err := c.listenerRules(ctx)
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	for _, r := range existing {
		if p, err := strconv.Atoi(aws.ToString(r.Priority)); err == nil {
			used[p] = true
		}
	}

	var result []state.ListenerRule
	next := 1
	for _, plan := range c.planRules(tgs) {
		if found, ok := findRule(existing, host, plan.paths); ok {
			p, _ := strconv.Atoi(aws.ToString(found.Priority))
			result = append(result, state.ListenerRule{ARN: aws.ToString(found.RuleArn), Priority: p})
			continue
		}
		for {
			for used[next] {
				next++
			}
			out, err := c.ELB.CreateRule(ctx, &elb.CreateRuleInput{
				ListenerArn: aws.String(c.Infra.ALB.HTTPSListenerARN),
				Priority:    aws.Int32(int32(next)),
				Conditions: []elbtypes.RuleCondition{
					{Field: aws.String("host-header"), HostHeaderConfig: &elbtypes.HostHeaderConditionConfig{Values: []string{host}}},
					{Field: aws.String("path-pattern"), PathPatternConfig: &elbtypes.PathPatternConditionConfig{Values: plan.paths}},
				},
				Actions: []elbtypes.Action{{Type: elbtypes.ActionTypeEnumForward, TargetGroupArn: aws.String(plan.tgARN)}},
				Tags:    []elbtypes.Tag{{Key: aws.String(reviewapp.TagKey), Value: aws.String(tagValue(pr))}},
			})
			var inUse *elbtypes.PriorityInUseException
			if errors.As(err, &inUse) {
				// Another review app took it since the listing.
				used[next] = true
				continue
			}
			if err != nil {
				return result, fmt.Errorf("create rule for %s %v: %w", plan.unit, plan.paths, err)
			}
			used[next] = true
			result = append(result, state.ListenerRule{ARN: aws.ToString(out.Rules[0].RuleArn), Priority: next})
			break
		}
	}
	return result, nil
}

func findRule(rules []elbtypes.Rule, host string, paths []string) (elbtypes.Rule, bool) {
	for _, r := range rules {
		if slices.Equal(ruleValues(r, "host-header"), []string{host}) && slices.Equal(ruleValues(r, "path-pattern"), paths) {
			return r, true
		}
	}
	return elbtypes.Rule{}, false
}

// hostRules returns the ARNs of every rule for a review app host.
func (c *Controller) hostRules(ctx context.Context, pr int) ([]string, error) {
	rules, err := c.listenerRules(ctx)
	if err != nil {
		return nil, err
	}
	host := reviewapp.Host(pr, c.Domain)
	var arns []string
	for _, r := range rules {
		if slices.Contains(ruleValues(r, "host-header"), host) {
			arns = append(arns, aws.ToString(r.RuleArn))
		}
	}
	return arns, nil
}

// describeService returns a service when it exists and is not deleted.
func (c *Controller) describeService(ctx context.Context, name string) (*ecstypes.Service, error) {
	out, err := c.ECS.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster:  aws.String(c.Infra.Cluster.ARN),
		Services: []string{name},
	})
	if err != nil {
		return nil, fmt.Errorf("describe service %s: %w", name, err)
	}
	for _, s := range out.Services {
		if aws.ToString(s.Status) == "ACTIVE" {
			return &s, nil
		}
	}
	return nil, nil
}

// serviceSpec is what ensureService needs for one unit.
type serviceSpec struct {
	PR        int
	Unit      string
	TaskDef   string
	Namespace string
	Ports     []int
	TGARN     string
	Route     *infra.Route
	Desired   int32
}

// ensureService creates the unit's service, or points an existing one at the
// new task definition.
func (c *Controller) ensureService(ctx context.Context, s serviceSpec) (string, error) {
	name := reviewapp.Service(s.PR, s.Unit)
	existing, err := c.describeService(ctx, name)
	if err != nil {
		return "", err
	}
	if existing != nil {
		_, err := c.ECS.UpdateService(ctx, &awsecs.UpdateServiceInput{
			Cluster:        aws.String(c.Infra.Cluster.ARN),
			Service:        aws.String(name),
			TaskDefinition: aws.String(s.TaskDef),
			DesiredCount:   aws.Int32(s.Desired),
		})
		if err != nil {
			return "", fmt.Errorf("update service %s: %w", name, err)
		}
		return name, nil
	}

	network, err := c.networkFor(ctx, s.Unit)
	if err != nil {
		return "", err
	}
	connect := &ecstypes.ServiceConnectConfiguration{Enabled: true, Namespace: aws.String(s.Namespace)}
	if len(s.Ports) > 0 {
		connect.Services = []ecstypes.ServiceConnectService{{
			PortName:      aws.String(portName(s.Ports[0])),
			DiscoveryName: aws.String(s.Unit),
			ClientAliases: []ecstypes.ServiceConnectClientAlias{{
				Port:    aws.Int32(int32(s.Ports[0])),
				DnsName: aws.String(s.Unit + "." + c.Blueprint.ServiceConnectDomain),
			}},
		}}
	}
	in := &awsecs.CreateServiceInput{
		Cluster:                     aws.String(c.Infra.Cluster.ARN),
		ServiceName:                 aws.String(name),
		TaskDefinition:              aws.String(s.TaskDef),
		DesiredCount:                aws.Int32(s.Desired),
		LaunchType:                  ecstypes.LaunchTypeFargate,
		NetworkConfiguration:        network,
		ServiceConnectConfiguration: connect,
		DeploymentConfiguration: &ecstypes.DeploymentConfiguration{
			MinimumHealthyPercent: aws.Int32(0),
			MaximumPercent:        aws.Int32(100),
		},
		PropagateTags: ecstypes.PropagateTagsService,
		Tags:          ecsTags(s.PR),
	}
	if s.TGARN != "" {
		in.LoadBalancers = []ecstypes.LoadBalancer{{
			TargetGroupArn: aws.String(s.TGARN),
			ContainerName:  aws.String(s.Unit),
			ContainerPort:  aws.Int32(int32(s.Route.Port)),
		}}
		in.HealthCheckGracePeriodSeconds = aws.Int32(120)
	}
	if _, err := c.ECS.CreateService(ctx, in); err != nil {
		return "", fmt.Errorf("create service %s: %w", name, err)
	}
	return name, nil
}

// waitStable waits until every service runs its desired count on its
// current task definition.
func (c *Controller) waitStable(ctx context.Context, services []string) error {
	timeout := c.StableTimeout
	if timeout == 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		pending := []string{}
		for chunk := range slices.Chunk(services, 10) {
			out, err := c.ECS.DescribeServices(ctx, &awsecs.DescribeServicesInput{
				Cluster:  aws.String(c.Infra.Cluster.ARN),
				Services: chunk,
			})
			if err != nil {
				return fmt.Errorf("describe services: %w", err)
			}
			for _, s := range out.Services {
				if len(s.Deployments) != 1 || s.RunningCount != s.DesiredCount {
					pending = append(pending, aws.ToString(s.ServiceName))
				}
			}
		}
		if len(pending) == 0 {
			return nil
		}
		if err := sleep(ctx, c.poll()); err != nil {
			return fmt.Errorf("services not stable: %s: %w", strings.Join(pending, ", "), err)
		}
	}
}

// scale sets the desired count of every service of a review app.
func (c *Controller) scale(ctx context.Context, services []string, desired int32) error {
	var errs []error
	for _, name := range services {
		if _, err := c.ECS.UpdateService(ctx, &awsecs.UpdateServiceInput{
			Cluster:      aws.String(c.Infra.Cluster.ARN),
			Service:      aws.String(name),
			DesiredCount: aws.Int32(desired),
		}); err != nil {
			errs = append(errs, fmt.Errorf("scale %s to %d: %w", name, desired, err))
		}
	}
	return errors.Join(errs...)
}
