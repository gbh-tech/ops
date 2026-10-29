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
	StatusSleeping   Status = "sleeping"
	StatusFailed     Status = "failed"
	StatusDestroying Status = "destroying"
)

// TTL is how long a review app lives, counted in wall-clock hours from creation.
const TTL = 48 * time.Hour

// IdleTimeout is how long a ready review app can go without requests before
// gc puts it to sleep.
const IdleTimeout = 8 * time.Hour

// ExpiryWarning is how long before expiry gc warns on the PR.
const ExpiryWarning = time.Hour

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

// Expired reports whether the review app is past its TTL at now.
func (s *State) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}

// Record appends an action to the history, keeping the most recent entries.
func (s *State) Record(e HistoryEntry) {
	s.History = append(s.History, e)
	if len(s.History) > historyLimit {
		s.History = s.History[len(s.History)-historyLimit:]
	}
}
