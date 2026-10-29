package reviewapp

import "testing"

func TestNames(t *testing.T) {
	for got, want := range map[string]string{
		Host(12, "reviewapps.example.com"): "pr-12.reviewapps.example.com",
		URL(12, "reviewapps.example.com"):  "https://pr-12.reviewapps.example.com",
		Service(12, "shell"):               "pr-12-shell",
		Namespace(12):                      "reviewapp-pr-12",
		Database(12):                       "reviewapp_pr_12",
		TopicPrefix(12):                    "pr-12.",
		DataPrefix(12):                     "pr-12/",
		Family(12, "shell"):                "reviewapp-pr-12-shell",
		ToolsFamily(12):                    "reviewapp-pr-12-tools",
	} {
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestTargetGroupFitsTheLimit(t *testing.T) {
	if got := TargetGroup(12, "shell"); got != "pr-12-shell" {
		t.Fatalf("got %q", got)
	}
	got := TargetGroup(12345, "subgraph-med-extraction-worker")
	if len(got) > 32 {
		t.Fatalf("%q is %d characters", got, len(got))
	}
	if got := TargetGroup(1234, "subgraph-notifications-x-yyyy"); got[len(got)-1] == '-' {
		t.Fatalf("%q ends with a hyphen", got)
	}
}
