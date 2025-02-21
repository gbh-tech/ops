package werf

var CommandsWithoutRepoList = []string{
	"dismiss",
	"render",
	"secrets",
}

var CommandsWithRepoList = []string{
	"build",
	"converge",
	"export",
	"plan",
}

var DefaultSecretKey = ".werf_secret_key"
var DefaultValuesFile = ".helm/values.yaml"
var DefaultSecretValuesFile = ".helm/secret-values.yaml"

var SecretValuesPaths = []string{
	".helm/values",
	".helm/service-values",
	".helm/client-values",
	".helm/extra-values",
}

var SecretValuesFiles = []string{
	".helm/secret-env-vars.yaml",
}

var ValuesPaths = []string{
	".helm/values",
	".helm/service-values",
	".helm/client-values",
	".helm/extra-values",
}

var ValuesFiles = []string{
	".helm/env-vars.yaml",
}
