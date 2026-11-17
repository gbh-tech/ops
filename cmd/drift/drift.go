// Package drift implements ops drift.
package drift

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"ops/pkg/aws"
	"ops/pkg/catalog"
	"ops/pkg/config"
	pkgdrift "ops/pkg/drift"

	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/spf13/cobra"
)

var (
	env          string
	manifestPath string
	format       string
	exitCode     bool
)

// manifest is the desired state of one environment: the image tag of each
// catalog unit. Other fields of the file are ignored.
type manifest struct {
	Environment  string            `json:"environment"`
	Applications map[string]string `json:"applications"`
}

// newChecker builds the AWS clients of the comparison. Tests replace it.
var newChecker = func(ctx context.Context, cfg *config.OpsConfig) pkgdrift.Checker {
	awsCfg := aws.NewAWSConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	return pkgdrift.Checker{
		ECS:    awsecs.NewFromConfig(awsCfg),
		ECR:    awsecr.NewFromConfig(awsCfg),
		Lambda: awslambda.NewFromConfig(awsCfg),
	}
}

var Command = &cobra.Command{
	Use:   "drift",
	Short: "List the catalog units that do not run the image their manifest names",
	Long: `Compares an environment with a manifest: a JSON file whose "applications"
object maps catalog unit IDs to image tags. It reads ECS services and Lambda
functions and changes nothing.

Each unit of kind "ecs" or "lambda" that the manifest names gets one status:

  in-sync      it runs the image of its manifest tag
  out-of-sync  it runs another image, or a rollout is not complete
  missing      its service, function or image repository does not exist in the
               environment, so its infrastructure has to be provisioned first
  unreadable   its state could not be read

An ECS unit is in sync when its task definition names the manifest image, its
rollout is complete and every running task has that tag's digest. A unit kept
at zero replicas is compared by its task definition alone. A Lambda unit is in
sync when its last update succeeded and it runs that tag's digest; set
lambda.function_name in the ops config to tell ops how functions are named.`,
	Example: `  # Show every unit of an environment
  ops drift --env stage --manifest deploy/environments/stage.json

  # The IDs to deploy, comma-separated
  ops drift --env stage --manifest deploy/environments/stage.json --format ids

  # Fail when the environment does not match, for a gate
  ops drift --env stage --manifest deploy/environments/stage.json --exit-code`,
	Args: cobra.NoArgs,
	RunE: run,
}

func run(cmd *cobra.Command, _ []string) error {
	// A failure from here on is about the environment or the inputs, not about
	// how the command was called.
	cmd.SilenceUsage = true

	switch format {
	case "table", "json", "ids":
	default:
		return fmt.Errorf("unsupported format %q; expected table, json or ids", format)
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}
	var desired manifest
	if err := json.Unmarshal(data, &desired); err != nil {
		return fmt.Errorf("parsing manifest %s: %w", manifestPath, err)
	}
	// Without it nothing would be compared, and a gate would pass unchecked.
	if desired.Applications == nil {
		return fmt.Errorf("manifest %s has no \"applications\" object", manifestPath)
	}
	if desired.Environment != "" && desired.Environment != env {
		return fmt.Errorf("manifest %s is for environment %q, not %q", manifestPath, desired.Environment, env)
	}

	cfg := config.LoadConfig()
	if cfg.CloudProvider() != "aws" {
		return fmt.Errorf("ops drift requires provider aws, the active provider is %q", cfg.CloudProvider())
	}
	units, err := catalog.Discover(cfg)
	if err != nil {
		return err
	}
	targets, err := pkgdrift.Targets(cfg, units, desired.Applications, env)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	results := newChecker(ctx, cfg).Check(ctx, targets)

	var drifted []string
	for _, result := range results {
		if result.Status != pkgdrift.InSync {
			drifted = append(drifted, result.ID)
		}
	}

	out := cmd.OutOrStdout()
	switch format {
	case "json":
		if results == nil {
			results = []pkgdrift.Result{}
		}
		if err := json.NewEncoder(out).Encode(results); err != nil {
			return err
		}
	case "ids":
		if _, err := fmt.Fprintln(out, strings.Join(drifted, ",")); err != nil {
			return err
		}
	default:
		for _, result := range results {
			if _, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", result.ID, result.Kind, result.Status, result.Reason); err != nil {
				return err
			}
		}
	}

	if exitCode && len(drifted) > 0 {
		return fmt.Errorf("%d of %d units do not run their manifest image in %s: %s", len(drifted), len(results), env, strings.Join(drifted, ", "))
	}
	return nil
}

func init() {
	Command.Flags().StringVarP(&env, "env", "e", "", "Environment to compare")
	Command.Flags().StringVarP(&manifestPath, "manifest", "m", "", "Manifest that names the image tag of each unit")
	Command.Flags().StringVar(&format, "format", "table", "Output format: table, json or ids (the comma-separated IDs that are not in sync)")
	Command.Flags().BoolVar(&exitCode, "exit-code", false, "Exit with status 1 when a unit is not in sync")
	for _, flag := range []string{"env", "manifest"} {
		if err := Command.MarkFlagRequired(flag); err != nil {
			panic(err)
		}
	}
}
