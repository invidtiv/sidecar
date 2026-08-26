package tdmonitor

import (
	"testing"

	"github.com/marcus/sidecar/internal/keymap"
	tdkeymap "github.com/marcus/td/pkg/monitor/keymap"
)

// The pane switcher's entry is bound per keymap context, and this plugin does
// not mint its own: td names the context and monitor/keymap.ContextToSidecar
// spells it for sidecar. So the strings in sidecar's keymap are a copy of
// someone else's constants, and a rename upstream would leave the switcher
// unreachable from td with nothing in this repo to show for it. Deriving them
// here is what turns that into a failure.
func TestBrowseContextsCarryThePaneSwitcherEntry(t *testing.T) {
	// The views where td is browsing its own issues with nothing overlaid.
	for _, ctx := range []tdkeymap.Context{
		tdkeymap.ContextMain,
		tdkeymap.ContextBoard,
		tdkeymap.ContextKanban,
	} {
		name := tdkeymap.ContextToSidecar(ctx)
		t.Run(name, func(t *testing.T) {
			if key := paneSwitcherKeyForContext(name); key != "ctrl+n" {
				t.Fatalf("%s: open-pane is bound to %q, want ctrl+n — the switcher is unreachable from here", name, key)
			}
			// A binding only fires where the key reaches the host's rung, and
			// either of these hands every key to the embedded model first.
			if contextConsumesTextInput(name) || contextBlocksGlobalKeys(name) {
				t.Fatalf("%s: the plugin forwards every key in this context, so the binding can never fire", name)
			}
		})
	}
}

// The contexts that must not carry it, for the two different reasons they must
// not. The input contexts type; td-modal and its sub-focus states are one open
// modal whose keys this plugin already routes to td wholesale, so a binding
// there would be a key that does nothing — which is worse than no key, because
// the footer would advertise it.
func TestOverlayAndInputContextsDoNotCarryThePaneSwitcherEntry(t *testing.T) {
	for _, ctx := range []tdkeymap.Context{
		tdkeymap.ContextSearch, tdkeymap.ContextForm, tdkeymap.ContextBoardEditor,
		tdkeymap.ContextConfirm, tdkeymap.ContextCloseConfirm,
		tdkeymap.ContextModal, tdkeymap.ContextEpicTasks, tdkeymap.ContextParentEpicFocused,
		tdkeymap.ContextBlockedByFocused, tdkeymap.ContextBlocksFocused,
		tdkeymap.ContextStats, tdkeymap.ContextHandoffs, tdkeymap.ContextNotes,
		tdkeymap.ContextHelp, tdkeymap.ContextTDQHelp, tdkeymap.ContextBoardPicker,
		tdkeymap.ContextGettingStarted, tdkeymap.ContextSyncPrompt,
	} {
		name := tdkeymap.ContextToSidecar(ctx)
		if key := paneSwitcherKeyForContext(name); key != "" {
			t.Errorf("%s: binds open-pane to %q, but the switcher cannot open from there", name, key)
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
