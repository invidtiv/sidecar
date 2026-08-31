package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/tty"
)

// strandedTerminalLeafPlugin builds the state every exitInteractiveMode() caller
// leaves behind: the terminal leaf is still the focused pane, drawn focused, but
// the host has dropped back to ViewModeList and no longer owns the keyboard.
//
// Eight sites produce it without moving focus off the terminal — the reposition
// modal (pane_reposition_modal.go:36), SetFocused(false) on a tab switch
// (plugin.go:738), a selection change (plugin.go:2067), a press away from the
// terminal regions (mouse.go:878), a preview action chip (mouse.go:49), and the
// three session-death messages. Reaching it through exitInteractiveMode is the
// shared truth all of them assert.
func strandedTerminalLeafPlugin(t *testing.T) (*Plugin, *panelayout.Node) {
	t.Helper()
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# n\n")
	p := docPaneTestPlugin(t, root, false)
	p.openTerminalPath("README.md", 1)
	primary := panelayout.FirstOfKind(p.paneRoot, panelayout.Terminal)
	if primary == nil {
		t.Fatal("fixture has no primary terminal")
	}
	terminal := tty.New(nil)
	terminal.ExitAction = tty.ExitReleasesInput
	terminal.State = &tty.State{Active: true}
	p.primaryTermPane().Terminal = terminal

	p.activePane = PanePreview
	p.paneFocus = primary.ID
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, LeafID: primary.ID}

	// The transition under test: input ownership goes away, focus does not move.
	p.exitInteractiveMode()

	if p.activePane != PanePreview || p.paneFocus != primary.ID {
		t.Fatalf("exitInteractiveMode moved focus off the terminal leaf: pane=%v focus=%d", p.activePane, p.paneFocus)
	}
	if p.viewMode != ViewModeList {
		t.Fatalf("host did not fall back to browse mode: %v", p.viewMode)
	}
	return p, primary
}

// TestBrowseModeTerminalLeafDoesNotSpendNOnCreate is the printable-key pin the
// terminal leaf never got. Every passive leaf kind — doc, issue, note, diff,
// resource — has its own FocusContext branch precisely so a pane drawn as
// focused cannot lose its keys to the list's vocabulary. A focused terminal leaf
// in browse mode falls through to "workspace-preview", which binds no `n`, so
// the key walks the whole app ladder down to level 5 and lands in
// handleListKeys' unguarded `case "n"` — opening the create-workspace modal on
// top of the shell the user believes they are typing into.
//
// `o` two cases below is already guarded on activePane; `n` was meant to be
// (keys.go: "The sidebar's n still opens it name-focused") and never was.
func TestBrowseModeTerminalLeafDoesNotSpendNOnCreate(t *testing.T) {
	p, _ := strandedTerminalLeafPlugin(t)

	if got := p.FocusContext(); got != "workspace-preview" {
		t.Fatalf("focused terminal leaf in browse mode reports %q, expected workspace-preview", got)
	}
	if leaf := panelayout.Find(p.paneRoot, p.paneFocus); leaf == nil || !panelayout.IsLive(leaf.Kind) {
		t.Fatalf("fixture no longer holds focus on a live terminal leaf: %+v", leaf)
	}

	p.handleKeyPress(tea.KeyPressMsg{Code: 'n', Text: "n"})

	if p.viewMode == ViewModeCreate {
		t.Fatal("`n` opened the create-workspace modal while a terminal leaf held focus: " +
			"a printable key was stolen from a pane drawn as focused")
	}
}

// TestSidebarKeepsNForCreate pins the other half: the guard must not cost the
// list its own key. `n` from the sidebar is how a workspace gets created.
func TestSidebarKeepsNForCreate(t *testing.T) {
	p, _ := strandedTerminalLeafPlugin(t)
	p.activePane = PaneSidebar

	if got := p.FocusContext(); got != "workspace-list" {
		t.Fatalf("sidebar reports %q, expected workspace-list", got)
	}

	p.handleKeyPress(tea.KeyPressMsg{Code: 'n', Text: "n"})

	if p.viewMode != ViewModeCreate {
		t.Fatalf("`n` no longer opens the create modal from the sidebar: mode=%v", p.viewMode)
	}
}
