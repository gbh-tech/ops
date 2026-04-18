package ecs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// TailLogs fetches and prints log events from a CloudWatch log group matching
// the given stream prefix, starting from `since`.
func TailLogs(ctx context.Context, client *cwlogs.Client, logGroup, streamPrefix string, since time.Time) error {
	input := &cwlogs.FilterLogEventsInput{
		LogGroupName: aws.String(logGroup),
		StartTime:    aws.Int64(since.UnixMilli()),
	}
	if streamPrefix != "" {
		input.LogStreamNamePrefix = aws.String(streamPrefix)
	}

	paginator := cwlogs.NewFilterLogEventsPaginator(client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("filter log events: %w", err)
		}
		for _, event := range page.Events {
			ts := time.UnixMilli(aws.ToInt64(event.Timestamp))
			fmt.Printf("%s %s\n", ts.Format(time.RFC3339), aws.ToString(event.Message))
		}
	}
	return nil
}

// PrintMigrationLogs prints logs for a migration task. The CloudWatch log
// stream follows the convention {appName}/{appName}/{taskID}.
func PrintMigrationLogs(ctx context.Context, client *cwlogs.Client, logGroup, appName, taskArn string) error {
	parts := strings.Split(taskArn, "/")
	taskID := parts[len(parts)-1]

	logStream := fmt.Sprintf("%s/%s/%s", appName, appName, taskID)
	log.Info("Fetching migration logs", "stream", logStream)

	out, err := client.GetLogEvents(ctx, &cwlogs.GetLogEventsInput{
		LogGroupName:  aws.String(logGroup),
		LogStreamName: aws.String(logStream),
		StartFromHead: aws.Bool(true),
	})
	if err != nil {
		log.Warn("Could not retrieve migration logs", "stream", logStream, "err", err)
		return nil
	}

	for _, event := range out.Events {
		ts := time.UnixMilli(aws.ToInt64(event.Timestamp))
		fmt.Printf("%s %s\n", ts.Format(time.RFC3339), aws.ToString(event.Message))
	}
	return nil
}

// PrintTaskLogs prints all log events for a one-off ECS task after it has
// stopped. The CloudWatch log stream follows the awslogs convention:
// {streamPrefix}/{containerName}/{taskID}.
// It paginates through all events so no output is truncated.
func PrintTaskLogs(ctx context.Context, client *cwlogs.Client, logGroup, streamPrefix, containerName, taskArn string) error {
	parts := strings.Split(taskArn, "/")
	taskID := parts[len(parts)-1]
	logStream := fmt.Sprintf("%s/%s/%s", streamPrefix, containerName, taskID)

	log.Info("Fetching task logs", "stream", logStream)

	var nextToken *string
	for {
		out, err := client.GetLogEvents(ctx, &cwlogs.GetLogEventsInput{
			LogGroupName:  aws.String(logGroup),
			LogStreamName: aws.String(logStream),
			StartFromHead: aws.Bool(true),
			NextToken:     nextToken,
		})
		if err != nil {
			log.Warn("Could not retrieve task logs", "stream", logStream, "err", err)
			return nil
		}
		for _, event := range out.Events {
			ts := time.UnixMilli(aws.ToInt64(event.Timestamp))
			fmt.Printf("%s %s\n", ts.Format(time.RFC3339), aws.ToString(event.Message))
		}
		// GetLogEvents returns the same token when there are no more events.
		if out.NextForwardToken == nil || aws.ToString(out.NextForwardToken) == aws.ToString(nextToken) {
			break
		}
		nextToken = out.NextForwardToken
	}
	return nil
}
