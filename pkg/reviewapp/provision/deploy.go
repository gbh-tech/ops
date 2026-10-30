package provision

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ops/pkg/reviewapp"
	"ops/pkg/reviewapp/state"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// Deploy brings a review app to a manifest. Without state it creates the
// review app: checks the limit, writes the state and runs bootstrap. With
// state it updates it: runs migrate and moves the services to the new task
// definitions. A review app whose last run failed resumes from where it
// stopped, and a paused one is unpaused. Every step is idempotent and the state
// is saved after each.
func (c *Controller) Deploy(ctx context.Context, pr int, ref state.ManifestRef) error {
	m, err := c.manifest(ctx, pr, ref)
	if err != nil {
		return err
	}
	// Check every image before changing anything.
	if err := c.checkImages(ctx, m); err != nil {
		return err
	}

	t, created, err := c.begin(ctx, pr, m)
	if err != nil {
		return err
	}
	if t.Status == state.StatusDestroying {
		return fmt.Errorf("%s is being destroyed; run destroy again before deploying it", t.ID)
	}
	// A ready review app keeps serving while it updates.
	if t.Status != state.StatusReady {
		t.Status = state.StatusCreating
	}
	t.SHA = m.SHA
	t.Manifest = ref
	t.LastActivityAt = c.now()
	action := "update"
	if created {
		action = "create"
	}
	c.record(t, action)
	if err := c.save(ctx, t); err != nil {
		return err
	}

	// bootstrap until it has run once, migrate afterwards.
	tools := "migrate"
	if !t.Resources.Bootstrapped {
		tools = "bootstrap"
	}
	if err := c.provision(ctx, t, m, tools); err != nil {
		return c.fail(ctx, t, err)
	}

	if created {
		c.comment(ctx, pr, fmt.Sprintf("Review app ready: %s (commit %s). It expires %s.",
			t.URL, shortSHA(m.SHA), t.ExpiresAt.Format("Mon Jan 2 15:04 UTC")))
	} else {
		c.comment(ctx, pr, fmt.Sprintf("Review app updated to commit %s: %s", shortSHA(m.SHA), t.URL))
	}
	return nil
}

// begin returns the state to work on and whether this run created it: the
// existing state when there is one, otherwise a new one checked against the
// limit.
func (c *Controller) begin(ctx context.Context, pr int, m *state.Manifest) (*tracked, bool, error) {
	st, etag, err := c.Store.Get(ctx, pr)
	if err == nil {
		c.logf("Updating %s (status %s)", st.ID, st.Status)
		return &tracked{State: st, etag: etag}, false, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return nil, false, err
	}
	if err := c.Store.CheckLimit(ctx, c.Limit); err != nil {
		return nil, false, err
	}
	c.logf("Creating %s", state.ID(pr))
	st = state.New(pr, m.SHA, c.Actor, reviewapp.URL(pr, c.Domain), c.now())
	st.Resources.KafkaTopicPrefix = reviewapp.TopicPrefix(pr)
	st.Resources.S3DataPrefix = reviewapp.DataPrefix(pr)
	etag, err = c.Store.Create(ctx, st)
	if errors.Is(err, state.ErrExists) {
		// Another run created it after our read; update its state instead.
		st, etag, err = c.Store.Get(ctx, pr)
		if err != nil {
			return nil, false, err
		}
		return &tracked{State: st, etag: etag}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &tracked{State: st, etag: etag}, true, nil
}

// provision brings a review app to the manifest, running the helper command
// (bootstrap or migrate) before the services.
func (c *Controller) provision(ctx context.Context, t *tracked, m *state.Manifest, tools string) error {
	pr := t.PR

	ns, err := c.ensureNamespace(ctx, pr)
	if err != nil {
		return err
	}
	t.Resources.Namespace = ns
	if err := c.save(ctx, t); err != nil {
		return err
	}

	db, err := c.ensureDatabase(ctx, pr)
	if err != nil {
		return err
	}
	t.Resources.Database = db
	if err := c.save(ctx, t); err != nil {
		return err
	}

	toolsDef, err := c.toolsTaskDefinition(ctx, pr, m.ToolsImage)
	if err != nil {
		return err
	}
	toolsARN, err := c.register(ctx, toolsDef)
	if err != nil {
		return err
	}
	t.Resources.ToolsTaskDef = toolsARN
	if err := c.save(ctx, t); err != nil {
		return err
	}

	if err := c.runOneOff(ctx, oneOff{PR: pr, Unit: ToolsUnit, Family: toolsARN, Container: ToolsContainer, Command: []string{tools}}); err != nil {
		return err
	}
	t.Resources.Bootstrapped = true
	if err := c.save(ctx, t); err != nil {
		return err
	}

	// Target groups and rules first, so services can register into them.
	tgs := map[string]string{}
	for _, unit := range sortedKeys(m.Units) {
		route, public := c.Blueprint.Routes[unit]
		if !public {
			continue
		}
		arn, err := c.ensureTargetGroup(ctx, pr, unit, route)
		if err != nil {
			return err
		}
		tgs[unit] = arn
		t.Resources.TargetGroups = appendUnique(t.Resources.TargetGroups, arn)
	}
	if err := c.save(ctx, t); err != nil {
		return err
	}
	rules, err := c.ensureRules(ctx, pr, tgs)
	t.Resources.ListenerRules = mergeRules(t.Resources.ListenerRules, rules)
	if saveErr := c.save(ctx, t); saveErr != nil {
		return errors.Join(err, saveErr)
	}
	if err != nil {
		return err
	}

	// A deploy always leaves the review app running, so a paused one restarts.
	desired := int32(1)
	t.Config = map[string]state.Env{}
	var services []string
	for _, unit := range sortedKeys(m.Units) {
		spec := m.Units[unit]
		def, err := c.unitTaskDefinition(ctx, pr, unit, m.Images[unit], spec)
		if err != nil {
			return err
		}
		defARN, err := c.register(ctx, def)
		if err != nil {
			return err
		}
		t.Resources.TaskDefinitions = appendUnique(t.Resources.TaskDefinitions, aws.ToString(def.Family))

		svc := serviceSpec{PR: pr, Unit: unit, TaskDef: defARN, Namespace: ns, Ports: spec.Ports, Desired: desired}
		if route, public := c.Blueprint.Routes[unit]; public {
			svc.TGARN = tgs[unit]
			svc.Route = &route
		}
		name, err := c.ensureService(ctx, svc)
		if err != nil {
			return err
		}
		services = append(services, name)
		t.Resources.Services = appendUnique(t.Resources.Services, name)
		t.Config[unit] = renderedEnv(def.ContainerDefinitions[0].Environment, spec.Secrets)
		if err := c.save(ctx, t); err != nil {
			return err
		}
	}

	c.logf("Waiting for %d services to reach steady state", len(services))
	if err := c.waitStable(ctx, services); err != nil {
		return err
	}
	t.Status = state.StatusReady
	c.record(t, string(t.Status))
	return c.save(ctx, t)
}

// manifest reads the manifest version the client uploaded and checks it.
func (c *Controller) manifest(ctx context.Context, pr int, ref state.ManifestRef) (*state.Manifest, error) {
	m, err := c.Store.GetManifest(ctx, pr, ref)
	if err != nil {
		return nil, err
	}
	if m.PR != pr {
		return nil, fmt.Errorf("manifest is for PR %d, not %d", m.PR, pr)
	}
	if m.ToolsImage == "" {
		return nil, errors.New("manifest has no tools_image")
	}
	if len(m.Images) == 0 {
		return nil, errors.New("manifest has no images")
	}
	for unit := range m.Images {
		if _, ok := m.Units[unit]; !ok {
			return nil, fmt.Errorf("manifest has an image for %s but no rendered config", unit)
		}
	}
	for unit := range m.Units {
		if _, ok := m.Images[unit]; !ok {
			return nil, fmt.Errorf("manifest has config for %s but no image", unit)
		}
	}
	return m, nil
}

// checkImages fails when an ECR image of the manifest does not exist, before
// the controller creates anything. Images outside ECR are not checked.
func (c *Controller) checkImages(ctx context.Context, m *state.Manifest) error {
	images := []string{m.ToolsImage}
	for _, unit := range sortedKeys(m.Images) {
		images = append(images, m.Images[unit])
	}
	var missing []string
	for _, image := range images {
		registry, repo, tag, ok := ecrImage(image)
		if !ok {
			continue
		}
		// RegistryId keeps images in another account's ECR from reading as
		// missing.
		_, err := c.ECR.DescribeImages(ctx, &ecr.DescribeImagesInput{
			RegistryId:     aws.String(registry),
			RepositoryName: aws.String(repo),
			ImageIds:       []ecrtypes.ImageIdentifier{{ImageTag: aws.String(tag)}},
		})
		var notFound *ecrtypes.ImageNotFoundException
		var noRepo *ecrtypes.RepositoryNotFoundException
		switch {
		case errors.As(err, &notFound), errors.As(err, &noRepo):
			missing = append(missing, image)
		case err != nil:
			return fmt.Errorf("check image %s: %w", image, err)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("images not found, nothing was created: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ecrImage splits <account>.dkr.ecr.<region>.amazonaws.com/<repo>:<tag>.
func ecrImage(image string) (registry, repo, tag string, ok bool) {
	host, path, found := strings.Cut(image, "/")
	if !found {
		return "", "", "", false
	}
	registry, _, found = strings.Cut(host, ".dkr.ecr.")
	if !found {
		return "", "", "", false
	}
	i := strings.LastIndex(path, ":")
	if i < 0 {
		return "", "", "", false
	}
	return registry, path[:i], path[i+1:], true
}

// renderedEnv is what the state keeps of a unit's config: plain values and
// secret names, never secret values.
func renderedEnv(vars []ecstypes.KeyValuePair, secrets map[string]string) state.Env {
	env := state.Env{Vars: map[string]string{}}
	for _, kv := range vars {
		env.Vars[aws.ToString(kv.Name)] = aws.ToString(kv.Value)
	}
	env.Secrets = sortedKeys(secrets)
	return env
}

func mergeRules(have, add []state.ListenerRule) []state.ListenerRule {
	for _, r := range add {
		if !slices.ContainsFunc(have, func(x state.ListenerRule) bool { return x.ARN == r.ARN }) {
			have = append(have, r)
		}
	}
	return have
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
