package overview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestGlobalPreviewDocumentLinksRegisterAndActivateIssueFileAndDiff(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	ws, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "links.txt"), []byte("README.md\ntd-196c42\nabcdef0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.ensurePreviewDocLinkResolution().Put(
		contentlink.Pending{Kind: contentlink.KindFile, Raw: "README.md"},
		contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"},
		true,
	)
	m.ensurePreviewDocLinkResolution().Put(
		contentlink.Pending{Kind: contentlink.KindDiff, Raw: "abcdef0"},
		contentlink.Ref{Kind: contentlink.KindDiff, Value: "c:abcdef0"},
		true,
	)
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "links.txt", Line: 1}, "Document"))
	if m.preview.doc == nil || m.preview.doc.view() == nil {
		t.Fatal("document pane did not open")
	}
	m.preview.doc.view().SetRendered(false)

	view := m.WorkspacesView(previewWide, previewTall)
	if !strings.Contains(view, "\x1b[4m") {
		t.Fatalf("document body was not decorated: %q", ansi.Strip(view))
	}

	issueHit := previewDocLinkHitAt(t, m, contentlink.KindIssue, "td-196c42")
	tab := previewDocTabRegion(m)
	if tab == nil {
		t.Fatal("document tab strip has no hit region")
	}
	if resolved := m.workspacesMouse.HitMap.Test(tab.Rect.X+1, tab.Rect.Y); resolved == nil || resolved.ID != previewDocTabKind {
		t.Fatalf("tab row resolved to %#v, want %s", resolved, previewDocTabKind)
	}

	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: issueHit.Rect.X, Y: issueHit.Rect.Y, Button: tea.MouseLeft}))
	if m.preview.issue == nil || m.preview.issue.view() == nil || m.preview.issue.view().IssueID() != "td-196c42" {
		t.Fatalf("issue pane = %#v", m.preview.issue)
	}

	m.WorkspacesView(previewWide, previewTall)
	fileHit := previewDocLinkHitAt(t, m, contentlink.KindFile, "README.md")
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: fileHit.Rect.X, Y: fileHit.Rect.Y, Button: tea.MouseLeft}))
	if m.preview.doc == nil || m.preview.doc.view() == nil || m.preview.doc.view().Title() != "README.md" {
		t.Fatalf("document after file click = %#v", m.preview.doc)
	}

	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "links.txt", Line: 1}, "Document"))
	m.preview.doc.view().SetRendered(false)
	m.ensurePreviewDocLinkResolution().Put(
		contentlink.Pending{Kind: contentlink.KindDiff, Raw: "abcdef0"},
		contentlink.Ref{Kind: contentlink.KindDiff, Value: "c:abcdef0"},
		true,
	)
	m.WorkspacesView(previewWide, previewTall)
	diffHit := previewDocLinkHitAt(t, m, contentlink.KindDiff, "c:abcdef0")
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: diffHit.Rect.X, Y: diffHit.Rect.Y, Button: tea.MouseLeft}))
	if m.preview.diff == nil || m.preview.diff.tabs.Find("c:abcdef0") < 0 {
		t.Fatalf("diff pane = %#v", m.preview.diff)
	}
}

func previewDocLinkHitAt(t *testing.T, m *Model, kind contentlink.Kind, value string) mouse.Region {
	t.Helper()
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if region.ID != previewDocLinkKind {
			continue
		}
		hit, ok := region.Data.(previewDocLinkHit)
		if !ok || hit.Ref.Kind != kind || hit.Ref.Value != value {
			continue
		}
		resolved := m.workspacesMouse.HitMap.Test(region.Rect.X, region.Rect.Y)
		if resolved == nil || resolved.ID != previewDocLinkKind {
			t.Fatalf("%s %s resolves to %#v, want %s", kind, value, resolved, previewDocLinkKind)
		}
		return region
	}
	t.Fatalf("no %s link for %q", kind, value)
	return mouse.Region{}
}

func previewDocTabRegion(m *Model) *mouse.Region {
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if region.ID != previewDocTabKind {
			continue
		}
		if hit, ok := region.Data.(previewDocTabHit); ok && !hit.Close {
			copy := region
			return &copy
		}
	}
	return nil
}
