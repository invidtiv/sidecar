package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func previewCloseRegion(m *Model, kind panelayout.Kind) *mouse.Region {
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		hit, ok := region.Data.(previewPaneCloseHit)
		if ok && hit.Kind == kind {
			copy := region
			return &copy
		}
	}
	return nil
}

func clickPreviewClose(t *testing.T, m *Model, kind panelayout.Kind) {
	t.Helper()
	region := previewCloseRegion(m, kind)
	if region == nil {
		t.Fatalf("no close region for %v", kind)
	}
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{
		X: region.Rect.X + region.Rect.W/2, Y: region.Rect.Y, Button: tea.MouseLeft,
	}))
}

func TestGlobalPreviewCloseButtonsCloseEachPane(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{
		X:      previewNeedleAction(t, m, "README.md").X,
		Y:      previewNeedleAction(t, m, "README.md").Y,
		Button: tea.MouseLeft,
	}))
	run(t, m, m.openPreviewIssue("td-196c42"))
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, ui.CloseButtonLabel) {
		t.Fatalf("global headers have no X: %q", view)
	}
	if previewCloseRegion(m, panelayout.Document) == nil ||
		previewCloseRegion(m, panelayout.Issue) == nil ||
		previewCloseRegion(m, panelayout.Diff) == nil {
		t.Fatal("a global content pane has no close region")
	}

	clickPreviewClose(t, m, panelayout.Issue)
	if m.preview.issue != nil {
		t.Fatal("issue X left the issue pane")
	}
	clickPreviewClose(t, m, panelayout.Diff)
	if m.preview.diff != nil {
		t.Fatal("diff X left the Diff pane")
	}
	clickPreviewClose(t, m, panelayout.Document)
	if m.preview.doc != nil {
		t.Fatal("doc X left the document pane")
	}
}

func TestGlobalPreviewCloseButtonHoverRestylesTheX(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{
		X:      previewNeedleAction(t, m, "README.md").X,
		Y:      previewNeedleAction(t, m, "README.md").Y,
		Button: tea.MouseLeft,
	}))
	m.WorkspacesView(previewWide, previewTall)
	region := previewCloseRegion(m, panelayout.Document)
	if region == nil {
		t.Fatal("no doc close region")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: region.Rect.X, Y: region.Rect.Y}))
	if !m.previewCloseHover || m.hoverPreviewClose != panelayout.Document {
		t.Fatalf("hover = %v kind=%v region=%#v", m.previewCloseHover, m.hoverPreviewClose, region)
	}
	hovered := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(hovered, ui.CloseButtonLabel) {
		t.Fatalf("hovered header dropped the X: %q", hovered)
	}
	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: 0, Y: 0}))
	if m.previewCloseHover {
		t.Fatal("hover did not clear off the button")
	}
}
