// Package reviewapp implements ops reviewapp (RFC 0004): per-PR environments
// on a shared ECS cluster. The read-only commands (list, status, logs) work
// from anywhere; the commands that change a review app run inside the
// reviewapp-controller task (pkg/reviewapp/client).
package reviewapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	pkgaws "ops/pkg/aws"
	"ops/pkg/config"
	pkgecs "ops/pkg/ecs"
	"ops/pkg/reviewapp"
	"ops/pkg/reviewapp/infra"
	"ops/pkg/reviewapp/state"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"charm.land/log/v2"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
)

// Command is ops reviewapp.
var Command = &cobra.Command{
	Use:   "reviewapp",
	Short: "Manage per-PR review apps on ECS",
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List the review apps",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		rc := load(cmd)
		states, err := rc.store().List(rc.ctx)
		if err != nil {
			log.Fatal("Failed to list review apps", "err", err)
		}
		if asJSON(cmd) {
			printJSON(states)
			return
		}
		if len(states) == 0 {
			fmt.Println("No review apps.")
			return
		}
		rows := make([][]string, len(states))
		for i, st := range states {
			rows[i] = []string{st.ID, string(st.Status), st.URL, st.Owner, ago(st.LastActivityAt), until(st.ExpiresAt)}
		}
		renderTable([]string{"ID", "STATUS", "URL", "OWNER", "LAST ACTIVITY", "EXPIRES"}, rows)
		fmt.Printf("%d of %d review apps running\n", state.CountActive(states), rc.settings.limit)
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show a review app and its services",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		pr := prFlag(cmd)
		rc := load(cmd)
		st, _, err := rc.store().Get(rc.ctx, pr)
		if err != nil {
			log.Fatal("Failed to read review app", "id", state.ID(pr), "err", err)
		}
		if asJSON(cmd) {
			printJSON(st)
			return
		}

		fmt.Printf("ID:            %s\n", st.ID)
		fmt.Printf("Status:        %s\n", st.Status)
		fmt.Printf("URL:           %s\n", st.URL)
		fmt.Printf("Owner:         %s\n", st.Owner)
		fmt.Printf("Commit:        %s\n", st.SHA)
		fmt.Printf("Created:       %s\n", st.CreatedAt.Format(time.RFC3339))
		fmt.Printf("Last activity: %s (%s)\n", st.LastActivityAt.Format(time.RFC3339), ago(st.LastActivityAt))
		fmt.Printf("Expires:       %s (%s)\n", st.ExpiresAt.Format(time.RFC3339), until(st.ExpiresAt))

		if len(st.Resources.Services) > 0 {
			ecsClient := awsecs.NewFromConfig(rc.aws)
			rows := make([][]string, len(st.Resources.Services))
			for i, svc := range st.Resources.Services {
				s, err := pkgecs.GetServiceStatus(rc.ctx, ecsClient, rc.infra.Cluster.ARN, svc)
				if err != nil {
					rows[i] = []string{svc, "unknown", "-", err.Error()}
					continue
				}
				rows[i] = []string{svc, s.Status, fmt.Sprintf("%d/%d", s.RunningCount, s.DesiredCount), s.LastEvent}
			}
			renderTable([]string{"SERVICE", "STATUS", "RUNNING", "LAST EVENT"}, rows)
		}

		if len(st.History) > 0 {
			fmt.Println("Recent actions:")
			for _, h := range st.History {
				fmt.Printf("  %s  %-8s %s\n", h.At.Format(time.RFC3339), h.Action, h.Actor)
			}
		}
	},
}

var logsCmd = &cobra.Command{
	Use:   "logs <unit>",
	Short: "Print the logs of one unit of a review app",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pr := prFlag(cmd)
		since, _ := cmd.Flags().GetDuration("since")
		rc := load(cmd)

		unit, err := rc.infra.LoadUnit(rc.ctx, ssm.NewFromConfig(rc.aws), args[0])
		if err != nil {
			log.Fatal("Unknown unit", "unit", args[0], "err", err)
		}
		prefix := reviewapp.LogStreamPrefix(pr) + "/"
		if err := pkgecs.TailLogs(rc.ctx, cwlogs.NewFromConfig(rc.aws), unit.LogGroup, prefix, time.Now().Add(-since)); err != nil {
			log.Fatal("Failed to read logs", "err", err)
		}
	},
}

func init() {
	pf := Command.PersistentFlags()
	pf.String("domain", "", "Parent domain of review app hosts (default: reviewapps.domain in .ops/config.yaml)")
	pf.String("infra-ssm", "", "SSM parameter with the review app infrastructure (default: reviewapps.infra_ssm)")
	pf.String("tools-image", "", "Helper image of the project (default: reviewapps.tools_image)")
	pf.String("region", "", "AWS region (default: aws.region, then AWS_REGION)")

	listCmd.Flags().Bool("json", false, "Print JSON")
	statusCmd.Flags().Bool("json", false, "Print JSON")
	logsCmd.Flags().Duration("since", time.Hour, "How far back to read")
	for _, c := range []*cobra.Command{statusCmd, logsCmd} {
		c.Flags().Int("pr", 0, "Pull request number")
		_ = c.MarkFlagRequired("pr")
	}

	Command.AddCommand(listCmd, statusCmd, logsCmd)
}

// settings are the reviewapps: values, from flags first and then the
// project's .ops/config.yaml. The controller task has no repository, so it
// gets every value as a flag.
type settings struct {
	domain     string
	infraSSM   string
	toolsImage string
	region     string
	profile    string
	limit      int
}

func resolveSettings(cmd *cobra.Command) settings {
	var s settings
	s.limit = config.DefaultReviewAppsLimit
	if _, err := os.Stat(".ops/config.yaml"); err == nil {
		cfg := config.LoadConfig()
		s.domain = cfg.ReviewApps.Domain
		s.infraSSM = cfg.ReviewApps.InfraSSM
		s.toolsImage = cfg.ReviewApps.ToolsImage
		s.limit = cfg.ReviewApps.ResolvedLimit()
		s.region = cfg.AWS.Region
		s.profile = cfg.AWS.Profile
	}
	for flag, dst := range map[string]*string{
		"domain":      &s.domain,
		"infra-ssm":   &s.infraSSM,
		"tools-image": &s.toolsImage,
		"region":      &s.region,
	} {
		if v, _ := cmd.Flags().GetString(flag); v != "" {
			*dst = v
		}
	}
	if s.region == "" {
		s.region = os.Getenv("AWS_REGION")
	}
	if s.infraSSM == "" {
		log.Fatal("No infra parameter: set reviewapps.infra_ssm in .ops/config.yaml or pass --infra-ssm")
	}
	return s
}

// runContext is what every subcommand needs: settings, AWS config and infra.
type runContext struct {
	ctx      context.Context
	settings settings
	aws      awssdk.Config
	infra    *infra.Infra
}

func load(cmd *cobra.Command) *runContext {
	ctx := context.Background()
	s := resolveSettings(cmd)
	awsCfg := pkgaws.NewAWSConfig(ctx, s.region, s.profile)
	inf, err := infra.Load(ctx, ssm.NewFromConfig(awsCfg), s.infraSSM)
	if err != nil {
		log.Fatal("Failed to read review app infrastructure", "err", err)
	}
	return &runContext{ctx: ctx, settings: s, aws: awsCfg, infra: inf}
}

func (rc *runContext) store() *state.Store {
	return state.NewStore(s3.NewFromConfig(rc.aws), rc.infra.Buckets.State)
}

func prFlag(cmd *cobra.Command) int {
	pr, _ := cmd.Flags().GetInt("pr")
	if pr <= 0 {
		log.Fatal("--pr must be a positive pull request number", "pr", pr)
	}
	return pr
}

func asJSON(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Fatal("Failed to print JSON", "err", err)
	}
}

func renderTable(headers []string, rows [][]string) {
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers(headers...).
		Rows(rows...)
	if _, err := lipgloss.Println(t); err != nil {
		log.Fatal("Failed to render table", "err", err)
	}
}

// ago formats how long ago t was, e.g. "3h ago".
func ago(t time.Time) string {
	return shortDuration(time.Since(t)) + " ago"
}

// until formats how long until t, e.g. "in 45h", or "expired".
func until(t time.Time) string {
	d := time.Until(t)
	if d <= 0 {
		return "expired"
	}
	return "in " + shortDuration(d)
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	default:
		return strconv.Itoa(int(d.Hours())) + "h"
	}
}
