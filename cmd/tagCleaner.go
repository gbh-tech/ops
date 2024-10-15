package cmd

import (
	"context"
	"github.com/charmbracelet/log"
	"github.com/google/go-github/v66/github"
	"github.com/spf13/cobra"
	gh "ops/pkg/github"
)

type TagCleanerCommandOptions struct {
	Owner    string
	Repo     string
	Quantity int
}

var tagCleanerCmd = &cobra.Command{
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
	tagCleanerCmd.Flags().StringP(
		"repo",
		"r",
		"",
		"Repository to clean tags from",
	)
	tagCleanerCmd.Flags().IntP(
		"quantity",
		"n",
		100,
		"Number of tags to clean",
	)

	rootCmd.AddCommand(tagCleanerCmd)
}
