package config

import "testing"

func TestResolvedFunctionName(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     string
	}{
		{name: "service and env", template: "platform-{service}-{env}", want: "platform-worker-stage"},
		{name: "no placeholders", template: "worker", want: "worker"},
		{name: "not configured", template: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LambdaConfig{FunctionName: tt.template}.ResolvedFunctionName("worker", "stage")
			if got != tt.want {
				t.Fatalf("ResolvedFunctionName() = %q, want %q", got, tt.want)
			}
		})
	}
}
