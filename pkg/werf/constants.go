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
