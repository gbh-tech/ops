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

func (c *Controller) idleTimeout() time.Duration {
	if c.IdleTimeout > 0 {
		return c.IdleTimeout
	}
	return state.DefaultIdleTimeout
}

// metricLag is how far back each activity query reaches before the last
// recorded activity: ALB metrics arrive a few minutes late, and a request
// counted twice only extends the idle clock by one gc run.
const metricLag = 5 * time.Minute

// MetricNamespace is the CloudWatch namespace of the gc heartbeat.
const MetricNamespace = "ReviewApps"

// MetricGcSuccess is published after a gc run with no errors; an alarm on
// its absence catches a broken scheduler.
const MetricGcSuccess = "ReviewAppGcSuccess"

// Pause scales every service of a review app to zero. The database, rules
// and state stay, so Unpause brings it back as it was. Pausing a paused
// review app changes nothing and succeeds.
func (c *Controller) Pause(ctx context.Context, pr int) error {
	st, etag, err := c.Store.Get(ctx, pr)
	if err != nil {
		return err
	}
	if st.Status == state.StatusPaused {
		c.logf("%s is already paused", st.ID)
		return nil
	}
	return c.pauseState(ctx, &tracked{State: st, etag: etag})
}

func (c *Controller) pauseState(ctx context.Context, t *tracked) error {
	if t.Status != state.StatusReady {
		return fmt.Errorf("%s is %s; only a ready review app can be paused", t.ID, t.Status)
	}
	if err := c.scale(ctx, t.Resources.Services, 0); err != nil {
		return err
	}
	t.Status = state.StatusPaused
	c.record(t, "pause")
	c.logf("%s is paused", t.ID)
	return c.save(ctx, t)
}

// PauseAll pauses (puts to sleep) every ready review app: the evening
// schedule. Creating, failed and destroying review apps are left alone.
func (c *Controller) PauseAll(ctx context.Context) error {
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
		if err := c.Pause(ctx, st.PR); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", st.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Unpause (wake) scales a paused review app back up and restarts its idle
// clock, so time asleep never counts as idle. The TTL keeps counting while
// it is paused (D7).
func (c *Controller) Unpause(ctx context.Context, pr int) error {
	st, etag, err := c.Store.Get(ctx, pr)
	if err != nil {
		return err
	}
	t := &tracked{State: st, etag: etag}
	if t.Status != state.StatusPaused && t.Status != state.StatusReady {
		return fmt.Errorf("%s is %s; only a paused review app can be unpaused", t.ID, t.Status)
	}
	if err := c.scale(ctx, t.Resources.Services, 1); err != nil {
		return err
	}
	if err := c.waitStable(ctx, t.Resources.Services); err != nil {
		return err
	}
	t.Status = state.StatusReady
	t.LastActivityAt = c.now()
	c.record(t, "unpause")
	c.logf("%s is running: %s", t.ID, t.URL)
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
// record activity, destroy at the TTL or after the idle time, warn an hour
// before either, and finish destroys that failed. It publishes the heartbeat
// only when every review app was handled.
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

	if t.Status == state.StatusDestroying {
		c.logf("%s is half destroyed; destroying", t.ID)
		return c.Destroy(ctx, pr)
	}

	// Activity first, so a review app in use is never destroyed as idle.
	if t.Status == state.StatusReady && !t.Expired(now) {
		active, err := c.hadRequests(ctx, c.activityTargetGroups(t.State), t.LastActivityAt.Add(-metricLag), now)
		if err != nil {
			return err
		}
		if active {
			t.LastActivityAt = now
			if err := c.save(ctx, t); err != nil {
				return err
			}
		}
	}

	idle := c.idleTimeout()
	// destroyReason is the limit a state has reached, if any. An idle one
	// counts only after metricLag past the deadline: a request just before it
	// can still be unpublished, and a later run's activity query covers it.
	destroyReason := func(s *state.State, at time.Time) state.Reason {
		lt := s.Lifetime(at, idle)
		if lt.Destroy == state.ReasonIdle && at.Before(s.IdleDeadline(idle).Add(metricLag)) {
			return ""
		}
		return lt.Destroy
	}
	still := func(s *state.State) bool { return destroyReason(s, c.now()) != "" }

	switch destroyReason(t.State, now) {
	case state.ReasonExpired:
		c.logf("%s expired at %s; destroying", t.ID, t.ExpiresAt.Format(time.RFC3339))
		return c.destroy(ctx, pr, fmt.Sprintf("it reached its %s lifetime.", hours(state.TTL)), still)
	case state.ReasonIdle:
		c.logf("%s idle since %s; destroying", t.ID, t.LastActivityAt.Format(time.RFC3339))
		return c.destroy(ctx, pr, fmt.Sprintf("it had no requests for %s.", hours(idle)), still)
	}

	lt := t.Lifetime(now, idle)
	// A warning counts only once it is delivered: on a failed comment, the
	// next run tries again. The error is not returned, so failing comments
	// do not withhold the gc heartbeat.
	switch lt.Warn {
	case state.ReasonExpired:
		if !c.comment(ctx, pr, fmt.Sprintf("Review app %s will be destroyed at %s, when it reaches its %s lifetime. Run the review app workflow with deploy after that to start a new one.",
			t.URL, lt.Deadline.Format("15:04 UTC"), hours(state.TTL))) {
			return nil
		}
		t.ExpiryWarnedAt = now
	case state.ReasonIdle:
		if !c.comment(ctx, pr, fmt.Sprintf("Review app %s has had no requests since %s and will be destroyed at %s. Open it to keep it running.",
			t.URL, t.LastActivityAt.Format("15:04 UTC"), lt.Deadline.Format("15:04 UTC"))) {
			return nil
		}
		t.IdleWarnedAt = now
	default:
		return nil
	}
	c.record(t, "warn")
	return c.save(ctx, t)
}

// hours formats a whole-hour duration, e.g. "48-hour".
func hours(d time.Duration) string {
	return fmt.Sprintf("%d-hour", int(d.Hours()))
}

// activityTargetGroups returns the target groups whose requests count as
// activity: those of the unit the ALB serves at /* (the app people open).
// Other units are reached by that app or by machines, so their requests say
// little about whether anyone uses the review app. Without such a unit,
// every target group counts.
func (c *Controller) activityTargetGroups(st *state.State) []string {
	var names []string
	if c.Blueprint != nil {
		for unit, route := range c.Blueprint.Routes {
			for _, rule := range route.Rules {
				if slices.Contains(rule, "/*") {
					names = append(names, reviewapp.TargetGroup(st.PR, unit))
				}
			}
		}
	}
	var arns []string
	for _, arn := range st.Resources.TargetGroups {
		for _, name := range names {
			if strings.Contains(arn, ":targetgroup/"+name+"/") {
				arns = append(arns, arn)
			}
		}
	}
	if len(arns) == 0 {
		return st.Resources.TargetGroups
	}
	return arns
}

// hadRequests reports whether the review app's target groups served any
// request between since and now.
func (c *Controller) hadRequests(ctx context.Context, targetGroups []string, since, now time.Time) (bool, error) {
	if len(targetGroups) == 0 {
		return false, nil
	}
	_, lb, ok := strings.Cut(c.Infra.ALB.ARN, ":loadbalancer/")
	if !ok {
		return false, fmt.Errorf("alb.arn %q is not a load balancer ARN", c.Infra.ALB.ARN)
	}
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
