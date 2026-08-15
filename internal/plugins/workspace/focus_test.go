package workspace

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
)

func sidebarTarget() panelayout.Target {
	return panelayout.Target{Kind: panelayout.TargetSidebar}
}

func leafTarget(id int) panelayout.Target {
	return panelayout.Target{Kind: panelayout.TargetLeaf, Leaf: id}
}

func panelTarget() panelayout.Target {
	return panelayout.Target{Kind: panelayout.TargetTermPanel}
}

// assertFocus reads the three fields the frame draws focus from rather than the
// setter's own answer, so a target that never reached the state it names fails
// here instead of agreeing with itself.
func assertFocus(t *testing.T, p *Plugin, want panelayout.Target, step string) {
	t.Helper()
	switch want.Kind {
	case panelayout.TargetSidebar:
		if p.activePane != PaneSidebar || p.termPanelFocused {
			t.Fatalf("%s: pane=%v panelFocused=%v, want the sidebar", step, p.activePane, p.termPanelFocused)
		}
	case panelayout.TargetTermPanel:
		if p.activePane != PanePreview || !p.termPanelFocused || p.paneFocus != terminalLeafID(p.paneRoot) {
			t.Fatalf("%s: pane=%v focus=%d panelFocused=%v, want the terminal panel",
				step, p.activePane, p.paneFocus, p.termPanelFocused)
		}
	default:
		if p.activePane != PanePreview || p.termPanelFocused || p.paneFocus != want.Leaf {
			t.Fatalf("%s: pane=%v focus=%d panelFocused=%v, want leaf %d",
				step, p.activePane, p.paneFocus, p.termPanelFocused, want.Leaf)
		}
	}
}

func tabKey() tea.KeyPressMsg      { return tea.KeyPressMsg{Code: tea.KeyTab} }
func shiftTabKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift} }

func walkFocus(t *testing.T, p *Plugin, key tea.KeyPressMsg, want []panelayout.Target, label string) {
	t.Helper()
	for i, target := range want {
		p.handleListKeys(key)
		assertFocus(t, p, target, fmt.Sprintf("%s step %d", label, i+1))
	}
}

// The panel is the window the hand-written cycler could not reach: it reset
// termPanelFocused on every move, so Tab walked past it forever. The full ring
// in both directions is that regression's test.
func TestTabCyclesEveryVisibleWindowIncludingTheTerminalPanel(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)
	p.sidebarVisible = true
	p.termPanelVisible = true
	p.setFocusTarget(sidebarTarget())

	forward := []panelayout.Target{leafTarget(1), leafTarget(2), leafTarget(3), panelTarget(), sidebarTarget()}
	walkFocus(t, p, tabKey(), forward, "tab")

	reverse := []panelayout.Target{panelTarget(), leafTarget(3), leafTarget(2), leafTarget(1), sidebarTarget()}
	walkFocus(t, p, shiftTabKey(), reverse, "shift+tab")
}

// Focusing the panel is an explicit navigation of it, so its window stops being
// pinned where a document left it. Without the thaw the panel arrives frozen
// and the first key moves nothing.
func TestTabToTheTerminalPanelThawsItsWindow(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)
	p.sidebarVisible = false
	p.termPanelVisible = true
	p.setFocusTarget(leafTarget(3))
	p.pinTermPanelWindow(4, true)
	if !p.termPanelFreeze.Active() {
		t.Fatal("the panel window did not pin")
	}

	p.handleListKeys(tabKey())
	assertFocus(t, p, panelTarget(), "tab to panel")
	if p.termPanelFreeze.Active() || p.termPanelFreezeDoc {
		t.Fatalf("panel focus arrived frozen: active=%v doc=%v", p.termPanelFreeze.Active(), p.termPanelFreezeDoc)
	}
}

func TestTabCyclesABareTerminalWithAndWithoutTheSidebar(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = true
	p.setFocusTarget(leafTarget(1))

	p.handleListKeys(tabKey())
	assertFocus(t, p, sidebarTarget(), "terminal to sidebar")
	p.handleListKeys(tabKey())
	assertFocus(t, p, leafTarget(1), "sidebar to terminal")

	// A hidden sidebar leaves one window, and a ring of one has nowhere to go:
	// Tab must leave the terminal focused rather than blanking the surface.
	p.sidebarVisible = false
	p.handleListKeys(tabKey())
	assertFocus(t, p, leafTarget(1), "bare terminal tab")
	p.handleListKeys(shiftTabKey())
	assertFocus(t, p, leafTarget(1), "bare terminal shift+tab")
}

// With the workspace_doc_panes flag off there is never a pane tree, so the ring
// has to fall back to the two windows the surface actually draws. Otherwise Tab
// dies on a ring of one and the preview becomes unreachable once left.
func TestTabCyclesTheSidebarAndPreviewWithNoPaneTree(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.paneRoot = nil
	p.paneFocus = 0
	p.sidebarVisible = true
	p.setFocusTarget(sidebarTarget())

	p.handleListKeys(tabKey())
	assertFocus(t, p, leafTarget(0), "no tree: sidebar to preview")
	p.handleListKeys(tabKey())
	assertFocus(t, p, sidebarTarget(), "no tree: preview to sidebar")
	p.handleListKeys(shiftTabKey())
	assertFocus(t, p, leafTarget(0), "no tree: sidebar back to preview")
}

// Diff draws one preview window beside the sidebar. Tab cycles those two and
// leaves the tab's own intra-window focus — file list ↔ diff — to h/l/enter.
func TestTabOnTheDiffTabLeavesDiffFocusUntouched(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	p.sidebarVisible = true
	p.previewTab = PreviewTabDiff
	p.diffTabFocus = DiffTabFocusDiff
	p.setFocusTarget(sidebarTarget())

	p.handleListKeys(tabKey())
	assertFocus(t, p, leafTarget(terminalLeafID(p.paneRoot)), "diff tab to preview")
	if p.diffTabFocus != DiffTabFocusDiff {
		t.Fatalf("tab moved diff focus to %v", p.diffTabFocus)
	}
	p.handleListKeys(tabKey())
	assertFocus(t, p, sidebarTarget(), "diff preview to sidebar")
	if p.diffTabFocus != DiffTabFocusDiff {
		t.Fatalf("tab moved diff focus to %v", p.diffTabFocus)
	}
}

// A terminal being typed into owns Tab. The exception is structural: the
// interactive mode dispatches before the list keys ever see the key.
func TestInteractiveModeTabDoesNotMoveTheFocusTarget(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)
	p.sidebarVisible = true
	p.setFocusTarget(leafTarget(1))
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, TargetPane: "%1", TargetSession: "focus-test"}
	t.Cleanup(p.stopTerminalModels)

	p.handleKeyPress(tabKey())
	assertFocus(t, p, leafTarget(1), "interactive tab")
	p.handleKeyPress(shiftTabKey())
	assertFocus(t, p, leafTarget(1), "interactive shift+tab")
}

// Every click writes focus through the one setter, so whatever was clicked ends
// up focused and whatever held it before does not.
func TestClickFocusesTheWindowUnderThePointer(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)
	p.sidebarVisible = true
	p.termPanelVisible = true
	p.setFocusTarget(sidebarTarget())

	clicks := []struct {
		name   string
		region mouse.Region
		want   panelayout.Target
	}{
		{"doc leaf", mouse.Region{ID: regionPaneLeaf, Data: 2}, leafTarget(2)},
		{"issue leaf", mouse.Region{ID: regionPaneLeaf, Data: 3}, leafTarget(3)},
		{"terminal panel", mouse.Region{ID: regionTermPanelContent}, panelTarget()},
		{"preview terminal", mouse.Region{ID: regionPreviewPane}, leafTarget(1)},
		{"sidebar", mouse.Region{ID: regionSidebar}, sidebarTarget()},
	}
	for _, click := range clicks {
		before := p.currentFocusTarget()
		region := click.region
		p.handleMouseClick(mouse.MouseAction{Type: mouse.ActionClick, Region: &region})
		assertFocus(t, p, click.want, "click on the "+click.name)
		if before == click.want {
			t.Fatalf("click on the %s started from the window it was meant to move focus to", click.name)
		}
		if got := p.currentFocusTarget(); got == before {
			t.Fatalf("click on the %s left focus on %+v", click.name, before)
		}
	}
}

// Tab used to be swallowed by a live terminal search input. Now that it walks
// the ring unconditionally, it has to close the input on the way out, or the
// search box keeps drawing a cursor for keys it will never receive.
func TestTabLeavesALiveTerminalSearchInput(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = true
	p.setFocusTarget(leafTarget(1))
	p.terminalSearch.InputActive = true
	p.terminalSearch.Query = "err"

	p.handleListKeys(tabKey())

	if p.terminalSearch.InputActive {
		t.Fatal("tab left the terminal search input taking keystrokes")
	}
	if p.terminalSearch.Query != "err" {
		t.Fatalf("tab dropped the search query: %q", p.terminalSearch.Query)
	}
	assertFocus(t, p, sidebarTarget(), "terminal search to sidebar")
}

// The panel is a window only while it is drawn. When the preview shrinks past
// the split's minimum the renderer falls back to output-only, and Tab must not
// park focus on a panel that is not on screen.
func TestTabSkipsTheTerminalPanelWhenTheSplitDoesNotFit(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)
	// The sidebar stays off so the preview keeps a real leaf box as the window
	// shrinks; without one the split sizes itself from default dimensions and
	// always fits.
	p.sidebarVisible = false
	p.termPanelVisible = true

	p.View(p.width, p.height)
	if !p.termPanelOnScreen() {
		t.Fatal("fixture should draw the panel before the window shrinks")
	}
	// A right split needs two 10-column boxes plus a divider; 20 columns of
	// preview is one short, so the renderer draws output only.
	p.termPanelLayout = TermPanelRight
	for _, size := range [][2]int{{80, 24}, {60, 20}, {50, 16}} {
		p.width, p.height = size[0], size[1]
		p.View(p.width, p.height)
	}
	if _, _, fits := p.termPanelSplitBoxes(); fits {
		t.Fatal("fixture failed to produce a split that does not fit")
	}
	if p.termPanelOnScreen() {
		t.Fatal("panel still counted as on screen after the split stopped fitting")
	}

	for _, target := range p.focusRing() {
		if target.Kind == panelayout.TargetTermPanel {
			t.Fatalf("undrawn panel is still in the ring: %+v", p.focusRing())
		}
	}
}
