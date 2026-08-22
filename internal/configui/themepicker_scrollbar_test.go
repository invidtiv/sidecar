package configui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/ui"
)

// defaultThemeConfig is the fixture config every appearance test runs under.
func defaultThemeConfig() *config.Config {
	cfg := config.Default()
	cfg.UI.Theme = config.ThemeConfig{Name: "default"}
	return cfg
}

// scrollbarModel opens Appearance with its picker rendered once, so the list
// and its bar have registered their regions exactly as the real page does.
func scrollbarModel(t *testing.T) (*Model, *themePicker) {
	t.Helper()
	m, _ := configFixture(t, defaultThemeConfig())
	m.Open(PageAppearance)
	m.View(160, 45)
	picker := m.activePicker()
	if picker == nil {
		t.Fatal("no active theme picker on Appearance")
	}
	if !picker.bar.has {
		t.Fatal("the appearance picker's theme list did not overflow; test needs a scrollbar")
	}
	return m, picker
}

// A track press is jump-to-spot anchored at the grabbed row, the gesture
// continues as a drag from there, and releasing anywhere settles it.
func TestThemeScrollbarTrackClickAnchorsAndDrags(t *testing.T) {
	m, picker := scrollbarModel(t)
	track := regionFor(t, m, ui.RegionScrollbarTrack)
	thumb := regionFor(t, m, ui.RegionScrollbarThumb)

	// Anchor below the thumb so this is a track press, not a thumb grab.
	anchor := thumb.Rect.Y + thumb.Rect.H + 2 - track.Rect.Y
	want := ui.OffsetAtRow(picker.bar.params, anchor)
	if want == 0 {
		t.Fatalf("test setup: anchor row %d maps to offset 0; pick a lower anchor", anchor)
	}

	m.Mouse(tea.MouseClickMsg{X: track.Rect.X, Y: track.Rect.Y + anchor, Button: tea.MouseLeft})
	if picker.scroll != want {
		t.Errorf("track click scrolled to %d, want %d", picker.scroll, want)
	}
	if !m.mouse.IsDragging() {
		t.Error("track click did not continue as a drag")
	}

	// The anchor holds: motion at the clicked row lands on the same offset,
	// and moving above it scrolls up from there.
	m.Mouse(tea.MouseMotionMsg{X: track.Rect.X, Y: track.Rect.Y + anchor, Button: tea.MouseLeft})
	if picker.scroll != want {
		t.Errorf("motion at the anchor row moved the window to %d, want %d", picker.scroll, want)
	}
	m.Mouse(tea.MouseMotionMsg{X: track.Rect.X, Y: track.Rect.Y + anchor - 3, Button: tea.MouseLeft})
	if picker.scroll >= want {
		t.Errorf("dragging above the anchor left the window at %d, want <%d", picker.scroll, want)
	}

	released := picker.scroll
	m.Mouse(tea.MouseReleaseMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	if m.mouse.IsDragging() || picker.gesture.active {
		t.Error("drag state survived release")
	}
	if picker.scroll != released {
		t.Errorf("window = %d after settle, want the dragged position %d", picker.scroll, released)
	}
	if !picker.previewing {
		t.Error("the gesture ended without previewing the row it landed on")
	}
}

// Grabbing the thumb preserves where within it the press landed, clamps past
// both ends of the track without ending the gesture, and never acts as a row
// click on the themes beneath it.
func TestThemeScrollbarThumbDragEndToEnd(t *testing.T) {
	m, picker := scrollbarModel(t)
	thumb := regionFor(t, m, ui.RegionScrollbarThumb)

	pressY := thumb.Rect.Y + 1
	m.Mouse(tea.MouseClickMsg{X: thumb.Rect.X, Y: pressY, Button: tea.MouseLeft})
	if !m.mouse.IsDragging() || !picker.gesture.active {
		t.Fatal("thumb press did not start a drag")
	}

	// Down several rows, then far past the bottom end.
	m.Mouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: pressY + 6, Button: tea.MouseLeft})
	dragged := picker.scroll
	if dragged == 0 {
		t.Fatal("thumb drag did not move the window")
	}
	m.Mouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: pressY + 500, Button: tea.MouseLeft})
	if maxScroll := picker.maxScroll(); picker.scroll != maxScroll {
		t.Errorf("dragging past the bottom left the window at %d, want %d", picker.scroll, maxScroll)
	}
	if !m.mouse.IsDragging() {
		t.Error("clamping ended the gesture")
	}
	// And back up past the top end.
	m.Mouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: pressY - 500, Button: tea.MouseLeft})
	if picker.scroll != 0 {
		t.Errorf("dragging past the top left the window at %d, want 0", picker.scroll)
	}
	if !m.mouse.IsDragging() {
		t.Error("clamping ended the gesture")
	}

	m.Mouse(tea.MouseReleaseMsg{X: thumb.Rect.X, Y: pressY, Button: tea.MouseLeft})
	if m.mouse.IsDragging() || picker.gesture.active {
		t.Error("drag state survived release")
	}

	// The bar is not a row: none of that ever ran the theme-row click path,
	// which would have committed the draft on an inline picker and jumped the
	// cursor to the pressed row regardless of scroll position. The cursor only
	// moved because the window it sits in did — re-anchored inside it.
	if picker.cursor < picker.scroll || picker.cursor >= picker.scroll+picker.rows {
		t.Errorf("cursor %d left outside the scrolled window [%d,%d)", picker.cursor, picker.scroll, picker.scroll+picker.rows)
	}
}

// The wheel still answers over the bar column: the track rects took priority
// from regionThemeList, so the wheel routing must name them too.
func TestThemeListWheelScrollsOverScrollbarColumn(t *testing.T) {
	m, picker := scrollbarModel(t)
	track := regionFor(t, m, ui.RegionScrollbarTrack)

	before := picker.scroll
	m.Mouse(tea.MouseWheelMsg{X: track.Rect.X, Y: track.Rect.Y, Button: tea.MouseWheelDown})
	m.View(160, 45)
	if picker.scroll <= before {
		t.Errorf("wheel over the bar column did not scroll: %d -> %d", before, picker.scroll)
	}

	// At a boundary the notch is claimed rather than passed through.
	picker.setViewport(picker.maxScroll())
	if !m.WheelAtBoundary(tea.MouseWheelMsg{X: track.Rect.X, Y: track.Rect.Y, Button: tea.MouseWheelDown}) {
		t.Error("a boundary notch over the bar column was not claimed")
	}
}

// Content that fits registers no bar regions and no gesture can start.
func TestThemeScrollbarAbsentWhenEverythingFits(t *testing.T) {
	m, picker := scrollbarModel(t)
	picker.search.SetValue(picker.filtered[1].Name)
	picker.refilter()
	if len(picker.filtered) > picker.rows {
		t.Fatal("test setup: filter did not shrink the list below one window")
	}
	m.View(160, 45)

	if picker.bar.has {
		t.Error("fitting content reported a scrollbar")
	}
	for _, r := range m.mouse.HitMap.Regions() {
		switch r.ID {
		case ui.RegionScrollbarThumb, ui.RegionScrollbarTrack:
			t.Errorf("scrollbar region %q registered for fitting content: %+v", r.ID, r.Rect)
		}
	}
}

// Hover lights the bar, and leaving it restores byte-identical idle output.
func TestThemeScrollbarIdleByteParityAcrossHover(t *testing.T) {
	m, _ := scrollbarModel(t)
	idle := m.View(160, 45)

	thumb := regionFor(t, m, ui.RegionScrollbarThumb)
	m.Mouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseNone})
	lit := m.View(160, 45)
	if lit == idle {
		t.Fatal("hovering the thumb produced no visible emphasis")
	}

	m.Mouse(tea.MouseMotionMsg{X: 2, Y: 2, Button: tea.MouseNone})
	back := m.View(160, 45)
	if back != idle {
		t.Fatal("idle output drifted after a hover round trip")
	}
}

// A release that never arrives (focus stolen, pointer left) is recovered by
// the first button-less motion instead of leaving the bar armed.
func TestThemeScrollbarDragRecoveredFromLostRelease(t *testing.T) {
	m, picker := scrollbarModel(t)
	thumb := regionFor(t, m, ui.RegionScrollbarThumb)

	m.Mouse(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})
	if !picker.gesture.active {
		t.Fatal("press did not arm the gesture")
	}

	m.Mouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseNone})
	if picker.gesture.active || m.mouse.IsDragging() {
		t.Error("button-less motion did not drop the gesture")
	}
	if picker.previewing {
		t.Error("a recovered gesture previewed as if it had settled")
	}
}

// The second press of a rapid double-press arrives as ActionDoubleClick; the
// bar must re-grab it exactly like the first one did instead of being gated
// to plain clicks only.
func TestThemeScrollbarSecondQuickPressStillGrabsTheBar(t *testing.T) {
	m, picker := scrollbarModel(t)
	thumb := regionFor(t, m, ui.RegionScrollbarThumb)

	m.Mouse(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y + 1, Button: tea.MouseLeft})
	if !picker.gesture.active {
		t.Fatal("first press did not arm the gesture")
	}
	m.Mouse(tea.MouseReleaseMsg{X: thumb.Rect.X, Y: thumb.Rect.Y + 1, Button: tea.MouseLeft})
	if picker.gesture.active {
		t.Fatal("release did not settle the first gesture")
	}

	double := tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y + 1, Button: tea.MouseLeft}
	m.Mouse(double)
	if !m.mouse.IsDragging() || !picker.gesture.active {
		t.Fatal("a quick second press on the thumb did not grab the bar")
	}

	m.Mouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: thumb.Rect.Y + 4, Button: tea.MouseLeft})
	if picker.scroll == 0 {
		t.Error("post-regrab drag did not move the window")
	}
	m.Mouse(tea.MouseReleaseMsg{X: thumb.Rect.X, Y: thumb.Rect.Y + 4, Button: tea.MouseLeft})
	if m.mouse.IsDragging() || picker.gesture.active {
		t.Error("drag state survived release")
	}
}
