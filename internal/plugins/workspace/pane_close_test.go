package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

func paneCloseRegion(p *Plugin, leafID int) *mouse.Region {
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionPaneClose {
			continue
		}
		if id, ok := region.Data.(int); ok && id == leafID {
			copy := region
			return &copy
		}
	}
	return nil
}

func clickPaneClose(t *testing.T, p *Plugin, leafID int) tea.Cmd {
	t.Helper()
	region := paneCloseRegion(p, leafID)
	if region == nil {
		t.Fatalf("leaf %d has no close region", leafID)
	}
	return p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		X:      region.Rect.X + region.Rect.W/2,
		Y:      region.Rect.Y,
		Region: region,
	})
}

func TestContentPaneCloseButtonClosesDocIssueAndDiff(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	p := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	selRoot, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("no selected terminal surface")
	}
	if cmd := p.openIssuePaneForSurface(selRoot, surface, "td-aaaa11"); cmd != nil {
		deliverLoads(t, p, cmd)
	}
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("diff did not open")
	}

	const width, height = 120, 24
	_ = composePaneTree(t, p, width, height)

	doc, docLeaf := p.activeDocPane()
	issue, issueLeaf := p.activeIssuePane()
	diff, diffLeaf := p.activeDiffPane()
	if doc == nil || issue == nil || diff == nil {
		t.Fatalf("missing panes: doc=%v issue=%v diff=%v", doc != nil, issue != nil, diff != nil)
	}

	header := p.docPaneHeaderRow(doc, 48, true)
	if !strings.Contains(ansi.Strip(header), ui.CloseButtonLabel) {
		t.Fatalf("doc header has no X: %q", ansi.Strip(header))
	}
	if paneCloseRegion(p, docLeaf.ID) == nil || paneCloseRegion(p, issueLeaf.ID) == nil || paneCloseRegion(p, diffLeaf.ID) == nil {
		t.Fatal("a content pane registered no close region")
	}

	if cmd := clickPaneClose(t, p, issueLeaf.ID); cmd == nil {
		t.Fatal("issue X did not close")
	}
	if still, _ := p.activeIssuePane(); still != nil {
		t.Fatal("issue X left the issue pane")
	}
	if p.hiddenPaneLayout != nil {
		t.Fatal("issue X hid the pane instead of forgetting it")
	}

	_ = composePaneTree(t, p, width, height)
	if cmd := clickPaneClose(t, p, diffLeaf.ID); cmd == nil {
		t.Fatal("diff X did not close")
	}
	if still, _ := p.activeDiffPane(); still != nil {
		t.Fatal("diff X left the Diff pane")
	}

	_ = composePaneTree(t, p, width, height)
	if cmd := clickPaneClose(t, p, docLeaf.ID); cmd == nil {
		t.Fatal("doc X did not close")
	}
	if p.activeDocPaneOrNil() != nil {
		t.Fatal("doc X left the document pane")
	}
	if p.hiddenPaneLayout != nil {
		t.Fatal("doc X hid the pane instead of forgetting it")
	}
}

func TestContentPaneCloseButtonHoverRestylesTheX(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	p := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	_, leaf := p.activeDocPane()
	_ = composePaneTree(t, p, 100, 20)
	region := paneCloseRegion(p, leaf.ID)
	if region == nil {
		t.Fatal("no close region")
	}

	rest := ansi.Strip(p.docPaneHeaderRow(p.activeDocPaneOrNil(), 40, true))
	p.handleMouseHover(mouse.MouseAction{Type: mouse.ActionHover, Region: region, X: region.Rect.X, Y: region.Rect.Y})
	if p.hoverPaneClose != leaf.ID {
		t.Fatalf("hoverPaneClose = %d, want %d", p.hoverPaneClose, leaf.ID)
	}
	hovered := ansi.Strip(p.docPaneHeaderRow(p.activeDocPaneOrNil(), 40, true))
	if !strings.Contains(hovered, ui.CloseButtonLabel) || !strings.Contains(rest, ui.CloseButtonLabel) {
		t.Fatalf("close label missing: rest=%q hover=%q", rest, hovered)
	}
	p.handleMouseHover(mouse.MouseAction{Type: mouse.ActionHover})
	if p.hoverPaneClose != 0 {
		t.Fatalf("hover did not clear: %d", p.hoverPaneClose)
	}
}

func TestDocCloseClickIsNotStolenByNearestTab(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	doc, leaf := p.activeDocPane()
	if doc.view().Title() != "main.go" {
		t.Fatalf("active = %q", doc.view().Title())
	}
	_ = composePaneTree(t, p, 100, 20)
	if cmd := clickPaneClose(t, p, leaf.ID); cmd == nil || p.activeDocPaneOrNil() != nil {
		t.Fatalf("close click was stolen or did not forget: cmd=%v doc=%v", cmd != nil, p.activeDocPaneOrNil() != nil)
	}
}

func TestDocCloseButtonWorksWhileFinderIsOpen(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	p := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	doc, leaf := p.activeDocPane()
	scanFinder(t, p, p.openDocFinder(doc))
	if doc.mode == nil {
		t.Fatal("finder did not open")
	}
	_ = composePaneTree(t, p, 100, 20)
	region := paneCloseRegion(p, leaf.ID)
	if region == nil {
		t.Fatal("finder pane has no close region")
	}
	if cmd := p.handleDocSearchMouse(doc, tea.MouseClickMsg(tea.Mouse{
		X: region.Rect.X, Y: region.Rect.Y, Button: tea.MouseLeft,
	})); cmd == nil || p.activeDocPaneOrNil() != nil {
		t.Fatalf("X during find did not close the pane: cmd=%v doc=%v", cmd != nil, p.activeDocPaneOrNil() != nil)
	}
}

func TestDiffCloseButtonForgetsThePane(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("diff did not open")
	}
	_, leaf := p.activeDiffPane()
	_ = composePaneTree(t, p, 100, 20)
	if paneCloseRegion(p, leaf.ID) == nil {
		t.Fatal("diff header has no close region")
	}
	if !strings.Contains(ansi.Strip(p.diffPaneHeaderRow(p.diffs[leaf.ContentID], 40, true)), ui.CloseButtonLabel) {
		t.Fatal("diff header has no X")
	}
	if cmd := clickPaneClose(t, p, leaf.ID); cmd == nil {
		t.Fatal("diff X did not close")
	}
	if still, _ := p.activeDiffPane(); still != nil || p.hiddenPaneLayout != nil {
		t.Fatalf("diff X hid instead of forgetting: pane=%v hidden=%v", still != nil, p.hiddenPaneLayout != nil)
	}
}
