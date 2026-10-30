package provision

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"ops/pkg/reviewapp"
	"ops/pkg/reviewapp/state"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// credentialURL matches a URL that carries a password, e.g.
// postgres://user:secret@host/db.
var credentialURL = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://[^/:@\s]+:[^@\s]+@`)

// placeholders returns the values a unit's [reviewapp.environment] can use.
func (c *Controller) placeholders(pr int) map[string]string {
	return map[string]string{
		"{pr}":           strconv.Itoa(pr),
		"{id}":           state.ID(pr),
		"{host}":         reviewapp.Host(pr, c.Domain),
		"{url}":          reviewapp.URL(pr, c.Domain),
		"{domain}":       c.Domain,
		"{database}":     reviewapp.Database(pr),
		"{topic_prefix}": reviewapp.TopicPrefix(pr),
		"{s3_prefix}":    reviewapp.DataPrefix(pr),
	}
}

// environment returns the unit environment with placeholders expanded and
// the variables every review app task gets. It refuses plain-text passwords:
// they belong in secrets.
func (c *Controller) environment(pr int, vars map[string]string) (map[string]string, error) {
	env := map[string]string{
		"DATABASE_NAME":      reviewapp.Database(pr),
		"KAFKA_TOPIC_PREFIX": reviewapp.TopicPrefix(pr),
		"KAFKA_BROKERS":      c.Infra.MSK.BootstrapBrokers,
		"S3_KEY_PREFIX":      reviewapp.DataPrefix(pr),
		"S3_BUCKET":          c.Infra.Buckets.Data,
		"REVIEWAPP_PR":       strconv.Itoa(pr),
		"REVIEWAPP_URL":      reviewapp.URL(pr, c.Domain),
	}
	repl := c.placeholders(pr)
	for k, v := range vars {
		for p, r := range repl {
			v = strings.ReplaceAll(v, p, r)
		}
		env[k] = v
	}
	for k, v := range env {
		if strings.Contains(strings.ToUpper(k), "PASSWORD") || credentialURL.MatchString(v) {
			return nil, fmt.Errorf("environment variable %s holds a credential; move it to secrets", k)
		}
	}
	return env, nil
}

func keyValues(m map[string]string) []ecstypes.KeyValuePair {
	out := make([]ecstypes.KeyValuePair, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, ecstypes.KeyValuePair{Name: aws.String(k), Value: aws.String(m[k])})
	}
	return out
}

func ecsSecrets(m map[string]string) []ecstypes.Secret {
	out := make([]ecstypes.Secret, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, ecstypes.Secret{Name: aws.String(k), ValueFrom: aws.String(m[k])})
	}
	return out
}

// portName is the name of a port mapping; Service Connect refers to it.
func portName(port int) string {
	return "p" + strconv.Itoa(port)
}

// unitTaskDefinition builds the task definition of one unit.
func (c *Controller) unitTaskDefinition(ctx context.Context, pr int, unit, image string, spec state.UnitSpec) (*awsecs.RegisterTaskDefinitionInput, error) {
	facts, err := c.unit(ctx, unit)
	if err != nil {
		return nil, err
	}
	env, err := c.environment(pr, spec.Environment)
	if err != nil {
		return nil, fmt.Errorf("unit %s: %w", unit, err)
	}

	container := ecstypes.ContainerDefinition{
		Name:        aws.String(unit),
		Image:       aws.String(image),
		Essential:   aws.Bool(true),
		Environment: keyValues(env),
		Secrets:     ecsSecrets(spec.Secrets),
		LogConfiguration: &ecstypes.LogConfiguration{
			LogDriver: ecstypes.LogDriverAwslogs,
			Options: map[string]string{
				"awslogs-group":         facts.LogGroup,
				"awslogs-region":        c.Region,
				"awslogs-stream-prefix": reviewapp.LogStreamPrefix(pr),
			},
		},
	}
	if len(spec.Command) > 0 {
		container.Command = spec.Command
	}
	if len(spec.EntryPoint) > 0 {
		container.EntryPoint = spec.EntryPoint
	}
	for _, p := range spec.Ports {
		container.PortMappings = append(container.PortMappings, ecstypes.PortMapping{
			Name:          aws.String(portName(p)),
			ContainerPort: aws.Int32(int32(p)),
			Protocol:      ecstypes.TransportProtocolTcp,
			AppProtocol:   ecstypes.ApplicationProtocolHttp,
		})
	}
	if len(spec.HealthCheck) > 0 {
		container.HealthCheck = &ecstypes.HealthCheck{
			Command:     spec.HealthCheck,
			Interval:    aws.Int32(30),
			Timeout:     aws.Int32(5),
			Retries:     aws.Int32(3),
			StartPeriod: aws.Int32(60),
		}
	}

	return &awsecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(reviewapp.Family(pr, unit)),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		Cpu:                     aws.String(strconv.Itoa(spec.CPU)),
		Memory:                  aws.String(strconv.Itoa(spec.Memory)),
		ExecutionRoleArn:        aws.String(facts.TaskExecRole),
		TaskRoleArn:             aws.String(facts.TaskRole),
		ContainerDefinitions:    []ecstypes.ContainerDefinition{container},
		Tags:                    ecsTags(pr),
	}, nil
}

// toolsTaskDefinition builds the helper task definition of a review app:
// the project's tools image with the review app's database, topics and data
// prefix. ops only passes bootstrap, migrate or teardown as its command.
func (c *Controller) toolsTaskDefinition(ctx context.Context, pr int, image string) (*awsecs.RegisterTaskDefinitionInput, error) {
	facts, err := c.unit(ctx, ToolsUnit)
	if err != nil {
		return nil, err
	}
	env, err := c.environment(pr, nil)
	if err != nil {
		return nil, err
	}
	secrets := map[string]string{
		"ADMIN_DATABASE_URL": c.Infra.SecretRef(c.Infra.Secrets.DB, "ADMIN_DATABASE_URL"),
		"RLS_DATABASE_URL":   c.Infra.SecretRef(c.Infra.Secrets.DB, "RLS_DATABASE_URL"),
	}
	if c.Infra.Secrets.SeedUsers != "" {
		secrets["SEED_USERS"] = c.Infra.SecretRef(c.Infra.Secrets.SeedUsers, "SEED_USERS")
	}
	return &awsecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(reviewapp.ToolsFamily(pr)),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		Cpu:                     aws.String("512"),
		Memory:                  aws.String("1024"),
		ExecutionRoleArn:        aws.String(facts.TaskExecRole),
		TaskRoleArn:             aws.String(facts.TaskRole),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name:        aws.String(ToolsContainer),
			Image:       aws.String(image),
			Essential:   aws.Bool(true),
			Environment: keyValues(env),
			Secrets:     ecsSecrets(secrets),
			LogConfiguration: &ecstypes.LogConfiguration{
				LogDriver: ecstypes.LogDriverAwslogs,
				Options: map[string]string{
					"awslogs-group":         facts.LogGroup,
					"awslogs-region":        c.Region,
					"awslogs-stream-prefix": reviewapp.LogStreamPrefix(pr),
				},
			},
		}},
		Tags: ecsTags(pr),
	}, nil
}

func (c *Controller) register(ctx context.Context, in *awsecs.RegisterTaskDefinitionInput) (string, error) {
	out, err := c.ECS.RegisterTaskDefinition(ctx, in)
	if err != nil {
		return "", fmt.Errorf("register task definition %s: %w", aws.ToString(in.Family), err)
	}
	return aws.ToString(out.TaskDefinition.TaskDefinitionArn), nil
}

// networkFor returns the awsvpc configuration of a unit's tasks: its own
// security group plus the shared namespace and MSK client groups.
func (c *Controller) networkFor(ctx context.Context, unit string) (*ecstypes.NetworkConfiguration, error) {
	facts, err := c.unit(ctx, unit)
	if err != nil {
		return nil, err
	}
	groups := []string{facts.SecurityGroup}
	for _, k := range []string{"namespace", "msk_client"} {
		if sg := c.Infra.Network.SecurityGroupIDs[k]; sg != "" {
			groups = appendUnique(groups, sg)
		}
	}
	return &ecstypes.NetworkConfiguration{AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
		Subnets:        c.Infra.Network.PrivateSubnetIDs,
		SecurityGroups: groups,
		AssignPublicIp: ecstypes.AssignPublicIpDisabled,
	}}, nil
}

// oneOff runs a task to completion, prints its logs, and fails on a non-zero
// exit code. It generalizes pkg/ecs RunMigrationTask: the network comes from
// the infra parameter instead of a running service.
type oneOff struct {
	PR        int
	Unit      string // whose network and log group the task uses
	Family    string
	Container string
	Command   []string
}

func (c *Controller) runOneOff(ctx context.Context, o oneOff) error {
	network, err := c.networkFor(ctx, o.Unit)
	if err != nil {
		return err
	}
	in := &awsecs.RunTaskInput{
		Cluster:              aws.String(c.Infra.Cluster.ARN),
		TaskDefinition:       aws.String(o.Family),
		LaunchType:           ecstypes.LaunchTypeFargate,
		NetworkConfiguration: network,
		StartedBy:            aws.String(state.ID(o.PR)),
		Tags:                 ecsTags(o.PR),
	}
	if len(o.Command) > 0 {
		in.Overrides = &ecstypes.TaskOverride{ContainerOverrides: []ecstypes.ContainerOverride{{
			Name: aws.String(o.Container), Command: o.Command,
		}}}
	}
	out, err := c.ECS.RunTask(ctx, in)
	if err != nil {
		return fmt.Errorf("run %s: %w", o.Family, err)
	}
	if len(out.Failures) > 0 {
		return fmt.Errorf("run %s: %s", o.Family, aws.ToString(out.Failures[0].Reason))
	}
	if len(out.Tasks) == 0 {
		return fmt.Errorf("run %s: no task started", o.Family)
	}
	taskARN := aws.ToString(out.Tasks[0].TaskArn)
	c.logf("Running %s %s (%s)", o.Family, strings.Join(o.Command, " "), taskARN)

	task, err := c.waitStopped(ctx, taskARN)
	if err != nil {
		return err
	}
	facts, err := c.unit(ctx, o.Unit)
	if err != nil {
		return err
	}
	c.printTaskLogs(ctx, facts.LogGroup, fmt.Sprintf("%s/%s/%s", reviewapp.LogStreamPrefix(o.PR), o.Container, taskARN[strings.LastIndex(taskARN, "/")+1:]))

	for _, ct := range task.Containers {
		if aws.ToString(ct.Name) != o.Container {
			continue
		}
		if ct.ExitCode == nil {
			return fmt.Errorf("%s stopped without an exit code: %s", o.Family, aws.ToString(task.StoppedReason))
		}
		if *ct.ExitCode != 0 {
			return fmt.Errorf("%s %s exited with code %d (logs above)", o.Family, strings.Join(o.Command, " "), *ct.ExitCode)
		}
		return nil
	}
	return fmt.Errorf("%s: container %s not found in the stopped task", o.Family, o.Container)
}

func (c *Controller) waitStopped(ctx context.Context, taskARN string) (*ecstypes.Task, error) {
	for {
		out, err := c.ECS.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
			Cluster: aws.String(c.Infra.Cluster.ARN),
			Tasks:   []string{taskARN},
		})
		if err != nil {
			return nil, fmt.Errorf("describe task %s: %w", taskARN, err)
		}
		if len(out.Tasks) == 0 {
			return nil, fmt.Errorf("task %s not found", taskARN)
		}
		if aws.ToString(out.Tasks[0].LastStatus) == "STOPPED" {
			return &out.Tasks[0], nil
		}
		if err := sleep(ctx, c.poll()); err != nil {
			return nil, fmt.Errorf("waiting for task %s: %w", taskARN, err)
		}
	}
}

func (c *Controller) printTaskLogs(ctx context.Context, group, stream string) {
	var token *string
	for {
		out, err := c.Logs.GetLogEvents(ctx, &cwlogs.GetLogEventsInput{
			LogGroupName:  aws.String(group),
			LogStreamName: aws.String(stream),
			StartFromHead: aws.Bool(true),
			NextToken:     token,
		})
		if err != nil {
			var nf *cwtypes.ResourceNotFoundException
			if !errors.As(err, &nf) {
				c.logf("WARNING: could not read logs of %s: %v", stream, err)
			}
			return
		}
		for _, e := range out.Events {
			c.logf("  | %s", aws.ToString(e.Message))
		}
		if out.NextForwardToken == nil || aws.ToString(out.NextForwardToken) == aws.ToString(token) {
			return
		}
		token = out.NextForwardToken
	}
}
