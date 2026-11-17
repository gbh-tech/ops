package drift

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ops/pkg/catalog"
	"ops/pkg/config"
)

func writeUnit(t *testing.T, dir, body string) catalog.Unit {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return catalog.Unit{Config: path}
}

func testConfig() *config.OpsConfig {
	return &config.OpsConfig{
		Provider: "aws",
		AWS:      config.AWSConfig{AccountId: "111122223333", Region: "us-east-1"},
		ECS:      config.ECSConfig{Cluster: "platform-{env}", Defaults: config.ECSDefaults{Replicas: 1}},
		Registry: config.RegistryConfig{Repository: "platform/{service}"},
		Lambda:   config.LambdaConfig{FunctionName: "platform-{service}-{env}"},
	}
}

func unit(t *testing.T, id, kind, body string) catalog.Unit {
	t.Helper()
	u := writeUnit(t, t.TempDir(), body)
	u.ID, u.Kind = id, kind
	return u
}

func TestTargetsResolvesECSAndLambdaUnits(t *testing.T) {
	units := []catalog.Unit{
		unit(t, "worker", "lambda", "[global]\nname = \"worker\"\nkind = \"lambda\"\n"),
		unit(t, "api", "ecs", "[global]\nname = \"api\"\nkind = \"ecs\"\n[stage]\nreplicas = 2\n"),
	}
	tags := map[string]string{"api": "1.0.0-abc1234", "worker": "2.0.0-def5678"}

	got, err := Targets(testConfig(), units, tags, "stage")
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{
			ID: "api", Kind: KindECS, Tag: "1.0.0-abc1234",
			Image:      "111122223333.dkr.ecr.us-east-1.amazonaws.com/platform/api:1.0.0-abc1234",
			Repository: "platform/api",
			Cluster:    "platform-stage", Service: "api", Container: "api", Replicas: 2,
		},
		{
			ID: "worker", Kind: KindLambda, Tag: "2.0.0-def5678",
			Repository: "platform/worker",
			Function:   "platform-worker-stage",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Targets() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestTargetsLeavesOutWhatHasNothingToCompare(t *testing.T) {
	units := []catalog.Unit{
		unit(t, "api", "ecs", "[global]\nname = \"api\"\nkind = \"ecs\"\n"),
		unit(t, "migrate", "migrator", "[global]\nname = \"migrate\"\nkind = \"migrator\"\n"),
		unit(t, "tools", "image-only", "[global]\nname = \"tools\"\nkind = \"image-only\"\n"),
		unit(t, "unlisted", "ecs", "[global]\nname = \"unlisted\"\nkind = \"ecs\"\n"),
	}
	tags := map[string]string{"api": "1", "migrate": "1", "tools": "1", "removed-unit": "1"}

	got, err := Targets(testConfig(), units, tags, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "api" {
		t.Fatalf("Targets() = %+v, want only api", got)
	}
}

func TestTargetsFollowsTheServiceNameAndReplicasOfTheEnvironment(t *testing.T) {
	units := []catalog.Unit{unit(t, "api", "ecs",
		"[global]\nname = \"api\"\nkind = \"ecs\"\n[stage]\nappend_environment = true\nreplicas = 0\n")}

	got, err := Targets(testConfig(), units, map[string]string{"api": "1"}, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Service != "api-stage" || got[0].Replicas != 0 {
		t.Fatalf("service, replicas = %q, %d, want api-stage, 0", got[0].Service, got[0].Replicas)
	}

	got, err = Targets(testConfig(), units, map[string]string{"api": "1"}, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Service != "api" || got[0].Replicas != 1 || got[0].Cluster != "platform-dev" {
		t.Fatalf("dev target = %+v, want service api with the default replica in platform-dev", got[0])
	}
}

func TestTargetsComparesAnOutsideImageByReference(t *testing.T) {
	units := []catalog.Unit{unit(t, "proxy", "ecs",
		"[global]\nname = \"proxy\"\nkind = \"ecs\"\nimage = \"docker.io/library/nginx:1.27\"\n")}

	got, err := Targets(testConfig(), units, map[string]string{"proxy": "9.9.9"}, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Image != "docker.io/library/nginx:1.27" || got[0].Repository != "" {
		t.Fatalf("image, repository = %q, %q, want the configured image and no repository", got[0].Image, got[0].Repository)
	}
}

func TestTargetsNamesARegistryInAnotherAccount(t *testing.T) {
	cfg := testConfig()
	cfg.Registry.URL = "444455556666.dkr.ecr.us-east-1.amazonaws.com"
	units := []catalog.Unit{unit(t, "api", "ecs", "[global]\nname = \"api\"\nkind = \"ecs\"\n")}

	got, err := Targets(cfg, units, map[string]string{"api": "1"}, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].RegistryID != "444455556666" || got[0].Repository != "platform/api" {
		t.Fatalf("registry id, repository = %q, %q, want 444455556666, platform/api", got[0].RegistryID, got[0].Repository)
	}
}

func TestTargetsRequiresTheFunctionNameForLambdaUnits(t *testing.T) {
	cfg := testConfig()
	cfg.Lambda.FunctionName = ""
	units := []catalog.Unit{unit(t, "worker", "lambda", "[global]\nname = \"worker\"\nkind = \"lambda\"\n")}

	_, err := Targets(cfg, units, map[string]string{"worker": "1"}, "stage")
	if err == nil || !strings.Contains(err.Error(), "lambda.function_name is not set") {
		t.Fatalf("err = %v, want it to name lambda.function_name", err)
	}

	// The setting is only needed when a Lambda unit is compared.
	if _, err := Targets(cfg, units, map[string]string{}, "stage"); err != nil {
		t.Fatalf("err = %v, want none when no Lambda unit is named", err)
	}
}

func TestTargetsReportsAnInvalidAppConfig(t *testing.T) {
	units := []catalog.Unit{unit(t, "api", "ecs", "[global]\nkind = \"ecs\"\n")}

	if _, err := Targets(testConfig(), units, map[string]string{"api": "1"}, "stage"); err == nil {
		t.Fatal("err = nil, want the missing name reported")
	}
}
