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

// BuildTaskDefinitionOptions bundles the inputs for BuildTaskDefinition and
// BuildScheduledTaskDefinition.
type BuildTaskDefinitionOptions struct {
	Base     *BaseConfig
	Merged   MergedConfig
	Names    Names
	Env      string
	ImageTag string
	Secrets  []ECSSecret
}

// BuildTaskDefinition assembles an ECS RegisterTaskDefinitionInput from the
// already-merged config and resolved secrets. No AWS calls are made here.
func BuildTaskDefinition(opts BuildTaskDefinitionOptions) awsecs.RegisterTaskDefinitionInput {
	return buildTaskDefinitionInput(buildTaskDefinitionInputOptions{
		Base:            opts.Base,
		Merged:          opts.Merged,
		Names:           opts.Names,
		Family:          opts.Names.Family,
		Env:             opts.Env,
		ImageTag:        opts.ImageTag,
		Secrets:         opts.Secrets,
		WithHealthCheck: true,
	})
}

// BuildScheduledTaskDefinition is like BuildTaskDefinition but registers under
// names.ScheduledFamily and strips port mappings and health checks. Used for
// EventBridge Scheduler scheduled tasks and ad-hoc schedule-run invocations.
func BuildScheduledTaskDefinition(opts BuildTaskDefinitionOptions) awsecs.RegisterTaskDefinitionInput {
	input := buildTaskDefinitionInput(buildTaskDefinitionInputOptions{
		Base:            opts.Base,
		Merged:          opts.Merged,
		Names:           opts.Names,
		Family:          opts.Names.ScheduledFamily,
		Env:             opts.Env,
		ImageTag:        opts.ImageTag,
		Secrets:         opts.Secrets,
		WithHealthCheck: false,
	})
	input.RequiresCompatibilities = addFargateCompatibilityForScheduledTasks(
		input.RequiresCompatibilities,
		opts.Base.ECS.CapacityProvider,
		opts.Merged.ScheduledTasks,
	)
	return input
}

// buildTaskDefinitionInputOptions bundles the inputs for buildTaskDefinitionInput.
type buildTaskDefinitionInputOptions struct {
	Base            *BaseConfig
	Merged          MergedConfig
	Names           Names
	Family          string
	Env             string
	ImageTag        string
	Secrets         []ECSSecret
	WithHealthCheck bool
}

// buildTaskDefinitionInput is the shared implementation for BuildTaskDefinition
// and BuildScheduledTaskDefinition. withHealthCheck controls whether port
// mappings and the container health check are included.
func buildTaskDefinitionInput(opts buildTaskDefinitionInputOptions) awsecs.RegisterTaskDefinitionInput {
	appName := opts.Merged.Name
	image := resolveImage(resolveImageOptions{
		ECRURL:     opts.Base.AWS.ECRUrl,
		Env:        opts.Env,
		ImageField: opts.Merged.Image,
		AppName:    appName,
		ImageTag:   opts.ImageTag,
	})

	taskVolumes, mountPoints := buildVolumes(opts.Merged.Volumes)
	container := buildContainer(buildContainerOptions{
		AppName:         appName,
		Image:           image,
		Merged:          opts.Merged,
		Names:           opts.Names,
		Region:          opts.Base.AWS.Region,
		Secrets:         opts.Secrets,
		MountPoints:     mountPoints,
		WithHealthCheck: opts.WithHealthCheck,
	})

	executionRole := ExpandTemplate(coalesce(opts.Merged.ExecutionRole, opts.Base.ECS.ExecutionRole), appName, opts.Env)
	taskRole := ExpandTemplate(coalesce(opts.Merged.TaskRole, opts.Base.ECS.TaskRole), appName, opts.Env)

	networkMode := coalesce(opts.Merged.NetworkMode, "awsvpc")
	launchType := coalesce(opts.Merged.LaunchType, "EC2")

	input := awsecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(opts.Family),
		NetworkMode:             ecstypes.NetworkMode(strings.ToLower(networkMode)),
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.Compatibility(launchType)},
		Cpu:                     aws.String(strconv.Itoa(opts.Merged.CPU)),
		Memory:                  aws.String(strconv.Itoa(opts.Merged.Memory)),
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

// resolveImageOptions bundles the inputs for resolveImage.
type resolveImageOptions struct {
	ECRURL     string
	Env        string
	ImageField string
	AppName    string
	ImageTag   string
}

// resolveImage derives the full image URI following the Python renderer logic:
//   - External images (containing '/') are used as-is if already tagged, else
//     the imageTag is appended.
//   - ECR images are prefixed with the ECR URL and env path.
func resolveImage(opts resolveImageOptions) string {
	repo := opts.ImageField
	if repo == "" {
		repo = opts.AppName
	}
	if strings.Contains(repo, "/") {
		basename := repo[strings.LastIndex(repo, "/")+1:]
		if strings.Contains(basename, ":") {
			return repo
		}
		return repo + ":" + opts.ImageTag
	}
	return fmt.Sprintf("%s/%s/%s:%s", opts.ECRURL, opts.Env, repo, opts.ImageTag)
}

// buildContainerOptions bundles the inputs for buildContainer.
type buildContainerOptions struct {
	AppName         string
	Image           string
	Merged          MergedConfig
	Names           Names
	Region          string
	Secrets         []ECSSecret
	MountPoints     []ecstypes.MountPoint
	WithHealthCheck bool
}

func buildContainer(opts buildContainerOptions) ecstypes.ContainerDefinition {
	c := ecstypes.ContainerDefinition{
		Name:      aws.String(opts.AppName),
		Image:     aws.String(opts.Image),
		Essential: aws.Bool(true),
	}

	if len(opts.MountPoints) > 0 {
		c.MountPoints = opts.MountPoints
	}

	if opts.WithHealthCheck {
		c.PortMappings = buildPortMappings(containerPorts(opts.Merged))
	}

	if len(opts.Merged.Environment) > 0 {
		c.Environment = make([]ecstypes.KeyValuePair, 0, len(opts.Merged.Environment))
		for k, v := range opts.Merged.Environment {
			c.Environment = append(c.Environment, ecstypes.KeyValuePair{
				Name:  aws.String(k),
				Value: aws.String(v),
			})
		}
	}

	if len(opts.Merged.Command) > 0 {
		c.Command = opts.Merged.Command
	}
	if len(opts.Merged.EntryPoint) > 0 {
		c.EntryPoint = opts.Merged.EntryPoint
	}

	if len(opts.Secrets) > 0 {
		c.Secrets = make([]ecstypes.Secret, len(opts.Secrets))
		for i, s := range opts.Secrets {
			c.Secrets[i] = ecstypes.Secret{
				Name:      aws.String(s.Name),
				ValueFrom: aws.String(s.ValueFrom),
			}
		}
	}

	if opts.Merged.GPU > 0 {
		c.ResourceRequirements = []ecstypes.ResourceRequirement{
			{
				Type:  ecstypes.ResourceTypeGpu,
				Value: aws.String(fmt.Sprintf("%d", opts.Merged.GPU)),
			},
		}
	}

	hasCustomCommand := len(opts.Merged.ContainerHC.Command) > 0
	hasCurlCheck := opts.Merged.HealthCheckPath != "" && primaryContainerPort(opts.Merged) != 0
	needsHealthCheck := opts.WithHealthCheck && (hasCustomCommand || hasCurlCheck)
	if needsHealthCheck {
		c.HealthCheck = buildHealthCheck(opts.Merged)
	}

	logDriver := coalesce(opts.Merged.LogDriver, "awslogs")
	c.LogConfiguration = &ecstypes.LogConfiguration{
		LogDriver: ecstypes.LogDriver(logDriver),
		Options: map[string]string{
			"awslogs-group":         opts.Names.LogGroup,
			"awslogs-region":        opts.Region,
			"awslogs-stream-prefix": opts.AppName,
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
