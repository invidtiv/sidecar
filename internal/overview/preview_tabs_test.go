package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestGlobalPreviewChipsOnlyForNonMainWorktrees(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))

	m.workspaces.SelectID("a")
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !m.previewTabsVisible() {
		t.Fatal("non-main worktree should show Output/Diff/Task chips")
	}
	if !strings.Contains(view, "Output") || !strings.Contains(view, "Diff") || !strings.Contains(view, "Task") {
		t.Fatalf("non-main worktree preview missing tab chips:\n%s", view)
	}

	m.workspaces.SelectID("c")
	if !m.previewTabsVisible() {
		t.Fatal("shell should show Output/Diff chips")
	}
	shell := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(shell, "Output") || !strings.Contains(shell, "Diff") {
		t.Fatalf("shell preview missing Output/Diff chips:\n%s", shell)
	}
	if strings.Contains(shell, "Task") {
		t.Fatalf("shell preview drew a Task chip:\n%s", shell)
	}

	ws := m.catalog["a"]
	ws.IsMain = true
	m.catalog["a"] = ws
	m.workspaces.SelectID("a")
	if m.previewTabsVisible() {
		t.Fatal("main worktree should have no tab row")
	}
}

func TestCommaAndPeriodCycleGlobalPreviewTabs(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.workspaces.SelectID("a")
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf("initial tab = %v, want Output", m.previewTab)
	}

	press(t, m, ".")
	if m.previewTab != workspacediff.TabDiff {
		t.Fatalf("period = %v, want Diff", m.previewTab)
	}
	if m.WorkspaceFocusContext() != "global-workspaces" {
		t.Fatalf("context while Diff is showing = %q, want global-workspaces (list still focused)", m.WorkspaceFocusContext())
	}

	press(t, m, ".")
	if m.previewTab != workspacediff.TabTask {
		t.Fatalf("second period = %v, want Task", m.previewTab)
	}
	press(t, m, ",")
	if m.previewTab != workspacediff.TabDiff {
		t.Fatalf("comma = %v, want Diff", m.previewTab)
	}

	// j/k still move the list while Diff is showing.
	before := m.workspaces.SelectedID()
	press(t, m, "j")
	if m.workspaces.SelectedID() == before {
		t.Fatal("j did not move the list while Diff was showing")
	}
}

func TestGlobalDiffLoadsFirstCommitWithoutCursorMove(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.workspaces.SelectID("a")
	press(t, m, ".")

	cmd := m.applyDiffSnapshot(workspacediff.SnapshotMsg{
		WorkspaceID: "a",
		Snapshot: &workspacediff.Snapshot{
			State: workspacediff.LoadStateReady,
			Commits: []workspacediff.CommitInfo{
				{Hash: "aaa1111", Subject: "first"},
				{Hash: "bbb2222", Subject: "second"},
			},
		},
	})
	if m.diff.Cursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.diff.Cursor)
	}
	if m.diff.FileCount() != 0 {
		t.Fatalf("file count = %d, want 0 so cursor sits on first commit", m.diff.FileCount())
	}
	if cmd == nil {
		t.Fatal("applying snapshot with cursor on first commit did not issue load")
	}
	msg := cmd()
	loaded, ok := msg.(workspacediff.CommitDetailMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want CommitDetailMsg", msg)
	}
	if loaded.Hash != "aaa1111" {
		t.Fatalf("loaded hash = %q, want first commit aaa1111", loaded.Hash)
	}
}

func TestGlobalTaskTabHasNoLinkHint(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.workspaces.SelectID("a")
	press(t, m, ".")
	press(t, m, ".")
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "No linked task") {
		t.Fatalf("task tab missing empty state:\n%s", view)
	}
	if strings.Contains(view, "Press 't'") || strings.Contains(view, "Press t") {
		t.Fatalf("global task tab offered a link key that is not bound:\n%s", view)
	}
}

func TestEnterOnDiffSwitchesToOutputAndTypes(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	m.workspaces.SelectID("a")
	press(t, m, ".")
	if m.previewTab != workspacediff.TabDiff {
		t.Fatal("premise: Diff tab should be showing")
	}

	press(t, m, "enter")
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf("enter left tab at %v, want Output", m.previewTab)
	}
	if !m.PreviewInteractive() {
		t.Fatal("enter from Diff did not start typing")
	}
	if terminal.target.Pane != "%1" {
		t.Fatalf("typed into %+v, want the selected live pane", terminal.target)
	}
	if m.WorkspaceFocusContext() != "global-workspaces-terminal" {
		t.Fatalf("context after enter = %q, want global-workspaces-terminal", m.WorkspaceFocusContext())
	}
}

func TestPeriodOnShellCyclesOutputAndDiff(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.workspaces.SelectID("c")
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf("initial shell tab = %v, want Output", m.previewTab)
	}
	press(t, m, ".")
	if m.previewTab != workspacediff.TabDiff {
		t.Fatalf("period on shell = %v, want Diff", m.previewTab)
	}
	press(t, m, ".")
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf("second period on shell = %v, want Output (not Task)", m.previewTab)
	}
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if strings.Contains(view, "Task") {
		t.Fatalf("shell cycle drew Task:\n%s", view)
	}
}

func TestMainHasNoTabCycle(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	ws := m.catalog["a"]
	ws.IsMain = true
	m.catalog["a"] = ws
	m.workspaces.SelectID("a")
	handled, _ := m.WorkspacesKey(key("."))
	if handled {
		t.Fatal("period on the main worktree was consumed as next-tab")
	}
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf("main tab = %v, want Output", m.previewTab)
	}
}

func TestPreviewDiffPathUsesProjectRootForShells(t *testing.T) {
	shell := workspaceinventory.Workspace{
		Kind: workspaceinventory.KindShell, Path: "/tmp/shell-cwd", ProjectRoot: "/repos/sidecar",
	}
	if got := previewDiffPath(shell); got != "/repos/sidecar" {
		t.Fatalf("shell diff path = %q, want ProjectRoot", got)
	}
	wt := workspaceinventory.Workspace{
		Kind: workspaceinventory.KindWorktree, Path: "/repos/sidecar-feature", ProjectRoot: "/repos/sidecar",
	}
	if got := previewDiffPath(wt); got != "/repos/sidecar-feature" {
		t.Fatalf("worktree diff path = %q, want Path", got)
	}
}

func TestChipClickDoesNotType(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	m.workspaces.SelectID("a")
	m.WorkspacesView(previewWide, previewTall)

	var chipX, chipY int
	found := false
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		tab, ok := region.Data.(previewTabHit)
		if !ok || int(tab) != int(workspacediff.TabDiff) {
			continue
		}
		chipX, chipY = region.Rect.X+1, region.Rect.Y
		found = true
		break
	}
	if !found {
		t.Fatal("no Diff chip hit region after render")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: chipX, Y: chipY, Button: tea.MouseLeft}))
	if m.previewTab != workspacediff.TabDiff {
		t.Fatalf("chip click tab = %v, want Diff", m.previewTab)
	}
	if m.PreviewInteractive() || terminal.opens != 0 {
		t.Fatal("clicking the Diff chip started typing")
	}
	if m.WorkspaceFocusContext() != "global-workspaces" {
		t.Fatalf("context after chip click = %q, want global-workspaces", m.WorkspaceFocusContext())
	}
}
