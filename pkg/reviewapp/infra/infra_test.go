package infra

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeSSM map[string]string

func (f fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	v, ok := f[aws.ToString(in.Name)]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String(v)}}, nil
}

const infraJSON = `{
  "cluster": {"name": "c", "arn": "arn:aws:ecs:us-east-1:1:cluster/c"},
  "network": {"vpc_id": "vpc-1", "private_subnet_ids": ["subnet-1", "subnet-2"], "security_group_ids": {"namespace": "sg-ns"}},
  "alb": {"arn": "arn:aws:elasticloadbalancing:us-east-1:1:loadbalancer/app/alb/abc", "dns_name": "alb.example", "https_listener_arn": "listener"},
  "msk": {"cluster_arn": "msk", "bootstrap_brokers": "b:9098"},
  "buckets": {"data": "data", "state": "state"},
  "rds": {"endpoint": "db:5432"},
  "secrets": {"db": "reviewapp/db", "rds_admin": "reviewapp/rds-admin", "seed_users": "reviewapp/seed-users", "github_app": "reviewapp/github-app"},
  "units_ssm": "/reviewapp/units/",
  "controller": {"task_definition": "reviewapp-controller"},
  "blueprint_ssm": "/reviewapp/blueprint"
}`

func TestLoad(t *testing.T) {
	inf, err := Load(context.Background(), fakeSSM{"/reviewapp/infra": infraJSON}, "/reviewapp/infra")
	if err != nil {
		t.Fatal(err)
	}
	if inf.Cluster.ARN != "arn:aws:ecs:us-east-1:1:cluster/c" || inf.Buckets.State != "state" {
		t.Fatalf("unexpected infra: %+v", inf)
	}
	if len(inf.Network.PrivateSubnetIDs) != 2 || inf.Secrets.DB != "reviewapp/db" {
		t.Fatalf("unexpected network or secrets: %+v", inf)
	}
}

func TestLoadMissingParameter(t *testing.T) {
	_, err := Load(context.Background(), fakeSSM{}, "/reviewapp/infra")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestLoadRequiredFields(t *testing.T) {
	_, err := Load(context.Background(), fakeSSM{"/p": `{"cluster": {"arn": "x"}}`}, "/p")
	if err == nil {
		t.Fatal("want an error for an incomplete parameter")
	}
	for _, field := range []string{"alb.arn", "buckets.state", "controller.task_definition", "network.private_subnet_ids"} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("err = %v, want it to name %s", err, field)
		}
	}
}

func TestLoadUnit(t *testing.T) {
	ssmc := fakeSSM{
		"/reviewapp/infra":                      infraJSON,
		"/reviewapp/units/reviewapp-controller": `{"security_group": "sg-c", "task_exec_role": "exec", "task_role": "task", "log_group": "/ecs/c"}`,
		"/reviewapp/units/broken":               `{"security_group": "sg-b"}`,
	}
	inf, err := Load(context.Background(), ssmc, "/reviewapp/infra")
	if err != nil {
		t.Fatal(err)
	}
	u, err := inf.LoadUnit(context.Background(), ssmc, ControllerUnit)
	if err != nil {
		t.Fatal(err)
	}
	if u.SecurityGroup != "sg-c" || u.LogGroup != "/ecs/c" {
		t.Fatalf("unexpected unit: %+v", u)
	}
	if _, err := inf.LoadUnit(context.Background(), ssmc, "broken"); err == nil {
		t.Fatal("want an error for an incomplete unit")
	}
}

func TestLoadBlueprint(t *testing.T) {
	ssmc := fakeSSM{
		"/reviewapp/infra": infraJSON,
		"/reviewapp/blueprint": `{
  "db_owner_role": "reviewapp_owner",
  "service_connect_domain": "platform.internal",
  "routes": {
    "shell": {"port": 3002, "health_check_path": "/", "rules": [["/*"]]},
    "apollo-router": {"port": 4000, "health_check_path": "/health", "health_check_port": 8088, "rules": [["/graphql", "/graphql/*"]]}
  }
}`,
	}
	inf, err := Load(context.Background(), ssmc, "/reviewapp/infra")
	if err != nil {
		t.Fatal(err)
	}
	bp, err := inf.LoadBlueprint(context.Background(), ssmc)
	if err != nil {
		t.Fatal(err)
	}
	if bp.DBOwnerRole != "reviewapp_owner" || bp.Routes["apollo-router"].HealthCheckPort != 8088 {
		t.Fatalf("unexpected blueprint: %+v", bp)
	}
}

func TestLoadBlueprintRejectsBadRoutes(t *testing.T) {
	for name, body := range map[string]string{
		"no owner":       `{"service_connect_domain": "d", "routes": {}}`,
		"no rules":       `{"db_owner_role": "o", "service_connect_domain": "d", "routes": {"shell": {"port": 1, "health_check_path": "/"}}}`,
		"too many paths": `{"db_owner_role": "o", "service_connect_domain": "d", "routes": {"shell": {"port": 1, "health_check_path": "/", "rules": [["/a", "/b", "/c", "/d", "/e"]]}}}`,
	} {
		ssmc := fakeSSM{"/reviewapp/infra": infraJSON, "/reviewapp/blueprint": body}
		inf, err := Load(context.Background(), ssmc, "/reviewapp/infra")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inf.LoadBlueprint(context.Background(), ssmc); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
}

func TestSecretRef(t *testing.T) {
	inf := &Infra{}
	inf.Cluster.ARN = "arn:aws:ecs:us-east-1:123456789012:cluster/c"
	if got := inf.SecretRef("reviewapp/db", "RLS_DATABASE_URL"); got != "arn:aws:secretsmanager:us-east-1:123456789012:secret:reviewapp/db:RLS_DATABASE_URL::" {
		t.Fatalf("got %s", got)
	}
}
