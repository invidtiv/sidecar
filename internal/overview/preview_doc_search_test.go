package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panesearch"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// focusedDocPreview is the browser with README.md open in a focused document
// pane, which is where all three of this pane's search keys live.
func focusedDocPreview(t *testing.T) *Model {
	t.Helper()
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, openPreviewDocSpan(m, mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	if m.preview.doc == nil {
		t.Fatal("README.md did not open in a document pane")
	}
	m.focusPreviewPane(panelayout.Document)
	if !m.docPaneFocused() {
		t.Fatal("the document pane does not hold the keyboard")
	}
	return m
}

func pressWorkspaces(t *testing.T, m *Model, msg tea.KeyPressMsg) bool {
	t.Helper()
	handled, cmd := m.WorkspacesKey(msg)
	run(t, m, cmd)
	return handled
}

// `/` on a focused document pane opens docview's in-file search, and it must
// reach the pane before the browser's own `/` filter: a slash typed into a
// focused document is a search, not a workspace query.
func TestPreviewDocInFileSearchBeatsTheListFilter(t *testing.T) {
	m := focusedDocPreview(t)

	if !pressWorkspaces(t, m, tea.KeyPressMsg{Code: '/', Text: "/"}) {
		t.Fatal("/ was not handled by the focused document pane")
	}
	if m.WorkspacesFilterFocused() {
		t.Fatal("/ focused the workspace filter instead of the document's search")
	}
	view := m.preview.doc.view()
	if !view.SearchActive() {
		t.Fatal("/ did not start an in-file search")
	}

	for _, r := range "hello" {
		pressWorkspaces(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := view.SearchQuery(); got != "hello" {
		t.Fatalf("typed query landed as %q", got)
	}
	// `q` would close the pane; while the bar is up it is query text.
	pressWorkspaces(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"})
	if m.preview.doc == nil || !view.SearchActive() {
		t.Fatal("q closed the pane out from under a live search")
	}

	pressWorkspaces(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if view.SearchActive() {
		t.Fatal("esc did not close the search")
	}
	if m.preview.doc == nil {
		t.Fatal("esc closed the pane rather than the search")
	}
}

// The focus context follows the bar, so the footer and the app's text-input
// gate both see a document pane that is taking typed text.
func TestPreviewDocFindHasItsOwnContext(t *testing.T) {
	m := focusedDocPreview(t)
	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspacesDoc {
		t.Fatalf("focus context before the search = %q", got)
	}
	pressWorkspaces(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspacesDocFind {
		t.Fatalf("focus context = %q, want %q", got, ctxGlobalWorkspacesDocFind)
	}
	for _, command := range m.Commands() {
		if command.Context != ctxGlobalWorkspacesDocFind {
			t.Fatalf("footer command %q is in context %q while the bar is up", command.ID, command.Context)
		}
	}
}

// ctrl+p and f open the same two surfaces the project workspace opens, rooted
// at the pane's own directory. This browser answered neither before.
func TestPreviewDocAnswersFinderAndProjectSearch(t *testing.T) {
	m := focusedDocPreview(t)

	if !pressWorkspaces(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}) {
		t.Fatal("ctrl+p was not handled by the focused document pane")
	}
	if m.preview.doc.mode == nil || m.preview.doc.mode.Kind() != panesearch.KindFinder {
		t.Fatalf("ctrl+p left mode %#v, want the file finder", m.preview.doc.mode)
	}
	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspacesDocSearch {
		t.Fatalf("focus context with the finder up = %q", got)
	}
	// The header says what the pane is doing while the surface is up.
	if m.preview.doc.mode.HeaderLabel() == "" {
		t.Fatal("the finder gave the pane header nothing to say")
	}
	pressWorkspaces(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.preview.doc != nil && m.preview.doc.mode != nil {
		t.Fatal("esc left the finder up")
	}

	m = focusedDocPreview(t)
	if !pressWorkspaces(t, m, tea.KeyPressMsg{Code: 'f', Text: "f"}) {
		t.Fatal("f was not handled by the focused document pane")
	}
	if m.preview.doc.mode == nil || m.preview.doc.mode.Kind() != panesearch.KindProject {
		t.Fatalf("f left mode %#v, want the project search", m.preview.doc.mode)
	}
	m.closePreviewDocSearch()
}

// Both pane surfaces register the same keys for the same bar.
func TestPreviewDocSearchBindingsMatchTheProjectSurface(t *testing.T) {
	registry := keymap.NewRegistry()
	keymap.RegisterDefaults(registry)
	for key, want := range map[string]string{
		"/":      "search-content",
		"ctrl+p": "find-file",
		"f":      "search-project",
	} {
		got, ok := registry.CommandForContextKey(ctxGlobalWorkspacesDoc, key)
		if !ok || got != want {
			t.Fatalf("%s %q -> %q (bound=%v), want %q", ctxGlobalWorkspacesDoc, key, got, ok, want)
		}
	}
	for key, want := range map[string]string{
		"enter": "confirm",
		"n":     "next-match",
		"N":     "prev-match",
		"esc":   "cancel",
	} {
		got, ok := registry.CommandForContextKey(ctxGlobalWorkspacesDocFind, key)
		if !ok || got != want {
			t.Fatalf("%s %q -> %q (bound=%v), want %q", ctxGlobalWorkspacesDocFind, key, got, ok, want)
		}
	}
}

// ctrl+c stays the host's while a pane search owns the keyboard. This browser
// answers before internal/app's text-input level, which is where every other
// surface's ctrl+c is intercepted, so a search that swallowed it would make the
// quit confirmation unreachable — the rule the focused list filter already
// states for itself.
func TestPreviewDocSearchHandsBackCtrlC(t *testing.T) {
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}

	m := focusedDocPreview(t)
	pressWorkspaces(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if handled, _ := m.WorkspacesKey(ctrlC); handled {
		t.Fatal("the in-file search swallowed ctrl+c")
	}

	m = focusedDocPreview(t)
	pressWorkspaces(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if handled, _ := m.WorkspacesKey(ctrlC); handled {
		t.Fatal("the file finder swallowed ctrl+c")
	}
}

// Losing the keyboard dismisses both searches: the modal, and the in-file bar.
func TestPreviewDocSearchClosesOnFocusLoss(t *testing.T) {
	m := focusedDocPreview(t)
	pressWorkspaces(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	view := m.preview.doc.view()
	if !view.SearchActive() {
		t.Fatal("no search to lose")
	}
	m.focusPreviewPane(panelayout.Terminal)
	if view.SearchActive() {
		t.Fatal("the in-file search survived the pane losing focus")
	}

	m = focusedDocPreview(t)
	pressWorkspaces(t, m, tea.KeyPressMsg{Code: 'f', Text: "f"})
	doc := m.preview.doc
	m.focusPreviewPane(panelayout.Terminal)
	if doc.mode != nil {
		t.Fatal("the project search survived the pane losing focus")
	}
}
