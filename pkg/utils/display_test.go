package utils

import "testing"

func TestOrDash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"hello", "hello"},
		{"", "-"},
		{"  ", "  "},
	}
	for _, tt := range tests {
		got := OrDash(tt.in)
		if got != tt.want {
			t.Fatalf("OrDash(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestJoinOrDash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"a", "b", "c"}, "a, b, c"},
		{[]string{"only"}, "only"},
		{[]string{}, "-"},
		{nil, "-"},
	}
	for _, tt := range tests {
		got := JoinOrDash(tt.in)
		if got != tt.want {
			t.Fatalf("JoinOrDash(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
