package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestGlobalPreviewActionChips(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))

	m.workspaces.SelectID("a")
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "Diff") {
		t.Fatalf("topic worktree preview missing Diff chip:\n%s", view)
	}
	if strings.Contains(view, "Output") {
		t.Fatalf("topic worktree still drew an Output tab:\n%s", view)
	}

	m.workspaces.SelectID("c")
	shell := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(shell, "Diff") {
		t.Fatalf("shell preview missing Diff chip:\n%s", shell)
	}
	if strings.Contains(shell, "Task") {
		t.Fatalf("shell preview drew a Task chip:\n%s", shell)
	}

	ws := m.catalog["a"]
	ws.IsMain = true
	m.catalog["a"] = ws
	m.workspaces.SelectID("a")
	main := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(main, "Diff") {
		t.Fatalf("main worktree preview missing Diff chip:\n%s", main)
	}
}

func TestCommaAndPeriodDoNotCycleGlobalPreview(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.workspaces.SelectID("a")
	before := m.workspaces.SelectedID()

	handled, _ := m.WorkspacesKey(key("."))
	if handled {
		t.Fatal(". on the list was consumed as next-tab")
	}
	if m.preview.diff != nil {
		t.Fatal(". on the list opened a Diff leaf")
	}
	handled, _ = m.WorkspacesKey(key(","))
	if handled {
		t.Fatal(", on the list was consumed as prev-tab")
	}
	if m.preview.diff != nil {
		t.Fatal(", on the list opened a Diff leaf")
	}
	if m.workspaces.SelectedID() != before {
		t.Fatal(",/. moved the list selection")
	}
}

func TestEnterTypesWithoutSwitchingATab(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	m.workspaces.SelectID("a")

	press(t, m, "enter")
	if !m.PreviewInteractive() {
		t.Fatal("enter did not start typing")
	}
	if terminal.target.Pane != "%1" {
		t.Fatalf("typed into %+v, want the selected live pane", terminal.target)
	}
	if m.WorkspaceFocusContext() != "global-workspaces-terminal" {
		t.Fatalf("context after enter = %q, want global-workspaces-terminal", m.WorkspaceFocusContext())
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

func TestDiffActionChipClickDoesNotType(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	m.workspaces.SelectID("a")
	m.WorkspacesView(previewWide, previewTall)

	var chipX, chipY int
	found := false
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		hit, ok := region.Data.(previewActionHit)
		if !ok || hit != previewActionDiff {
			continue
		}
		chipX, chipY = region.Rect.X+1, region.Rect.Y
		found = true
		break
	}
	if !found {
		t.Fatal("no Diff action chip hit region after render")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: chipX, Y: chipY, Button: tea.MouseLeft}))
	if m.preview.diff == nil {
		t.Fatal("Diff chip did not open a Diff leaf")
	}
	if m.PreviewInteractive() {
		t.Fatal("clicking the Diff chip started typing")
	}
	_ = terminal
}
