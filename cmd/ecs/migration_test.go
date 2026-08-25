package ecs

import "testing"

func TestMigrationExecutionPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		invocation migrationInvocation
		enabled    bool
		command    []string
		replicas   int
		wantRun    bool
		wantErr    bool
	}{
		{
			name:       "explicit migration runs with zero replicas",
			invocation: explicitMigration,
			enabled:    true,
			command:    []string{"bin/migrate"},
			replicas:   0,
			wantRun:    true,
		},
		{
			name:       "disabled explicit migration skips",
			invocation: explicitMigration,
			enabled:    false,
			command:    []string{"bin/migrate"},
			replicas:   0,
		},
		{
			name:       "explicit migration requires command at zero replicas",
			invocation: explicitMigration,
			enabled:    true,
			replicas:   0,
			wantErr:    true,
		},
		{
			name:       "deploy migration skips with zero replicas",
			invocation: deployMigration,
			enabled:    true,
			command:    []string{"bin/migrate"},
			replicas:   0,
		},
		{
			name:       "deploy migration requires command at zero replicas",
			invocation: deployMigration,
			enabled:    true,
			replicas:   0,
			wantErr:    true,
		},
		{
			name:       "deploy migration runs with replicas",
			invocation: deployMigration,
			enabled:    true,
			command:    []string{"bin/migrate"},
			replicas:   1,
			wantRun:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotRun, err := migrationExecutionPolicy(tt.invocation, tt.enabled, tt.command, tt.replicas)
			if (err != nil) != tt.wantErr {
				t.Fatalf("migrationExecutionPolicy() error = %v, wantErr %v", err, tt.wantErr)
			}
			if gotRun != tt.wantRun {
				t.Errorf("migrationExecutionPolicy() = %v, want %v", gotRun, tt.wantRun)
			}
		})
	}
}
