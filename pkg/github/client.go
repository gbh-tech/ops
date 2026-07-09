package github

import (
	"github.com/google/go-github/v84/github"
	"ops/pkg/utils"
)

type config struct {
	Organization string
	Repository   string
	Token        string
}

func Client() *github.Client {
	cfg := BuildGitHubConfig()
	client := github.NewClient(nil).WithAuthToken(cfg.Token)
	return client
}

func BuildGitHubConfig() config {
	org := utils.GetEnvironment("GITHUB_OWNER")
	repo := utils.GetEnvironment("GITHUB_REPO")
	token := utils.GetEnvironment("GITHUB_TOKEN")

	return config{
		Organization: org,
		Repository:   repo,
		Token:        token,
	}
}
