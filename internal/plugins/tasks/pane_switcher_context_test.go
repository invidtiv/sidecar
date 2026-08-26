package tasks

import (
	"testing"

	"github.com/marcus/sidecar/internal/keymap"
	tasksui "github.com/marcus/tasks/pkg/tui"
)

// The pane switcher's entry is bound per keymap context, and this plugin does
// not mint its own: Tasks names the context and the plugin reports it verbatim.
// So the strings in sidecar's keymap are a copy of someone else's constants, and
// a rename upstream would leave the switcher unreachable from Tasks with nothing
// in this repo to show for it. Deriving them here is what turns that into a
// failure.
//
// The set is exactly Tasks' root contexts, and that is not a coincidence:
// everything else Tasks reports is an overlay, and BlocksGlobalKeys hands those
// keys to the tab at precedence level 2, above the host's own rung. Root is
// therefore both "browse or preview" and "a key can reach the host here".
func TestBrowseContextsCarryThePaneSwitcherEntry(t *testing.T) {
	for _, ctx := range []tasksui.FocusContext{
		tasksui.FocusList,
		tasksui.FocusDetail,
		tasksui.FocusResponse,
		tasksui.FocusResponseDetail,
	} {
		name := string(ctx)
		t.Run(name, func(t *testing.T) {
			if !IsRootContext(name) {
				t.Fatalf("%s is no longer a root context, so its keys go to the tab before the host sees them", name)
			}
			if key := paneSwitcherKeyForContext(name); key != "ctrl+n" {
				t.Fatalf("%s: open-pane is bound to %q, want ctrl+n — the switcher is unreachable from here", name, key)
			}
		})
	}
}

// The other half: a Tasks context that is not root is an overlay or an input,
// and the plugin forwards every key there. A binding on one would be a key that
// does nothing and a footer hint that lies.
func TestNonRootTasksContextsDoNotCarryThePaneSwitcherEntry(t *testing.T) {
	for _, b := range keymap.DefaultBindings() {
		if b.Command != "open-pane" || !IsTasksContext(b.Context) {
			continue
		}
		if !IsRootContext(b.Context) {
			t.Errorf("%s binds open-pane, but Tasks owns every key in a non-root context", b.Context)
		}
	}
}

func paneSwitcherKeyForContext(context string) string {
	for _, b := range keymap.DefaultBindings() {
		if b.Context == context && b.Command == "open-pane" {
			return b.Key
		}
	}
	return ""
}
