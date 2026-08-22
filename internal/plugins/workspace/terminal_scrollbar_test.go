package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// terminalScrollbarOverflowPlugin seeds the primary terminal's captured
// scrollback far taller than any viewport, then renders the frame so its bar
// regions are registered.
func terminalScrollbarOverflowPlugin(t *testing.T) *Plugin {
	t.Helper()
	p := docPaneTestPlugin(t, t.TempDir(), false)
	wt := p.selectedWorktree()
	if wt == nil || wt.Agent == nil {
		t.Fatal("no selected worktree agent")
	}
	wt.Agent.OutputBuf = tty.NewOutputBuffer(400)
	if !wt.Agent.OutputBuf.Update(strings.Repeat("scrollback line\n", 200)) {
		t.Fatal("seeding scrollback changed nothing")
	}
	if got := p.View(140, 36); got == "" {
		t.Fatal("plugin rendered nothing")
	}
	return p
}

// termBarRegion returns the hit region of a terminal bar part named by id, as
// the last frame registered it.
func termBarRegion(t *testing.T, p *Plugin, id string) mouse.Region {
	t.Helper()
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == id {
			if _, ok := region.Data.(terminalScrollbarHit); ok {
				return region
			}
		}
	}
	t.Fatalf("the frame registered no %s region for the terminal bar", id)
	return mouse.Region{}
}

// The full gesture through this plugin's own input path: a thumb press arms a
// drag without moving anything, held motion walks the window through
// scrollback inside its freeze, and a release far away settles it — following
// resumes only when the window is back at the live edge.
func TestTerminalScrollbarDragEndToEndThroughHost(t *testing.T) {
	p := terminalScrollbarOverflowPlugin(t)
	before := p.previewScroll

	thumb := termBarRegion(t, p, regionTermScrollbarThumb)
	p.handleMouse(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})
	if got := p.mouseHandler.DragRegion(); got != regionTermScrollbarThumb {
		t.Fatalf("bar press started drag %q, want %s", got, regionTermScrollbarThumb)
	}
	if !p.termBar.active || !p.previewFreeze.Active() {
		t.Fatal("bar press did not arm the host gesture and its freeze")
	}
	if p.previewScroll != before {
		t.Fatalf("thumb grab at rest moved the window to offset %d", p.previewScroll)
	}

	// The window is at the live edge (thumb at the bottom of the track), so
	// dragging UP walks back into history; down would clamp at live without
	// ending anything.
	pinned := p.previewFreeze.Start()
	p.handleMouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: thumb.Rect.Y - 3, Button: tea.MouseLeft})
	layout := calculateTerminalViewportLayout(p.terminalWindowInputFor(false))
	if got := layout.Start; got != p.previewFreeze.Start() {
		t.Fatalf("drawn start %d does not match the frozen pin %d", got, p.previewFreeze.Start())
	}
	if p.previewFreeze.Start() >= pinned {
		t.Fatalf("dragging up did not walk back: start %d >= pinned %d", p.previewFreeze.Start(), pinned)
	}

	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})
	if p.termBar.active || p.previewFreeze.Active() {
		t.Fatal("release did not settle the gesture and its freeze")
	}
	follow, offset, fromBottom := p.terminalScrollState(false)
	if !fromBottom || follow || offset == 0 {
		t.Fatalf("window parked in history reports follow=%v offset=%d fromBottom=%v", follow, offset, fromBottom)
	}
	if offset != p.previewScroll {
		t.Fatalf("thawed offset %d does not hold the rows the drag left at %d", offset, p.previewScroll)
	}

	// A fresh frame re-registers the bar where the new offset put the thumb,
	// and a fresh grab is refused by nothing.
	if got := p.View(140, 36); got == "" {
		t.Fatal("plugin rendered nothing")
	}
	fresh := termBarRegion(t, p, regionTermScrollbarThumb)
	p.handleMouse(tea.MouseClickMsg{X: fresh.Rect.X, Y: fresh.Rect.Y, Button: tea.MouseLeft})
	if !p.termBar.active {
		t.Fatal("fresh grab refused after settle")
	}
	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})
}

// A track click jumps so the thumb top anchors at the grabbed row, the same
// gesture keeps dragging from there through the press-time snapshot, and
// parking at the oldest row is where older history would be reached for.
func TestTerminalTrackClickAnchorsAndContinues(t *testing.T) {
	p := terminalScrollbarOverflowPlugin(t)

	track := termBarRegion(t, p, regionTermScrollbarTrack)
	input := p.terminalWindowInputFor(false)
	sbLayout := calculateTerminalViewportLayout(input)
	sb := tty.WindowScrollbarFor(sbLayout, input.TotalItems)
	params := ui.ScrollbarParams{
		TotalItems: sb.Total, ScrollOffset: sb.Offset,
		VisibleItems: sb.Visible, TrackHeight: sb.Track,
	}
	pressRow := params.TrackHeight / 2 // below the thumb: a genuine track press
	wantStart := sb.StartAtTrackRow(pressRow)

	p.handleMouse(tea.MouseClickMsg{X: track.Rect.X, Y: track.Rect.Y + pressRow, Button: tea.MouseLeft})
	if !p.termBar.active {
		t.Fatal("track press did not arm the gesture")
	}
	if p.previewFreeze.Start() != wantStart {
		t.Fatalf("track click pinned start %d, want anchor %d", p.previewFreeze.Start(), wantStart)
	}

	p.handleMouse(tea.MouseMotionMsg{X: track.Rect.X, Y: track.Rect.Y + pressRow - 3, Button: tea.MouseLeft})
	if p.previewFreeze.Start() >= wantStart {
		t.Fatalf("anchored drag did not move back up: %d >= %d", p.previewFreeze.Start(), wantStart)
	}
	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})
}

// The bar wins its column over the terminal drawn under it: a press there
// never arms a text selection or reaches the pane app, whatever that
// application is doing with the mouse (plan rule 4). A rapid double-press
// re-grabs exactly like the first press.
func TestTerminalBarPressBeatsContentAndDoublePressReGrabs(t *testing.T) {
	p := terminalScrollbarOverflowPlugin(t)

	thumb := termBarRegion(t, p, regionTermScrollbarThumb)
	// The point under the bar column resolves to the bar, not to the terminal
	// content region beneath it.
	hit := p.mouseHandler.HitMap.Test(thumb.Rect.X, thumb.Rect.Y+1)
	if hit == nil {
		t.Fatal("nothing registered under the bar column")
	}
	if _, ok := hit.Data.(terminalScrollbarHit); !ok {
		t.Fatalf("point under the bar resolved to %#v, want the terminal bar", hit.Data)
	}

	click := tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft}
	p.handleMouse(click)
	if !p.termBar.active {
		t.Fatal("first press did not arm the gesture")
	}
	if p.selection.Anchor.Valid() {
		t.Fatal("press on the bar armed a text selection")
	}
	p.handleMouse(tea.MouseReleaseMsg{X: thumb.Rect.X, Y: thumb.Rect.Y})
	if p.termBar.active {
		t.Fatal("release did not settle the first grab")
	}
	// Bubble Tea emits the second press as ActionDoubleClick; it re-grabs.
	p.handleMouse(click)
	if !p.termBar.active {
		t.Fatal("a rapid second press on the bar did not re-grab it")
	}
	if got := p.mouseHandler.DragRegion(); got != regionTermScrollbarThumb {
		t.Fatalf("second press started drag %q, want %s", got, regionTermScrollbarThumb)
	}
	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})
}

// A release lost off-window recovers on the next button-less motion, which is
// where the shared drag machinery ends a stale gesture on every other surface.
func TestTerminalScrollbarLostReleaseSettlesOnHover(t *testing.T) {
	p := terminalScrollbarOverflowPlugin(t)
	thumb := termBarRegion(t, p, regionTermScrollbarThumb)

	p.handleMouse(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})
	if !p.termBar.active {
		t.Fatal("press did not arm the gesture")
	}

	p.handleMouse(tea.MouseMotionMsg{X: 1, Y: 1})
	if p.termBar.active || p.previewFreeze.Active() {
		t.Fatal("lost release left the scrollbar gesture and its freeze live")
	}
}

// Content that fits draws no interactive bar and registers no regions: the
// reserved column is an anti-jitter spacer, not a control.
func TestTerminalFittingContentRegistersNoBarRegions(t *testing.T) {
	p := docPaneTestPlugin(t, t.TempDir(), false)
	wt := p.selectedWorktree()
	if wt == nil || wt.Agent == nil {
		t.Fatal("no selected worktree agent")
	}
	wt.Agent.OutputBuf = tty.NewOutputBuffer(400)
	if !wt.Agent.OutputBuf.Update("a short pane\n") {
		t.Fatal("seeding output changed nothing")
	}
	if got := p.View(140, 36); got == "" {
		t.Fatal("plugin rendered nothing")
	}
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if isTermScrollbarDragID(region.ID) {
			t.Fatalf("fitting content registered a bar region at %#v", region.Rect)
		}
	}
}

// Dragging off the live edge disengages Follow, and a track jump back to the
// bottom of the track — the live edge — re-engages it. Following is derived
// from position, never tracked (rule 5).
func TestTerminalScrollbarTrackAnchorsMapToWindowPositions(t *testing.T) {
	p := terminalScrollbarOverflowPlugin(t)
	track := termBarRegion(t, p, regionTermScrollbarTrack)

	// Upper half of the track is history; parking there ends following.
	middle := track.Rect.Y + track.Rect.H/2
	p.handleMouse(tea.MouseClickMsg{X: track.Rect.X, Y: middle, Button: tea.MouseLeft})
	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})
	follow, offset, _ := p.terminalScrollState(false)
	if follow || offset == 0 {
		t.Fatalf("window parked in history reports follow=%v offset=%d", follow, offset)
	}

	// A fresh frame, then the bottom row of the track: offset zero is the
	// live edge, and following resumes.
	if got := p.View(140, 36); got == "" {
		t.Fatal("plugin rendered nothing")
	}
	fresh := termBarRegion(t, p, regionTermScrollbarTrack)
	bottom := fresh.Rect.Y + fresh.Rect.H - 1
	p.handleMouse(tea.MouseClickMsg{X: fresh.Rect.X, Y: bottom, Button: tea.MouseLeft})
	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})

	follow, offset, fromBottom := p.terminalScrollState(false)
	if !follow || !fromBottom || offset != 0 {
		t.Fatalf("bottom-of-track jump left follow=%v offset=%d fromBottom=%v, want the live edge", follow, offset, fromBottom)
	}
}
