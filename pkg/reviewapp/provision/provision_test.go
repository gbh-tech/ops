package provision

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ops/pkg/reviewapp/state"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

var ctx = context.Background()

func TestDeployCreatesTheReviewApp(t *testing.T) {
	h := newHarness()
	ref := h.manifest(7, "abc1234")

	if err := h.c.Deploy(ctx, 7, ref); err != nil {
		t.Fatal(err)
	}

	st := h.store.states[7]
	if st.Status != state.StatusReady || st.SHA != "abc1234" || st.Manifest != ref {
		t.Fatalf("state = %s %s %+v", st.Status, st.SHA, st.Manifest)
	}
	if st.URL != "https://pr-7.reviewapps.example.com" || st.Owner != "octocat" {
		t.Fatalf("url/owner = %s/%s", st.URL, st.Owner)
	}
	if !st.ExpiresAt.Equal(h.now.Add(48 * time.Hour)) {
		t.Fatalf("expires = %s", st.ExpiresAt)
	}
	r := st.Resources
	if r.Namespace == "" || r.Database != "reviewapp_pr_7" || !r.Bootstrapped || r.ToolsTaskDef == "" {
		t.Fatalf("resources = %+v", r)
	}
	if !slices.Equal(r.Services, []string{"pr-7-cron-alerts", "pr-7-shell", "pr-7-subgraph-users"}) {
		t.Fatalf("services = %v", r.Services)
	}
	if len(r.TargetGroups) != 2 || len(r.ListenerRules) != 2 {
		t.Fatalf("target groups/rules = %v / %v", r.TargetGroups, r.ListenerRules)
	}

	// Database: shared roles created, database owned by the owner role.
	for _, role := range []string{"reviewapp_owner", "leopard_app", "leopard_pii"} {
		if !h.db.roles[role] {
			t.Fatalf("role %s not created", role)
		}
	}
	if !h.db.dbs["reviewapp_pr_7"] || !slices.Contains(h.db.stmts, "CREATE DATABASE reviewapp_pr_7 OWNER reviewapp_owner") {
		t.Fatalf("database statements = %v", h.db.stmts)
	}

	// Helper task: bootstrap ran once.
	if len(h.aws.runs) != 1 || !slices.Equal(h.aws.runs[0], []string{"bootstrap"}) {
		t.Fatalf("runs = %v", h.aws.runs)
	}

	// Rules: specific path first, catch-all last, skipping the priorities
	// another review app holds.
	rules := h.aws.hostRules("pr-7.reviewapps.example.com")
	priorities := map[string]string{}
	for _, rl := range rules {
		priorities[rl.Conditions[1].PathPatternConfig.Values[0]] = aws.ToString(rl.Priority)
	}
	if priorities["/users"] != "3" || priorities["/*"] != "4" {
		t.Fatalf("priorities = %v", priorities)
	}

	// Services: public ones behind the ALB, every one on Service Connect.
	var shell *awsecs.CreateServiceInput
	for _, in := range h.aws.created {
		if aws.ToString(in.ServiceName) == "pr-7-shell" {
			shell = in
		}
		if !in.ServiceConnectConfiguration.Enabled || aws.ToString(in.ServiceConnectConfiguration.Namespace) != r.Namespace {
			t.Fatalf("%s is not on the namespace", aws.ToString(in.ServiceName))
		}
		if in.LaunchType != "FARGATE" {
			t.Fatalf("%s launch type = %s", aws.ToString(in.ServiceName), in.LaunchType)
		}
	}
	if shell == nil || len(shell.LoadBalancers) != 1 || aws.ToInt32(shell.LoadBalancers[0].ContainerPort) != 3002 {
		t.Fatalf("shell service = %+v", shell)
	}
	alias := shell.ServiceConnectConfiguration.Services[0].ClientAliases[0]
	if aws.ToString(alias.DnsName) != "shell.platform.internal" {
		t.Fatalf("alias = %s", aws.ToString(alias.DnsName))
	}
	groups := shell.NetworkConfiguration.AwsvpcConfiguration.SecurityGroups
	if !slices.Equal(groups, []string{"sg-shell", "sg-ns", "sg-msk"}) {
		t.Fatalf("security groups = %v", groups)
	}

	// Task definition: review app variables, expanded placeholders, secrets
	// by reference, logs under pr-7.
	def := registered(h, "reviewapp-pr-7-shell")
	env := map[string]string{}
	for _, kv := range def.ContainerDefinitions[0].Environment {
		env[aws.ToString(kv.Name)] = aws.ToString(kv.Value)
	}
	if env["APP_BASE_URL"] != "https://pr-7.reviewapps.example.com" || env["DATABASE_NAME"] != "reviewapp_pr_7" || env["KAFKA_TOPIC_PREFIX"] != "pr-7." {
		t.Fatalf("environment = %v", env)
	}
	if aws.ToString(def.ContainerDefinitions[0].Secrets[0].ValueFrom) != "arn:secret:shell/reviewapp:AUTH0_SECRET::" {
		t.Fatalf("secrets = %+v", def.ContainerDefinitions[0].Secrets)
	}
	if def.ContainerDefinitions[0].LogConfiguration.Options["awslogs-stream-prefix"] != "pr-7" {
		t.Fatal("logs are not under pr-7")
	}
	if aws.ToString(def.TaskRoleArn) != "task-shell" || aws.ToString(def.ExecutionRoleArn) != "exec-shell" {
		t.Fatalf("roles = %s/%s", aws.ToString(def.TaskRoleArn), aws.ToString(def.ExecutionRoleArn))
	}

	// The state keeps secret names, never values.
	if !slices.Equal(st.Config["shell"].Secrets, []string{"AUTH0_SECRET"}) {
		t.Fatalf("config secrets = %v", st.Config["shell"].Secrets)
	}

	if len(h.comments.bodies) != 1 || !strings.Contains(h.comments.bodies[0], "https://pr-7.reviewapps.example.com") {
		t.Fatalf("comments = %v", h.comments.bodies)
	}
}

func registered(h *harness, family string) *awsecs.RegisterTaskDefinitionInput {
	var last *awsecs.RegisterTaskDefinitionInput
	for _, in := range h.aws.registered {
		if aws.ToString(in.Family) == family {
			last = in
		}
	}
	return last
}

func TestDeployTwiceUpdatesWithoutCreating(t *testing.T) {
	h := newHarness()
	ref := h.manifest(7, "abc1234")
	if err := h.c.Deploy(ctx, 7, ref); err != nil {
		t.Fatal(err)
	}
	ns, tgs, rules, services := h.aws.createdNamespace, h.aws.createdTGs, h.aws.createdRules, len(h.aws.created)

	if err := h.c.Deploy(ctx, 7, ref); err != nil {
		t.Fatal(err)
	}
	if h.aws.createdNamespace != ns || h.aws.createdTGs != tgs || h.aws.createdRules != rules || len(h.aws.created) != services {
		t.Fatal("a second deploy created resources")
	}
	if got := h.aws.runs[len(h.aws.runs)-1]; !slices.Equal(got, []string{"migrate"}) {
		t.Fatalf("second deploy ran %v, want migrate", got)
	}
	hist := h.store.states[7].History
	if hist[0].Action != "create" || !slices.ContainsFunc(hist, func(e state.HistoryEntry) bool { return e.Action == "update" }) {
		t.Fatalf("history = %+v", hist)
	}
	if got := h.comments.bodies[len(h.comments.bodies)-1]; !strings.Contains(got, "updated to commit") {
		t.Fatalf("second comment = %q", got)
	}
	if len(h.store.states[7].Resources.ListenerRules) != 2 {
		t.Fatalf("rules = %v", h.store.states[7].Resources.ListenerRules)
	}
}

func TestDeployResumesAfterAFailedBootstrap(t *testing.T) {
	h := newHarness()
	ref := h.manifest(7, "abc1234")
	h.aws.taskExit = func(*awsecs.RunTaskInput) int32 { return 1 }

	err := h.c.Deploy(ctx, 7, ref)
	if err == nil || !strings.Contains(err.Error(), "exited with code 1") {
		t.Fatalf("err = %v, want the helper exit code", err)
	}
	st := h.store.states[7]
	if st.Status != state.StatusFailed || st.Resources.Bootstrapped || len(st.Resources.Services) != 0 {
		t.Fatalf("state after failure = %s %+v", st.Status, st.Resources)
	}

	h.aws.taskExit = func(*awsecs.RunTaskInput) int32 { return 0 }
	if err := h.c.Deploy(ctx, 7, ref); err != nil {
		t.Fatal(err)
	}
	if h.store.states[7].Status != state.StatusReady {
		t.Fatalf("status = %s", h.store.states[7].Status)
	}
	if h.aws.createdNamespace != 1 {
		t.Fatalf("namespace created %d times", h.aws.createdNamespace)
	}
	if got := h.aws.runs[len(h.aws.runs)-1]; !slices.Equal(got, []string{"bootstrap"}) {
		t.Fatalf("resume ran %v, want bootstrap", got)
	}
}

func TestDeployEleventhFails(t *testing.T) {
	h := newHarness()
	for pr := 100; pr < 110; pr++ {
		h.store.states[pr] = state.New(pr, "x", "a", "u", h.now)
		h.store.etags[pr] = 1
	}
	err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234"))
	if !errors.Is(err, state.ErrLimit) {
		t.Fatalf("err = %v, want ErrLimit", err)
	}
	if _, ok := h.store.states[7]; ok || h.aws.createdNamespace != 0 {
		t.Fatal("the eleventh deploy wrote state or created resources")
	}
}

func TestDeployMissingImageCreatesNothing(t *testing.T) {
	h := newHarness()
	ref := h.manifest(7, "abc1234", ecrHost+"/shell:pr-abc1234")

	err := h.c.Deploy(ctx, 7, ref)
	if err == nil || !strings.Contains(err.Error(), "shell:pr-abc1234") {
		t.Fatalf("err = %v, want the missing image named", err)
	}
	if _, ok := h.store.states[7]; ok || h.aws.createdNamespace != 0 || len(h.db.stmts) != 0 {
		t.Fatal("a missing image still created something")
	}
}

func TestEcrImageKeepsTheRegistry(t *testing.T) {
	registry, repo, tag, ok := ecrImage("222.dkr.ecr.us-east-1.amazonaws.com/shared/shell:pr-abc")
	if !ok || registry != "222" || repo != "shared/shell" || tag != "pr-abc" {
		t.Fatalf("ecrImage = %q %q %q %v", registry, repo, tag, ok)
	}
	if _, _, _, ok := ecrImage("ghcr.io/acme/shell:1"); ok {
		t.Fatal("parsed a non-ECR image")
	}
}

func TestDeployRefusesAManifestForAnotherPR(t *testing.T) {
	h := newHarness()
	ref := h.manifest(8, "abc1234")
	if err := h.c.Deploy(ctx, 7, ref); err == nil {
		t.Fatal("want an error")
	}
}

func TestEnvironmentRefusesCredentials(t *testing.T) {
	h := newHarness()
	for name, vars := range map[string]map[string]string{
		"password name": {"DB_PASSWORD": "x"},
		"url with user": {"DATABASE_URL": "postgres://app:secret@db:5432/app"},
	} {
		if _, err := h.c.environment(7, vars); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
	if _, err := h.c.environment(7, map[string]string{"API_URL": "https://example.com/graphql"}); err != nil {
		t.Fatalf("plain URL refused: %v", err)
	}
}

func TestDeployUpdatesToANewCommit(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	created := len(h.aws.created)

	ref := h.manifest(7, "def5678")
	if err := h.c.Deploy(ctx, 7, ref); err != nil {
		t.Fatal(err)
	}
	if len(h.aws.created) != created {
		t.Fatal("an update created services")
	}
	if !strings.Contains(aws.ToString(h.aws.services["pr-7-shell"].TaskDefinition), "reviewapp-pr-7-shell:2") {
		t.Fatalf("shell runs %s", aws.ToString(h.aws.services["pr-7-shell"].TaskDefinition))
	}
	if got := h.aws.runs[len(h.aws.runs)-1]; !slices.Equal(got, []string{"migrate"}) {
		t.Fatalf("deploy ran %v", got)
	}
	if aws.ToString(registered(h, "reviewapp-pr-7-tools").ContainerDefinitions[0].Image) != ecrHost+"/reviewapp-tools:pr-def5678" {
		t.Fatal("helper image not updated")
	}
	st := h.store.states[7]
	if st.SHA != "def5678" || st.Manifest != ref || st.Status != state.StatusReady {
		t.Fatalf("state = %s %+v %s", st.SHA, st.Manifest, st.Status)
	}
}

func TestDestroy(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	// A neighbour whose names share the prefix must survive.
	h.aws.taskDefs["reviewapp-pr-70-shell"] = 1
	h.aws.data["pr-7/upload.pdf"] = true
	h.aws.data["pr-70/upload.pdf"] = true
	h.aws.streams["/ecs/shell"] = []string{"pr-7/shell/t1", "pr-70/shell/t2"}

	if err := h.c.Destroy(ctx, 7); err != nil {
		t.Fatal(err)
	}

	for name, svc := range h.aws.services {
		if strings.HasPrefix(name, "pr-7-") && aws.ToString(svc.Status) != "INACTIVE" {
			t.Fatalf("service %s still active", name)
		}
	}
	if len(h.aws.hostRules("pr-7.reviewapps.example.com")) != 0 {
		t.Fatal("rules left for the host")
	}
	if len(h.aws.hostRules("pr-99.reviewapps.example.com")) != 2 {
		t.Fatal("another review app's rules were deleted")
	}
	if len(h.aws.targetGroups) != 0 || len(h.aws.namespaces) != 0 {
		t.Fatalf("left target groups %v namespaces %v", h.aws.targetGroups, h.aws.namespaces)
	}
	if got := h.aws.runs[len(h.aws.runs)-1]; !slices.Equal(got, []string{"teardown"}) {
		t.Fatalf("last helper run = %v, want teardown", got)
	}
	if h.db.dbs["reviewapp_pr_7"] || !h.db.roles["leopard_app"] {
		t.Fatal("database not dropped, or a shared role removed")
	}
	for _, arn := range h.aws.deregs {
		if strings.Contains(arn, "reviewapp-pr-70-") {
			t.Fatalf("deregistered a neighbour's %s", arn)
		}
	}
	if len(h.aws.deregs) != 4 {
		t.Fatalf("deregistered %v", h.aws.deregs)
	}
	if h.aws.data["pr-7/upload.pdf"] || !h.aws.data["pr-70/upload.pdf"] {
		t.Fatalf("data = %v", h.aws.data)
	}
	if !slices.Equal(h.aws.streams["/ecs/shell"], []string{"pr-70/shell/t2"}) {
		t.Fatalf("streams = %v", h.aws.streams["/ecs/shell"])
	}
	if _, ok := h.store.states[7]; ok || !slices.Equal(h.store.archived, []int{7}) {
		t.Fatal("state not archived")
	}
}

func TestDestroyWithoutStateUsesTags(t *testing.T) {
	h := newHarness()
	h.aws.services["pr-7-shell"] = &ecstypesService
	h.aws.targetGroups["pr-7-shell"] = "arn:aws:elasticloadbalancing:us-east-1:1:targetgroup/pr-7-shell/1"
	h.aws.tagged = []string{
		"arn:aws:ecs:us-east-1:1:service/reviewapp/pr-7-shell",
		"arn:aws:elasticloadbalancing:us-east-1:1:targetgroup/pr-7-shell/1",
	}

	if err := h.c.Destroy(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if aws.ToString(h.aws.services["pr-7-shell"].Status) != "INACTIVE" || len(h.aws.targetGroups) != 0 {
		t.Fatal("tagged resources survived")
	}
	if len(h.store.archived) != 0 {
		t.Fatal("archived a state that did not exist")
	}
}

func TestDestroyFailureKeepsTheState(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	h.aws.taskExit = func(*awsecs.RunTaskInput) int32 { return 2 }

	err := h.c.Destroy(ctx, 7)
	if err == nil || !strings.Contains(err.Error(), "tear down topics") {
		t.Fatalf("err = %v", err)
	}
	st, ok := h.store.states[7]
	if !ok || st.Status != state.StatusDestroying {
		t.Fatal("a failed destroy must keep the state as destroying")
	}
	if h.db.dbs["reviewapp_pr_7"] {
		t.Fatal("later steps did not run after the failed one")
	}

	h.aws.taskExit = func(*awsecs.RunTaskInput) int32 { return 0 }
	if err := h.c.Destroy(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.store.states[7]; ok {
		t.Fatal("second destroy did not finish")
	}
}

func TestPauseAndUnpause(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	if err := h.c.Pause(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if h.store.states[7].Status != state.StatusPaused || h.aws.services["pr-7-shell"].DesiredCount != 0 {
		t.Fatal("not paused")
	}
	updates, history, etag := len(h.aws.updates), len(h.store.states[7].History), h.store.etags[7]
	if err := h.c.Pause(ctx, 7); err != nil {
		t.Fatalf("pausing a paused review app failed: %v", err)
	}
	if len(h.aws.updates) != updates || len(h.store.states[7].History) != history || h.store.etags[7] != etag {
		t.Fatal("pausing twice changed something")
	}

	h.now = h.now.Add(3 * time.Hour)
	if err := h.c.Unpause(ctx, 7); err != nil {
		t.Fatal(err)
	}
	st := h.store.states[7]
	if st.Status != state.StatusReady || h.aws.services["pr-7-shell"].DesiredCount != 1 || !st.LastActivityAt.Equal(h.now) {
		t.Fatalf("after unpause: %s desired=%d last=%s", st.Status, h.aws.services["pr-7-shell"].DesiredCount, st.LastActivityAt)
	}
	if !st.ExpiresAt.Equal(st.CreatedAt.Add(state.TTL)) {
		t.Fatal("unpause moved the TTL")
	}
}

func TestRun(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	if err := h.c.Run(ctx, 7, "cron-alerts", nil); err != nil {
		t.Fatal(err)
	}
	var last *awsecs.RunTaskInput
	for _, in := range h.aws.tasks {
		if aws.ToString(in.TaskDefinition) == "reviewapp-pr-7-cron-alerts" {
			last = in
		}
	}
	if last == nil {
		t.Fatal("cron-alerts did not run")
	}
	if err := h.c.Run(ctx, 7, "nope", nil); err == nil || !strings.Contains(err.Error(), "cron-alerts") {
		t.Fatalf("err = %v, want the known units listed", err)
	}
}

func TestGC(t *testing.T) {
	h := newHarness()
	for _, pr := range []int{1, 2, 3, 4, 5, 6} {
		if err := h.c.Deploy(ctx, pr, h.manifest(pr, "sha"+strconv.Itoa(pr))); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.c.Pause(ctx, 5); err != nil {
		t.Fatal(err)
	}
	created := h.now

	// pr-1 expires, pr-2 has been idle 9 hours, pr-3 had requests on its
	// shell, pr-4 is idle but too recently active, pr-5 sleeps (asleep
	// time is not idle time), and pr-6 had requests only on an API unit,
	// which do not count as activity.
	h.store.states[1].ExpiresAt = created.Add(time.Hour)
	h.store.states[4].LastActivityAt = created.Add(8 * time.Hour)
	h.now = created.Add(9 * time.Hour)
	h.c.Metrics = &perPRMetrics{fakeAWS: h.aws, active: []string{"pr-3-shell", "pr-6-subgraph-users"}}
	before := len(h.comments.bodies)

	if err := h.c.GC(ctx); err != nil {
		t.Fatal(err)
	}
	for _, pr := range []int{1, 2, 6} {
		if _, ok := h.store.states[pr]; ok {
			t.Fatalf("pr-%d not destroyed", pr)
		}
	}
	if st := h.store.states[3]; st.Status != state.StatusReady || !st.LastActivityAt.Equal(h.now) {
		t.Fatalf("active pr-3: %s last=%s", st.Status, st.LastActivityAt)
	}
	if h.store.states[4].Status != state.StatusReady {
		t.Fatal("pr-4 destroyed too early")
	}
	if h.store.states[5].Status != state.StatusPaused {
		t.Fatalf("sleeping pr-5 is %s", h.store.states[5].Status)
	}
	if !slices.Equal(h.aws.metrics, []string{MetricGcSuccess}) {
		t.Fatalf("metrics = %v", h.aws.metrics)
	}
	got := strings.Join(h.comments.bodies[before:], "\n")
	for _, want := range []string{
		"pr-1 destroyed: it reached its 48-hour lifetime",
		"pr-2 destroyed: it had no requests for 8-hour",
		"pr-6 destroyed: it had no requests",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("comments lack %q:\n%s", want, got)
		}
	}
}

func TestGCWarnsOnceBeforeExpiry(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	h.aws.requests = 1
	h.now = h.store.states[7].ExpiresAt.Add(-30 * time.Minute)
	before := len(h.comments.bodies)

	for range 2 {
		if err := h.c.GC(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.comments.bodies) != before+1 || !strings.Contains(h.comments.bodies[before], "48-hour lifetime") {
		t.Fatalf("comments = %v", h.comments.bodies[before:])
	}
	if st := h.store.states[7]; !st.ExpiryWarnedAt.Equal(h.now) || st.Status != state.StatusReady {
		t.Fatalf("expiry_warned_at = %s, status %s", st.ExpiryWarnedAt, st.Status)
	}
}

func TestGCWarnsBeforeIdleAndAgainAfterNewActivity(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	created := h.now
	before := len(h.comments.bodies)

	// 7h30m without requests: one warning, however many runs.
	h.now = created.Add(7*time.Hour + 30*time.Minute)
	for range 2 {
		if err := h.c.GC(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.comments.bodies) != before+1 || !strings.Contains(h.comments.bodies[before], "no requests since 12:00 UTC and will be destroyed at 20:00 UTC") {
		t.Fatalf("comments = %v", h.comments.bodies[before:])
	}

	// Someone opens it: the clock restarts and nothing is destroyed.
	h.aws.requests = 1
	h.now = created.Add(7*time.Hour + 45*time.Minute)
	if err := h.c.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if st := h.store.states[7]; st.Status != state.StatusReady || !st.LastActivityAt.Equal(h.now) {
		t.Fatalf("after activity: %s last=%s", st.Status, st.LastActivityAt)
	}

	// Idle again: a new warning, then the destroy.
	h.aws.requests = 0
	h.now = h.store.states[7].LastActivityAt.Add(7*time.Hour + 30*time.Minute)
	if err := h.c.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.comments.bodies) != before+2 {
		t.Fatalf("want a second idle warning, comments = %v", h.comments.bodies[before:])
	}
	h.now = h.now.Add(30 * time.Minute)
	if err := h.c.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.store.states[7]; ok {
		t.Fatal("idle pr-7 not destroyed")
	}
}

func TestGCWarnsAboutTheLimitReachedFirst(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	// Active until 47h: the TTL comes before the idle limit.
	h.store.states[7].LastActivityAt = h.store.states[7].ExpiresAt.Add(-time.Hour)
	h.now = h.store.states[7].ExpiresAt.Add(-30 * time.Minute)
	before := len(h.comments.bodies)
	if err := h.c.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.comments.bodies) != before+1 || !strings.Contains(h.comments.bodies[before], "lifetime") {
		t.Fatalf("comments = %v", h.comments.bodies[before:])
	}
	if !h.store.states[7].IdleWarnedAt.IsZero() {
		t.Fatal("warned about idleness too")
	}
}

func TestGCNeverCountsSleepAsIdle(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	created := h.now
	h.now = created.Add(time.Hour)
	if err := h.c.Pause(ctx, 7); err != nil {
		t.Fatal(err)
	}

	// Asleep overnight: no warning, no destroy.
	before := len(h.comments.bodies)
	h.now = created.Add(20 * time.Hour)
	if err := h.c.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if st := h.store.states[7]; st.Status != state.StatusPaused || len(h.comments.bodies) != before {
		t.Fatalf("asleep: %s, comments %v", st.Status, h.comments.bodies[before:])
	}

	// Wake restarts the idle clock.
	if err := h.c.Unpause(ctx, 7); err != nil {
		t.Fatal(err)
	}
	h.now = created.Add(21 * time.Hour)
	if err := h.c.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if st := h.store.states[7]; st.Status != state.StatusReady {
		t.Fatalf("an hour after wake: %s", st.Status)
	}
}

func TestGCDestroysASleepingReviewAppAtTheTTL(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	if err := h.c.Pause(ctx, 7); err != nil {
		t.Fatal(err)
	}
	h.now = h.store.states[7].CreatedAt.Add(state.TTL)
	if err := h.c.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.store.states[7]; ok {
		t.Fatal("sleeping pr-7 survived its 48 hours")
	}
}

func TestGCWithAnErrorPublishesNoHeartbeat(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	h.now = h.store.states[7].ExpiresAt.Add(time.Minute)
	h.aws.taskExit = func(*awsecs.RunTaskInput) int32 { return 1 }

	if err := h.c.GC(ctx); err == nil {
		t.Fatal("want an error")
	}
	if len(h.aws.metrics) != 0 {
		t.Fatal("published the heartbeat after a failed run")
	}
}

func TestDeployRestartsAPausedReviewApp(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	if err := h.c.Pause(ctx, 7); err != nil {
		t.Fatal(err)
	}
	created := h.store.states[7].CreatedAt

	if err := h.c.Deploy(ctx, 7, h.manifest(7, "def5678")); err != nil {
		t.Fatal(err)
	}
	st := h.store.states[7]
	if st.Status != state.StatusReady || h.aws.services["pr-7-shell"].DesiredCount != 1 {
		t.Fatalf("after deploy: %s desired=%d", st.Status, h.aws.services["pr-7-shell"].DesiredCount)
	}
	if !st.CreatedAt.Equal(created) || !st.ExpiresAt.Equal(created.Add(state.TTL)) {
		t.Fatal("an update moved the TTL")
	}
}

func TestDeployRefusesAReviewAppBeingDestroyed(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	h.store.states[7].Status = state.StatusDestroying
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "def5678")); err == nil || !strings.Contains(err.Error(), "being destroyed") {
		t.Fatalf("err = %v", err)
	}
}

func TestGCUsesTheConfiguredIdleTimeout(t *testing.T) {
	h := newHarness()
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(45 * time.Minute)

	// The 8h default leaves it running.
	if err := h.c.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if h.store.states[7].Status != state.StatusReady {
		t.Fatalf("after 45m with the 8h default: %s", h.store.states[7].Status)
	}

	h.c.IdleTimeout = 30 * time.Minute
	if err := h.c.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.store.states[7]; ok {
		t.Fatal("not destroyed after 45m with a 30m idle timeout")
	}
}

func TestPauseRefusesAReviewAppThatIsNotReady(t *testing.T) {
	for _, status := range []state.Status{state.StatusCreating, state.StatusFailed, state.StatusDestroying} {
		h := newHarness()
		if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
			t.Fatal(err)
		}
		h.store.states[7].Status = status
		err := h.c.Pause(ctx, 7)
		if err == nil || !strings.Contains(err.Error(), string(status)) {
			t.Fatalf("%s: err = %v, want a refusal naming the status", status, err)
		}
	}
}

func TestDeployAddsTheInfraTags(t *testing.T) {
	h := newHarness()
	// reviewapp in the infra tags cannot replace the tag destroy finds by.
	h.c.Infra.Tags = map[string]string{"Environment": "reviewapp", "reviewapp": "nope"}
	if err := h.c.Deploy(ctx, 7, h.manifest(7, "abc1234")); err != nil {
		t.Fatal(err)
	}
	want := []string{"Environment=reviewapp", "reviewapp=pr-7"}
	ecsTags := func(tags []ecstypes.Tag) []string {
		var out []string
		for _, tag := range tags {
			out = append(out, aws.ToString(tag.Key)+"="+aws.ToString(tag.Value))
		}
		return out
	}
	for _, in := range h.aws.created {
		if got := ecsTags(in.Tags); !slices.Equal(got, want) || in.PropagateTags != ecstypes.PropagateTagsService {
			t.Fatalf("service %s tags = %v, propagate %s", aws.ToString(in.ServiceName), got, in.PropagateTags)
		}
	}
	for _, in := range h.aws.registered {
		if got := ecsTags(in.Tags); !slices.Equal(got, want) {
			t.Fatalf("task definition %s tags = %v", aws.ToString(in.Family), got)
		}
	}
	for _, in := range h.aws.tasks {
		if got := ecsTags(in.Tags); !slices.Equal(got, want) {
			t.Fatalf("task tags = %v", got)
		}
	}
}
