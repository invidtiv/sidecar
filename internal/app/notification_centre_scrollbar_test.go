package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/ui"
)

// centreBarParams is the ScrollbarParams the panel's renderer and its pointer
// handlers must agree on, computed the way both compute it.
func centreBarParams(t *testing.T, m *Model) ui.ScrollbarParams {
	t.Helper()
	panelWidth := m.notificationCentrePanelWidth()
	if panelWidth <= 0 {
		t.Fatal("panel is not visible")
	}
	height := m.contentHeight()
	_, bodyInner, _, bodyHeight := notificationCentreLayout(panelWidth, height)
	rows := m.notificationCentreBody(bodyInner, time.Now())
	return ui.ScrollbarParams{
		TotalItems:   len(rows),
		ScrollOffset: m.notificationCentreScroll,
		VisibleItems: bodyHeight,
		TrackHeight:  bodyHeight,
	}
}

func TestCentreScrollbarRegionsRegisterAboveBodyRows(t *testing.T) {
	m := fillCentreList(t, 50)

	thumb := centreRegion(t, &m, ui.RegionScrollbarThumb)
	track := centreRegion(t, &m, ui.RegionScrollbarTrack)

	panelWidth := m.notificationCentrePanelWidth()
	_, bodyInner, _, bodyHeight := notificationCentreLayout(panelWidth, m.contentHeight())
	barX := m.width - panelWidth + 2 + bodyInner
	if track.Rect.X != barX || track.Rect.W != 1 || track.Rect.H != bodyHeight {
		t.Fatalf("track region = %+v, want x=%d w=1 h=%d", track.Rect, barX, bodyHeight)
	}
	if !track.Rect.Contains(thumb.Rect.X, thumb.Rect.Y) ||
		!track.Rect.Contains(thumb.Rect.X, thumb.Rect.Y+thumb.Rect.H-1) {
		t.Fatalf("thumb %+v escapes track %+v", thumb.Rect, track.Rect)
	}

	// Entry rows span the whole panel width beneath the bar; the bar was
	// registered after them, so the reverse scan hands it every cell of the
	// column — the thumb where it sits, the track below it.
	item := centreRegion(t, &m, regionNotificationCentreItem+"0")
	if got := m.notificationCentreMouse.HitMap.Test(track.Rect.X, item.Rect.Y); got == nil ||
		(got.ID != ui.RegionScrollbarThumb && got.ID != ui.RegionScrollbarTrack) {
		t.Fatalf("bar-column cell over entry row 0 resolved to %+v, want a scrollbar region", got)
	}
	belowThumb := thumb.Rect.Y + thumb.Rect.H
	if belowThumb < track.Rect.Y+track.Rect.H {
		if got := m.notificationCentreMouse.HitMap.Test(track.Rect.X, belowThumb); got == nil || got.ID != ui.RegionScrollbarTrack {
			t.Fatalf("track cell resolved to %+v, want the track", got)
		}
	}
}

func TestCentreScrollbarRegistersNothingWhenEverythingFits(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "files"})
	postCentreNotification(t, &m, notify.SourceTasks, "only")
	m.toggleNotificationCentre()
	m.View()
	for _, r := range m.notificationCentreMouse.HitMap.Regions() {
		if r.ID == ui.RegionScrollbarThumb || r.ID == ui.RegionScrollbarTrack {
			t.Fatalf("a fitting list registered %q at %+v", r.ID, r.Rect)
		}
	}
}

// Track click = jump-to-spot anchored at the grabbed row, the same gesture
// keeps dragging from there, and a release outside the bar settles cleanly.
func TestCentreTrackClickJumpsAnchoredAndKeepsDragging(t *testing.T) {
	m := fillCentreList(t, 50)
	params := centreBarParams(t, &m)
	track := centreRegion(t, &m, ui.RegionScrollbarTrack)

	grabRow := track.Rect.H - 1
	handled, _ := m.notificationCentreMouseEvent(
		tea.MouseClickMsg{Button: tea.MouseLeft, X: track.Rect.X, Y: track.Rect.Y + grabRow})
	if !handled {
		t.Fatal("a click on the track was not handled")
	}
	if want := ui.OffsetAtRow(params, grabRow); m.notificationCentreScroll != want {
		t.Fatalf("scroll after track click = %d, want %d", m.notificationCentreScroll, want)
	}
	if !m.notificationCentreMouse.IsDragging() || m.notificationCentreMouse.DragRegion() != ui.RegionScrollbarTrack {
		t.Fatal("track click did not start a continuing drag")
	}

	upTo := track.Rect.Y + 1
	handled, _ = m.notificationCentreMouseEvent(
		tea.MouseMotionMsg{X: track.Rect.X, Y: upTo, Button: tea.MouseLeft})
	if !handled {
		t.Fatal("drag motion was not handled")
	}
	if want := ui.OffsetAtRow(params, upTo-track.Rect.Y); m.notificationCentreScroll != want {
		t.Fatalf("scroll during drag = %d, want %d", m.notificationCentreScroll, want)
	}

	handled, _ = m.notificationCentreMouseEvent(
		tea.MouseReleaseMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	if !handled {
		t.Fatal("releasing away from the bar was not handled")
	}
	if m.notificationCentreMouse.IsDragging() {
		t.Fatal("release did not settle the drag")
	}
	if m.notificationCentreScroll != ui.OffsetAtRow(params, upTo-track.Rect.Y) {
		t.Fatal("release moved the scroll position")
	}
}

// Press thumb → drag → offset follows ui.OffsetAtRow of the pointer, clamps
// at both ends without losing the gesture, and settles on release.
func TestCentreThumbDragScrollsProportionallyEndToEnd(t *testing.T) {
	m := fillCentreList(t, 50)
	params := centreBarParams(t, &m)
	thumb := centreRegion(t, &m, ui.RegionScrollbarThumb)

	pressY := thumb.Rect.Y
	handled, _ := m.notificationCentreMouseEvent(
		tea.MouseClickMsg{Button: tea.MouseLeft, X: thumb.Rect.X, Y: pressY})
	if !handled {
		t.Fatal("a press on the thumb was not handled")
	}
	if !m.notificationCentreMouse.IsDragging() || m.notificationCentreMouse.DragRegion() != ui.RegionScrollbarThumb {
		t.Fatal("pressing the thumb did not start a drag")
	}

	down := pressY + 4
	m.notificationCentreMouseEvent(tea.MouseMotionMsg{X: thumb.Rect.X, Y: down, Button: tea.MouseLeft})
	if want := ui.OffsetAtRow(params, down-pressY); m.notificationCentreScroll != want {
		t.Fatalf("scroll after dragging down = %d, want %d", m.notificationCentreScroll, want)
	}

	maxOffset := params.TotalItems - params.VisibleItems
	handled, _ = m.notificationCentreMouseEvent(
		tea.MouseMotionMsg{X: thumb.Rect.X, Y: pressY + 500, Button: tea.MouseLeft})
	if !handled {
		t.Fatal("motion past the end was not handled")
	}
	if m.notificationCentreScroll != maxOffset {
		t.Fatalf("scroll past the end = %d, want the clamp at %d", m.notificationCentreScroll, maxOffset)
	}
	if !m.notificationCentreMouse.IsDragging() {
		t.Fatal("clamping at the end lost the gesture")
	}

	handled, _ = m.notificationCentreMouseEvent(
		tea.MouseMotionMsg{X: thumb.Rect.X, Y: pressY - 500, Button: tea.MouseLeft})
	if !handled {
		t.Fatal("motion above the start was not handled")
	}
	if m.notificationCentreScroll != 0 {
		t.Fatalf("scroll above the start = %d, want the clamp at 0", m.notificationCentreScroll)
	}

	handled, _ = m.notificationCentreMouseEvent(
		tea.MouseReleaseMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	if !handled {
		t.Fatal("release was not handled")
	}
	if m.notificationCentreMouse.IsDragging() {
		t.Fatal("release did not settle the drag")
	}
}

// Hover lights the whole rail; the idle render stays byte-identical.
func TestCentreScrollbarHoverRestylesAndIdleDoesNotChange(t *testing.T) {
	m := fillCentreList(t, 50)
	height := m.contentHeight()
	idle := m.renderNotificationCentre(height)

	thumb := centreRegion(t, &m, ui.RegionScrollbarThumb)
	m.notificationCentreMouseEvent(tea.MouseMotionMsg{X: thumb.Rect.X, Y: thumb.Rect.Y})
	if !m.notificationCentreHoverBar {
		t.Fatal("hovering the bar did not set the hover state")
	}
	hovered := m.renderNotificationCentre(height)
	if hovered == idle {
		t.Fatal("the hover state did not restyle the rendered panel")
	}

	m.notificationCentreMouseEvent(tea.MouseMotionMsg{X: 1, Y: 1})
	if m.notificationCentreHoverBar {
		t.Fatal("moving off the bar left the hover state behind")
	}
	if m.renderNotificationCentre(height) != idle {
		t.Fatal("idle render changed after a hover round trip")
	}
}
