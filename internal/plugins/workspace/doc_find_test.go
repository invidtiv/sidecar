package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/keymap"
)

// `/` on a focused document pane opens docview's in-file search, and while the
// bar is up it owns every key in the pane: the document's own keys, the pane's
// ways out, and the workspace behind it all stop seeing them.
func TestDocPaneInFileSearchOwnsTheKeyboard(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()

	handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !handled {
		t.Fatal("/ was not handled by the focused document pane")
	}
	if !doc.view().SearchActive() {
		t.Fatal("/ did not start an in-file search")
	}
	if doc.mode != nil {
		t.Fatal("/ opened the file-finder overlay instead of the in-file bar")
	}

	// A printable key is query text, not a document key.
	for _, r := range "readme" {
		p.handleDocKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := doc.view().SearchQuery(); got != "readme" {
		t.Fatalf("typed query landed as %q", got)
	}
	// `q` would hide the pane and `x` would close the tab. Neither may fire.
	p.handleDocKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	p.handleDocKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !doc.view().SearchActive() {
		t.Fatal("a document key closed the search out from under the query")
	}
	if got := doc.view().SearchQuery(); got != "readmeqx" {
		t.Fatalf("query after q/x = %q, want them taken as text", got)
	}

	// Esc is the way out, and it gives the document back.
	p.handleDocKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if doc.view().SearchActive() {
		t.Fatal("esc did not close the search")
	}
	if !p.docFocused() {
		t.Fatal("esc closed the pane rather than the search")
	}
}

// A live in-file search is a text-input context: the host must not let a global
// shortcut take a printable character out of the query.
func TestDocPaneInFileSearchIsATextInputContext(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	if p.FocusContext() != "workspace-doc" {
		t.Fatalf("focus context before the search = %q", p.FocusContext())
	}
	p.handleDocKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	if got := p.FocusContext(); got != "workspace-doc-find" {
		t.Fatalf("focus context = %q, want workspace-doc-find", got)
	}
	if !p.ConsumesTextInput() {
		t.Fatal("a live in-file search does not claim typed text")
	}

	p.handleDocKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if p.ConsumesTextInput() {
		t.Fatal("a closed search still claims typed text")
	}
}

// The footer says what the bar answers to, through the registered bindings.
func TestDocPaneInFileSearchCommandsAreBound(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	p.handleDocKey(tea.KeyPressMsg{Code: '/', Text: "/"})

	registry := keymap.NewRegistry()
	keymap.RegisterDefaults(registry)
	for key, want := range map[string]string{
		"enter": "confirm",
		"n":     "next-match",
		"N":     "prev-match",
		"esc":   "cancel",
	} {
		got, ok := registry.CommandForContextKey("workspace-doc-find", key)
		if !ok || got != want {
			t.Fatalf("workspace-doc-find %q -> %q (bound=%v), want %q", key, got, ok, want)
		}
	}
	for _, command := range p.Commands() {
		if command.Context != "workspace-doc-find" {
			t.Fatalf("footer command %q is in context %q while the search is up", command.ID, command.Context)
		}
	}
	// And `/` itself is advertised on the pane's own context.
	if got, ok := registry.CommandForContextKey("workspace-doc", "/"); !ok || got != "search-content" {
		t.Fatalf("workspace-doc / -> %q (bound=%v), want search-content", got, ok)
	}
}

// Losing focus dismisses the bar: a search is scoped to the pane that owns it,
// and a pane that is no longer taking keys must not keep one drawn.
func TestDocPaneInFileSearchClosesOnFocusLoss(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()
	p.handleDocKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	view := doc.view()
	if !view.SearchActive() {
		t.Fatal("no search to lose")
	}

	p.activePane = PaneSidebar
	p.closeUnfocusedDocSearches()
	if view.SearchActive() {
		t.Fatal("the search survived the pane losing focus")
	}
}
