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

var ValuesPaths = []string{
	".helm/values",
	".helm/service-values",
	".helm/client-values",
	".helm/extra-values",
}
