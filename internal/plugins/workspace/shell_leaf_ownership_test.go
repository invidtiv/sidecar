package workspace

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
)

// A split terminal is a peer IN a workspace, not a plugin-wide preference that
// follows the sidebar around. These are the tests that say so: the workspace it
// was opened on keeps it, every other workspace is left alone, and what lands on
// disk says the same thing.

// twoShellPlugin is a drawn plugin with two selectable shells and a workspace
// state that survives a save so a layout can be written and read back.
func twoShellPlugin(t *testing.T) (*Plugin, *state.WorkspaceState) {
	t.Helper()
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	stubTd(t)
	p := docPaneTestPlugin(t, t.TempDir(), true)
	p.shells = append(p.shells, &ShellSession{
		Name: "Second", TmuxName: "test-shell-two",
		Agent: &Agent{TmuxPane: "%903", OutputBuf: tty.NewOutputBuffer(20)},
	})
	saved := &state.WorkspaceState{}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return *saved },
		setWorkspaceState: func(_ string, s state.WorkspaceState) error { *saved = s; return nil },
	}
	p.sidebarVisible = false
	p.View(p.width, p.height)
	return p, saved
}

// selectShell moves the sidebar the way a selection change does: the tree is
// rebuilt for the newly selected workspace from what that workspace saved.
func selectShell(p *Plugin, idx int) {
	p.selectedShellIdx = idx
	p.restoreIncomingPaneLayout()
}

func shellLeafJSON(layout *state.PaneLayoutJSON) *state.PaneLayoutJSON {
	if layout == nil {
		return nil
	}
	if layout.Split != nil {
		if found := shellLeafJSON(layout.Split.A); found != nil {
			return found
		}
		return shellLeafJSON(layout.Split.B)
	}
	if layout.Kind == contentKindShell {
		return layout
	}
	return nil
}

func TestSplitTerminalStaysOnTheWorkspaceItWasOpenedOn(t *testing.T) {
	p, saved := twoShellPlugin(t)

	p.toggleTermPanel()
	if !p.shellLeafVisible() || p.shellLeaf() == nil {
		t.Fatal("ctrl+t did not open a split on the selected workspace")
	}
	firstSurface := p.shellLeafSurface
	if firstSurface != "shell:test-shell" {
		t.Fatalf("split claimed surface %q, want the workspace it was opened on", firstSurface)
	}

	selectShell(p, 1)
	if p.shellLeafVisible() || p.shellLeaf() != nil {
		t.Fatal("the split followed the selection onto a workspace that was never split")
	}
	if p.shellLeafFocused() {
		t.Fatal("a released split left its focus behind")
	}
	p.saveSelectionState()
	if layout := saved.PaneLayoutFor("shell:test-shell-two"); shellLeafJSON(layout) != nil {
		t.Fatal("a workspace the user never split was persisted with a shell leaf")
	}
	if p.paneRowBadge("shell:test-shell-two") != "" {
		t.Fatal("a workspace the user never split wears a split badge")
	}

	selectShell(p, 0)
	if !p.shellLeafVisible() || p.shellLeaf() == nil {
		t.Fatal("the owning workspace did not get its split back")
	}
	if p.shellLeafSurface != firstSurface {
		t.Fatalf("restored split claims surface %q, want %q", p.shellLeafSurface, firstSurface)
	}
}

// The leaf's durable session selector has to be on disk from the save that
// FIRST encodes the leaf, or a relaunch reattaches it to whatever the selection
// happens to derive.
func TestCreatingASplitPersistsItsSessionSelector(t *testing.T) {
	p, saved := twoShellPlugin(t)

	p.createTerminalSplit("dev server", "auto")
	if p.requireShellTermPane().Session == "" {
		t.Fatal("a created split has no session")
	}

	leaf := shellLeafJSON(saved.PaneLayoutFor("shell:test-shell"))
	if leaf == nil {
		t.Fatal("the created split was not persisted as a shell leaf")
	}
	if leaf.Session != p.requireShellTermPane().Session {
		t.Fatalf("persisted session %q, want the leaf's own %q", leaf.Session, p.requireShellTermPane().Session)
	}
	if !strings.HasPrefix(leaf.Session, termPanelSessionPrefix) {
		t.Fatalf("persisted session %q is not a durable selector", leaf.Session)
	}
}
