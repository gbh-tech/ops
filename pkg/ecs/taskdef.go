package ecs

import (
	"fmt"
	"strconv"
	"strings"

	"ops/pkg/app"

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
	return buildTaskDefinitionInput(base, merged, names, names.Family, env, imageTag, secrets, true)
}

// BuildScheduledTaskDefinition is like BuildTaskDefinition but registers under
// names.ScheduledFamily and strips port mappings and health checks. Used for
// EventBridge Scheduler scheduled tasks and ad-hoc schedule-run invocations.
func BuildScheduledTaskDefinition(
	base *BaseConfig,
	merged MergedConfig,
	names Names,
	env, imageTag string,
	secrets []ECSSecret,
) awsecs.RegisterTaskDefinitionInput {
	input := buildTaskDefinitionInput(base, merged, names, names.ScheduledFamily, env, imageTag, secrets, false)
	input.RequiresCompatibilities = addFargateCompatibilityForScheduledTasks(
		input.RequiresCompatibilities,
		base.ECS.CapacityProvider,
		merged.ScheduledTasks,
	)
	return input
}

// buildTaskDefinitionInput is the shared implementation for BuildTaskDefinition
// and BuildScheduledTaskDefinition. withHealthCheck controls whether port
// mappings and the container health check are included.
func buildTaskDefinitionInput(
	base *BaseConfig,
	merged MergedConfig,
	names Names,
	family, env, imageTag string,
	secrets []ECSSecret,
	withHealthCheck bool,
) awsecs.RegisterTaskDefinitionInput {
	appName := merged.Name
	image := resolveImage(base.AWS.ECRUrl, env, merged.Image, appName, imageTag)

	taskVolumes, mountPoints := buildVolumes(merged.Volumes)
	container := buildContainer(appName, image, merged, names, base.AWS.Region, secrets, mountPoints, withHealthCheck)

	executionRole := ExpandTemplate(coalesce(merged.ExecutionRole, base.ECS.ExecutionRole), appName, env)
	taskRole := ExpandTemplate(coalesce(merged.TaskRole, base.ECS.TaskRole), appName, env)

	networkMode := coalesce(merged.NetworkMode, "awsvpc")
	launchType := coalesce(merged.LaunchType, "EC2")

	input := awsecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(family),
		NetworkMode:             ecstypes.NetworkMode(strings.ToLower(networkMode)),
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.Compatibility(launchType)},
		Cpu:                     aws.String(strconv.Itoa(merged.CPU)),
		Memory:                  aws.String(strconv.Itoa(merged.Memory)),
		ContainerDefinitions:    []ecstypes.ContainerDefinition{container},
	}

	if len(taskVolumes) > 0 {
		input.Volumes = taskVolumes
	}

	if executionRole != "" {
		input.ExecutionRoleArn = aws.String(executionRole)
	}
	if taskRole != "" {
		input.TaskRoleArn = aws.String(taskRole)
	}

	return input
}

// addFargateCompatibilityForScheduledTasks keeps the scheduled task definition
// compatible with the capacity providers used to launch scheduled tasks.
//
// CapacityProviderStrategy is set later when EventBridge Scheduler runs the
// task, but AWS still requires the registered task definition to include
// FARGATE in RequiresCompatibilities when the selected capacity provider is
// FARGATE or FARGATE_SPOT.
func addFargateCompatibilityForScheduledTasks(
	current []ecstypes.Compatibility,
	defaultCapacityProvider string,
	tasks []app.ScheduledTaskConfig,
) []ecstypes.Compatibility {
	for _, task := range tasks {
		if usesFargateCapacityProvider(task.CapacityProvider) {
			return appendCompatibility(current, ecstypes.CompatibilityFargate)
		}
	}
	if usesFargateCapacityProvider(defaultCapacityProvider) {
		return appendCompatibility(current, ecstypes.CompatibilityFargate)
	}
	return current
}

func appendCompatibility(current []ecstypes.Compatibility, compatibility ecstypes.Compatibility) []ecstypes.Compatibility {
	for _, existing := range current {
		if existing == compatibility {
			return current
		}
	}
	return append(current, compatibility)
}

func usesFargateCapacityProvider(capacityProvider string) bool {
	switch strings.ToUpper(capacityProvider) {
	case "FARGATE", "FARGATE_SPOT":
		return true
	default:
		return false
	}
}

// buildVolumes converts the merged VolumeConfig list into the two parallel
// slices that ECS expects: task-level Volume definitions and container-level
// MountPoint references. Each entry in volumes produces exactly one of each.
func buildVolumes(volumes []app.VolumeConfig) ([]ecstypes.Volume, []ecstypes.MountPoint) {
	if len(volumes) == 0 {
		return nil, nil
	}

	taskVolumes := make([]ecstypes.Volume, 0, len(volumes))
	mountPoints := make([]ecstypes.MountPoint, 0, len(volumes))

	for _, v := range volumes {
		vol := ecstypes.Volume{Name: aws.String(v.Name)}

		switch {
		case v.EFS != nil:
			efsCfg := &ecstypes.EFSVolumeConfiguration{
				FileSystemId: aws.String(v.EFS.FileSystemId),
			}
			if v.EFS.RootDirectory != "" {
				efsCfg.RootDirectory = aws.String(v.EFS.RootDirectory)
			}
			if v.EFS.TransitEncryption != "" {
				efsCfg.TransitEncryption = ecstypes.EFSTransitEncryption(strings.ToUpper(v.EFS.TransitEncryption))
			}
			if v.EFS.AccessPointId != "" || v.EFS.IAM != "" {
				efsCfg.AuthorizationConfig = &ecstypes.EFSAuthorizationConfig{}
				if v.EFS.AccessPointId != "" {
					efsCfg.AuthorizationConfig.AccessPointId = aws.String(v.EFS.AccessPointId)
				}
				if v.EFS.IAM != "" {
					efsCfg.AuthorizationConfig.Iam = ecstypes.EFSAuthorizationConfigIAM(strings.ToUpper(v.EFS.IAM))
				}
			}
			vol.EfsVolumeConfiguration = efsCfg

		case v.Host != nil:
			vol.Host = &ecstypes.HostVolumeProperties{}
			if v.Host.SourcePath != "" {
				vol.Host.SourcePath = aws.String(v.Host.SourcePath)
			}

		case v.Docker != nil:
			dockerCfg := &ecstypes.DockerVolumeConfiguration{}
			if v.Docker.Driver != "" {
				dockerCfg.Driver = aws.String(v.Docker.Driver)
			}
			if v.Docker.Scope != "" {
				dockerCfg.Scope = ecstypes.Scope(strings.ToLower(v.Docker.Scope))
			}
			if v.Docker.Autoprovision != nil {
				dockerCfg.Autoprovision = v.Docker.Autoprovision
			}
			if len(v.Docker.DriverOpts) > 0 {
				dockerCfg.DriverOpts = v.Docker.DriverOpts
			}
			if len(v.Docker.Labels) > 0 {
				dockerCfg.Labels = v.Docker.Labels
			}
			vol.DockerVolumeConfiguration = dockerCfg

		}

		taskVolumes = append(taskVolumes, vol)
		mountPoints = append(mountPoints, ecstypes.MountPoint{
			SourceVolume:  aws.String(v.Name),
			ContainerPath: aws.String(v.ContainerPath),
			ReadOnly:      aws.Bool(v.ReadOnly),
		})
	}

	return taskVolumes, mountPoints
}

// ExpandTemplate replaces {service} and {env} placeholders in s.
func ExpandTemplate(s, service, env string) string {
	s = strings.ReplaceAll(s, "{service}", service)
	s = strings.ReplaceAll(s, "{env}", env)
	return s
}

// ExpandSchedulerTemplate replaces {cluster} and {env} placeholders in s.
// Used to expand ecs.scheduler.{group_name,role_arn} values from .ops/config.yaml.
func ExpandSchedulerTemplate(s, cluster, env string) string {
	s = strings.ReplaceAll(s, "{cluster}", cluster)
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
	mountPoints []ecstypes.MountPoint,
	withHealthCheck bool,
) ecstypes.ContainerDefinition {
	c := ecstypes.ContainerDefinition{
		Name:      aws.String(appName),
		Image:     aws.String(image),
		Essential: aws.Bool(true),
	}

	if len(mountPoints) > 0 {
		c.MountPoints = mountPoints
	}

	if withHealthCheck {
		c.PortMappings = buildPortMappings(containerPorts(merged))
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
	if len(merged.EntryPoint) > 0 {
		c.EntryPoint = merged.EntryPoint
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

	hasCustomCommand := len(merged.ContainerHC.Command) > 0
	hasCurlCheck := merged.HealthCheckPath != "" && primaryContainerPort(merged) != 0
	if withHealthCheck && (hasCustomCommand || hasCurlCheck) {
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

func containerPorts(merged MergedConfig) []int {
	ports := make([]int, 0, 1+len(merged.Ports))
	seen := make(map[int]struct{}, 1+len(merged.Ports))

	add := func(port int) {
		if port == 0 {
			return
		}
		if _, exists := seen[port]; exists {
			return
		}
		seen[port] = struct{}{}
		ports = append(ports, port)
	}

	add(merged.Port)
	for _, port := range merged.Ports {
		add(port)
	}
	return ports
}

func primaryContainerPort(merged MergedConfig) int {
	if merged.Port != 0 {
		return merged.Port
	}
	for _, port := range merged.Ports {
		if port != 0 {
			return port
		}
	}
	return 0
}

func buildPortMappings(ports []int) []ecstypes.PortMapping {
	if len(ports) == 0 {
		return nil
	}

	mappings := make([]ecstypes.PortMapping, 0, len(ports))
	for _, port := range ports {
		mappings = append(mappings, ecstypes.PortMapping{
			ContainerPort: aws.Int32(int32(port)),
			Protocol:      ecstypes.TransportProtocolTcp,
		})
	}
	return mappings
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

	command := hc.Command
	if len(command) == 0 {
		port := primaryContainerPort(merged)
		command = []string{
			"CMD-SHELL",
			fmt.Sprintf("curl -f http://localhost:%d%s || exit 1", port, merged.HealthCheckPath),
		}
	}

	return &ecstypes.HealthCheck{
		Command:     command,
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
