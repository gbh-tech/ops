package aws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// NewSecretsManagerClient returns a Secrets Manager client using the given AWS
// config. Pass the result of NewAWSConfig to satisfy the aws.Config parameter.
func NewSecretsManagerClient(cfg aws.Config) *secretsmanager.Client {
	return secretsmanager.NewFromConfig(cfg)
}

// FetchSecretKeys fetches secretARN from Secrets Manager, parses the JSON
// value, and returns the plaintext string for each key in keys.
//
// The secret value must be a JSON object. Missing keys produce an error.
func FetchSecretKeys(ctx context.Context, client *secretsmanager.Client, secretARN string, keys []string) (map[string]string, error) {
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &secretARN,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching secret %s: %w", secretARN, err)
	}

	if out.SecretString == nil {
		return nil, fmt.Errorf("secret %s has no string value (binary secrets are not supported)", secretARN)
	}

	var blob map[string]string
	if err := json.Unmarshal([]byte(*out.SecretString), &blob); err != nil {
		return nil, fmt.Errorf("secret %s is not a JSON object: %w", secretARN, err)
	}

	result := make(map[string]string, len(keys))
	for _, key := range keys {
		val, ok := blob[key]
		if !ok {
			return nil, fmt.Errorf("key %q not found in secret %s", key, secretARN)
		}
		result[key] = val
	}
	return result, nil
}
