package workspaceops

import (
	"strings"
	"testing"
)

func TestWorktreeActionRefusalSharedMatrix(t *testing.T) {
	path := t.TempDir()
	cases := []struct {
		name  string
		state *WorktreeActionState
		want  string
	}{
		{"none", nil, "No worktree"},
		{"main", &WorktreeActionState{Path: path, Branch: "main", IsMain: true}, "main worktree"},
		{"detached", &WorktreeActionState{Path: path, IsDetached: true}, "checked-out branch"},
		{"locked", &WorktreeActionState{Path: path, Branch: "topic", IsLocked: true}, "locked"},
		{"missing", &WorktreeActionState{Path: path + "/gone", Branch: "topic"}, "path is missing"},
		{"safe", &WorktreeActionState{Path: path, Branch: "topic"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WorktreeActionRefusal(tc.state, WorktreeActionMerge)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("refusal = %q, want substring %q", got, tc.want)
			}
		})
	}
}
