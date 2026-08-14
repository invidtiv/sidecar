package workspace

import (
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
)

// Slice 4 items 2-3 of docs/plans/active/global-overview-workspaces.md, from the
// project's side: a selection that arrives from the global browser is handled by
// this plugin's own rules, including the pane layout ones.
//
// Nothing global reads or writes a pane layout — it hands over an identity. The
// exact-identity resolution itself (duplicate names, stale targets) is already
// covered in pending_overview_selection_test.go and is not restated here.

// navigatedShellPlugin is a two-shell project whose persisted state selects the
// first shell with a document open beside it.
func navigatedShellPlugin(t *testing.T) (*Plugin, *state.WorkspaceState) {
	t.Helper()
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# restored\n")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	p.shells = append(p.shells, &ShellSession{Name: "Shell B", TmuxName: "test-shell-b",
		Agent: &Agent{TmuxPane: "%903", OutputBuf: tty.NewOutputBuffer(20)}})
	p.worktreesLoaded = true
	p.shellStartupLoading = false

	saved := &state.WorkspaceState{
		ShellTmuxName: "test-shell",
		PaneLayout: &state.PaneLayoutJSON{Root: resolved, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
			Axis: "cols", Ratio: 60,
			A: &state.PaneLayoutJSON{Kind: "terminal"},
			B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "README.md", Mode: "rendered"}}},
		}},
	}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return *saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { *saved = next; return nil },
	}
	if !p.restoreSelectionState() {
		t.Fatal("persisted selection was not restored")
	}
	if p.activeDocPaneOrNil() == nil {
		t.Fatal("persisted pane layout did not restore its document")
	}
	return p, saved
}

func TestNavigatingToTheDocumentsOwnerRestoresIt(t *testing.T) {
	p, saved := navigatedShellPlugin(t)

	p.SetPendingWorkspaceSelection(plugin.PendingWorkspaceSelection{
		Kind: plugin.WorkspaceSelectionShell, Key: "test-shell",
	})

	if !p.shellSelected || p.selectedShellIdx != 0 {
		t.Fatalf("navigation moved off the owning shell: shell=%v idx=%d", p.shellSelected, p.selectedShellIdx)
	}
	doc := p.activeDocPaneOrNil()
	if doc == nil || doc.view().Title() != "README.md" {
		t.Fatalf("navigating to the document's owner closed it: %#v", doc)
	}
	if p.paneRestoreCmd == nil {
		t.Fatal("the restored document's load was dropped")
	}
	if !layoutHasDocPath(workspacePaneLayout(*saved, "shell:test-shell"), "README.md") {
		t.Fatalf("the persisted layout was rewritten: %#v", saved.PaneLayouts)
	}
}

func TestNavigatingToASiblingClosesTheDocumentThroughTheProjectsOwnRule(t *testing.T) {
	p, saved := navigatedShellPlugin(t)

	p.SetPendingWorkspaceSelection(plugin.PendingWorkspaceSelection{
		Kind: plugin.WorkspaceSelectionShell, Key: "test-shell-b",
	})

	if !p.shellSelected || p.selectedShellIdx != 1 {
		t.Fatalf("navigation did not land on the sibling shell: shell=%v idx=%d", p.shellSelected, p.selectedShellIdx)
	}
	if doc := p.activeDocPaneOrNil(); doc != nil {
		t.Fatalf("the sibling kept the other shell's document: %#v", doc)
	}
	if p.paneRoot == nil || p.paneRoot.Split != nil || p.paneRoot.Kind != PaneTerminal {
		t.Fatalf("the doc subtree was not collapsed: %#v", p.paneRoot)
	}
	if p.paneRestoreCmd != nil {
		t.Fatal("a load for the closed document is still pending")
	}
	// A's document stays in the map; B's live tree is terminal-only.
	if !layoutHasDocPath(workspacePaneLayout(*saved, "shell:test-shell"), "README.md") {
		t.Fatalf("shell A layout missing after navigating to B: %#v", saved.PaneLayouts)
	}
	if layoutHasDocPath(workspacePaneLayout(*saved, "shell:test-shell-b"), "README.md") {
		t.Fatalf("sibling stole the document: %#v", saved.PaneLayouts)
	}
	if saved.ShellTmuxName != "test-shell-b" {
		t.Fatalf("persisted selection = %q, want the navigated shell", saved.ShellTmuxName)
	}
	// Navigation is a selection, not an attach: nothing entered interactive
	// mode and nothing was typed at a terminal.
	if p.viewMode == ViewModeInteractive {
		t.Fatal("navigation entered interactive mode")
	}
}

// Shell discovery and the worktree refresh race at startup. Whichever finishes
// last, the destination the user chose in the global browser has to win over
// the project's persisted selection.
func TestNavigatedDestinationOutranksThePersistedSelection(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	second := t.TempDir()
	saved := state.WorkspaceState{ShellTmuxName: "test-shell"}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}

	// The activation arrives while both collections are still loading, so it
	// waits rather than selecting a neighbour.
	p.worktrees, p.shells = nil, nil
	p.worktreesLoaded, p.shellStartupLoading = false, true
	p.SetPendingWorkspaceSelection(plugin.PendingWorkspaceSelection{
		Kind: plugin.WorkspaceSelectionWorktree, Path: second,
	})
	if p.pendingOverviewSelection == nil {
		t.Fatal("the activation was consumed before the lists loaded")
	}

	// Both arms land, and the one-time state restoration runs.
	p.worktrees = []*Worktree{{Name: "main", Path: root}, {Name: "topic", Path: second}}
	p.shells = []*ShellSession{{Name: "Shell", TmuxName: "test-shell"}}
	p.worktreesLoaded, p.shellStartupLoading = true, false
	p.completeInitialWorkspaceLoad()

	if p.shellSelected || p.selectedIdx != 1 {
		t.Fatalf("persisted state won over the navigated destination: shell=%v idx=%d", p.shellSelected, p.selectedIdx)
	}
	if p.pendingOverviewSelection != nil {
		t.Fatalf("the activation was not consumed: %#v", p.pendingOverviewSelection)
	}
}
