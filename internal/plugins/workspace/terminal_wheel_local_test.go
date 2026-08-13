package workspace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
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
	p.previewTab = PreviewTabOutput
	p.termPanelVisible = true
	p.termPanelSession = "panel-session"
	p.termPanelPaneID = "%2"
	p.termPanelOutput = panel
	p.terminalHistory[terminalHistoryKey("panel", p.termPanelSession)] = terminalHistoryState{HistorySize: 1200}
	if p.termPanelMaxScroll() <= 0 {
		t.Fatal("test premise: the panel fixture has nothing to scroll back through")
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
