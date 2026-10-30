package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v84/github"
)

// GitHubCommenter posts review app notices on pull requests.
type GitHubCommenter struct {
	Client *github.Client
	Owner  string
	Repo   string
}

// NewGitHubCommenter returns a commenter for owner/repo with a token. The
// token comes from the GitHub App secret named in the infra parameter.
func NewGitHubCommenter(repository, token string) (*GitHubCommenter, error) {
	owner, repo, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || repo == "" {
		return nil, fmt.Errorf("repository %q is not owner/name", repository)
	}
	return &GitHubCommenter{Client: github.NewClient(nil).WithAuthToken(token), Owner: owner, Repo: repo}, nil
}

// Comment adds a comment to the pull request.
func (g *GitHubCommenter) Comment(ctx context.Context, pr int, body string) error {
	_, _, err := g.Client.Issues.CreateComment(ctx, g.Owner, g.Repo, pr, &github.IssueComment{Body: github.Ptr(body)})
	return err
}
