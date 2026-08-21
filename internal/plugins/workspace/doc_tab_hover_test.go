package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
)

func docTabCloseRegion(p *Plugin, index int) *mouse.Region {
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionDocTab {
			continue
		}
		hit, ok := region.Data.(docTabHit)
		if !ok || !hit.Close || hit.Index != index {
			continue
		}
		copied := region
		return &copied
	}
	return nil
}

func renderedDocTabPlugin(t *testing.T) *Plugin {
	t.Helper()
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	p.mouseHandler.Clear()
	_ = p.renderListView(p.width, p.height)
	return p
}

// The per-tab × answers the pointer the way the pane X does: it lights while
// the pointer is inside it and lets go when the pointer leaves. The row must
// not reflow either way, or the target would move out from under the pointer
// that is hovering it.
func TestDocTabCloseHoverRestylesTheGlyph(t *testing.T) {
	p := renderedDocTabPlugin(t)
	doc := p.activeDocPaneOrNil()
	if doc == nil {
		t.Fatal("no document pane")
	}
	region := docTabCloseRegion(p, 0)
	if region == nil {
		t.Fatal("tab 0 has no × hit region")
	}
	rest := p.renderListView(p.width, p.height)

	_ = p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: region.Rect.X, Y: region.Rect.Y}))
	if got := p.hoverTabClose.IndexFor(doc.leafID); got != 0 {
		t.Fatalf("hovered tab = %d, want 0 (%+v)", got, p.hoverTabClose)
	}
	hovered := p.renderListView(p.width, p.height)
	if hovered == rest {
		t.Fatal("hover painted nothing")
	}
	if ansi.Strip(hovered) != ansi.Strip(rest) {
		t.Fatal("hover moved the glyphs in the row")
	}

	_ = p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: 0, Y: 0}))
	if p.hoverTabClose.IndexFor(doc.leafID) >= 0 {
		t.Fatal("hover did not clear off the ×")
	}
	if p.renderListView(p.width, p.height) != rest {
		t.Fatal("leaving the × did not restore the resting row")
	}
}

// Only the × half hovers. The label half selects, and a select target that
// lit up would promise a close the click does not do.
func TestDocTabLabelDoesNotHoverTheClose(t *testing.T) {
	p := renderedDocTabPlugin(t)
	doc := p.activeDocPaneOrNil()
	if doc == nil {
		t.Fatal("no document pane")
	}
	region := docTabCloseRegion(p, 0)
	if region == nil {
		t.Fatal("tab 0 has no × hit region")
	}
	_ = p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: region.Rect.X - 2, Y: region.Rect.Y}))
	if p.hoverTabClose.IndexFor(doc.leafID) >= 0 {
		t.Fatalf("the label half lit the ×: %+v", p.hoverTabClose)
	}
}

// One leaf's hover must not light the same tab index in another leaf.
func TestTabCloseHoverIsScopedToItsLeaf(t *testing.T) {
	p := renderedDocTabPlugin(t)
	doc := p.activeDocPaneOrNil()
	if doc == nil {
		t.Fatal("no document pane")
	}
	region := docTabCloseRegion(p, 0)
	if region == nil {
		t.Fatal("tab 0 has no × hit region")
	}
	_ = p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: region.Rect.X, Y: region.Rect.Y}))
	if p.hoverTabClose.IndexFor(doc.leafID+1000) >= 0 {
		t.Fatal("hover leaked to another leaf")
	}
}
