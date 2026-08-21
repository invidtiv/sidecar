package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/mouse"
)

// A shell leaf has no sidebar row, so its title is its rename. The region is
// the shared frame's, registered after the leaf's own body, which is what makes
// the name the clickable thing rather than one more cell of terminal.

func titleRegion(t *testing.T, p *Plugin) mouse.Region {
	t.Helper()
	leaf := p.shellLeaf()
	if leaf == nil {
		t.Fatal("no shell leaf on screen")
	}
	var found *mouse.Region
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionPaneTitle {
			continue
		}
		if id, ok := region.Data.(int); ok && id == leaf.ID {
			r := region
			found = &r
		}
	}
	if found == nil {
		t.Fatal("the shell leaf's title registered no hit region")
	}
	return *found
}

func terminalSplitPlugin(t *testing.T) *Plugin {
	t.Helper()
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	stubTd(t)
	p := docPaneTestPlugin(t, t.TempDir(), true)
	p.sidebarVisible = false
	p.View(p.width, p.height)
	p.createTerminalSplit("dev server", "right")
	p.View(p.width, p.height)
	return p
}

func TestClickingAShellLeafTitleOpensTheRenameModal(t *testing.T) {
	p := terminalSplitPlugin(t)
	region := titleRegion(t, p)

	p.clickPaneTitle(region.Data)

	if p.viewMode != ViewModeRenameShell {
		t.Fatalf("view mode = %v, want the rename modal", p.viewMode)
	}
	if p.renameShellLeafTarget() == nil {
		t.Fatal("the rename modal is not aimed at the leaf whose title was clicked")
	}
	if got := p.renameShellInput.Value(); got != "dev server" {
		t.Fatalf("rename prefilled %q, want the leaf's current name", got)
	}
}

// A rename lands in the header, which is where a pane with no row says its
// name. An agent renaming the same pane arrives the same way.
func TestRenamingAShellLeafShowsInItsHeader(t *testing.T) {
	p := terminalSplitPlugin(t)
	p.clickPaneTitle(titleRegion(t, p).Data)
	p.renameShellInput.SetValue("build watch")

	p.executeRenameShell()

	if got := p.shellLeafTitle(); got != "build watch" {
		t.Fatalf("header title = %q, want the new name", got)
	}
	if p.viewMode == ViewModeRenameShell {
		t.Fatal("the rename modal stayed open after it was confirmed")
	}
	if p.renameShellLeafID != 0 {
		t.Fatal("the rename target outlived the modal")
	}
}

// The title covers only the name. A press further along the header row is
// click-to-focus and must not open a rename.
func TestTheTitleRegionCoversOnlyTheNameNotTheWholeHeader(t *testing.T) {
	p := terminalSplitPlugin(t)
	region := titleRegion(t, p)
	if region.Rect.H != 1 {
		t.Fatalf("title region is %d rows, want the header row alone", region.Rect.H)
	}
	box, ok := p.shellLeafBox()
	if !ok {
		t.Fatal("the shell leaf has no box")
	}
	if region.Rect.W >= box.W {
		t.Fatalf("title region spans %d of the header's %d columns", region.Rect.W, box.W)
	}
	if region.Rect.X != box.X || region.Rect.Y != box.Y {
		t.Fatalf("title region at %d,%d, want the header's first cell %d,%d",
			region.Rect.X, region.Rect.Y, box.X, box.Y)
	}
}

// The primary terminal is named by the sidebar row that selected it, and that
// row already answers R. Two rename paths for one name is a way for them to
// disagree.
func TestThePrimaryTerminalTitleIsNotAClickTarget(t *testing.T) {
	p := terminalSplitPlugin(t)
	terminal := firstPaneLeafOfKind(p.paneRoot, PaneTerminal)
	if terminal == nil {
		t.Fatal("no primary terminal leaf")
	}
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionPaneTitle {
			continue
		}
		if id, ok := region.Data.(int); ok && id == terminal.ID {
			t.Fatal("the primary terminal's title registered a rename target")
		}
	}
}
