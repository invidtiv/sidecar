package gitstatus

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// twoPanePlugin is git status in its ordinary shape: a visible sidebar and a
// diff pane with a file selected to draw in it.
func twoPanePlugin() *Plugin {
	return &Plugin{
		tree:             &FileTree{},
		sidebarVisible:   true,
		activePane:       PaneSidebar,
		viewMode:         ViewModeStatus,
		selectedDiffFile: "main.go",
	}
}

// With the centre open the toggle is a ring: the sidebar is not the wrap point
// going forward, the diff pane is, and shift+tab wraps at the other end.
func TestGitStatusRingWrapsAtTheDiffPane(t *testing.T) {
	p := twoPanePlugin()
	if p.AtFocusCycleEnd(false) {
		t.Fatal("the sidebar is not the end of the forward cycle; the diff pane is")
	}
	if !p.AtFocusCycleEnd(true) {
		t.Fatal("the sidebar is where a reverse cycle wraps")
	}

	p.activePane = PaneDiff
	if !p.AtFocusCycleEnd(false) {
		t.Fatal("the diff pane is where a forward cycle wraps")
	}
	if p.AtFocusCycleEnd(true) {
		t.Fatal("the diff pane is not the start of the ring")
	}
}

// Coming back from the centre lands on the window the toggle resumes at.
func TestGitStatusFocusCycleStart(t *testing.T) {
	p := twoPanePlugin()
	p.activePane = PaneDiff
	p.FocusCycleStart(false)
	if p.activePane != PaneSidebar {
		t.Fatalf("forward handback focused %v, want the sidebar", p.activePane)
	}
	p.FocusCycleStart(true)
	if p.activePane != PaneDiff {
		t.Fatalf("reverse handback focused %v, want the diff pane", p.activePane)
	}
}

// A pane that is not drawn is not a stop: with nothing selected the sidebar is
// the whole ring and so is itself the wrap point in both directions.
func TestGitStatusRingSkipsAnEmptyDiffPane(t *testing.T) {
	p := twoPanePlugin()
	p.selectedDiffFile = ""
	if !p.AtFocusCycleEnd(false) || !p.AtFocusCycleEnd(true) {
		t.Fatal("with no diff pane drawn the sidebar is the whole ring")
	}
}

// Sub-modes keep tab. Each of these owns the keyboard for something that is not
// a pane cycle, so the shell must not offer the centre a stop in them.
func TestGitStatusSubModesKeepTab(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*Plugin)
	}{
		{"full-screen diff", func(p *Plugin) { p.viewMode = ViewModeDiff }},
		{"commit editor", func(p *Plugin) { p.viewMode = ViewModeCommit }},
		{"push menu", func(p *Plugin) { p.viewMode = ViewModePushMenu }},
		{"history search", func(p *Plugin) { p.historySearchMode = true }},
		{"path filter", func(p *Plugin) { p.pathFilterMode = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := twoPanePlugin()
			p.activePane = PaneDiff
			tc.apply(p)
			if p.AtFocusCycleEnd(false) || p.AtFocusCycleEnd(true) {
				t.Fatalf("%s offered the centre a tab stop", tc.name)
			}
		})
	}
}

// With the centre closed the shell never asks, and the plugin's own tab handler
// is the exact toggle it always was.
func TestGitStatusTabTogglesPanesUnchanged(t *testing.T) {
	p := twoPanePlugin()
	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	p.updateStatus(tab)
	if p.activePane != PaneDiff {
		t.Fatalf("tab from the sidebar focused %v, want the diff pane", p.activePane)
	}
	p.updateStatusDiffPane(tab)
	if p.activePane != PaneSidebar {
		t.Fatalf("tab from the diff pane focused %v, want the sidebar", p.activePane)
	}
}
