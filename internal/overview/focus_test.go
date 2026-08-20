package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// tabKey and shiftTabKey are the two keys the ring answers.
func tabKey() tea.KeyPressMsg      { return tea.KeyPressMsg{Code: tea.KeyTab} }
func shiftTabKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift} }

// focusRingModel is the mirrored tree: a terminal with a document and an issue
// leaf stacked beside it, which is the shape the project surface cycles too.
func focusRingModel(t *testing.T) *Model {
	t.Helper()
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, openPreviewDocSpan(m, mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	run(t, m, m.openPreviewIssue("td-196c42"))
	m.WorkspacesView(previewWide, previewTall)
	return m
}

// focusTargetName says where the keyboard is, in the terms the table uses.
func focusTargetName(t *testing.T, m *Model) string {
	t.Helper()
	if !m.PreviewFocused() {
		return "sidebar"
	}
	leaf := panelayout.Find(m.preview.paneRoot, m.preview.paneFocus)
	if leaf == nil {
		t.Fatalf("focused leaf %d is not in the tree", m.preview.paneFocus)
	}
	switch leaf.Kind {
	case panelayout.Terminal:
		return "terminal"
	case panelayout.Document:
		return "doc"
	case panelayout.Issue:
		return "issue"
	}
	t.Fatalf("focused leaf %d has an unnamed kind %v", leaf.ID, leaf.Kind)
	return ""
}

func TestGlobalTabCyclesEveryWindowOnScreen(t *testing.T) {
	tests := []struct {
		name    string
		key     tea.KeyPressMsg
		want    []string
		reverse bool
	}{
		{
			name: "forward",
			key:  tabKey(),
			want: []string{"terminal", "doc", "issue", "sidebar", "terminal"},
		},
		{
			name: "reverse",
			key:  shiftTabKey(),
			want: []string{"issue", "doc", "terminal", "sidebar", "issue"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := focusRingModel(t)
			// The issue took focus when it opened; start every walk from the list
			// so the table reads as the whole ring rather than a suffix of it.
			run(t, m, m.setFocusTarget(panelayout.Target{Kind: panelayout.TargetSidebar}))
			if got := focusTargetName(t, m); got != "sidebar" {
				t.Fatalf("premise: focus starts at %q", got)
			}
			for i, want := range test.want {
				handled, cmd := m.WorkspacesKey(test.key)
				if !handled {
					t.Fatalf("step %d: tab was not handled", i)
				}
				run(t, m, cmd)
				if got := focusTargetName(t, m); got != want {
					t.Fatalf("step %d: focus = %q, want %q", i, got, want)
				}
			}
		})
	}
}

// A focused leaf and the doc/issue focused bools are one fact, whichever window
// the ring lands on.
func TestGlobalTabKeepsPaneFocusBoolsInStep(t *testing.T) {
	m := focusRingModel(t)
	run(t, m, m.setFocusTarget(panelayout.Target{Kind: panelayout.TargetSidebar}))
	for _, want := range []string{"terminal", "doc", "issue"} {
		_, cmd := m.WorkspacesKey(tabKey())
		run(t, m, cmd)
		if got := focusTargetName(t, m); got != want {
			t.Fatalf("focus = %q, want %q", got, want)
		}
		if m.preview.doc.focused != (want == "doc") || m.preview.issue.focused != (want == "issue") {
			t.Fatalf("on %q: doc focused %v, issue focused %v", want, m.preview.doc.focused, m.preview.issue.focused)
		}
	}
	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspacesIssue {
		t.Fatalf("issue leaf context = %q", got)
	}
}

// Tab out of a focused query moves the keyboard without discarding what was
// typed: the filter stops owning keys, the narrowing stays.
func TestGlobalTabLeavesTheFilterWithTheQueryIntact(t *testing.T) {
	m := focusRingModel(t)
	run(t, m, m.setFocusTarget(panelayout.Target{Kind: panelayout.TargetSidebar}))
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	run(t, m, cmd)
	if !handled || !m.workspaces.Filter().Focused() {
		t.Fatal("premise: / did not focus the filter")
	}
	for _, r := range "alp" {
		if handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: r, Text: string(r)}); handled {
			run(t, m, cmd)
		}
	}
	if got := m.workspaces.Filter().Query(); got != "alp" {
		t.Fatalf("premise: query = %q", got)
	}

	handled, cmd = m.WorkspacesKey(tabKey())
	run(t, m, cmd)
	if !handled {
		t.Fatal("tab was not handled while the filter had focus")
	}
	if m.workspaces.Filter().Focused() {
		t.Fatal("tab left the keyboard on the filter")
	}
	if got := m.workspaces.Filter().Query(); got != "alp" {
		t.Fatalf("tab out of the filter changed the query to %q", got)
	}
	if !m.workspaces.Filter().Active() {
		t.Fatal("tab out of the filter stopped narrowing the list")
	}
	if got := focusTargetName(t, m); got != "terminal" {
		t.Fatalf("tab from the filter focused %q, want the first preview window", got)
	}
}

// A pane being typed into keeps its own tab: the interactive short-circuit runs
// above the ring, so Tab is forwarded rather than consumed as a focus move.
func TestGlobalTabIsForwardedWhileTypingInAPane(t *testing.T) {
	m := focusRingModel(t)
	run(t, m, m.setFocusTarget(panelayout.Target{Kind: panelayout.TargetSidebar}))
	run(t, m, m.enterPreviewInteractive())
	if !m.PreviewInteractive() {
		t.Fatal("premise: the preview is not interactive")
	}
	before := m.preview.paneFocus
	handled, cmd := m.WorkspacesKey(tabKey())
	run(t, m, cmd)
	if !handled {
		t.Fatal("tab was not forwarded to the live pane")
	}
	if !m.PreviewInteractive() {
		t.Fatal("tab ended interactive mode")
	}
	if m.preview.paneFocus != before {
		t.Fatalf("tab moved focus from leaf %d to %d while typing", before, m.preview.paneFocus)
	}
}

// The ring is the windows on screen. With the sidebar hidden it is the panes
// alone; in the narrow arrangement there is no preview box to focus.
func TestGlobalFocusRingFollowsTheArrangement(t *testing.T) {
	m := focusRingModel(t)
	terminal := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal)
	doc := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document)
	issue := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Issue)
	if terminal == nil || doc == nil || issue == nil {
		t.Fatal("premise: the mirrored tree is missing a leaf")
	}
	want := []panelayout.Target{
		{Kind: panelayout.TargetSidebar},
		{Kind: panelayout.TargetLeaf, Leaf: terminal.ID},
		{Kind: panelayout.TargetLeaf, Leaf: doc.ID},
		{Kind: panelayout.TargetLeaf, Leaf: issue.ID},
	}
	if got := m.focusRing(); !sameTargets(got, want) {
		t.Fatalf("split ring = %v, want %v", got, want)
	}

	// previewOnly: the sidebar is hidden, so it is not a window Tab can reach.
	run(t, m, m.toggleWorkspaceSidebar())
	if m.WorkspaceSidebarVisible() {
		t.Fatal("premise: the sidebar is still visible")
	}
	if got := m.focusRing(); !sameTargets(got, want[1:]) {
		t.Fatalf("preview-only ring = %v, want %v", got, want[1:])
	}
	run(t, m, m.toggleWorkspaceSidebar())

	// listOnly: too narrow for a preview box, so the list is the only window.
	m.WorkspacesResize(globalListMinWidth, previewTall)
	m.WorkspacesView(globalListMinWidth, previewTall)
	if got := m.focusRing(); !sameTargets(got, want[:1]) {
		t.Fatalf("list-only ring = %v, want %v", got, want[:1])
	}
	// Tab in the narrow arrangement has nowhere else to go and stays put.
	handled, cmd := m.WorkspacesKey(tabKey())
	run(t, m, cmd)
	if !handled || m.PreviewFocused() {
		t.Fatalf("narrow tab handled=%v previewFocused=%v", handled, m.PreviewFocused())
	}
}

func sameTargets(got, want []panelayout.Target) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
