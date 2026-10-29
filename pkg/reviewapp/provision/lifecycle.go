package provision

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ops/pkg/reviewapp"
	"ops/pkg/reviewapp/state"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// MetricNamespace is the CloudWatch namespace of the gc heartbeat.
const MetricNamespace = "ReviewApps"

// MetricGcSuccess is published after a gc run with no errors; an alarm on
// its absence catches a broken scheduler.
const MetricGcSuccess = "ReviewAppGcSuccess"

// Sleep scales every service of a review app to zero.
func (c *Controller) Sleep(ctx context.Context, pr int) error {
	st, etag, err := c.Store.Get(ctx, pr)
	if err != nil {
		return err
	}
	return c.sleepState(ctx, &tracked{State: st, etag: etag})
}

func (c *Controller) sleepState(ctx context.Context, t *tracked) error {
	if t.Status != state.StatusReady {
		return fmt.Errorf("%s is %s; only a ready review app can sleep", t.ID, t.Status)
	}
	if err := c.scale(ctx, t.Resources.Services, 0); err != nil {
		return err
	}
	t.Status = state.StatusSleeping
	c.record(t, "sleep")
	c.logf("%s is asleep", t.ID)
	return c.save(ctx, t)
}

// SleepAll puts every ready review app to sleep (the evening schedule).
func (c *Controller) SleepAll(ctx context.Context) error {
	states, err := c.Store.List(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, st := range states {
		if st.Status != state.StatusReady {
			continue
		}
		// Re-read for a fresh ETag: List does not return one.
		if err := c.Sleep(ctx, st.PR); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", st.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Wake scales a sleeping review app back up and restarts its idle clock.
// The TTL keeps counting while it sleeps (D7).
func (c *Controller) Wake(ctx context.Context, pr int) error {
	st, etag, err := c.Store.Get(ctx, pr)
	if err != nil {
		return err
	}
	t := &tracked{State: st, etag: etag}
	if t.Status != state.StatusSleeping && t.Status != state.StatusReady {
		return fmt.Errorf("%s is %s; only a sleeping review app can wake", t.ID, t.Status)
	}
	if err := c.scale(ctx, t.Resources.Services, 1); err != nil {
		return err
	}
	if err := c.waitStable(ctx, t.Resources.Services); err != nil {
		return err
	}
	t.Status = state.StatusReady
	t.LastActivityAt = c.now()
	c.record(t, "wake")
	c.logf("%s is awake: %s", t.ID, t.URL)
	return c.save(ctx, t)
}

// Run starts a one-off task of a unit (for example a scheduled function)
// with the unit's task definition and waits for it.
func (c *Controller) Run(ctx context.Context, pr int, unit string, command []string) error {
	st, etag, err := c.Store.Get(ctx, pr)
	if err != nil {
		return err
	}
	family := reviewapp.Family(pr, unit)
	if !slices.Contains(st.Resources.TaskDefinitions, family) {
		return fmt.Errorf("%s has no unit %s (units: %s)", st.ID, unit, strings.Join(sortedKeys(st.Config), ", "))
	}
	t := &tracked{State: st, etag: etag}
	c.record(t, "run "+unit)
	if err := c.save(ctx, t); err != nil {
		return err
	}
	return c.runOneOff(ctx, oneOff{PR: pr, Unit: unit, Family: family, Container: unit, Command: command})
}

// GC applies the lifetime rules to every review app (D7, SCRUM-4550):
// destroy at the TTL, sleep after the idle timeout, warn an hour before
// expiry, and finish destroys that failed. It publishes the heartbeat only
// when every review app was handled.
func (c *Controller) GC(ctx context.Context) error {
	states, err := c.Store.List(ctx)
	if err != nil {
		return err
	}
	now := c.now()
	var errs []error
	for _, st := range states {
		if err := c.gcOne(ctx, st.PR, now); err != nil {
			c.logf("ERROR: %s: %v", st.ID, err)
			errs = append(errs, fmt.Errorf("%s: %w", st.ID, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	_, err = c.Metrics.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
		Namespace: aws.String(MetricNamespace),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String(MetricGcSuccess),
			Value:      aws.Float64(1),
			Unit:       cwtypes.StandardUnitCount,
			Timestamp:  aws.Time(now),
		}},
	})
	if err != nil {
		return fmt.Errorf("publish %s: %w", MetricGcSuccess, err)
	}
	return nil
}

func (c *Controller) gcOne(ctx context.Context, pr int, now time.Time) error {
	st, etag, err := c.Store.Get(ctx, pr)
	if err != nil {
		return err
	}
	t := &tracked{State: st, etag: etag}

	if t.Status == state.StatusDestroying || t.Expired(now) {
		c.logf("%s expired or half destroyed; destroying", t.ID)
		return c.Destroy(ctx, pr)
	}

	if t.Status == state.StatusReady {
		active, err := c.hadRequests(ctx, t.Resources.TargetGroups, t.LastActivityAt, now)
		if err != nil {
			return err
		}
		if active {
			t.LastActivityAt = now
			if err := c.save(ctx, t); err != nil {
				return err
			}
		} else if now.Sub(t.LastActivityAt) >= state.IdleTimeout {
			c.logf("%s idle since %s; sleeping", t.ID, t.LastActivityAt.Format(time.RFC3339))
			if err := c.sleepState(ctx, t); err != nil {
				return err
			}
		}
	}

	if t.ExpiresAt.Sub(now) <= state.ExpiryWarning && !warned(t.State) {
		c.comment(ctx, pr, fmt.Sprintf("Review app %s expires at %s. Run the review app workflow with create to start a new one.",
			t.URL, t.ExpiresAt.Format("15:04 UTC")))
		c.record(t, "warn")
		return c.save(ctx, t)
	}
	return nil
}

func warned(st *state.State) bool {
	for _, h := range st.History {
		if h.Action == "warn" {
			return true
		}
	}
	return false
}

// hadRequests reports whether the review app's target groups served any
// request between since and now.
func (c *Controller) hadRequests(ctx context.Context, targetGroups []string, since, now time.Time) (bool, error) {
	if len(targetGroups) == 0 {
		return false, nil
	}
	lb := c.Infra.ALB.ARN[strings.Index(c.Infra.ALB.ARN, "loadbalancer/")+len("loadbalancer/"):]
	queries := make([]cwtypes.MetricDataQuery, len(targetGroups))
	for i, arn := range targetGroups {
		tg := arn[strings.LastIndex(arn, ":")+1:]
		queries[i] = cwtypes.MetricDataQuery{
			Id: aws.String(fmt.Sprintf("tg%d", i)),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String("AWS/ApplicationELB"),
					MetricName: aws.String("RequestCount"),
					Dimensions: []cwtypes.Dimension{
						{Name: aws.String("LoadBalancer"), Value: aws.String(lb)},
						{Name: aws.String("TargetGroup"), Value: aws.String(tg)},
					},
				},
				Period: aws.Int32(300),
				Stat:   aws.String("Sum"),
			},
		}
	}
	out, err := c.Metrics.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
		MetricDataQueries: queries,
		StartTime:         aws.Time(since),
		EndTime:           aws.Time(now),
	})
	if err != nil {
		return false, fmt.Errorf("read request counts: %w", err)
	}
	for _, r := range out.MetricDataResults {
		for _, v := range r.Values {
			if v > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}
