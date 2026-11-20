package drift

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/spf13/pflag"
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

func (f *fakeAWS) BatchGetImage(context.Context, *awsecr.BatchGetImageInput, ...func(*awsecr.Options)) (*awsecr.BatchGetImageOutput, error) {
	return nil, errors.New("no image is an index")
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
	viper.Reset()
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
		newChecker = oldChecker
		viper.Reset()
	})
	return fake
}

const stageManifest = `{"schemaVersion":3,"environment":"stage","applications":{"api":"1.0.0","worker":"1.0.0","migrate":"1.0.0","removed-unit":"1.0.0"}}`

// runCommand runs ops drift with args, through the flag parser, against the
// stage manifest of the repository, and returns what it printed.
func runCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	// The command is a package variable, so flags keep the last run's values.
	Command.Flags().VisitAll(func(flag *pflag.Flag) {
		if err := flag.Value.Set(flag.DefValue); err != nil {
			t.Fatal(err)
		}
		flag.Changed = false
	})
	var output bytes.Buffer
	Command.SetOut(&output)
	Command.SetErr(&bytes.Buffer{})
	Command.SetArgs(args)
	t.Cleanup(func() {
		Command.SetOut(nil)
		Command.SetErr(nil)
		Command.SetArgs(nil)
	})
	err := Command.ExecuteContext(context.Background())
	return output.String(), err
}

var stage = []string{"--env", "stage", "--manifest", "manifest.json"}

func TestTableListsEveryComparedUnit(t *testing.T) {
	fake := repository(t, stageManifest)

	got, err := runCommand(t, stage...)
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

	got, err := runCommand(t, append(stage, "--format", "ids")...)
	if err != nil {
		t.Fatal(err)
	}
	if got != "worker\n" {
		t.Fatalf("output = %q, want %q", got, "worker\n")
	}
}

func TestIDsIsAnEmptyLineWhenEverythingIsInSync(t *testing.T) {
	repository(t, `{"environment":"stage","applications":{"api":"1.0.0"}}`)

	got, err := runCommand(t, append(stage, "--format", "ids", "--exit-code")...)
	if err != nil {
		t.Fatal(err)
	}
	if got != "\n" {
		t.Fatalf("output = %q, want an empty line", got)
	}
}

func TestJSONCarriesEveryField(t *testing.T) {
	repository(t, stageManifest)

	got, err := runCommand(t, "-e", "stage", "-m", "manifest.json", "--format", "json")
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
			_, err := runCommand(t, append(stage, "--exit-code")...)
			if err == nil || !strings.Contains(err.Error(), `has no "applications" object`) {
				t.Fatalf("err = %v, want the missing applications reported", err)
			}
		})
	}
}

func TestJSONIsAnEmptyArrayWhenTheManifestNamesNoUnit(t *testing.T) {
	repository(t, `{"applications":{"migrate":"1.0.0","removed-unit":"1.0.0"}}`)

	got, err := runCommand(t, append(stage, "--format", "json")...)
	if err != nil {
		t.Fatal(err)
	}
	if got != "[]\n" {
		t.Fatalf("output = %q, want %q", got, "[]\n")
	}
}

func TestExitCodeFailsWhenNothingWasCompared(t *testing.T) {
	repository(t, `{"applications":{"migrate":"1.0.0","removed-unit":"1.0.0"}}`)

	_, err := runCommand(t, append(stage, "--exit-code")...)
	if err == nil || !strings.Contains(err.Error(), "nothing was compared") {
		t.Fatalf("err = %v, want a gate that compared nothing to fail", err)
	}
}

func TestExitCodeFailsAfterPrintingWhenAUnitIsNotInSync(t *testing.T) {
	repository(t, stageManifest)

	got, err := runCommand(t, append(stage, "--exit-code")...)
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

	_, err := runCommand(t, stage...)
	if err == nil || !strings.Contains(err.Error(), `is for environment "dev", not "stage"`) {
		t.Fatalf("err = %v, want the environment mismatch reported", err)
	}
}

func TestReportsBadInputs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "format", args: append(stage, "--format", "yaml"), want: `unsupported format "yaml"`},
		{name: "missing manifest", args: []string{"--env", "stage", "--manifest", "nope.json"}, want: "reading manifest"},
		{name: "invalid manifest", args: []string{"--env", "stage", "--manifest", ".ops/config.yaml"}, want: "parsing manifest"},
		{name: "empty tag", args: []string{"--env", "stage", "--manifest", "empty-tag.json"}, want: `names no tag for unit "api"`},
		{name: "no env", args: []string{"--manifest", "manifest.json"}, want: `required flag(s) "env" not set`},
		{name: "no manifest", args: []string{"--env", "stage"}, want: `required flag(s) "manifest" not set`},
		{name: "argument", args: append(stage, "extra"), want: "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository(t, stageManifest)
			write(t, "empty-tag.json", `{"applications":{"api":""}}`)

			_, err := runCommand(t, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
