package tdmonitor

import (
	"log/slog"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/td/pkg/monitor"
)

func equalBounds(a, b map[monitor.Panel]monitor.Rect) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if other, ok := b[k]; !ok || other != v {
			return false
		}
	}
	return true
}

// newStartedPlugin builds a plugin over a temp td project and settles the async
// monitor build, mirroring the app's ordering: the window size is known (and a
// frame has been rendered) before the monitor finishes building.
func newStartedPlugin(t *testing.T, renderFirst bool) *Plugin {
	t.Helper()

	dir := tempTdProject(t)
	p := New()
	ctx := &plugin.Context{
		WorkDir: dir,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if renderFirst {
		// The loading frame the app draws while the monitor is still building.
		p.View(120, 40)
	}
	startAndSettle(t, p)
	t.Cleanup(p.Stop)
	if p.model == nil {
		t.Fatal("monitor model was not adopted")
	}
	return p
}

// TestMonitorHasPanelBoundsAfterAsyncBuild guards the mouse regression from the
// async monitor build: td computes its panel bounds only from WindowSizeMsg, so
// a model adopted after the app's window size arrived had none, and every mouse
// hit test missed (wheel scrolling and clicks did nothing).
func TestMonitorHasPanelBoundsAfterAsyncBuild(t *testing.T) {
	p := newStartedPlugin(t, true)

	if len(p.model.PanelBounds) == 0 {
		t.Fatal("monitor has no panel bounds after adoption; mouse hit tests will all miss")
	}
	if panel := p.model.HitTestPanel(10, 20); panel < 0 {
		t.Errorf("HitTestPanel(10, 20) = %d, want a panel; bounds = %+v", panel, p.model.PanelBounds)
	}
}

// TestViewRecomputesPanelBoundsOnResize covers the case where the monitor is
// adopted before any frame is drawn: View must still bring the bounds up to
// date with the size it renders at.
func TestViewRecomputesPanelBoundsOnResize(t *testing.T) {
	p := newStartedPlugin(t, false)

	p.View(120, 40)
	if len(p.model.PanelBounds) == 0 {
		t.Fatal("View did not establish panel bounds")
	}
	before := make(map[monitor.Panel]monitor.Rect, len(p.model.PanelBounds))
	for k, v := range p.model.PanelBounds {
		before[k] = v
	}

	p.View(120, 60)
	if p.model.Height != 60 {
		t.Errorf("model height = %d, want 60", p.model.Height)
	}
	if equalBounds(before, p.model.PanelBounds) {
		t.Errorf("panel bounds unchanged after resize: %+v", p.model.PanelBounds)
	}
	if panel := p.model.HitTestPanel(10, 50); panel < 0 {
		t.Errorf("HitTestPanel(10, 50) = %d, want a panel after growing to 60 rows; bounds = %+v",
			panel, p.model.PanelBounds)
	}
}

// TestWheelReachesMonitor verifies the app's routing all the way through: a
// wheel event delivered to the plugin moves the hovered panel's scroll offset.
func TestWheelReachesMonitor(t *testing.T) {
	p := newStartedPlugin(t, true)

	// A fresh project opens the getting-started overlay, which deliberately
	// swallows scroll; dismiss it so the panels are the scroll target.
	p.model.GettingStartedOpen = false
	// Give the task list enough rows to have somewhere to scroll to.
	p.model.TaskListRows = make([]monitor.TaskListRow, 100)

	panel := p.model.HitTestPanel(10, 20)
	if panel < 0 {
		t.Fatalf("no panel under (10, 20); bounds = %+v", p.model.PanelBounds)
	}
	before := p.model.ScrollOffset[panel]

	p.Update(tea.MouseWheelMsg{X: 10, Y: 20, Button: tea.MouseWheelDown})

	if got := p.model.ScrollOffset[panel]; got == before {
		t.Errorf("wheel down did not scroll panel %d: offset still %d", panel, got)
	}
}
