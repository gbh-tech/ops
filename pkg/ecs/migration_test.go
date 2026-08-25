package ecs

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestEvaluateMigrationTaskResult(t *testing.T) {
	tests := []struct {
		name        string
		output      *awsecs.DescribeTasksOutput
		wantErr     bool
		wantDetails []string
	}{
		{
			name: "target exited successfully despite failed sidecar",
			output: migrationDescribeOutput(
				ecstypes.Container{Name: aws.String("app"), ExitCode: aws.Int32(0)},
				ecstypes.Container{Name: aws.String("sidecar"), ExitCode: aws.Int32(1), Reason: aws.String("sidecar failed")},
			),
		},
		{
			name: "describe failure",
			output: &awsecs.DescribeTasksOutput{Failures: []ecstypes.Failure{{
				Arn:    aws.String("arn:aws:ecs:task/failed"),
				Reason: aws.String("MISSING"),
				Detail: aws.String("task disappeared"),
			}}},
			wantErr:     true,
			wantDetails: []string{"describe migration task failed", "arn:aws:ecs:task/failed", "MISSING", "task disappeared"},
		},
		{
			name:        "describe failure without details",
			output:      &awsecs.DescribeTasksOutput{Failures: []ecstypes.Failure{{}}},
			wantErr:     true,
			wantDetails: []string{"unknown failure"},
		},
		{
			name:        "no task returned",
			output:      &awsecs.DescribeTasksOutput{},
			wantErr:     true,
			wantDetails: []string{"no migration task returned"},
		},
		{
			name: "target container absent",
			output: &awsecs.DescribeTasksOutput{Tasks: []ecstypes.Task{{
				StoppedReason: aws.String("essential container stopped"),
				Containers:    []ecstypes.Container{{Name: aws.String("sidecar"), ExitCode: aws.Int32(0)}},
			}}},
			wantErr:     true,
			wantDetails: []string{`migration container "app" not found`, "task stopped reason: essential container stopped"},
		},
		{
			name: "target container reason",
			output: &awsecs.DescribeTasksOutput{Tasks: []ecstypes.Task{{
				StoppedReason: aws.String("task could not start"),
				Containers: []ecstypes.Container{{
					Name:     aws.String("app"),
					ExitCode: aws.Int32(0),
					Reason:   aws.String("cannot pull image"),
				}},
			}}},
			wantErr:     true,
			wantDetails: []string{`migration container "app" failed`, "container reason: cannot pull image", "task stopped reason: task could not start"},
		},
		{
			name: "target exit code missing",
			output: &awsecs.DescribeTasksOutput{Tasks: []ecstypes.Task{{
				StoppedReason: aws.String("task stopped without status"),
				Containers:    []ecstypes.Container{{Name: aws.String("app")}},
			}}},
			wantErr:     true,
			wantDetails: []string{`migration container "app" has no exit code`, "task stopped reason: task stopped without status"},
		},
		{
			name: "target exited non-zero",
			output: &awsecs.DescribeTasksOutput{Tasks: []ecstypes.Task{{
				StoppedReason: aws.String("essential container exited"),
				Containers: []ecstypes.Container{{
					Name:     aws.String("app"),
					ExitCode: aws.Int32(2),
				}},
			}}},
			wantErr:     true,
			wantDetails: []string{`migration container "app" exited with code 2`, "task stopped reason: essential container exited"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := evaluateMigrationTaskResult(tt.output, "app")
			if tt.wantErr && err == nil {
				t.Fatal("evaluateMigrationTaskResult() error = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("evaluateMigrationTaskResult() error = %v, want nil", err)
			}
			for _, detail := range tt.wantDetails {
				if !strings.Contains(err.Error(), detail) {
					t.Errorf("evaluateMigrationTaskResult() error = %q, want it to contain %q", err, detail)
				}
			}
		})
	}
}

func migrationDescribeOutput(containers ...ecstypes.Container) *awsecs.DescribeTasksOutput {
	return &awsecs.DescribeTasksOutput{Tasks: []ecstypes.Task{{Containers: containers}}}
}
