package utils

import "testing"

func TestGetTicketId(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{"main branch passthrough", "main", "main"},
		{"lowercase jira id", "feat/abc-123-add-thing", "ABC-123"},
		{"uppercase jira id", "fix/GBH-456-some-fix", "GBH-456"},
		{"id with long project key", "feat/MYPROJ-789", "MYPROJ-789"},
		{"id mid-string", "some-prefix-AB-99-suffix", "AB-99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := GetTicketId(tt.ref)
			if got != tt.want {
				t.Fatalf("GetTicketId(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}
