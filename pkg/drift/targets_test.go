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
			Repository: "platform/api", ImageTag: "1.0.0-abc1234",
			Cluster: "platform-stage", Service: "api", Container: "api",
		},
		{
			ID: "worker", Kind: KindLambda, Tag: "2.0.0-def5678",
			Repository: "platform/worker", ImageTag: "2.0.0-def5678",
			Function: "platform-worker-stage",
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

func TestTargetsFollowsTheServiceNameOfTheEnvironment(t *testing.T) {
	units := []catalog.Unit{unit(t, "api", "ecs",
		"[global]\nname = \"api\"\nkind = \"ecs\"\n[stage]\nappend_environment = true\n")}

	got, err := Targets(testConfig(), units, map[string]string{"api": "1"}, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Service != "api-stage" || got[0].Cluster != "platform-stage" {
		t.Fatalf("service, cluster = %q, %q, want api-stage, platform-stage", got[0].Service, got[0].Cluster)
	}

	// An environment without a section of its own uses [global], as a deployment does.
	got, err = Targets(testConfig(), units, map[string]string{"api": "1"}, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Service != "api" || got[0].Cluster != "platform-dev" {
		t.Fatalf("dev target = %+v, want service api in platform-dev", got[0])
	}
}

func TestTargetsRefusesAnEmptyTag(t *testing.T) {
	units := []catalog.Unit{
		unit(t, "api", "ecs", "[global]\nname = \"api\"\nkind = \"ecs\"\n"),
		unit(t, "migrate", "migrator", "[global]\nname = \"migrate\"\nkind = \"migrator\"\n"),
	}

	_, err := Targets(testConfig(), units, map[string]string{"api": ""}, "stage")
	if err == nil || !strings.Contains(err.Error(), `names no tag for unit "api"`) {
		t.Fatalf("err = %v, want the unit without a tag named", err)
	}
	// A unit that is not compared may have any entry.
	if _, err := Targets(testConfig(), units, map[string]string{"migrate": ""}, "stage"); err != nil {
		t.Fatalf("err = %v, want none for a unit that is not compared", err)
	}
}

func TestTargetsRequiresTheClusterForECSUnits(t *testing.T) {
	cfg := testConfig()
	cfg.ECS.Cluster = ""
	units := []catalog.Unit{unit(t, "api", "ecs", "[global]\nname = \"api\"\nkind = \"ecs\"\n")}

	_, err := Targets(cfg, units, map[string]string{"api": "1"}, "stage")
	if err == nil || !strings.Contains(err.Error(), "ecs.cluster is not set") {
		t.Fatalf("err = %v, want it to name ecs.cluster", err)
	}
}

func TestRegistryRegion(t *testing.T) {
	tests := map[string]string{
		"111122223333.dkr.ecr.eu-west-1.amazonaws.com":     "eu-west-1",
		"111122223333.dkr.ecr.us-gov-west-1.amazonaws.com": "us-gov-west-1",
		"registry.example.com":                             "",
		"":                                                 "",
	}
	for registry, want := range tests {
		if got := RegistryRegion(registry); got != want {
			t.Fatalf("RegistryRegion(%q) = %q, want %q", registry, got, want)
		}
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

func TestTargetsResolvesAnImagePinnedInTheRegistryFromItsOwnReference(t *testing.T) {
	const registry = "111122223333.dkr.ecr.us-east-1.amazonaws.com"
	tests := []struct {
		name            string
		image           string
		repository, tag string
		digest          string
	}{
		{name: "tag", image: registry + "/platform/api:stable", repository: "platform/api", tag: "stable"},
		{name: "digest", image: registry + "/platform/api@sha256:abc", repository: "platform/api", digest: "sha256:abc"},
		{name: "tag and digest", image: registry + "/platform/api:stable@sha256:abc", repository: "platform/api", digest: "sha256:abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// resolveImage keeps a tagged image as it is; a digest counts as a tag for it.
			units := []catalog.Unit{unit(t, "api", "ecs", "[global]\nname = \"api\"\nkind = \"ecs\"\nimage = \""+tt.image+"\"\n")}

			got, err := Targets(testConfig(), units, map[string]string{"api": "9.9.9"}, "stage")
			if err != nil {
				t.Fatal(err)
			}
			if got[0].Image != tt.image || got[0].Repository != tt.repository || got[0].ImageTag != tt.tag || got[0].ImageDigest != tt.digest {
				t.Fatalf("target = %+v, want image %q in %q at tag %q, digest %q", got[0], tt.image, tt.repository, tt.tag, tt.digest)
			}
		})
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
