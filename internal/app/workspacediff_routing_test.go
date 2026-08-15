package app

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// TestWorkspaceDiffLoadIsNotSwallowedByTheGlobalBrowser is the Diff pane's
// version of TestWorkspaceIssueLoadIsNotSwallowedByThePreviewHost. Clicking a
// commit hash in a workspace terminal opens a Diff tab whose load returns a
// workspacediff message; the global Workspaces browser hosts the same view
// type, so the app must offer it the result rather than claim it. Claiming it
// left every commit tab on "Loading diff…" forever.
func TestWorkspaceDiffLoadIsNotSwallowedByTheGlobalBrowser(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
	}{
		{"commit", workspacediff.CommitDetailMsg{Identity: "c:abc1234", Hash: "abc1234"}},
		{"snapshot", workspacediff.SnapshotMsg{Identity: workspacediff.IdentityWorkingTree}},
		{"range", workspacediff.RangeMsg{Identity: "r:abc1234..def5678"}},
		{"commit file", workspacediff.CommitFileDiffMsg{Identity: "c:abc1234", CommitHash: "abc1234", FilePath: "main.go"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := &nativeTestPlugin{focused: true}
			m := nativeTestModel(t, probe)
			m.overview = overview.New(workspaceinventory.Collector{})

			m.Update(tc.msg)

			if len(probe.seen) != 1 {
				t.Fatalf("plugin saw %d messages, want the Diff pane's load result", len(probe.seen))
			}
			if !reflect.DeepEqual(probe.seen[0], tc.msg) {
				t.Fatalf("plugin saw %#v, want %#v", probe.seen[0], tc.msg)
			}
		})
	}
}
