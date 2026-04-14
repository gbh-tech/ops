package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/charmbracelet/log"
)

// NewAWSConfig returns an AWS SDK config loaded from the environment,
// applying the given region and profile overrides when non-empty.
// AWS_PROFILE and AWS_REGION are already seeded by ops' LoadConfig, so
// calling this with empty strings is valid and uses those env values.
func NewAWSConfig(ctx context.Context, region, profile string) aws.Config {
	var opts []func(*config.LoadOptions) error

	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		log.Fatalf("Failed to load AWS config: %v", err)
	}

	return cfg
}
