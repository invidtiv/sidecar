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
