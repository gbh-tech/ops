package git

import (
	"context"
	gh "ops/pkg/github"

	"charm.land/log/v2"
	"github.com/google/go-github/v84/github"
	"github.com/spf13/cobra"
)

type TagCleanerCommandOptions struct {
	Owner    string
	Repo     string
	Quantity int
}

var TagCleanerCommand = &cobra.Command{
	Use:   "tag-cleaner",
	Short: "Helps cleanup old tags from specified repository origins",
	Run: func(cmd *cobra.Command, args []string) {
		config := gh.BuildGitHubConfig()
		opts := tagCleanerCommandFlags(cmd)

		var owner string
		var repo string
		var tagOptions *github.ListOptions

		if opts.Owner == "" {
			owner = config.Organization
		}

		if opts.Repo == "" {
			repo = config.Repository
		}

		tags, _, _ := gh.Client().Repositories.ListTags(
			context.Background(),
			owner,
			repo,
			tagOptions,
		)

		for _, tag := range tags {
			log.Infof("Tag name: %s", tag.GetName())
		}
	},
}

func tagCleanerCommandFlags(cmd *cobra.Command) TagCleanerCommandOptions {
	owner, _ := cmd.Flags().GetString("owner")
	repo, _ := cmd.Flags().GetString("repo")
	qty, _ := cmd.Flags().GetInt("quantity")

	return TagCleanerCommandOptions{
		Owner:    owner,
		Repo:     repo,
		Quantity: qty,
	}
}

func init() {
	TagCleanerCommand.Flags().StringP(
		"repo",
		"r",
		"",
		"Repository to clean tags from",
	)
	TagCleanerCommand.Flags().IntP(
		"quantity",
		"n",
		100,
		"Number of tags to clean",
	)
}
