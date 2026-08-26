package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// termScrollbarPreview seeds the preview terminal's captured scrollback far
// taller than the viewport, then renders the frame so its bar regions are
// registered.
func termScrollbarPreview(t *testing.T) *Model {
	t.Helper()
	m, _ := previewModel(t)
	m.previewTerminalLeaf().Buffer = tty.NewOutputBuffer(400)
	if !m.previewTerminalLeaf().Buffer.Update(strings.Repeat("history line\n", 200)) {
		t.Fatal("seeding scrollback changed nothing")
	}
	m.WorkspacesView(previewWide, previewTall)
	window := m.previewWindow()
	if !window.ok || !window.layout.ShowScrollbar {
		t.Fatal("the preview drew no scrollbar for overflowing scrollback")
	}
	return m
}

// termBarRect returns the hit rect of the terminal bar part named by id, as
// the last frame registered it.
func termBarRect(t *testing.T, m *Model, id string) mouse.Rect {
	t.Helper()
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if region.ID != id {
			continue
		}
		if kind, ok := regionKind(&region); ok && kind == previewTermBarKind {
			return region.Rect
		}
	}
	t.Fatalf("the frame registered no %s region for the terminal bar", id)
	return mouse.Rect{}
}

// The full gesture through this surface's own input path: a bar press arms a
// drag without moving anything, held motion walks the window through
// scrollback inside its freeze, and a release far away settles it — following
// resumes only when the window is back at the live edge.
func TestPreviewTermScrollbarDragEndToEndThroughHost(t *testing.T) {
	m := termScrollbarPreview(t)

	thumb := termBarRect(t, m, ui.RegionScrollbarThumb)
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: thumb.X, Y: thumb.Y, Button: tea.MouseLeft}))
	if got := m.workspacesMouse.DragRegion(); got != ui.RegionScrollbarThumb {
		t.Fatalf("bar press started drag %q, want %s", got, ui.RegionScrollbarThumb)
	}
	if !m.previewTerminalState().termBar.active || !m.previewTerminalLeaf().Freeze.Active() {
		t.Fatal("bar press did not arm the host gesture and its freeze")
	}
	pinned := m.previewTerminalLeaf().Freeze.Start()
	if m.previewTerminalLeaf().Scroll != 0 {
		t.Fatalf("thumb grab at rest moved the offset to %d", m.previewTerminalLeaf().Scroll)
	}

	// The window is at the live edge (thumb at the bottom of the track), so
	// dragging UP walks back into history; down would clamp at live without
	// ending anything.
	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: thumb.X, Y: thumb.Y - 3, Button: tea.MouseLeft}))
	window := m.previewWindow()
	if window.layout.Start != m.previewTerminalLeaf().Freeze.Start() {
		t.Fatalf("drawn start %d does not match the frozen pin %d", window.layout.Start, m.previewTerminalLeaf().Freeze.Start())
	}
	if m.previewTerminalLeaf().Freeze.Start() >= pinned {
		t.Fatalf("dragging up did not walk back: start %d >= pinned %d", m.previewTerminalLeaf().Freeze.Start(), pinned)
	}

	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: 1, Y: 1}))
	if m.previewTerminalState().termBar.active || m.previewTerminalLeaf().Freeze.Active() {
		t.Fatal("release did not settle the gesture and its freeze")
	}
	if m.previewTerminalLeaf().Scroll == 0 {
		t.Fatal("release lost the rows the drag walked back to")
	}

	// A fresh frame re-registers the bar where the new offset put the thumb,
	// and a fresh grab is refused by nothing.
	m.WorkspacesView(previewWide, previewTall)
	fresh := termBarRect(t, m, ui.RegionScrollbarThumb)
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: fresh.X, Y: fresh.Y, Button: tea.MouseLeft}))
	if !m.previewTerminalState().termBar.active {
		t.Fatal("fresh grab refused after settle")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: 1, Y: 1}))
}

// A track click jumps so the thumb top anchors at the grabbed row (macOS
// jump-to-spot), and the same gesture keeps dragging from there through the
// press-time snapshot.
func TestPreviewTermTrackClickAnchorsAndContinues(t *testing.T) {
	m := termScrollbarPreview(t)

	track := termBarRect(t, m, ui.RegionScrollbarTrack)
	pressRow := track.H / 2 // a genuine track press below the thumb

	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: track.X, Y: track.Y + pressRow, Button: tea.MouseLeft}))
	if !m.previewTerminalState().termBar.active {
		t.Fatal("track press did not arm the gesture")
	}
	got := m.previewTerminalLeaf().Freeze.Start()
	if got == 0 {
		t.Fatal("track click did not jump off the live edge")
	}

	// The jump anchors the gesture: continuing motion maps straight onto
	// track rows through the press-time snapshot.
	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: track.X, Y: track.Y + pressRow - 3, Button: tea.MouseLeft}))
	if m.previewTerminalLeaf().Freeze.Start() >= got {
		t.Fatalf("anchored drag did not move back up: %d >= %d", m.previewTerminalLeaf().Freeze.Start(), got)
	}
	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: 1, Y: 1}))
}

// The bar wins its column over the terminal drawn under it: a point where the
// pane body is drawn under the bar resolves to the bar — never a forwarded
// click or a text selection, whatever the application in the pane is doing
// with the mouse (plan rule 4). A rapid double-press re-grabs like the first.
func TestPreviewTermBarPressBeatsBodyAndDoublePressReGrabs(t *testing.T) {
	m := termScrollbarPreview(t)

	thumb := termBarRect(t, m, ui.RegionScrollbarThumb)
	hit := m.workspacesMouse.HitMap.Test(thumb.X, thumb.Y+1)
	if hit == nil {
		t.Fatal("nothing registered under the bar column")
	}
	if kind, _ := regionKind(hit); kind != previewTermBarKind {
		t.Fatalf("point under the bar resolved to %q, want the terminal bar", kind)
	}

	click := tea.MouseClickMsg{X: thumb.X, Y: thumb.Y, Button: tea.MouseLeft}
	run(t, m, m.WorkspacesMouse(click))
	if !m.previewTerminalState().termBar.active {
		t.Fatal("first press did not arm the gesture")
	}
	if m.previewTerminalLeaf().Selection.Anchor.Valid() {
		t.Fatal("press on the bar armed a text selection")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: thumb.X, Y: thumb.Y}))
	if m.previewTerminalState().termBar.active {
		t.Fatal("release did not settle the first grab")
	}
	// Bubble Tea emits the second press as ActionDoubleClick; it re-grabs.
	run(t, m, m.WorkspacesMouse(click))
	if !m.previewTerminalState().termBar.active {
		t.Fatal("a rapid second press on the bar did not re-grab it")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: 1, Y: 1}))
}

// A release lost off-window recovers on the next button-less motion, which is
// where the shared drag machinery ends a stale gesture on every other surface.
func TestPreviewTermScrollbarLostReleaseRecoversOnHover(t *testing.T) {
	m := termScrollbarPreview(t)
	thumb := termBarRect(t, m, ui.RegionScrollbarThumb)

	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: thumb.X, Y: thumb.Y, Button: tea.MouseLeft}))
	if !m.previewTerminalState().termBar.active {
		t.Fatal("press did not arm the gesture")
	}

	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: thumb.X, Y: thumb.Y + 2}))
	if m.previewTerminalState().termBar.active || m.previewTerminalLeaf().Freeze.Active() {
		t.Fatal("lost release left the scrollbar gesture and its freeze live")
	}
	if m.workspacesMouse.IsDragging() {
		t.Fatal("shared handler still dragging after lost release")
	}
}

// Content that fits draws no interactive bar and registers no regions: the
// reserved column is an anti-jitter spacer, not a control.
func TestPreviewTermFittingContentRegistersNoBarRegions(t *testing.T) {
	m, _ := previewModel(t)
	m.WorkspacesView(previewWide, previewTall)
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if kind, ok := regionKind(&region); ok && kind == previewTermBarKind {
			t.Fatalf("fitting content registered a bar region at %#v", region.Rect)
		}
	}
}
