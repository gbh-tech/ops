// Package infra reads the review app infrastructure facts that Terraform
// writes to SSM (RFC 0004, D12): the infra parameter (cluster, network, ALB,
// MSK, buckets, secret names) and one parameter per unit (roles, security
// group, log group). None of these live in the application repository.
package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// ControllerUnit is the unit whose roles, security group and log group the
// reviewapp-controller task uses.
const ControllerUnit = "reviewapp-controller"

// API is the subset of the SSM client this package uses.
type API interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// Infra is the content of the infra parameter.
type Infra struct {
	Cluster struct {
		Name string `json:"name"`
		ARN  string `json:"arn"`
	} `json:"cluster"`
	Network struct {
		VPCID            string            `json:"vpc_id"`
		PrivateSubnetIDs []string          `json:"private_subnet_ids"`
		SecurityGroupIDs map[string]string `json:"security_group_ids"`
	} `json:"network"`
	ALB struct {
		ARN              string `json:"arn"`
		DNSName          string `json:"dns_name"`
		HTTPSListenerARN string `json:"https_listener_arn"`
	} `json:"alb"`
	MSK struct {
		ClusterARN       string `json:"cluster_arn"`
		BootstrapBrokers string `json:"bootstrap_brokers"`
	} `json:"msk"`
	Buckets struct {
		Data  string `json:"data"`
		State string `json:"state"`
	} `json:"buckets"`
	RDS struct {
		Endpoint string `json:"endpoint"`
	} `json:"rds"`
	Secrets struct {
		DB        string `json:"db"`
		RDSAdmin  string `json:"rds_admin"`
		SeedUsers string `json:"seed_users"`
		GitHubApp string `json:"github_app"`
	} `json:"secrets"`
	UnitsSSM   string `json:"units_ssm"`
	Controller struct {
		TaskDefinition string `json:"task_definition"`
	} `json:"controller"`
	BlueprintSSM string `json:"blueprint_ssm"`
}

// Unit holds the facts of one unit: what a pr-<n> copy of it runs with.
type Unit struct {
	SecurityGroup string `json:"security_group"`
	TaskExecRole  string `json:"task_exec_role"`
	TaskRole      string `json:"task_role"`
	LogGroup      string `json:"log_group"`
}

// Load reads and validates the infra parameter.
func Load(ctx context.Context, client API, name string) (*Infra, error) {
	var inf Infra
	if err := getJSON(ctx, client, name, &inf); err != nil {
		return nil, err
	}
	if err := inf.validate(); err != nil {
		return nil, fmt.Errorf("parameter %s: %w", name, err)
	}
	return &inf, nil
}

// LoadUnit reads the facts of one unit from <units_ssm><unit>.
func (inf *Infra) LoadUnit(ctx context.Context, client API, unit string) (*Unit, error) {
	name := inf.UnitsSSM + unit
	var u Unit
	if err := getJSON(ctx, client, name, &u); err != nil {
		return nil, err
	}
	if u.SecurityGroup == "" || u.TaskExecRole == "" || u.TaskRole == "" || u.LogGroup == "" {
		return nil, fmt.Errorf("parameter %s: security_group, task_exec_role, task_role and log_group are required", name)
	}
	return &u, nil
}

func (inf *Infra) validate() error {
	var missing []string
	for field, v := range map[string]string{
		"cluster.arn":                inf.Cluster.ARN,
		"buckets.state":              inf.Buckets.State,
		"units_ssm":                  inf.UnitsSSM,
		"controller.task_definition": inf.Controller.TaskDefinition,
		"alb.arn":                    inf.ALB.ARN,
		"alb.https_listener_arn":     inf.ALB.HTTPSListenerARN,
		"network.vpc_id":             inf.Network.VPCID,
		"secrets.db":                 inf.Secrets.DB,
	} {
		if v == "" {
			missing = append(missing, field)
		}
	}
	if len(inf.Network.PrivateSubnetIDs) == 0 {
		missing = append(missing, "network.private_subnet_ids")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func getJSON(ctx context.Context, client API, name string, v any) error {
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name)})
	if err != nil {
		var nf *ssmtypes.ParameterNotFound
		if errors.As(err, &nf) {
			return fmt.Errorf("SSM parameter %s not found", name)
		}
		return fmt.Errorf("get SSM parameter %s: %w", name, err)
	}
	if err := json.Unmarshal([]byte(aws.ToString(out.Parameter.Value)), v); err != nil {
		return fmt.Errorf("parse SSM parameter %s: %w", name, err)
	}
	return nil
}

// Blueprint is the content of the blueprint parameter: what the project's
// catalog says about review apps that no unit config holds. Terraform writes
// it from the same catalog that builds the shared environments.
type Blueprint struct {
	// DBOwnerRole owns each review app database. The other roles whose
	// passwords are in the db secret get access to it.
	DBOwnerRole string `json:"db_owner_role"`
	// ServiceConnectDomain is the suffix of each unit's Service Connect alias
	// (<unit>.<domain>), the same names the shared environments use.
	ServiceConnectDomain string `json:"service_connect_domain"`
	// Routes are the units the ALB serves, by unit name.
	Routes map[string]Route `json:"routes"`
}

// Route is how the ALB serves one unit on the review app host.
type Route struct {
	Port            int    `json:"port"`
	HealthCheckPath string `json:"health_check_path"`
	// HealthCheckPort is the target group health check port when it differs
	// from Port.
	HealthCheckPort int `json:"health_check_port,omitempty"`
	// Rules are the path patterns of each listener rule, most specific first.
	// A rule holds at most four patterns: the host header takes the fifth
	// condition value.
	Rules [][]string `json:"rules"`
}

// LoadBlueprint reads and validates the blueprint parameter.
func (inf *Infra) LoadBlueprint(ctx context.Context, client API) (*Blueprint, error) {
	if inf.BlueprintSSM == "" {
		return nil, errors.New("the infra parameter names no blueprint_ssm")
	}
	var bp Blueprint
	if err := getJSON(ctx, client, inf.BlueprintSSM, &bp); err != nil {
		return nil, err
	}
	if bp.DBOwnerRole == "" || bp.ServiceConnectDomain == "" {
		return nil, fmt.Errorf("parameter %s: db_owner_role and service_connect_domain are required", inf.BlueprintSSM)
	}
	for unit, r := range bp.Routes {
		if r.Port == 0 || r.HealthCheckPath == "" || len(r.Rules) == 0 {
			return nil, fmt.Errorf("parameter %s: route %s needs port, health_check_path and rules", inf.BlueprintSSM, unit)
		}
		for _, rule := range r.Rules {
			if len(rule) == 0 || len(rule) > 4 {
				return nil, fmt.Errorf("parameter %s: route %s has a rule with %d path patterns, want 1 to 4", inf.BlueprintSSM, unit, len(rule))
			}
		}
	}
	return &bp, nil
}

// SecretARNPrefix returns arn:aws:secretsmanager:<region>:<account>:secret,
// taken from the cluster ARN, to build secret references.
func (inf *Infra) SecretARNPrefix() string {
	parts := strings.Split(inf.Cluster.ARN, ":")
	if len(parts) < 5 {
		return ""
	}
	return fmt.Sprintf("arn:%s:secretsmanager:%s:%s:secret", parts[1], parts[3], parts[4])
}

// SecretRef returns the ECS valueFrom of one JSON key of a secret.
func (inf *Infra) SecretRef(secret, key string) string {
	return inf.SecretARNPrefix() + ":" + secret + ":" + key + "::"
}
