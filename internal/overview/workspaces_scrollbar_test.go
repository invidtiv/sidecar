package overview

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The Sessions list's interactive scrollbar: the same gesture contract as every
// other surface, driven through WorkspacesMouse exactly as a user's pointer
// drives it.

// scrollCatalogModel is catalogModel with n live shells in one project, long
// enough to overflow the sidebar at these test dimensions.
func scrollCatalogModel(t *testing.T, n int) *Model {
	t.Helper()
	original := ActivityStorePath
	ActivityStorePath = func() string { return "" }
	t.Cleanup(func() { ActivityStorePath = original })
	now := time.Now()
	workspaces := make([]workspaceinventory.Workspace, 0, n)
	for i := range n {
		workspaces = append(workspaces, workspaceinventory.Workspace{
			ID: fmt.Sprintf("w%02d", i), ProjectKey: "sidecar", ProjectName: "sidecar",
			Kind: workspaceinventory.KindShell, Name: fmt.Sprintf("Shell %02d", i),
			TmuxName: fmt.Sprintf("sidecar-sh-%02d", i), Live: true,
			Presentation: agentstatus.Presentation{ChangedAt: now.Add(-2 * time.Hour)},
		})
	}
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "sidecar", Path: "/tmp/sidecar", Key: "sidecar"}}
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar", Workspaces: workspaces}
	m.syncBoard()
	return m
}

type barRegions struct {
	track, thumb *mouse.Region
}

// sessionsBar renders the tab once and reports where the list's bar landed.
func sessionsBar(t *testing.T, m *Model) barRegions {
	t.Helper()
	m.WorkspacesView(50, 20)
	var out barRegions
	regions := m.workspacesMouse.HitMap.Regions()
	for i := range regions {
		switch r := &regions[i]; r.ID {
		case ui.RegionScrollbarTrack:
			out.track = r
		case ui.RegionScrollbarThumb:
			out.thumb = r
		}
	}
	if out.track == nil || out.thumb == nil {
		t.Fatal("no scrollbar regions registered for the Sessions list")
	}
	return out
}

// A track press jumps so the grabbed point anchors the thumb, continues as a
// drag, never selects a session row beneath it, and settles wherever the
// release happens — including well outside the bar.
func TestSessionsScrollbarTrackClickAnchorsAndDrags(t *testing.T) {
	m := scrollCatalogModel(t, 40)
	bar := sessionsBar(t, m)
	selectedBefore := m.workspaces.SelectedID()

	anchorRow := bar.thumb.Rect.Y + bar.thumb.Rect.H + 2 - bar.track.Rect.Y
	want := ui.OffsetAtRow(m.wsBar.bar.Params, anchorRow)
	if want == 0 {
		t.Fatalf("test setup: anchor row %d maps to offset 0; pick a lower anchor", anchorRow)
	}

	pointerDown(t, m, bar.track.Rect.X, bar.track.Rect.Y+anchorRow)
	if got := m.workspaces.ScrollOffset(); got != want {
		t.Errorf("track click scrolled to %d, want %d", got, want)
	}
	if m.workspaces.SelectedID() != selectedBefore {
		t.Errorf("scrollbar click moved the selection to %q, want %q held", m.workspaces.SelectedID(), selectedBefore)
	}
	if !m.workspacesMouse.IsDragging() {
		t.Error("track click did not continue as a drag")
	}

	dragTo(t, m, bar.track.Rect.X, bar.track.Rect.Y+anchorRow-3)
	if got := m.workspaces.ScrollOffset(); got >= want {
		t.Errorf("dragging above the anchor left offset %d, want <%d", got, want)
	}

	dragged := m.workspaces.ScrollOffset()
	release(t, m, 1, 1) // released far outside the bar
	if m.workspacesMouse.IsDragging() || m.wsBar.gesture.Active() {
		t.Error("drag state survived release outside the bar")
	}
	if got := m.workspaces.ScrollOffset(); got != dragged {
		t.Errorf("offset = %d after settle, want the dragged position %d", got, dragged)
	}
}

// The bar's column answers to the bar even though the whole-sidebar background
// region reaches under it: HitMap.Test scans reverse and the bar registered
// last.
func TestSessionsScrollbarRegionWinsItsColumn(t *testing.T) {
	m := scrollCatalogModel(t, 40)
	bar := sessionsBar(t, m)

	hit := m.workspacesMouse.HitMap.Test(bar.track.Rect.X, bar.track.Rect.Y+bar.track.Rect.H/2)
	if hit == nil || (hit.ID != ui.RegionScrollbarThumb && hit.ID != ui.RegionScrollbarTrack) {
		t.Fatalf("bar column hit %#v, want a scrollbar region", hit)
	}

	// Behavioral form of the same rule: pressing there starts a scrollbar
	// gesture rather than whatever a row click would have done.
	before := m.workspaces.SelectedID()
	pointerDown(t, m, bar.track.Rect.X, bar.track.Rect.Y+2)
	if id := m.workspacesMouse.DragRegion(); !isWorkspacesScrollbarDragID(id) {
		t.Errorf("press in the bar column started drag %q, want a scrollbar region", id)
	}
	if m.workspaces.SelectedID() != before {
		t.Error("pressing the bar selected something underneath it")
	}
	release(t, m, bar.track.Rect.X, bar.track.Rect.Y+2)
}

// Content that fits registers no bar regions at all.
func TestSessionsNoScrollbarRegionsWhenContentFits(t *testing.T) {
	m := scrollCatalogModel(t, 3)
	m.WorkspacesView(50, 30)
	regions := m.workspacesMouse.HitMap.Regions()
	for i := range regions {
		switch id := regions[i].ID; id {
		case ui.RegionScrollbarThumb, ui.RegionScrollbarTrack:
			t.Errorf("scrollbar region %q registered for fitting content", id)
		}
	}
}

// Hover lights the bar, and moving away restores byte-identical idle output.
func TestSessionsScrollbarIdleByteParityAcrossHover(t *testing.T) {
	m := scrollCatalogModel(t, 40)
	bar := sessionsBar(t, m)
	idle := m.WorkspacesView(50, 20)

	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: bar.thumb.Rect.X, Y: bar.thumb.Rect.Y}))
	lit := m.WorkspacesView(50, 20)
	if lit == idle {
		t.Fatal("hovering the bar produced no visible emphasis")
	}

	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: 1, Y: 1}))
	back := m.WorkspacesView(50, 20)
	if back != idle {
		t.Fatal("idle output drifted after a hover round trip")
	}
}

// A free-scrolled viewport survives a re-render, and moving the selection
// hands following back to the keyboard.
func TestSessionsFreeScrollSurvivesRenderUntilSelectionMoves(t *testing.T) {
	m := scrollCatalogModel(t, 40)
	sessionsBar(t, m) // renders once so the bar snapshot exists

	const dragged = 5
	m.workspaces.SetScrollViewport(dragged)
	m.renderWorkspaceList(globalContentInset, 1, 40, 18)
	if got := m.wsBar.bar.Params.ScrollOffset; got != dragged {
		t.Errorf("re-rendered viewport = %d, want the gesture position %d", got, dragged)
	}

	m.workspaces.Move(1)
	m.renderWorkspaceList(globalContentInset, 1, 40, 18)
	if got := m.wsBar.bar.Params.ScrollOffset; got >= dragged {
		t.Errorf("viewport = %d after the selection moved, want it following again (<%d)", got, dragged)
	}
}

// The second press of a rapid double-press arrives as ActionDoubleClick; the
// bar must re-grab it exactly like the first one did, before the click switch
// can ever reach row activation with it.
func TestSessionsScrollbarSecondQuickPressStillGrabsTheBar(t *testing.T) {
	m := scrollCatalogModel(t, 40)
	bar := sessionsBar(t, m)
	selectedBefore := m.workspaces.SelectedID()

	pointerDown(t, m, bar.thumb.Rect.X, bar.thumb.Rect.Y+1)
	if !m.wsBar.gesture.Active() {
		t.Fatal("first press did not begin a gesture")
	}
	release(t, m, bar.thumb.Rect.X, bar.thumb.Rect.Y+1)
	if m.wsBar.gesture.Active() {
		t.Fatal("release did not settle the first gesture")
	}

	double := tea.MouseClickMsg{X: bar.thumb.Rect.X, Y: bar.thumb.Rect.Y + 1, Button: tea.MouseLeft}
	m.WorkspacesMouse(double)
	if !m.workspacesMouse.IsDragging() || !m.wsBar.gesture.Active() {
		t.Fatal("a quick second press on the thumb did not grab the bar")
	}
	if m.workspaces.SelectedID() != selectedBefore {
		t.Errorf("second press selected %q, want %q held", m.workspaces.SelectedID(), selectedBefore)
	}

	dragTo(t, m, bar.thumb.Rect.X, bar.thumb.Rect.Y+6)
	if got := m.workspaces.ScrollOffset(); got <= 0 {
		t.Fatalf("post-regrab drag left offset %d, want it following the pointer", got)
	}
	release(t, m, 1, 1)
}
