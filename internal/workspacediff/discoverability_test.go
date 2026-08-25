package workspacediff

import (
	"testing"

	"github.com/marcus/sidecar/internal/keymap"
)

var diffContexts = []string{"workspace-diff", "global-workspaces-diff"}

func bindingsFor(context string) []keymap.Binding {
	var out []keymap.Binding
	for _, b := range keymap.DefaultBindings() {
		if b.Context == context {
			out = append(out, b)
		}
	}
	return out
}

// Every key the viewer answers is registered, in both Diff contexts, or it is
// invisible to the footer, the help sheet and the command palette.
func TestEveryViewerKeyIsRegistered(t *testing.T) {
	want := []string{
		"j", "k", "down", "up", "g", "G",
		"l", "right", "enter", "h", "left",
		"v", "z", "f",
		// Next / previous change. These were `n` / `N` until the pane switcher
		// took `n` in every content pane; the viewer no longer answers either
		// of those, so neither belongs in this list.
		">", "<",
		"ctrl+d", "ctrl+u", "pgup", "pgdown",
		",", ".",
	}
	for _, context := range diffContexts {
		bound := map[string]bool{}
		for _, b := range bindingsFor(context) {
			bound[b.Key] = true
		}
		for _, key := range want {
			if key == "f" && context == "global-workspaces-diff" {
				continue // the global preview has no file picker
			}
			if !bound[key] {
				t.Errorf("%s: key %q is handled by the viewer but registered nowhere", context, key)
			}
		}
	}
}

// Commands and bindings must agree: a command with no binding never reaches the
// footer, and a binding whose command nothing names is a key with no label.
func TestViewerCommandsAllHaveBindings(t *testing.T) {
	modes := []ViewMode{ViewUnified, ViewSideBySide, ViewFullFile}
	focuses := []Focus{FocusFileList, FocusDiff, FocusCommitFiles, FocusCommitDiff}
	for _, context := range diffContexts {
		commands := map[string]bool{}
		for _, b := range bindingsFor(context) {
			commands[b.Command] = true
		}
		for _, mode := range modes {
			for _, focus := range focuses {
				v := &View{
					ViewMode: mode, Focus: focus,
					Files: []File{{Path: "a.go"}, {Path: "b.go"}},
				}
				for _, cmd := range v.Commands(context) {
					if cmd.Context != context {
						t.Errorf("%s: command %q declares context %q", context, cmd.ID, cmd.Context)
					}
					if cmd.Name == "" {
						t.Errorf("%s: command %q has no footer name", context, cmd.ID)
					}
					if len(cmd.Name) > 8 {
						t.Errorf("%s: command %q name %q is too long for the footer", context, cmd.ID, cmd.Name)
					}
					if !commands[cmd.ID] {
						t.Errorf("%s: command %q (mode %d, focus %d) has no key binding", context, cmd.ID, mode, focus)
					}
				}
			}
		}
	}
}

// The footer leads with how to move around the diff.
func TestNavigationLeadsTheFooter(t *testing.T) {
	v := &View{Files: []File{{Path: "a.go"}, {Path: "b.go"}}}
	cmds := v.Commands("workspace-diff")
	byID := map[string]int{}
	for _, c := range cmds {
		byID[c.ID] = c.Priority
	}
	for _, id := range []string{"diff-open", "diff-down", "diff-up"} {
		if p, ok := byID[id]; !ok || p > 5 {
			t.Fatalf("%s priority = %d (present %v), want a leading hint", id, p, ok)
		}
	}
}

// One rule everywhere: wherever { and } are bound at all, they cycle tabs.
// The Diff pane was the exception until they were swapped with , / . — this is
// what stops the exception coming back.
func TestBracesAlwaysMeanTabCycling(t *testing.T) {
	for _, b := range keymap.DefaultBindings() {
		switch b.Key {
		case "{":
			if b.Command != "prev-tab" {
				t.Errorf("%s: { is bound to %q, want prev-tab", b.Context, b.Command)
			}
		case "}":
			if b.Command != "next-tab" {
				t.Errorf("%s: } is bound to %q, want next-tab", b.Context, b.Command)
			}
		}
	}
}

// Both Diff surfaces answer the same keys with the same commands. A binding
// that lands on one and not the other is a parity bug.
func TestDiffNavigationKeysMatchAcrossBothSurfaces(t *testing.T) {
	want := map[string]string{
		"{": "prev-tab", "}": "next-tab",
		",": "prev-file", ".": "next-file",
	}
	for _, context := range diffContexts {
		got := map[string]string{}
		for _, b := range bindingsFor(context) {
			if _, interesting := want[b.Key]; interesting {
				got[b.Key] = b.Command
			}
		}
		for key, command := range want {
			if got[key] != command {
				t.Errorf("%s: %q is bound to %q, want %q", context, key, got[key], command)
			}
		}
	}
}
