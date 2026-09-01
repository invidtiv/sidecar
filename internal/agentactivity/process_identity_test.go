package agentactivity

import "testing"

func TestForegroundProcessArgv0sIgnoresOnlyInitAdoptedMembers(t *testing.T) {
	tests := []struct {
		name      string
		processes []foregroundProcess
		want      []string
	}{
		{
			name: "detached daemon is not shell work",
			processes: []foregroundProcess{
				{PID: 100, ParentPID: 10, Argv0: "zsh"},
				{PID: 101, ParentPID: 1, Argv0: "git"},
			},
			want: []string{"zsh"},
		},
		{
			name: "live helper still refuses shell readiness",
			processes: []foregroundProcess{
				{PID: 100, ParentPID: 10, Argv0: "zsh"},
				{PID: 101, ParentPID: 100, Argv0: "helper"},
			},
			want: []string{"zsh", "helper"},
		},
		{
			name:      "adopted group leader remains authoritative",
			processes: []foregroundProcess{{PID: 100, ParentPID: 1, Argv0: "claude"}},
			want:      []string{"claude"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := foregroundProcessArgv0s(100, tt.processes)
			if len(got) != len(tt.want) {
				t.Fatalf("argv0s = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("argv0s = %v, want %v", got, tt.want)
				}
			}
		})
	}
}
