package keymap_test

import (
	"testing"

	"github.com/marcus/sidecar/internal/keymap"
)

// The pane switcher is reachable from whatever pane has focus, on both
// projections of the pane model — the project workspace and the global
// Workspaces browser. Every content pane absorbs the keys it does not own, so
// a context that forgets this binding is a pane you cannot open another pane
// from without leaving it first, which is the whole point of the entry.
//
// The two hosts compare the key as a literal in their own packages
// (workspace.paneSwitcherKeyName, overview.paneSwitcherKeyName); this is what
// stops one of them changing it alone.
func TestPaneSwitcherIsBoundInEveryContentPaneContext(t *testing.T) {
	const command = "open-pane"
	const key = "n"

	contexts := []string{
		"workspace-doc", "workspace-issue", "workspace-note",
		"workspace-diff", "workspace-resource",
		"global-workspaces-doc", "global-workspaces-issue", "global-workspaces-note",
		"global-workspaces-diff", "global-workspaces-resource",
	}
	for _, context := range contexts {
		found := ""
		for _, b := range keymap.DefaultBindings() {
			if b.Context == context && b.Command == command {
				found = b.Key
				break
			}
		}
		switch {
		case found == "":
			t.Errorf("%s: %q has no binding, so the switcher is unreachable from that pane", context, command)
		case found != key:
			t.Errorf("%s: %q is bound to %q, want %q — one key, every pane", context, command, found, key)
		}
	}
}

// The plugin half of the same rule. An ordinary plugin cannot spend `n` on the
// switcher — every plugin that has a create already answers it — so its browse
// and preview contexts answer `ctrl+n` instead (app.paneSwitcherKeyName). A
// context that forgets the binding is a plugin you cannot open a pane from
// without leaving it first.
//
// The list is the contexts the plugins' FocusContext() genuinely reports while
// browsing, which is not the same set as the contexts that have bindings: Git
// reports git-status-commits with the sidebar cursor on a commit and
// git-status-diff with the file diff focused, and never reports the
// `git-history` this file also has bindings for. A binding on a context nobody
// stands in is a key that does nothing.
func TestPaneSwitcherIsBoundInEveryPluginBrowseContext(t *testing.T) {
	const command = "open-pane"
	const key = "ctrl+n"

	// Tasks and td-monitor embed a foreign model and report its context names
	// unchanged, so their rows are held to the upstream constants in the packages
	// that own the mapping (tasks/tdmonitor.TestBrowseContextsCarryThePaneSwitcherEntry).
	// Restating the strings here is what makes this list the whole roster of five
	// deck-eligible plugins rather than the three whose contexts sidecar mints.
	contexts := []string{
		"notes-list",
		"file-browser-tree", "file-browser-preview",
		"git-status", "git-status-commits", "git-status-diff",
		"git-diff", "git-commit-preview",
		"tasks-list", "tasks-detail", "tasks-response", "tasks-response-detail",
		"td-monitor", "td-board", "td-kanban",
	}
	for _, context := range contexts {
		found := ""
		for _, b := range keymap.DefaultBindings() {
			if b.Context == context && b.Command == command {
				found = b.Key
				break
			}
		}
		switch {
		case found == "":
			t.Errorf("%s: %q has no binding, so the switcher is unreachable from that plugin", context, command)
		case found != key:
			t.Errorf("%s: %q is bound to %q, want %q — one key, every plugin", context, command, found, key)
		}
	}
}

// The other half of the rule, and the reason the key is not `n`'s equal
// everywhere: `ctrl+n` walks a list in every filter, finder, search and editor
// context. Claiming it there would take the cursor out from under a user who is
// typing, so those contexts keep cursor-down and never name the switcher.
func TestPaneSwitcherLeavesCursorContextsAlone(t *testing.T) {
	contexts := []string{
		"notes-editor", "notes-search",
		"file-browser-quick-open", "file-browser-project-search",
		"global-workspaces-filter", "project-switcher",
	}
	for _, context := range contexts {
		sawCursorDown := false
		for _, b := range keymap.DefaultBindings() {
			if b.Context != context {
				continue
			}
			if b.Command == "open-pane" {
				t.Errorf("%s: binds open-pane, but ctrl+n there moves a cursor", context)
			}
			if b.Key == "ctrl+n" {
				if b.Command != "cursor-down" {
					t.Errorf("%s: ctrl+n is bound to %q, want cursor-down", context, b.Command)
				}
				sawCursorDown = true
			}
		}
		if !sawCursorDown {
			t.Errorf("%s: no ctrl+n binding at all — the premise this test protects is gone", context)
		}
	}

	// The same rule where the cursor is not in this file at all. The note
	// preview answers ctrl+n/ctrl+p as its own cursor motion inside the plugin
	// (notes.handleEditorPreviewKey), with nothing in the keymap to show for it,
	// which is exactly how it came to be on the switcher's list once.
	for _, context := range []string{"notes-preview"} {
		for _, b := range keymap.DefaultBindings() {
			if b.Context == context && b.Command == "open-pane" {
				t.Errorf("%s: binds open-pane, but the plugin answers ctrl+n there with a cursor move", context)
			}
		}
	}

	// td's input contexts, and the modal whose keys tdmonitor.BlocksGlobalKeys
	// hands to the embedded td model wholesale. These have no ctrl+n row of their
	// own to protect — td's registry owns every binding in them — so what is
	// asserted is only that the switcher never appears there. The td-side reason
	// for each is in tdmonitor's own context test.
	for _, context := range []string{
		"td-search", "td-form", "td-board-editor", "td-confirm", "td-close-confirm",
		"td-modal",
	} {
		for _, b := range keymap.DefaultBindings() {
			if b.Context == context && b.Command == "open-pane" {
				t.Errorf("%s: binds open-pane, but every key in that context goes to td before the host sees it", context)
			}
		}
	}
}

// The terminal preview keeps its own entry, and both surfaces spell it the
// same way. `n` there belongs to the list's create, which is why the preview
// answers `o` instead.
func TestPaneSwitcherPreviewEntryMatchesOnBothSurfaces(t *testing.T) {
	keyFor := func(context string) string {
		for _, b := range keymap.DefaultBindings() {
			if b.Context == context && b.Command == "open-pane" {
				return b.Key
			}
		}
		return ""
	}
	project, global := keyFor("workspace-preview"), keyFor("global-workspaces")
	if project == "" {
		t.Error("workspace-preview lost its pane-switcher binding")
	}
	if global == "" {
		t.Error("global-workspaces has no pane-switcher binding, so the browser's preview cannot open one")
	}
	if project != "" && global != "" && project != global {
		t.Errorf("the preview switcher is %q on the project surface and %q on the global one", project, global)
	}
}

// Moving the Diff pane's next-change off `n` is only safe if it landed
// somewhere, in both Diff contexts. An unbound command is a key with no label
// in the footer and no row in the help sheet.
func TestDiffNextChangeMovedOffTheSwitcherKey(t *testing.T) {
	for _, context := range []string{"workspace-diff", "global-workspaces-diff"} {
		keys := map[string]bool{}
		for _, b := range keymap.DefaultBindings() {
			if b.Context == context && b.Command == "diff-next-change" {
				keys[b.Key] = true
			}
		}
		if keys["n"] || keys["N"] {
			t.Errorf("%s: diff-next-change still claims n/N, which the pane switcher now owns", context)
		}
		for _, want := range []string{">", "<"} {
			if !keys[want] {
				t.Errorf("%s: diff-next-change is not bound to %q", context, want)
			}
		}
	}
}
