package state

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLifetime(t *testing.T) {
	created := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC) // a Friday
	idle := DefaultIdleTimeout
	at := func(d time.Duration) time.Time { return created.Add(d) }

	cases := []struct {
		name   string
		status Status
		last   time.Duration // LastActivityAt, from creation
		expiry time.Duration // ExpiryWarnedAt, from creation; 0 means never
		idleW  time.Duration // IdleWarnedAt, from creation; 0 means never
		now    time.Duration
		want   Lifetime
	}{
		{name: "fresh", status: StatusReady, now: time.Hour},
		{name: "idle warning due", status: StatusReady, now: 7 * time.Hour,
			want: Lifetime{Warn: ReasonIdle, Deadline: at(8 * time.Hour)}},
		{name: "idle warning sent", status: StatusReady, idleW: 7 * time.Hour, now: 7*time.Hour + 30*time.Minute},
		{name: "idle warning stale after activity", status: StatusReady, idleW: 7 * time.Hour, last: 7*time.Hour + 10*time.Minute, now: 14*time.Hour + 30*time.Minute,
			want: Lifetime{Warn: ReasonIdle, Deadline: at(15*time.Hour + 10*time.Minute)}},
		{name: "idle", status: StatusReady, now: 8 * time.Hour, want: Lifetime{Destroy: ReasonIdle}},
		{name: "asleep is never idle", status: StatusPaused, now: 30 * time.Hour},
		{name: "failed is never idle", status: StatusFailed, now: 30 * time.Hour},
		{name: "creating is never idle", status: StatusCreating, now: 9 * time.Hour},
		{name: "ttl warning while asleep", status: StatusPaused, now: 47 * time.Hour,
			want: Lifetime{Warn: ReasonExpired, Deadline: at(TTL)}},
		{name: "ttl warning sent", status: StatusPaused, expiry: 47 * time.Hour, now: 47*time.Hour + 30*time.Minute},
		{name: "ttl first when both are near", status: StatusReady, last: 40*time.Hour + 30*time.Minute, now: 47*time.Hour + 15*time.Minute,
			want: Lifetime{Warn: ReasonExpired, Deadline: at(TTL)}},
		{name: "idle first when both are near", status: StatusReady, last: 39*time.Hour + 30*time.Minute, now: 47 * time.Hour,
			want: Lifetime{Warn: ReasonIdle, Deadline: at(47*time.Hour + 30*time.Minute)}},
		{name: "expired while asleep", status: StatusPaused, now: TTL, want: Lifetime{Destroy: ReasonExpired}},
		{name: "expired while active", status: StatusReady, last: 47*time.Hour + 59*time.Minute, now: TTL, want: Lifetime{Destroy: ReasonExpired}},
		{name: "expired when failed", status: StatusFailed, now: 49 * time.Hour, want: Lifetime{Destroy: ReasonExpired}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := New(7, "abc", "octocat", "https://pr-7.example.com", created)
			st.Status = tc.status
			st.LastActivityAt = at(tc.last)
			if tc.expiry > 0 {
				st.ExpiryWarnedAt = at(tc.expiry)
			}
			if tc.idleW > 0 {
				st.IdleWarnedAt = at(tc.idleW)
			}
			if got := st.Lifetime(at(tc.now), idle); got != tc.want {
				t.Fatalf("Lifetime = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLifetimeDefaultsTheIdleTimeout(t *testing.T) {
	created := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	st := New(7, "abc", "octocat", "u", created)
	st.Status = StatusReady
	if got := st.Lifetime(created.Add(DefaultIdleTimeout), 0); got.Destroy != ReasonIdle {
		t.Fatalf("Lifetime with a zero timeout = %+v", got)
	}
}

func TestWarningFieldsAreOmittedUntilSet(t *testing.T) {
	st := New(7, "abc", "octocat", "u", time.Now())
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "warned_at") {
		t.Fatalf("zero warning times are written: %s", data)
	}
}

func TestWasReady(t *testing.T) {
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		state State
		want  bool
	}{
		{name: "first create", state: State{Status: StatusCreating}},
		{name: "failed create", state: State{Status: StatusFailed, History: []HistoryEntry{{Action: "create"}, {Action: "failed"}}}},
		{name: "ready", state: State{Status: StatusReady}, want: true},
		{name: "asleep", state: State{Status: StatusPaused}, want: true},
		{name: "failed update", state: State{Status: StatusFailed, ReadyAt: now}, want: true},
		// States written before ready_at existed say it in their history.
		{name: "failed update, older state", state: State{Status: StatusFailed, History: []HistoryEntry{{Action: "create"}, {Action: "ready"}, {Action: "update"}, {Action: "failed"}}}, want: true},
	}
	for _, tc := range cases {
		if got := tc.state.WasReady(); got != tc.want {
			t.Errorf("%s: WasReady = %v, want %v", tc.name, got, tc.want)
		}
	}
}
