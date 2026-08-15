package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestOverviewCompositorDiffArmIsNotBlank(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if m.preview.diff == nil || m.preview.diff.view() == nil {
		t.Fatal("openPreviewDiff did not attach a Diff view")
	}
	if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Diff) == nil {
		t.Fatal("tree has no Diff leaf")
	}

	// A loaded empty snapshot still paints "Working Tree" chrome. Without an
	// explicit compositor arm the box would be blank.
	view := m.preview.diff.view()
	view.State = workspacediff.LoadStateClean
	view.ApplySnapshot()

	got := m.renderOutputPreview(previewWide, previewTall)
	stripped := ansi.Strip(got)
	if !strings.Contains(stripped, "Working Tree") && !strings.Contains(stripped, "Loading diff") {
		t.Fatalf("Diff compositor arm drew a blank box:\n%s", stripped)
	}
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf("previewTab = %v, want Output", m.previewTab)
	}
}

func TestOverviewDiffFocusContextIsNotRoot(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspacesDiff {
		t.Fatalf("context = %q, want %q", got, ctxGlobalWorkspacesDiff)
	}
	handled, cmd := m.previewDiffPaneKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if !handled {
		t.Fatal("q was not handled")
	}
	if cmd != nil {
		run(t, m, cmd)
	}
	if m.preview.diff != nil {
		t.Fatal("q did not hide the Diff leaf")
	}
	if m.WorkspaceFocusContext() == ctxGlobalWorkspacesDiff {
		t.Fatal("hidden Diff kept global-workspaces-diff context")
	}
}

func TestOverviewOpenPreviewDiffForcesOutput(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	m.previewTab = workspacediff.TabDiff
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf("previewTab = %v, want Output", m.previewTab)
	}
	if m.preview.diff == nil {
		t.Fatal("openPreviewDiff did not open a Diff leaf")
	}
}
