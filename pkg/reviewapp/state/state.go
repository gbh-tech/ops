// Package state keeps the registry of review apps in S3 (RFC 0004).
//
// Layout of the state bucket:
//
//	reviewapps/pr-<n>/state.json       one object per review app
//	reviewapps/pr-<n>/manifest.json    image tags to deploy, written by the client
//	archive/pr-<n>/<timestamp>.json    final state after destroy
//
// Only the reviewapp-controller task writes state.json. Writes are
// conditional (If-None-Match on create, If-Match on update), so two
// controller runs for the same PR cannot overwrite each other.
package state

import (
	"fmt"
	"time"
)

// Status is the lifecycle status of a review app.
type Status string

const (
	StatusCreating   Status = "creating"
	StatusReady      Status = "ready"
	StatusPaused     Status = "paused"
	StatusFailed     Status = "failed"
	StatusDestroying Status = "destroying"
)

// TTL is how long a review app lives, counted in wall-clock hours from creation.
const TTL = 48 * time.Hour

// DefaultIdleTimeout is how many awake hours a ready review app can go
// without requests before gc destroys it. Time asleep does not count: wake
// restarts the clock. reviewapps.idle_timeout overrides it.
const DefaultIdleTimeout = 8 * time.Hour

// Warning is how long before either limit (TTL or idle) gc warns on the PR.
const Warning = time.Hour

// DefaultLimit is the maximum number of review apps that can run at once.
const DefaultLimit = 10

// historyLimit is how many history entries a state keeps.
const historyLimit = 20

// State is the content of reviewapps/pr-<n>/state.json.
type State struct {
	ID     string `json:"id"`
	PR     int    `json:"pr"`
	SHA    string `json:"sha"`
	Owner  string `json:"owner"`
	URL    string `json:"url"`
	Status Status `json:"status"`

	CreatedAt      time.Time `json:"created_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	// ReadyAt is when the review app first became ready. States written
	// before it existed lack it; WasReady covers them.
	ReadyAt time.Time `json:"ready_at,omitzero"`

	// ExpiryWarnedAt is when gc warned that the TTL is near.
	ExpiryWarnedAt time.Time `json:"expiry_warned_at,omitzero"`
	// IdleWarnedAt is when gc last warned that the idle limit is near. A
	// warning older than LastActivityAt is stale.
	IdleWarnedAt time.Time `json:"idle_warned_at,omitzero"`

	Manifest  ManifestRef    `json:"manifest"`
	Config    map[string]Env `json:"config"`
	Resources Resources      `json:"resources"`
	History   []HistoryEntry `json:"history"`
}

// ManifestRef points to the exact manifest version a run deployed.
type ManifestRef struct {
	VersionID string `json:"version_id"`
	SHA256    string `json:"sha256"`
}

// Env is the rendered environment of one unit. It holds non-secret values
// and the names of the secrets the unit reads, never their values.
type Env struct {
	Vars    map[string]string `json:"vars"`
	Secrets []string          `json:"secrets"`
}

// Resources lists what the controller created for a review app, so destroy
// can find it without searching.
type Resources struct {
	Namespace        string         `json:"namespace"`
	Services         []string       `json:"services"`
	TaskDefinitions  []string       `json:"task_definitions"`
	TargetGroups     []string       `json:"target_groups"`
	ListenerRules    []ListenerRule `json:"listener_rules"`
	Database         string         `json:"database"`
	ToolsTaskDef     string         `json:"tools_task_definition"`
	KafkaTopicPrefix string         `json:"kafka_topic_prefix"`
	S3DataPrefix     string         `json:"s3_data_prefix"`
	Bootstrapped     bool           `json:"bootstrapped"`
}

// ListenerRule is an ALB listener rule and the priority it holds.
type ListenerRule struct {
	ARN      string `json:"arn"`
	Priority int    `json:"priority"`
}

// HistoryEntry records one action taken on a review app.
type HistoryEntry struct {
	Action         string    `json:"action"`
	Actor          string    `json:"actor"`
	At             time.Time `json:"at"`
	WorkflowRunURL string    `json:"workflow_run_url,omitempty"`
	TaskARN        string    `json:"task_arn,omitempty"`
}

// Manifest is the content of reviewapps/pr-<n>/manifest.json: what to deploy.
// The client writes it from the repository at the PR's commit, so the
// controller, which has no repository, deploys exactly that commit's config.
type Manifest struct {
	PR  int    `json:"pr"`
	SHA string `json:"sha"`
	// Images is the image URI of each unit to deploy.
	Images map[string]string `json:"images"`
	// ToolsImage is the URI of the helper image (bootstrap, migrate, teardown).
	ToolsImage string `json:"tools_image,omitempty"`
	// Tools is the helper unit's config rendered for the review app
	// environment, when the project declares one. Its environment and
	// secrets go to the helper task on top of the ones ops sets.
	Tools *UnitSpec `json:"tools,omitempty"`
	// Units is each unit's config rendered for the review app environment.
	Units map[string]UnitSpec `json:"units,omitempty"`
}

// UnitSpec is one unit's rendered config. Secrets map an environment
// variable to its Secrets Manager reference, never to a value.
type UnitSpec struct {
	CPU         int               `json:"cpu"`
	Memory      int               `json:"memory"`
	Ports       []int             `json:"ports,omitempty"`
	Command     []string          `json:"command,omitempty"`
	EntryPoint  []string          `json:"entrypoint,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Secrets     map[string]string `json:"secrets,omitempty"`
	HealthCheck []string          `json:"health_check,omitempty"`
}

// ID returns the review app id for a PR number, e.g. "pr-123".
func ID(pr int) string {
	return fmt.Sprintf("pr-%d", pr)
}

// New returns the initial state of a review app being created at now.
func New(pr int, sha, owner, url string, now time.Time) *State {
	now = now.UTC()
	return &State{
		ID:             ID(pr),
		PR:             pr,
		SHA:            sha,
		Owner:          owner,
		URL:            url,
		Status:         StatusCreating,
		CreatedAt:      now,
		LastActivityAt: now,
		ExpiresAt:      now.Add(TTL),
		Config:         map[string]Env{},
	}
}

// Active reports whether the review app counts toward the limit.
func (s *State) Active() bool {
	return s.Status != StatusDestroying
}

// WasReady reports whether the review app has ever been ready: it was
// marked ready, is ready or asleep now, or its history records it ready.
func (s *State) WasReady() bool {
	if !s.ReadyAt.IsZero() || s.Status == StatusReady || s.Status == StatusPaused {
		return true
	}
	for _, e := range s.History {
		if e.Action == string(StatusReady) {
			return true
		}
	}
	return false
}

// Expired reports whether the review app is past its TTL at now.
func (s *State) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}

// IdleDeadline is when a ready review app without requests is destroyed.
func (s *State) IdleDeadline(idleTimeout time.Duration) time.Time {
	return s.LastActivityAt.Add(idleTimeout)
}

// Reason says why gc destroys a review app.
type Reason string

const (
	ReasonNone    Reason = ""
	ReasonExpired Reason = "expired"
	ReasonIdle    Reason = "idle"
)

// Lifetime is what gc does with a review app at one point in time.
type Lifetime struct {
	// Destroy is why the review app must be destroyed now, if it must.
	Destroy Reason
	// Warn is the limit to warn about on the PR, if a warning is due.
	Warn Reason
	// Deadline is when the warned limit is reached.
	Deadline time.Time
}

// Lifetime applies the lifetime rules (RFC 0004, D7) at now. The TTL counts
// wall-clock hours from creation, asleep or not. The idle limit applies only
// to a ready review app, counting from its last activity, so a sleeping one
// never dies of idleness. Each limit is warned once, Warning before it; the
// one reached first wins. Call it after recording any new activity.
func (s *State) Lifetime(now time.Time, idleTimeout time.Duration) Lifetime {
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeout
	}
	if s.Expired(now) {
		return Lifetime{Destroy: ReasonExpired}
	}
	idle := s.Status == StatusReady
	if idle && !now.Before(s.IdleDeadline(idleTimeout)) {
		return Lifetime{Destroy: ReasonIdle}
	}

	limit, deadline := ReasonExpired, s.ExpiresAt
	if idle && s.IdleDeadline(idleTimeout).Before(deadline) {
		limit, deadline = ReasonIdle, s.IdleDeadline(idleTimeout)
	}
	if deadline.Sub(now) > Warning {
		return Lifetime{}
	}
	switch {
	case limit == ReasonExpired && !s.ExpiryWarnedAt.IsZero():
		return Lifetime{}
	case limit == ReasonIdle && !s.IdleWarnedAt.IsZero() && !s.IdleWarnedAt.Before(s.LastActivityAt):
		return Lifetime{}
	}
	return Lifetime{Warn: limit, Deadline: deadline}
}

// Record appends an action to the history, keeping the most recent entries.
func (s *State) Record(e HistoryEntry) {
	s.History = append(s.History, e)
	if len(s.History) > historyLimit {
		s.History = s.History[len(s.History)-historyLimit:]
	}
}
