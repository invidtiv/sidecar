package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// One rule everywhere: { and } cycle the tabs of whatever pane has focus. The
// Diff leaf used to be the exception — it spent the braces on file stepping —
// so these pin the arrangement that replaced it in the project surface.
func threeTabDiffPane(t *testing.T) (*Plugin, *diffPane) {
	t.Helper()
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("show-diff opened nothing")
	}
	diff, _ := p.activeDiffPane()
	if diff == nil {
		t.Fatal("no Diff leaf")
	}
	for _, hash := range []string{"aaaaaaa", "bbbbbbb"} {
		diff.tabs.OpenOrFocus(
			workspacediff.Target{Kind: workspacediff.TargetCommit, A: hash},
			&workspacediff.View{},
		)
	}
	if len(diff.tabs.Items) != 3 {
		t.Fatalf("tabs = %d, want 3", len(diff.tabs.Items))
	}
	diff.tabs.Select(0)
	return p, diff
}

func TestBracesCycleDiffTargetTabsInProjectWorkspace(t *testing.T) {
	p, diff := threeTabDiffPane(t)

	for _, want := range []int{1, 2, 0} { // last step wraps past the end
		handled, _ := p.handleDiffKey(tea.KeyPressMsg{Code: '}', Text: "}"})
		if !handled {
			t.Fatal("} was not handled by the focused Diff leaf")
		}
		if diff.tabs.Active != want {
			t.Fatalf("} -> active %d, want %d", diff.tabs.Active, want)
		}
	}

	for _, want := range []int{2, 1, 0} { // first step wraps past the start
		handled, _ := p.handleDiffKey(tea.KeyPressMsg{Code: '{', Text: "{"})
		if !handled {
			t.Fatal("{ was not handled by the focused Diff leaf")
		}
		if diff.tabs.Active != want {
			t.Fatalf("{ -> active %d, want %d", diff.tabs.Active, want)
		}
	}
}

func TestCommaAndPeriodStepFilesInProjectWorkspaceDiff(t *testing.T) {
	p, diff := threeTabDiffPane(t)
	view := diff.view()
	if view == nil {
		t.Fatal("no active Diff view")
	}
	view.State = workspacediff.LoadStateClean
	view.Files = []workspacediff.File{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}}
	view.Cursor = 0

	before := diff.tabs.Active
	if handled, _ := p.handleDiffKey(tea.KeyPressMsg{Code: '.', Text: "."}); !handled {
		t.Fatal(". was not handled")
	}
	if view.Cursor != 1 {
		t.Fatalf(". -> cursor %d, want 1", view.Cursor)
	}
	if handled, _ := p.handleDiffKey(tea.KeyPressMsg{Code: ',', Text: ","}); !handled {
		t.Fatal(", was not handled")
	}
	if view.Cursor != 0 {
		t.Fatalf(", -> cursor %d, want 0", view.Cursor)
	}
	if diff.tabs.Active != before {
		t.Fatalf("file stepping moved the active tab to %d", diff.tabs.Active)
	}
}
