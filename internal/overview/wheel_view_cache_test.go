package overview

import (
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
)

func TestHeldGlobalTerminalWheelReusesOnlyOneSameSizeFrame(t *testing.T) {
	m, _, _ := interactiveModel(t)
	at := time.Unix(300, 0)
	m.collector.Now = func() time.Time { return at }
	m.WorkspacesView(previewWide, previewTall)
	surface, ok := m.previewSurface()
	if !ok {
		t.Fatal("rendered preview has no terminal surface")
	}
	wheel := tea.MouseWheelMsg{X: surface.X + 2, Y: surface.Y + 3, Button: tea.MouseWheelUp}

	// The first event applies immediately and receives a normal repaint.
	m.WorkspacesMouse(wheel)
	m.WorkspacesView(previewWide, previewTall)
	wantRegions := m.workspacesMouse.HitMap.Regions()
	m.workspacesViewCache = "already rendered"

	// The next dense event is held by WheelBurst and changes no visible state.
	at = at.Add(tty.WheelDebounceInterval / 2)
	m.WorkspacesMouse(wheel)
	if !m.reuseWorkspacesViewOnce {
		t.Fatal("held terminal wheel did not request frame reuse")
	}
	m.workspacesMouse.HitMap.AddRect("stale", 0, 0, 1, 1, nil)
	if got := m.WorkspacesView(previewWide, previewTall); got != "already rendered" {
		t.Fatalf("held terminal wheel rebuilt Workspaces view, got %q", got)
	}
	if m.reuseWorkspacesViewOnce {
		t.Fatal("held-wheel frame reuse was not consumed after one View")
	}
	if got := m.workspacesMouse.HitMap.Regions(); !reflect.DeepEqual(got, wantRegions) {
		t.Fatalf("held-wheel frame regions were not cleared and replayed\n got: %#v\nwant: %#v", got, wantRegions)
	}
}
