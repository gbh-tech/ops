package config

import "strings"

// LambdaConfig holds the settings for catalog units of kind "lambda". ops does
// not deploy Lambda functions; it only needs to find the function of a unit.
type LambdaConfig struct {
	// FunctionName is the name of a unit's Lambda function. Supports the
	// {service} and {env} template placeholders, for example
	// "my-platform-{service}-{env}".
	FunctionName string `mapstructure:"function_name"`
}

// ResolvedFunctionName expands the {service} and {env} placeholders of the
// function name. It returns "" when no function name is configured.
func (l LambdaConfig) ResolvedFunctionName(service, env string) string {
	name := strings.ReplaceAll(l.FunctionName, "{service}", service)
	return strings.ReplaceAll(name, "{env}", env)
}
