package ecs

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// BuildTaskDefinition assembles an ECS RegisterTaskDefinitionInput from the
// already-merged config and resolved secrets. No AWS calls are made here.
func BuildTaskDefinition(
	base *BaseConfig,
	merged MergedConfig,
	names Names,
	env, imageTag string,
	secrets []ECSSecret,
) awsecs.RegisterTaskDefinitionInput {
	appName := merged.Name
	image := resolveImage(base.AWS.ECRUrl, env, merged.Image, appName, imageTag)
	container := buildContainer(appName, image, merged, names, base.AWS.Region, secrets)

	executionRole := ExpandTemplate(coalesce(merged.ExecutionRole, base.ECS.ExecutionRole), appName, env)
	taskRole := ExpandTemplate(coalesce(merged.TaskRole, base.ECS.TaskRole), appName, env)

	networkMode := coalesce(merged.NetworkMode, "awsvpc")
	launchType := coalesce(merged.LaunchType, "EC2")

	input := awsecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(names.Family),
		NetworkMode:             ecstypes.NetworkMode(strings.ToLower(networkMode)),
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.Compatibility(launchType)},
		Cpu:                     aws.String(fmt.Sprintf("%d", merged.CPU)),
		Memory:                  aws.String(fmt.Sprintf("%d", merged.Memory)),
		ContainerDefinitions:    []ecstypes.ContainerDefinition{container},
	}

	if executionRole != "" {
		input.ExecutionRoleArn = aws.String(executionRole)
	}
	if taskRole != "" {
		input.TaskRoleArn = aws.String(taskRole)
	}

	return input
}

// ExpandTemplate replaces {service} and {env} placeholders in s.
func ExpandTemplate(s, service, env string) string {
	s = strings.ReplaceAll(s, "{service}", service)
	s = strings.ReplaceAll(s, "{env}", env)
	return s
}

// resolveImage derives the full image URI following the Python renderer logic:
//   - External images (containing '/') are used as-is if already tagged, else
//     the imageTag is appended.
//   - ECR images are prefixed with the ECR URL and env path.
func resolveImage(ecrURL, env, imageField, appName, imageTag string) string {
	repo := imageField
	if repo == "" {
		repo = appName
	}
	if strings.Contains(repo, "/") {
		basename := repo[strings.LastIndex(repo, "/")+1:]
		if strings.Contains(basename, ":") {
			return repo
		}
		return repo + ":" + imageTag
	}
	return fmt.Sprintf("%s/%s/%s:%s", ecrURL, env, repo, imageTag)
}

func buildContainer(
	appName, image string,
	merged MergedConfig,
	names Names,
	region string,
	secrets []ECSSecret,
) ecstypes.ContainerDefinition {
	c := ecstypes.ContainerDefinition{
		Name:      aws.String(appName),
		Image:     aws.String(image),
		Essential: aws.Bool(true),
	}

	if merged.Port != 0 {
		c.PortMappings = []ecstypes.PortMapping{
			{
				ContainerPort: aws.Int32(int32(merged.Port)),
				Protocol:      ecstypes.TransportProtocolTcp,
			},
		}
	}

	if len(merged.Environment) > 0 {
		c.Environment = make([]ecstypes.KeyValuePair, 0, len(merged.Environment))
		for k, v := range merged.Environment {
			c.Environment = append(c.Environment, ecstypes.KeyValuePair{
				Name:  aws.String(k),
				Value: aws.String(v),
			})
		}
	}

	if len(merged.Command) > 0 {
		c.Command = merged.Command
	}

	if len(secrets) > 0 {
		c.Secrets = make([]ecstypes.Secret, len(secrets))
		for i, s := range secrets {
			c.Secrets[i] = ecstypes.Secret{
				Name:      aws.String(s.Name),
				ValueFrom: aws.String(s.ValueFrom),
			}
		}
	}

	if merged.HealthCheckPath != "" && merged.Port != 0 {
		c.HealthCheck = buildHealthCheck(merged)
	}

	logDriver := coalesce(merged.LogDriver, "awslogs")
	c.LogConfiguration = &ecstypes.LogConfiguration{
		LogDriver: ecstypes.LogDriver(logDriver),
		Options: map[string]string{
			"awslogs-group":         names.LogGroup,
			"awslogs-region":        region,
			"awslogs-stream-prefix": appName,
		},
	}

	return c
}

func buildHealthCheck(merged MergedConfig) *ecstypes.HealthCheck {
	hc := merged.ContainerHC

	interval := int32(30)
	timeout := int32(5)
	retries := int32(3)
	startPeriod := int32(60)

	if hc.Interval != 0 {
		interval = int32(hc.Interval)
	}
	if hc.Timeout != 0 {
		timeout = int32(hc.Timeout)
	}
	if hc.Retries != 0 {
		retries = int32(hc.Retries)
	}
	if hc.StartPeriod != 0 {
		startPeriod = int32(hc.StartPeriod)
	}

	return &ecstypes.HealthCheck{
		Command: []string{
			"CMD-SHELL",
			fmt.Sprintf("curl -f http://localhost:%d%s || exit 1", merged.Port, merged.HealthCheckPath),
		},
		Interval:    aws.Int32(interval),
		Timeout:     aws.Int32(timeout),
		Retries:     aws.Int32(retries),
		StartPeriod: aws.Int32(startPeriod),
	}
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
