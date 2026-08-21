package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func previewDocTabCloseRegion(m *Model) *mouse.Region {
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		hit, ok := region.Data.(previewDocTabHit)
		if ok && hit.Close {
			copied := region
			return &copied
		}
	}
	return nil
}

// The per-tab × is the pane X's smaller twin, so it owes the same feedback:
// it lights under the pointer, it lets go when the pointer leaves, and the
// row it lives in does not move while either happens.
func TestGlobalPreviewTabCloseHoverRestylesTheGlyph(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{
		X:      previewNeedleAction(t, m, "README.md").X,
		Y:      previewNeedleAction(t, m, "README.md").Y,
		Button: tea.MouseLeft,
	}))
	rest := m.WorkspacesView(previewWide, previewTall)
	region := previewDocTabCloseRegion(m)
	if region == nil {
		t.Fatal("no per-tab close region on the doc preview")
	}

	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: region.Rect.X, Y: region.Rect.Y}))
	if m.tabCloseHoverIn(panelayout.Document) < 0 {
		t.Fatalf("pointer inside the × did not hover it: %+v", m.hoverTabClose)
	}
	hovered := m.WorkspacesView(previewWide, previewTall)
	if hovered == rest {
		t.Fatal("hover painted nothing")
	}
	if ansi.Strip(hovered) != ansi.Strip(rest) {
		t.Fatal("hover moved the glyphs in the row")
	}

	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: 0, Y: 0}))
	if m.tabCloseHoverIn(panelayout.Document) >= 0 {
		t.Fatal("hover did not clear off the ×")
	}
	if m.WorkspacesView(previewWide, previewTall) != rest {
		t.Fatal("leaving the × did not restore the resting row")
	}
}

// Hovering the label half of a pill must not light the ×: that half selects.
func TestGlobalPreviewTabBodyDoesNotHoverTheClose(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{
		X:      previewNeedleAction(t, m, "README.md").X,
		Y:      previewNeedleAction(t, m, "README.md").Y,
		Button: tea.MouseLeft,
	}))
	m.WorkspacesView(previewWide, previewTall)
	region := previewDocTabCloseRegion(m)
	if region == nil {
		t.Fatal("no per-tab close region on the doc preview")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: region.Rect.X - 2, Y: region.Rect.Y}))
	if m.tabCloseHoverIn(panelayout.Document) >= 0 {
		t.Fatal("the label half of the pill lit the ×")
	}
}
