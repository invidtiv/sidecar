package overview

import (
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func assertPreviewDeckProjection(t *testing.T, m *Model, kind panelayout.Kind, viewer any) {
	t.Helper()
	if m.preview.deck == nil {
		t.Fatal("content open did not create the shared preview deck")
	}
	leafID := m.preview.deck.Leaf(kind)
	leaf := panelayout.FirstOfKind(m.preview.paneRoot, kind)
	if leaf == nil || leaf.ID != leafID {
		t.Fatalf("host leaf = %#v, shared deck leaf=%d", leaf, leafID)
	}
	items, active := m.preview.deck.Tabs(leafID)
	if len(items) != 1 || active != 0 {
		t.Fatalf("shared deck tabs = %d active=%d, want one active tab", len(items), active)
	}
	if items[0].Viewer != viewer {
		t.Fatalf("host viewer %p differs from shared deck viewer %p", viewer, items[0].Viewer)
	}
}

func TestGlobalContentKindsAreSharedDeckProjections(t *testing.T) {
	t.Run("Document", func(t *testing.T) {
		m := linkPreviewModel(t, workspaceinventory.KindWorktree)
		run(t, m, openPreviewDocSpan(m, mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
		assertPreviewDeckProjection(t, m, panelayout.Document, m.preview.doc.view())
	})

	t.Run("Issue", func(t *testing.T) {
		stubPreviewTd(t)
		m := linkPreviewModel(t, workspaceinventory.KindWorktree)
		run(t, m, m.openPreviewIssue("td-1111aa"))
		assertPreviewDeckProjection(t, m, panelayout.Issue, m.preview.issue.view())
	})

	t.Run("Diff", func(t *testing.T) {
		m := linkPreviewModel(t, workspaceinventory.KindWorktree)
		run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
		assertPreviewDeckProjection(t, m, panelayout.Diff, m.preview.diff.view())
	})

	t.Run("Resource", func(t *testing.T) {
		m := resourcePreviewModel(t)
		m.SetResourceMatchers(jiraMatchers())
		m.SetResourceResolver((&fakeResolver{}).resolve)
		clickResourceKey(t, m, "CASH-1245")
		assertPreviewDeckProjection(t, m, panelayout.Resource, m.preview.resource.view())
	})
}

func TestGlobalShellDiffDeckBindsProjectRoot(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindShell)
	ws := m.catalog["a"]
	ws.Path = t.TempDir()
	ws.ProjectRoot = t.TempDir()
	m.catalog["a"] = ws
	result := m.results["sidecar"]
	result.ProjectRoot = ws.ProjectRoot
	result.Workspaces[0] = ws
	m.results["sidecar"] = result
	m.syncBoard()
	m.workspaces.SelectID("a")
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if got := m.preview.diff.view().WorkDir; got != ws.ProjectRoot {
		t.Fatalf("shell Diff WorkDir = %q, want ProjectRoot %q (shell cwd %q)", got, ws.ProjectRoot, ws.Path)
	}
}
