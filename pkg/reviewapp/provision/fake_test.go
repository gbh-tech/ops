package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ops/pkg/reviewapp/infra"
	"ops/pkg/reviewapp/state"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	sd "github.com/aws/aws-sdk-go-v2/service/servicediscovery"
	sdtypes "github.com/aws/aws-sdk-go-v2/service/servicediscovery/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

const (
	listenerARN = "arn:aws:elasticloadbalancing:us-east-1:1:listener/app/alb/abc/def"
	clusterARN  = "arn:aws:ecs:us-east-1:1:cluster/reviewapp"
)

// fakeAWS is an in-memory account with the AWS services the controller uses.
type fakeAWS struct {
	mu sync.Mutex

	taskDefs   map[string]int // family -> latest revision
	deregs     []string
	services   map[string]*ecstypes.Service
	created    []*awsecs.CreateServiceInput
	updates    []*awsecs.UpdateServiceInput
	tasks      map[string]*awsecs.RunTaskInput
	taskExit   func(in *awsecs.RunTaskInput) int32
	runs       [][]string // container command of each RunTask
	registered []*awsecs.RegisterTaskDefinitionInput

	targetGroups map[string]string // name -> ARN
	rules        []elbtypes.Rule
	createdRules int
	createdTGs   int

	namespaces       map[string]string // name -> id
	createdNamespace int

	secrets map[string]string
	images  map[string]bool // repo:tag

	data   map[string]bool
	tagged []string

	requests float64
	metrics  []string

	streams map[string][]string // group -> stream names
	ssm     map[string]string
}

func newFakeAWS() *fakeAWS {
	f := &fakeAWS{
		taskDefs:     map[string]int{},
		services:     map[string]*ecstypes.Service{},
		tasks:        map[string]*awsecs.RunTaskInput{},
		targetGroups: map[string]string{},
		namespaces:   map[string]string{},
		images:       map[string]bool{},
		data:         map[string]bool{},
		streams:      map[string][]string{},
		taskExit:     func(*awsecs.RunTaskInput) int32 { return 0 },
		secrets: map[string]string{
			"reviewapp/rds-admin": `{"username":"admin","password":"x","DATABASE_URL":"postgres://admin:x@db:5432/postgres"}`,
			"reviewapp/db":        `{"ADMIN_DATABASE_URL":"u","RLS_DATABASE_URL":"u","REVIEWAPP_OWNER_PASSWORD":"o","LEOPARD_APP_PASSWORD":"a","LEOPARD_PII_PASSWORD":"p"}`,
		},
		ssm: map[string]string{},
	}
	for _, u := range []string{"shell", "subgraph-users", "cron-alerts", ToolsUnit} {
		f.ssm["/reviewapp/units/"+u] = fmt.Sprintf(`{"security_group":"sg-%s","task_exec_role":"exec-%s","task_role":"task-%s","log_group":"/ecs/%s"}`, u, u, u, u)
	}
	// Another review app already holds priorities 1 and 2.
	for p := 1; p <= 2; p++ {
		f.rules = append(f.rules, rule(p, "pr-99.reviewapps.example.com", []string{"/x"}, "tg-99"))
	}
	f.rules = append(f.rules, elbtypes.Rule{RuleArn: aws.String("default"), Priority: aws.String("default"), IsDefault: aws.Bool(true)})
	return f
}

func rule(priority int, host string, paths []string, tg string) elbtypes.Rule {
	return elbtypes.Rule{
		RuleArn:  aws.String(fmt.Sprintf("%s/rule-%d", listenerARN, priority)),
		Priority: aws.String(strconv.Itoa(priority)),
		Conditions: []elbtypes.RuleCondition{
			{Field: aws.String("host-header"), HostHeaderConfig: &elbtypes.HostHeaderConditionConfig{Values: []string{host}}},
			{Field: aws.String("path-pattern"), PathPatternConfig: &elbtypes.PathPatternConditionConfig{Values: paths}},
		},
		Actions: []elbtypes.Action{{TargetGroupArn: aws.String(tg)}},
	}
}

// --- ECS

func (f *fakeAWS) RegisterTaskDefinition(_ context.Context, in *awsecs.RegisterTaskDefinitionInput, _ ...func(*awsecs.Options)) (*awsecs.RegisterTaskDefinitionOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	family := aws.ToString(in.Family)
	f.taskDefs[family]++
	f.registered = append(f.registered, in)
	arn := fmt.Sprintf("arn:aws:ecs:us-east-1:1:task-definition/%s:%d", family, f.taskDefs[family])
	return &awsecs.RegisterTaskDefinitionOutput{TaskDefinition: &ecstypes.TaskDefinition{TaskDefinitionArn: aws.String(arn), Family: in.Family}}, nil
}

func (f *fakeAWS) DeregisterTaskDefinition(_ context.Context, in *awsecs.DeregisterTaskDefinitionInput, _ ...func(*awsecs.Options)) (*awsecs.DeregisterTaskDefinitionOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deregs = append(f.deregs, aws.ToString(in.TaskDefinition))
	return &awsecs.DeregisterTaskDefinitionOutput{}, nil
}

func (f *fakeAWS) ListTaskDefinitions(_ context.Context, in *awsecs.ListTaskDefinitionsInput, _ ...func(*awsecs.Options)) (*awsecs.ListTaskDefinitionsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var arns []string
	// Like ECS, FamilyPrefix names one whole family.
	family := aws.ToString(in.FamilyPrefix)
	for r := 1; r <= f.taskDefs[family]; r++ {
		arns = append(arns, fmt.Sprintf("arn:aws:ecs:us-east-1:1:task-definition/%s:%d", family, r))
	}
	return &awsecs.ListTaskDefinitionsOutput{TaskDefinitionArns: arns}, nil
}

func (f *fakeAWS) ListTaskDefinitionFamilies(_ context.Context, in *awsecs.ListTaskDefinitionFamiliesInput, _ ...func(*awsecs.Options)) (*awsecs.ListTaskDefinitionFamiliesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var families []string
	for family := range f.taskDefs {
		if strings.HasPrefix(family, aws.ToString(in.FamilyPrefix)) {
			families = append(families, family)
		}
	}
	sort.Strings(families)
	return &awsecs.ListTaskDefinitionFamiliesOutput{Families: families}, nil
}

func (f *fakeAWS) CreateService(_ context.Context, in *awsecs.CreateServiceInput, _ ...func(*awsecs.Options)) (*awsecs.CreateServiceOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := aws.ToString(in.ServiceName)
	f.created = append(f.created, in)
	f.services[name] = &ecstypes.Service{
		ServiceName: in.ServiceName, Status: aws.String("ACTIVE"),
		DesiredCount: aws.ToInt32(in.DesiredCount), RunningCount: aws.ToInt32(in.DesiredCount),
		Deployments: []ecstypes.Deployment{{}}, TaskDefinition: in.TaskDefinition,
	}
	return &awsecs.CreateServiceOutput{}, nil
}

func (f *fakeAWS) UpdateService(_ context.Context, in *awsecs.UpdateServiceInput, _ ...func(*awsecs.Options)) (*awsecs.UpdateServiceOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, in)
	s, ok := f.services[aws.ToString(in.Service)]
	if !ok {
		return nil, fmt.Errorf("ServiceNotFoundException")
	}
	if in.DesiredCount != nil {
		s.DesiredCount = *in.DesiredCount
		s.RunningCount = *in.DesiredCount
	}
	if in.TaskDefinition != nil {
		s.TaskDefinition = in.TaskDefinition
	}
	return &awsecs.UpdateServiceOutput{}, nil
}

func (f *fakeAWS) DeleteService(_ context.Context, in *awsecs.DeleteServiceInput, _ ...func(*awsecs.Options)) (*awsecs.DeleteServiceOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.services[aws.ToString(in.Service)]; ok {
		s.Status = aws.String("INACTIVE")
	}
	return &awsecs.DeleteServiceOutput{}, nil
}

func (f *fakeAWS) DescribeServices(_ context.Context, in *awsecs.DescribeServicesInput, _ ...func(*awsecs.Options)) (*awsecs.DescribeServicesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := &awsecs.DescribeServicesOutput{}
	for _, name := range in.Services {
		if s, ok := f.services[name]; ok {
			out.Services = append(out.Services, *s)
		}
	}
	return out, nil
}

func (f *fakeAWS) RunTask(_ context.Context, in *awsecs.RunTaskInput, _ ...func(*awsecs.Options)) (*awsecs.RunTaskOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	arn := fmt.Sprintf("arn:aws:ecs:us-east-1:1:task/reviewapp/t%d", len(f.tasks))
	f.tasks[arn] = in
	var cmd []string
	if in.Overrides != nil {
		cmd = in.Overrides.ContainerOverrides[0].Command
	}
	f.runs = append(f.runs, cmd)
	return &awsecs.RunTaskOutput{Tasks: []ecstypes.Task{{TaskArn: aws.String(arn)}}}, nil
}

func (f *fakeAWS) DescribeTasks(_ context.Context, in *awsecs.DescribeTasksInput, _ ...func(*awsecs.Options)) (*awsecs.DescribeTasksOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	arn := in.Tasks[0]
	run := f.tasks[arn]
	// The container is the one of the registered family.
	family := aws.ToString(run.TaskDefinition)
	family = family[strings.LastIndex(family, "/")+1:]
	if i := strings.LastIndex(family, ":"); i >= 0 {
		family = family[:i]
	}
	var container string
	for _, def := range f.registered {
		if aws.ToString(def.Family) == family {
			container = aws.ToString(def.ContainerDefinitions[0].Name)
		}
	}
	code := f.taskExit(run)
	return &awsecs.DescribeTasksOutput{Tasks: []ecstypes.Task{{
		TaskArn: aws.String(arn), LastStatus: aws.String("STOPPED"),
		Containers: []ecstypes.Container{{Name: aws.String(container), ExitCode: aws.Int32(code)}},
	}}}, nil
}

// --- ELBv2

func (f *fakeAWS) CreateTargetGroup(_ context.Context, in *elb.CreateTargetGroupInput, _ ...func(*elb.Options)) (*elb.CreateTargetGroupOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createdTGs++
	arn := "arn:aws:elasticloadbalancing:us-east-1:1:targetgroup/" + aws.ToString(in.Name) + "/123"
	f.targetGroups[aws.ToString(in.Name)] = arn
	return &elb.CreateTargetGroupOutput{TargetGroups: []elbtypes.TargetGroup{{TargetGroupArn: aws.String(arn)}}}, nil
}

func (f *fakeAWS) DescribeTargetGroups(_ context.Context, in *elb.DescribeTargetGroupsInput, _ ...func(*elb.Options)) (*elb.DescribeTargetGroupsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	arn, ok := f.targetGroups[in.Names[0]]
	if !ok {
		return nil, &elbtypes.TargetGroupNotFoundException{}
	}
	return &elb.DescribeTargetGroupsOutput{TargetGroups: []elbtypes.TargetGroup{{TargetGroupArn: aws.String(arn)}}}, nil
}

func (f *fakeAWS) ModifyTargetGroupAttributes(context.Context, *elb.ModifyTargetGroupAttributesInput, ...func(*elb.Options)) (*elb.ModifyTargetGroupAttributesOutput, error) {
	return &elb.ModifyTargetGroupAttributesOutput{}, nil
}

func (f *fakeAWS) DeleteTargetGroup(_ context.Context, in *elb.DeleteTargetGroupInput, _ ...func(*elb.Options)) (*elb.DeleteTargetGroupOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, arn := range f.targetGroups {
		if arn == aws.ToString(in.TargetGroupArn) {
			delete(f.targetGroups, name)
			return &elb.DeleteTargetGroupOutput{}, nil
		}
	}
	return nil, &elbtypes.TargetGroupNotFoundException{}
}

func (f *fakeAWS) DescribeRules(context.Context, *elb.DescribeRulesInput, ...func(*elb.Options)) (*elb.DescribeRulesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &elb.DescribeRulesOutput{Rules: append([]elbtypes.Rule(nil), f.rules...)}, nil
}

func (f *fakeAWS) CreateRule(_ context.Context, in *elb.CreateRuleInput, _ ...func(*elb.Options)) (*elb.CreateRuleOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := int(aws.ToInt32(in.Priority))
	for _, r := range f.rules {
		if aws.ToString(r.Priority) == strconv.Itoa(p) {
			return nil, &elbtypes.PriorityInUseException{}
		}
	}
	f.createdRules++
	r := rule(p, in.Conditions[0].HostHeaderConfig.Values[0], in.Conditions[1].PathPatternConfig.Values, aws.ToString(in.Actions[0].TargetGroupArn))
	f.rules = append(f.rules, r)
	return &elb.CreateRuleOutput{Rules: []elbtypes.Rule{r}}, nil
}

func (f *fakeAWS) DeleteRule(_ context.Context, in *elb.DeleteRuleInput, _ ...func(*elb.Options)) (*elb.DeleteRuleOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, r := range f.rules {
		if aws.ToString(r.RuleArn) == aws.ToString(in.RuleArn) {
			f.rules = append(f.rules[:i], f.rules[i+1:]...)
			return &elb.DeleteRuleOutput{}, nil
		}
	}
	return nil, &elbtypes.RuleNotFoundException{}
}

func (f *fakeAWS) hostRules(host string) []elbtypes.Rule {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []elbtypes.Rule
	for _, r := range f.rules {
		if len(r.Conditions) > 0 && r.Conditions[0].HostHeaderConfig.Values[0] == host {
			out = append(out, r)
		}
	}
	return out
}

// --- Cloud Map

func (f *fakeAWS) ListNamespaces(_ context.Context, in *sd.ListNamespacesInput, _ ...func(*sd.Options)) (*sd.ListNamespacesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := in.Filters[0].Values[0]
	out := &sd.ListNamespacesOutput{}
	if id, ok := f.namespaces[name]; ok {
		out.Namespaces = []sdtypes.NamespaceSummary{{Name: aws.String(name), Id: aws.String(id), Arn: aws.String("arn:ns/" + id)}}
	}
	return out, nil
}

func (f *fakeAWS) CreateHttpNamespace(_ context.Context, in *sd.CreateHttpNamespaceInput, _ ...func(*sd.Options)) (*sd.CreateHttpNamespaceOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createdNamespace++
	id := "ns-" + aws.ToString(in.Name)
	f.namespaces[aws.ToString(in.Name)] = id
	return &sd.CreateHttpNamespaceOutput{OperationId: aws.String("op-" + id)}, nil
}

func (f *fakeAWS) GetOperation(_ context.Context, in *sd.GetOperationInput, _ ...func(*sd.Options)) (*sd.GetOperationOutput, error) {
	id := strings.TrimPrefix(aws.ToString(in.OperationId), "op-")
	return &sd.GetOperationOutput{Operation: &sdtypes.Operation{
		Status:  sdtypes.OperationStatusSuccess,
		Targets: map[string]string{"NAMESPACE": id},
	}}, nil
}

func (f *fakeAWS) GetNamespace(_ context.Context, in *sd.GetNamespaceInput, _ ...func(*sd.Options)) (*sd.GetNamespaceOutput, error) {
	return &sd.GetNamespaceOutput{Namespace: &sdtypes.Namespace{Arn: aws.String("arn:ns/" + aws.ToString(in.Id))}}, nil
}

func (f *fakeAWS) DeleteNamespace(_ context.Context, in *sd.DeleteNamespaceInput, _ ...func(*sd.Options)) (*sd.DeleteNamespaceOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, id := range f.namespaces {
		if id == aws.ToString(in.Id) {
			delete(f.namespaces, name)
		}
	}
	return &sd.DeleteNamespaceOutput{OperationId: aws.String("op-" + aws.ToString(in.Id))}, nil
}

// --- Secrets Manager, ECR, SSM

func (f *fakeAWS) GetSecretValue(_ context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	v, ok := f.secrets[aws.ToString(in.SecretId)]
	if !ok {
		return nil, fmt.Errorf("ResourceNotFoundException: %s", aws.ToString(in.SecretId))
	}
	return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(v)}, nil
}

func (f *fakeAWS) DescribeImages(_ context.Context, in *ecr.DescribeImagesInput, _ ...func(*ecr.Options)) (*ecr.DescribeImagesOutput, error) {
	if !f.images[aws.ToString(in.RegistryId)+"/"+aws.ToString(in.RepositoryName)+":"+aws.ToString(in.ImageIds[0].ImageTag)] {
		return nil, &ecrtypes.ImageNotFoundException{}
	}
	return &ecr.DescribeImagesOutput{}, nil
}

func (f *fakeAWS) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	v, ok := f.ssm[aws.ToString(in.Name)]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String(v)}}, nil
}

// --- S3 data, CloudWatch, Logs, Tagging

func (f *fakeAWS) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := &s3.ListObjectsV2Output{}
	for k := range f.data {
		if strings.HasPrefix(k, aws.ToString(in.Prefix)) {
			out.Contents = append(out.Contents, s3types.Object{Key: aws.String(k)})
		}
	}
	return out, nil
}

func (f *fakeAWS) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range in.Delete.Objects {
		delete(f.data, aws.ToString(o.Key))
	}
	return &s3.DeleteObjectsOutput{}, nil
}

func (f *fakeAWS) GetMetricData(context.Context, *cloudwatch.GetMetricDataInput, ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	return &cloudwatch.GetMetricDataOutput{MetricDataResults: []cwtypes.MetricDataResult{{Values: []float64{f.requests}}}}, nil
}

func (f *fakeAWS) PutMetricData(_ context.Context, in *cloudwatch.PutMetricDataInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.PutMetricDataOutput, error) {
	f.metrics = append(f.metrics, aws.ToString(in.MetricData[0].MetricName))
	return &cloudwatch.PutMetricDataOutput{}, nil
}

func (f *fakeAWS) GetLogEvents(context.Context, *cwlogs.GetLogEventsInput, ...func(*cwlogs.Options)) (*cwlogs.GetLogEventsOutput, error) {
	return &cwlogs.GetLogEventsOutput{Events: []logtypes.OutputLogEvent{{Message: aws.String("helper output")}}}, nil
}

func (f *fakeAWS) DescribeLogStreams(_ context.Context, in *cwlogs.DescribeLogStreamsInput, _ ...func(*cwlogs.Options)) (*cwlogs.DescribeLogStreamsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := &cwlogs.DescribeLogStreamsOutput{}
	for _, s := range f.streams[aws.ToString(in.LogGroupName)] {
		if strings.HasPrefix(s, aws.ToString(in.LogStreamNamePrefix)) {
			out.LogStreams = append(out.LogStreams, logtypes.LogStream{LogStreamName: aws.String(s)})
		}
	}
	return out, nil
}

func (f *fakeAWS) DeleteLogStream(_ context.Context, in *cwlogs.DeleteLogStreamInput, _ ...func(*cwlogs.Options)) (*cwlogs.DeleteLogStreamOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	group := aws.ToString(in.LogGroupName)
	var keep []string
	for _, s := range f.streams[group] {
		if s != aws.ToString(in.LogStreamName) {
			keep = append(keep, s)
		}
	}
	f.streams[group] = keep
	return &cwlogs.DeleteLogStreamOutput{}, nil
}

func (f *fakeAWS) GetResources(context.Context, *resourcegroupstaggingapi.GetResourcesInput, ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	out := &resourcegroupstaggingapi.GetResourcesOutput{}
	for _, arn := range f.tagged {
		out.ResourceTagMappingList = append(out.ResourceTagMappingList, tagtypes.ResourceTagMapping{ResourceARN: aws.String(arn)})
	}
	return out, nil
}

// fakeStore keeps states in memory with ETags, like the S3 store.
type fakeStore struct {
	states    map[int]*state.State
	etags     map[int]int
	manifests map[string]*state.Manifest
	archived  []int
}

func newFakeStore() *fakeStore {
	return &fakeStore{states: map[int]*state.State{}, etags: map[int]int{}, manifests: map[string]*state.Manifest{}}
}

func clone(st *state.State) *state.State {
	b, _ := json.Marshal(st)
	var out state.State
	_ = json.Unmarshal(b, &out)
	return &out
}

func (s *fakeStore) Get(_ context.Context, pr int) (*state.State, string, error) {
	st, ok := s.states[pr]
	if !ok {
		return nil, "", fmt.Errorf("%w: %s", state.ErrNotFound, state.ID(pr))
	}
	return clone(st), strconv.Itoa(s.etags[pr]), nil
}

func (s *fakeStore) Create(_ context.Context, st *state.State) (string, error) {
	if _, ok := s.states[st.PR]; ok {
		return "", state.ErrExists
	}
	s.states[st.PR] = clone(st)
	s.etags[st.PR] = 1
	return "1", nil
}

func (s *fakeStore) Update(_ context.Context, st *state.State, etag string) (string, error) {
	if strconv.Itoa(s.etags[st.PR]) != etag {
		return "", state.ErrConflict
	}
	s.states[st.PR] = clone(st)
	s.etags[st.PR]++
	return strconv.Itoa(s.etags[st.PR]), nil
}

func (s *fakeStore) List(context.Context) ([]*state.State, error) {
	var out []*state.State
	for _, pr := range sortedInts(s.states) {
		out = append(out, clone(s.states[pr]))
	}
	return out, nil
}

func (s *fakeStore) CheckLimit(ctx context.Context, limit int) error {
	states, _ := s.List(ctx)
	if n := state.CountActive(states); n >= limit {
		return fmt.Errorf("%w: %d of %d", state.ErrLimit, n, limit)
	}
	return nil
}

func (s *fakeStore) GetManifest(_ context.Context, pr int, ref state.ManifestRef) (*state.Manifest, error) {
	m, ok := s.manifests[ref.VersionID]
	if !ok {
		return nil, fmt.Errorf("no manifest version %s", ref.VersionID)
	}
	return m, nil
}

func (s *fakeStore) Archive(_ context.Context, pr int) (string, error) {
	delete(s.states, pr)
	s.archived = append(s.archived, pr)
	return "archive/" + state.ID(pr), nil
}

func sortedInts[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// fakeDB records the statements the controller runs. QueryString renders
// format() calls by substituting the arguments, which is enough to check
// which statements ran.
type fakeDB struct {
	roles  map[string]bool
	dbs    map[string]bool
	stmts  []string
	closed int
}

func newFakeDB() *fakeDB { return &fakeDB{roles: map[string]bool{}, dbs: map[string]bool{}} }

func (d *fakeDB) connect(context.Context, string) (DB, error) { return d, nil }

func (d *fakeDB) Exec(_ context.Context, sql string, _ ...any) error {
	d.stmts = append(d.stmts, sql)
	f := strings.Fields(sql)
	switch {
	case strings.HasPrefix(sql, "CREATE ROLE"):
		d.roles[f[2]] = true
	case strings.HasPrefix(sql, "CREATE DATABASE"):
		d.dbs[f[2]] = true
	case strings.HasPrefix(sql, "DROP DATABASE"):
		delete(d.dbs, f[4])
	}
	return nil
}

func (d *fakeDB) QueryBool(_ context.Context, sql string, args ...any) (bool, error) {
	name := args[0].(string)
	if strings.Contains(sql, "pg_roles") {
		return d.roles[name], nil
	}
	return d.dbs[name], nil
}

func (d *fakeDB) QueryString(_ context.Context, sql string, args ...any) (string, error) {
	tmpl := sql[strings.Index(sql, "'")+1:]
	tmpl = tmpl[:strings.Index(tmpl, "'")]
	for _, a := range args {
		tmpl = strings.Replace(tmpl, "%I", a.(string), 1)
		tmpl = strings.Replace(tmpl, "%L", "'"+a.(string)+"'", 1)
	}
	tmpl = strings.Replace(tmpl, "%I", "admin", 1)
	return tmpl, nil
}

func (d *fakeDB) Close(context.Context) error { d.closed++; return nil }

type fakeComments struct{ bodies []string }

func (c *fakeComments) Comment(_ context.Context, _ int, body string) error {
	c.bodies = append(c.bodies, body)
	return nil
}

// harness wires a controller to the fakes.
type harness struct {
	aws      *fakeAWS
	store    *fakeStore
	db       *fakeDB
	comments *fakeComments
	c        *Controller
	now      time.Time
}

func newHarness() *harness {
	h := &harness{aws: newFakeAWS(), store: newFakeStore(), db: newFakeDB(), comments: &fakeComments{}}
	h.now = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	inf := &infra.Infra{}
	inf.Cluster.ARN = clusterARN
	inf.Network.VPCID = "vpc-1"
	inf.Network.PrivateSubnetIDs = []string{"subnet-1", "subnet-2"}
	inf.Network.SecurityGroupIDs = map[string]string{"namespace": "sg-ns", "msk_client": "sg-msk"}
	inf.ALB.ARN = "arn:aws:elasticloadbalancing:us-east-1:1:loadbalancer/app/alb/abc"
	inf.ALB.HTTPSListenerARN = listenerARN
	inf.MSK.BootstrapBrokers = "b:9098"
	inf.Buckets.Data = "data"
	inf.Buckets.State = "state"
	inf.Secrets.DB = "reviewapp/db"
	inf.Secrets.RDSAdmin = "reviewapp/rds-admin"
	inf.Secrets.SeedUsers = "reviewapp/seed-users"
	inf.UnitsSSM = "/reviewapp/units/"

	h.c = &Controller{
		ECS: h.aws, ELB: h.aws, Discovery: h.aws, Secrets: h.aws, ECR: h.aws,
		Data: h.aws, Metrics: h.aws, Logs: h.aws, Tagging: h.aws, SSM: h.aws,
		Store:    h.store,
		Connect:  h.db.connect,
		Comments: h.comments,
		Infra:    inf,
		Blueprint: &infra.Blueprint{
			DBOwnerRole:          "reviewapp_owner",
			ServiceConnectDomain: "platform.internal",
			Routes: map[string]infra.Route{
				"shell":          {Port: 3002, HealthCheckPath: "/", Rules: [][]string{{"/*"}}},
				"subgraph-users": {Port: 4003, HealthCheckPath: "/health", Rules: [][]string{{"/users", "/users/*"}}},
			},
		},
		Domain:       "reviewapps.example.com",
		Region:       "us-east-1",
		Limit:        10,
		Actor:        "octocat",
		Out:          io.Discard,
		Now:          func() time.Time { return h.now },
		PollInterval: time.Millisecond,
	}
	return h
}

const ecrHost = "1.dkr.ecr.us-east-1.amazonaws.com"

// manifest stores a manifest for pr and returns its reference. Its images
// exist in ECR unless missing lists them.
func (h *harness) manifest(pr int, sha string, missing ...string) state.ManifestRef {
	m := &state.Manifest{
		PR:         pr,
		SHA:        sha,
		ToolsImage: ecrHost + "/reviewapp-tools:pr-" + sha,
		Images: map[string]string{
			"shell":          ecrHost + "/shell:pr-" + sha,
			"subgraph-users": ecrHost + "/subgraph-users:pr-" + sha,
			"cron-alerts":    ecrHost + "/cron-alerts:pr-" + sha,
		},
		Units: map[string]state.UnitSpec{
			"shell":          {CPU: 256, Memory: 512, Ports: []int{3002}, Environment: map[string]string{"APP_BASE_URL": "{url}"}, Secrets: map[string]string{"AUTH0_SECRET": "arn:secret:shell/reviewapp:AUTH0_SECRET::"}},
			"subgraph-users": {CPU: 256, Memory: 512, Ports: []int{4003}},
			"cron-alerts":    {CPU: 256, Memory: 512},
		},
	}
	for _, image := range append([]string{m.ToolsImage}, m.Images["shell"], m.Images["subgraph-users"], m.Images["cron-alerts"]) {
		registry, repo, tag, _ := ecrImage(image)
		h.aws.images[registry+"/"+repo+":"+tag] = true
	}
	for _, image := range missing {
		registry, repo, tag, _ := ecrImage(image)
		delete(h.aws.images, registry+"/"+repo+":"+tag)
	}
	id := fmt.Sprintf("v-%d-%s", pr, sha)
	h.store.manifests[id] = m
	return state.ManifestRef{VersionID: id, SHA256: "x"}
}

var ecstypesService = ecstypes.Service{ServiceName: aws.String("pr-7-shell"), Status: aws.String("ACTIVE")}

// perPRMetrics reports requests only for target groups whose name starts
// with active.
type perPRMetrics struct {
	*fakeAWS
	active string
}

func (m *perPRMetrics) GetMetricData(_ context.Context, in *cloudwatch.GetMetricDataInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	out := &cloudwatch.GetMetricDataOutput{}
	for _, q := range in.MetricDataQueries {
		v := 0.0
		for _, d := range q.MetricStat.Metric.Dimensions {
			if aws.ToString(d.Name) == "TargetGroup" && strings.HasPrefix(aws.ToString(d.Value), "targetgroup/"+m.active) {
				v = 5
			}
		}
		out.MetricDataResults = append(out.MetricDataResults, cwtypes.MetricDataResult{Values: []float64{v}})
	}
	return out, nil
}
