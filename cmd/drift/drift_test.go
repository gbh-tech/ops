package drift

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ops/pkg/config"
	pkgdrift "ops/pkg/drift"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/spf13/viper"
)

const (
	registry  = "111122223333.dkr.ecr.us-east-1.amazonaws.com"
	newDigest = "sha256:new"
)

// fakeAWS serves an api service that runs the 1.0.0 image and a worker
// function that still runs an older digest. Every tag resolves to newDigest.
type fakeAWS struct {
	cluster  string
	function string
}

func (f *fakeAWS) DescribeServices(_ context.Context, in *awsecs.DescribeServicesInput, _ ...func(*awsecs.Options)) (*awsecs.DescribeServicesOutput, error) {
	f.cluster = aws.ToString(in.Cluster)
	return &awsecs.DescribeServicesOutput{Services: []ecstypes.Service{{
		Status:         aws.String("ACTIVE"),
		TaskDefinition: aws.String("api-stage:1"),
		DesiredCount:   1,
		RunningCount:   1,
		Deployments:    []ecstypes.Deployment{{RolloutState: ecstypes.DeploymentRolloutStateCompleted}},
	}}}, nil
}

func (f *fakeAWS) DescribeTaskDefinition(context.Context, *awsecs.DescribeTaskDefinitionInput, ...func(*awsecs.Options)) (*awsecs.DescribeTaskDefinitionOutput, error) {
	return &awsecs.DescribeTaskDefinitionOutput{TaskDefinition: &ecstypes.TaskDefinition{
		ContainerDefinitions: []ecstypes.ContainerDefinition{{Name: aws.String("api"), Image: aws.String(registry + "/platform/api:1.0.0")}},
	}}, nil
}

func (f *fakeAWS) ListTasks(context.Context, *awsecs.ListTasksInput, ...func(*awsecs.Options)) (*awsecs.ListTasksOutput, error) {
	return &awsecs.ListTasksOutput{TaskArns: []string{"task-1"}}, nil
}

func (f *fakeAWS) DescribeTasks(context.Context, *awsecs.DescribeTasksInput, ...func(*awsecs.Options)) (*awsecs.DescribeTasksOutput, error) {
	return &awsecs.DescribeTasksOutput{Tasks: []ecstypes.Task{{Containers: []ecstypes.Container{
		{Name: aws.String("api"), ImageDigest: aws.String(newDigest)},
	}}}}, nil
}

func (f *fakeAWS) DescribeImages(context.Context, *awsecr.DescribeImagesInput, ...func(*awsecr.Options)) (*awsecr.DescribeImagesOutput, error) {
	return &awsecr.DescribeImagesOutput{ImageDetails: []ecrtypes.ImageDetail{{ImageDigest: aws.String(newDigest)}}}, nil
}

func (f *fakeAWS) GetFunction(_ context.Context, in *awslambda.GetFunctionInput, _ ...func(*awslambda.Options)) (*awslambda.GetFunctionOutput, error) {
	f.function = aws.ToString(in.FunctionName)
	return &awslambda.GetFunctionOutput{
		Configuration: &lambdatypes.FunctionConfiguration{LastUpdateStatus: lambdatypes.LastUpdateStatusSuccessful},
		Code:          &lambdatypes.FunctionCodeLocation{ResolvedImageUri: aws.String(registry + "/platform/worker@sha256:old")},
	}, nil
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repository creates a repository with an ECS unit, a Lambda unit and a
// migrator, moves into it, and returns the fake AWS the command will read.
func repository(t *testing.T, manifest string) *fakeAWS {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, ".ops", "config.yaml"), strings.Join([]string{
		"provider: aws",
		"deployment: ecs",
		"repo_mode: mono",
		"app_dirs: [services, functions]",
		"aws: {region: us-east-1, account_id: '111122223333'}",
		"registry: {repository: 'platform/{service}'}",
		"ecs: {cluster: 'platform-{env}', defaults: {replicas: 1}}",
		"lambda: {function_name: 'platform-{service}-{env}'}",
		"",
	}, "\n"))
	for dir, kind := range map[string]string{"services/api": "ecs", "services/migrate": "migrator", "functions/worker": "lambda"} {
		name := filepath.Base(dir)
		write(t, filepath.Join(root, dir, "deploy", "config.toml"), "[global]\nname = \""+name+"\"\nkind = \""+kind+"\"\n")
		write(t, filepath.Join(root, dir, "Dockerfile"), "FROM scratch\n")
	}
	write(t, filepath.Join(root, "manifest.json"), manifest)

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	fake := &fakeAWS{}
	oldChecker := newChecker
	newChecker = func(context.Context, *config.OpsConfig) pkgdrift.Checker {
		return pkgdrift.Checker{ECS: fake, ECR: fake, Lambda: fake}
	}
	oldEnv, oldManifest, oldFormat, oldExitCode := env, manifestPath, format, exitCode
	env, manifestPath, format, exitCode = "stage", "manifest.json", "table", false
	viper.Reset()
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
		newChecker = oldChecker
		env, manifestPath, format, exitCode = oldEnv, oldManifest, oldFormat, oldExitCode
		Command.SetOut(nil)
		viper.Reset()
	})
	return fake
}

const stageManifest = `{"schemaVersion":3,"environment":"stage","applications":{"api":"1.0.0","worker":"1.0.0","migrate":"1.0.0","removed-unit":"1.0.0"}}`

func runCommand(t *testing.T) (string, error) {
	t.Helper()
	var output bytes.Buffer
	Command.SetOut(&output)
	err := Command.RunE(Command, nil)
	return output.String(), err
}

func TestTableListsEveryComparedUnit(t *testing.T) {
	fake := repository(t, stageManifest)

	got, err := runCommand(t)
	if err != nil {
		t.Fatal(err)
	}
	want := "api\tecs\tin-sync\t\n" +
		"worker\tlambda\tout-of-sync\tfunction runs image \"" + registry + "/platform/worker@sha256:old\", expected digest sha256:new\n"
	if got != want {
		t.Fatalf("output =\n%s\nwant\n%s", got, want)
	}
	if fake.cluster != "platform-stage" || fake.function != "platform-worker-stage" {
		t.Fatalf("read cluster %q and function %q, want platform-stage and platform-worker-stage", fake.cluster, fake.function)
	}
}

func TestIDsListsOnlyWhatIsNotInSync(t *testing.T) {
	repository(t, stageManifest)
	format = "ids"

	got, err := runCommand(t)
	if err != nil {
		t.Fatal(err)
	}
	if got != "worker\n" {
		t.Fatalf("output = %q, want %q", got, "worker\n")
	}
}

func TestIDsIsAnEmptyLineWhenEverythingIsInSync(t *testing.T) {
	repository(t, `{"environment":"stage","applications":{"api":"1.0.0"}}`)
	format, exitCode = "ids", true

	got, err := runCommand(t)
	if err != nil {
		t.Fatal(err)
	}
	if got != "\n" {
		t.Fatalf("output = %q, want an empty line", got)
	}
}

func TestJSONCarriesEveryField(t *testing.T) {
	repository(t, stageManifest)
	format = "json"

	got, err := runCommand(t)
	if err != nil {
		t.Fatal(err)
	}
	var results []pkgdrift.Result
	if err := json.Unmarshal([]byte(got), &results); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, got)
	}
	if len(results) != 2 ||
		results[0] != (pkgdrift.Result{ID: "api", Kind: "ecs", Tag: "1.0.0", Status: pkgdrift.InSync}) ||
		results[1].ID != "worker" || results[1].Status != pkgdrift.OutOfSync || results[1].Reason == "" {
		t.Fatalf("results = %+v", results)
	}
}

func TestRefusesAManifestWithoutApplications(t *testing.T) {
	for name, manifest := range map[string]string{
		"missing":       `{"environment":"stage","images":{"api":"1.0.0"}}`,
		"null":          `{"environment":"stage","applications":null}`,
		"null document": `null`,
	} {
		t.Run(name, func(t *testing.T) {
			repository(t, manifest)
			// A gate must not pass on a manifest that names nothing to compare.
			exitCode = true

			_, err := runCommand(t)
			if err == nil || !strings.Contains(err.Error(), `has no "applications" object`) {
				t.Fatalf("err = %v, want the missing applications reported", err)
			}
		})
	}
}

func TestJSONIsAnEmptyArrayWhenTheManifestNamesNoUnit(t *testing.T) {
	repository(t, `{"applications":{}}`)
	format = "json"

	got, err := runCommand(t)
	if err != nil {
		t.Fatal(err)
	}
	if got != "[]\n" {
		t.Fatalf("output = %q, want %q", got, "[]\n")
	}
}

func TestExitCodeFailsAfterPrintingWhenAUnitIsNotInSync(t *testing.T) {
	repository(t, stageManifest)
	exitCode = true

	got, err := runCommand(t)
	if err == nil || err.Error() != "1 of 2 units do not run their manifest image in stage: worker" {
		t.Fatalf("err = %v, want the unit that is not in sync named", err)
	}
	if !strings.Contains(got, "worker\tlambda\tout-of-sync") {
		t.Fatalf("output = %q, want the table printed before the failure", got)
	}
	if !Command.SilenceUsage {
		t.Fatal("usage is printed on a failure that is not about how the command was called")
	}
}

func TestRefusesAManifestOfAnotherEnvironment(t *testing.T) {
	repository(t, `{"environment":"dev","applications":{"api":"1.0.0"}}`)

	_, err := runCommand(t)
	if err == nil || !strings.Contains(err.Error(), `is for environment "dev", not "stage"`) {
		t.Fatalf("err = %v, want the environment mismatch reported", err)
	}
}

func TestReportsBadInputs(t *testing.T) {
	tests := []struct {
		name   string
		change func()
		want   string
	}{
		{name: "format", change: func() { format = "yaml" }, want: `unsupported format "yaml"`},
		{name: "missing manifest", change: func() { manifestPath = "nope.json" }, want: "reading manifest"},
		{name: "invalid manifest", change: func() { manifestPath = ".ops/config.yaml" }, want: "parsing manifest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository(t, stageManifest)
			tt.change()

			_, err := runCommand(t)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestEnvAndManifestAreRequired(t *testing.T) {
	for _, name := range []string{"env", "manifest"} {
		flag := Command.Flags().Lookup(name)
		if flag == nil || flag.Annotations["cobra_annotation_bash_completion_one_required_flag"] == nil {
			t.Fatalf("flag --%s is not required", name)
		}
	}
}
