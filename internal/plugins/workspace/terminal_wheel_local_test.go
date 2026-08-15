package workspace

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// passiveWheelPanelPlugin is a list-mode plugin whose terminal panel is on
// screen with more loaded rows than the panel can draw, and whose history state
// is ready to serve an older chunk.
func passiveWheelPanelPlugin(t *testing.T) *Plugin {
	t.Helper()
	rows := make([]string, 0, 120)
	for i := range 120 {
		rows = append(rows, fmt.Sprintf("panel row %03d", i))
	}
	panel := tty.NewOutputBuffer(400)
	panel.ApplySnapshot(tty.PaneSnapshot{
		Output: strings.Join(rows, "\n"), BaseLine: 500, Absolute: true,
		PaneRows: len(rows),
	})

	p := New()
	p.width, p.height = 120, 40
	p.sidebarWidth = 40
	p.viewMode = ViewModeList
	p.termPanelVisible = true
	p.termPanelSession = "panel-session"
	p.termPanelPaneID = "%2"
	p.termPanelOutput = panel
	p.terminalHistory[terminalHistoryKey("panel", p.termPanelSession)] = tty.HistoryReach{HistorySize: 1200}
	if p.termPanelMaxScroll() <= 0 {
		t.Fatal("test premise: the panel fixture has nothing to scroll back through")
	}
	// The panel is coalesced by the shared burst like every other terminal
	// surface, so the fixture spaces its notches: each one reads a clock a
	// debounce window later than the last, which is a deliberate scroll rather
	// than a flick. These tests are about where the window lands.
	at := time.Now()
	p.clock = func() time.Time {
		at = at.Add(2 * tty.WheelDebounceInterval)
		return at
	}
	return p
}

// A wheel notch over the passive panel is placed by the shared window rule, so
// it stops at the top of the loaded buffer instead of running the offset off
// into rows that do not exist — and stopping there is what lets the window ask
// for older history. This path used to do its own arithmetic with a lower clamp
// only, which walked past the bound and stepped over the request (td-c3649a).
func TestPassivePanelWheelStopsAtTheLoadedTopAndAsksForHistory(t *testing.T) {
	p := passiveWheelPanelPlugin(t)
	region := &mouse.Region{ID: regionTermPanelContent}

	var lastCmd any
	for range 30 {
		cmd := p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -5, Region: region})
		if cmd != nil {
			lastCmd = cmd
		}
	}

	if bound := p.termPanelMaxScroll(); p.termPanelScroll != bound {
		t.Fatalf("panel wheel left scroll %d, want the loaded bound %d", p.termPanelScroll, bound)
	}
	if lastCmd == nil {
		t.Fatal("reaching the top of the loaded buffer never asked for older history")
	}
	if state := p.terminalHistory[terminalHistoryKey("panel", p.termPanelSession)]; !state.Loading {
		t.Fatal("the history request was never recorded against the panel")
	}
}

// A notch answers the selection against the surface the reader is about to be
// looking at, not the document snapshot the pin was showing. The snapshot is
// built without absolute coordinates, so asking it whether a selection survives
// a scroll always says no — and the live panel buffer, which is absolute, says
// yes. Thawing before the clear is what puts that question to the right buffer.
func TestPanelWheelKeepsAnAbsoluteSelectionThroughADocProjection(t *testing.T) {
	p := passiveWheelPanelPlugin(t)
	live := p.termPanelOutput

	// The panel is pinned by a document activation, showing a snapshot of the
	// rows that were on screen — the shape captureTerminalViewportForDocOpen
	// leaves behind.
	snapshot := tty.NewOutputBuffer(20)
	snapshot.ApplySnapshot(tty.PaneSnapshot{Output: "panel row 010\npanel row 011", PaneRows: 2})
	p.terminalDocProjection = terminalDocProjection{
		buffer: snapshot, source: live, termPanel: true,
		identity: p.terminalProjectionIdentity(true),
	}
	p.pinTermPanelWindow(30, true)
	p.selection.Clear()
	p.selection.Start = ui.SelectionPoint{Line: 12, Col: 0}
	p.selection.End = ui.SelectionPoint{Line: 14, Col: 4}
	p.selection.Active = true

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -5, Region: &mouse.Region{ID: regionTermPanelContent}})

	if !p.selection.HasSelection() {
		t.Fatal("the notch dropped a selection the live absolute buffer keeps")
	}
	if p.projectedTerminalBuffer(true) != nil {
		t.Fatal("the notch left the document projection standing in for the live buffer")
	}
}

// A pin a pointer gesture placed is handed back as a distance from the live
// bottom, not dropped: releasing it instead leaves the placement below resuming
// from whatever offset the surface held before the gesture froze it.
func TestPanelWheelThawsAGesturePinRatherThanDroppingIt(t *testing.T) {
	p := passiveWheelPanelPlugin(t)
	// A buffer without absolute coordinates is the case that reaches the clear:
	// a selection cannot survive a scroll there, so the clear runs its release.
	rows := make([]string, 0, 120)
	for i := range 120 {
		rows = append(rows, fmt.Sprintf("panel row %03d", i))
	}
	relative := tty.NewOutputBuffer(400)
	relative.ApplySnapshot(tty.PaneSnapshot{Output: strings.Join(rows, "\n"), PaneRows: len(rows)})
	p.termPanelOutput = relative

	bound := p.termPanelMaxScroll()
	// A gesture froze the window well back through the scrollback while the
	// offset behind it still read the live edge: thawing that pin and dropping
	// it land in visibly different places.
	const pinnedStart = 20
	p.termPanelScroll = 0
	p.pinTermPanelWindow(pinnedStart, false)
	p.selection.Clear()
	p.selection.Start = ui.SelectionPoint{Line: 1, Col: 0}
	p.selection.End = ui.SelectionPoint{Line: 2, Col: 4}
	p.selection.Active = true

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -5, Region: &mouse.Region{ID: regionTermPanelContent}})

	// The pinned rows come back as a distance from the live bottom, and the
	// notch steps five rows further back from there. Dropping the pin instead
	// would resume from the stale zero the gesture froze over and land on 5.
	if want := tty.ThawOffsetFrom(pinnedStart, bound) + 5; p.termPanelScroll != want {
		t.Fatalf("panel wheel over a gesture pin left scroll %d, want %d", p.termPanelScroll, want)
	}
	if p.termPanelFreeze.Active() {
		t.Fatal("the notch left the gesture pin holding the window")
	}
}

// Notching back down returns the window to the live edge and no further.
func TestPassivePanelWheelReturnsToTheLiveEdge(t *testing.T) {
	p := passiveWheelPanelPlugin(t)
	region := &mouse.Region{ID: regionTermPanelContent}

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -5, Region: region})
	if p.termPanelScroll != 5 {
		t.Fatalf("panel wheel up left scroll %d, want 5", p.termPanelScroll)
	}
	for range 10 {
		p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollDown, Delta: 5, Region: region})
	}
	if p.termPanelScroll != 0 {
		t.Fatalf("panel wheel down left scroll %d, want the live edge", p.termPanelScroll)
	}
}
