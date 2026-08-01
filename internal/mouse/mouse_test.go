package mouse

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRect_Contains(t *testing.T) {
	r := Rect{X: 10, Y: 20, W: 5, H: 3}

	tests := []struct {
		name string
		x, y int
		want bool
	}{
		{"inside", 12, 21, true},
		{"top-left corner", 10, 20, true},
		{"bottom-right edge excluded", 15, 23, false},
		{"just inside right", 14, 22, true},
		{"left of rect", 9, 21, false},
		{"above rect", 12, 19, false},
		{"below rect", 12, 23, false},
		{"right of rect", 15, 21, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Contains(tt.x, tt.y); got != tt.want {
				t.Errorf("Rect(%+v).Contains(%d, %d) = %v, want %v", r, tt.x, tt.y, got, tt.want)
			}
		})
	}
}

func TestRect_Contains_ZeroSize(t *testing.T) {
	r := Rect{X: 5, Y: 5, W: 0, H: 0}
	if r.Contains(5, 5) {
		t.Error("zero-size rect should not contain any point")
	}
}

func TestHitMap_AddAndTest(t *testing.T) {
	hm := NewHitMap()
	hm.Add("btn1", Rect{X: 0, Y: 0, W: 10, H: 5}, "button1")

	region := hm.Test(5, 3)
	if region == nil {
		t.Fatal("expected to hit btn1")
	}
	if region.ID != "btn1" {
		t.Errorf("region.ID = %q, want %q", region.ID, "btn1")
	}
	if region.Data != "button1" {
		t.Errorf("region.Data = %v, want %q", region.Data, "button1")
	}
}

func TestHitMap_OverlappingRegions(t *testing.T) {
	hm := NewHitMap()
	hm.Add("bottom", Rect{X: 0, Y: 0, W: 20, H: 20}, nil)
	hm.Add("top", Rect{X: 5, Y: 5, W: 10, H: 10}, nil)

	region := hm.Test(7, 7)
	if region == nil {
		t.Fatal("expected to hit a region")
	}
	if region.ID != "top" {
		t.Errorf("overlapping region: got %q, want %q (later region should win)", region.ID, "top")
	}
}

func TestHitMap_Clear(t *testing.T) {
	hm := NewHitMap()
	hm.Add("a", Rect{X: 0, Y: 0, W: 10, H: 10}, nil)
	hm.Clear()

	if region := hm.Test(5, 5); region != nil {
		t.Error("expected nil after Clear()")
	}
}

func TestHitMap_AddRect(t *testing.T) {
	hm := NewHitMap()
	hm.AddRect("item", 10, 20, 5, 3, 42)

	region := hm.Test(12, 21)
	if region == nil {
		t.Fatal("expected to hit item")
	}
	if region.ID != "item" {
		t.Errorf("ID = %q, want %q", region.ID, "item")
	}
	if region.Data != 42 {
		t.Errorf("Data = %v, want 42", region.Data)
	}
}

func TestHitMap_Regions(t *testing.T) {
	hm := NewHitMap()
	hm.Add("a", Rect{X: 0, Y: 0, W: 5, H: 5}, nil)
	hm.Add("b", Rect{X: 10, Y: 10, W: 5, H: 5}, nil)

	regions := hm.Regions()
	if len(regions) != 2 {
		t.Fatalf("len(Regions()) = %d, want 2", len(regions))
	}

	// Verify it's a copy
	regions[0].ID = "modified"
	original := hm.Regions()
	if original[0].ID == "modified" {
		t.Error("Regions() should return a copy")
	}
}

func TestHitMap_TestMiss(t *testing.T) {
	hm := NewHitMap()
	hm.Add("a", Rect{X: 100, Y: 100, W: 5, H: 5}, nil)

	if region := hm.Test(0, 0); region != nil {
		t.Error("expected nil for miss")
	}
}

func TestHandler_HandleClick(t *testing.T) {
	h := NewHandler()
	h.HitMap.Add("btn", Rect{X: 0, Y: 0, W: 10, H: 10}, nil)

	result := h.HandleClick(5, 5)
	if result.Region == nil {
		t.Fatal("expected to hit btn")
	}
	if result.Region.ID != "btn" {
		t.Errorf("region ID = %q, want %q", result.Region.ID, "btn")
	}
	if result.IsDoubleClick {
		t.Error("first click should not be double click")
	}
}

func TestHandler_HandleClick_Miss(t *testing.T) {
	h := NewHandler()
	h.HitMap.Add("btn", Rect{X: 100, Y: 100, W: 5, H: 5}, nil)

	result := h.HandleClick(0, 0)
	if result.Region != nil {
		t.Error("expected nil region for miss")
	}
}

func TestHandler_DoubleClick(t *testing.T) {
	h := NewHandler()
	h.HitMap.Add("btn", Rect{X: 0, Y: 0, W: 10, H: 10}, nil)

	// First click
	r1 := h.HandleClick(5, 5)
	if r1.IsDoubleClick {
		t.Error("first click should not be double click")
	}

	// Second click immediately — within 400ms
	r2 := h.HandleClick(5, 5)
	if !r2.IsDoubleClick {
		t.Error("second immediate click should be double click")
	}

	// Third click completes a triple-click gesture, but is not another double.
	r3 := h.HandleClick(5, 5)
	if r3.IsDoubleClick {
		t.Error("third click should not be double click")
	}
	if !r3.IsTripleClick || r3.ClickCount != 3 {
		t.Fatalf("third click = %#v, want triple click", r3)
	}

	// Fourth click starts a fresh gesture.
	r4 := h.HandleClick(5, 5)
	if r4.IsDoubleClick || r4.IsTripleClick || r4.ClickCount != 1 {
		t.Fatalf("fourth click = %#v, want fresh single click", r4)
	}
}

func TestHandler_ClickCountIncludesModifiers(t *testing.T) {
	h := NewHandler()
	h.HitMap.Add("btn", Rect{X: 0, Y: 0, W: 10, H: 10}, nil)

	if got := h.handleClickWithModifiers(5, 5, false, false); got.ClickCount != 1 {
		t.Fatalf("plain click count = %d, want 1", got.ClickCount)
	}
	if got := h.handleClickWithModifiers(5, 5, true, false); got.ClickCount != 1 || got.IsDoubleClick {
		t.Fatalf("plain→Shift click was combined: %#v", got)
	}
	if got := h.handleClickWithModifiers(5, 5, false, true); got.ClickCount != 1 || got.IsDoubleClick {
		t.Fatalf("Shift→Alt click was combined: %#v", got)
	}
}

func TestHandler_DragLifecycle(t *testing.T) {
	h := NewHandler()

	if h.IsDragging() {
		t.Error("should not be dragging initially")
	}

	h.StartDrag(10, 20, "sidebar", 200)

	if !h.IsDragging() {
		t.Error("should be dragging after StartDrag")
	}
	if h.DragRegion() != "sidebar" {
		t.Errorf("DragRegion = %q, want %q", h.DragRegion(), "sidebar")
	}
	if h.DragStartValue() != 200 {
		t.Errorf("DragStartValue = %d, want 200", h.DragStartValue())
	}

	dx, dy := h.DragDelta(15, 25)
	if dx != 5 || dy != 5 {
		t.Errorf("DragDelta = (%d, %d), want (5, 5)", dx, dy)
	}

	h.EndDrag()

	if h.IsDragging() {
		t.Error("should not be dragging after EndDrag")
	}
	if h.DragRegion() != "" {
		t.Errorf("DragRegion after end = %q, want empty", h.DragRegion())
	}
}

// Drop targeting needs to know where the pointer is and what is underneath it,
// on every drag motion and at the release point. ActionDrag used to carry no
// region and ActionDragEnd carried neither region nor coordinates.
func TestHandleMouse_DragCarriesRegionAndCoords(t *testing.T) {
	h := NewHandler()
	h.HitMap.Add("source-row", Rect{X: 0, Y: 0, W: 20, H: 1}, 3)
	h.HitMap.Add("target-row", Rect{X: 0, Y: 5, W: 20, H: 1}, 8)

	h.StartDrag(2, 0, "source-row", 3)

	drag := h.HandleMouse(tea.MouseMotionMsg(tea.Mouse{X: 4, Y: 5, Button: tea.MouseLeft}))
	if drag.Type != ActionDrag {
		t.Fatalf("motion while dragging = %v, want ActionDrag", drag.Type)
	}
	if drag.Region == nil || drag.Region.ID != "target-row" {
		t.Fatalf("ActionDrag.Region = %#v, want the region under the cursor", drag.Region)
	}
	if drag.Region.Data != 8 {
		t.Errorf("ActionDrag.Region.Data = %v, want 8", drag.Region.Data)
	}
	if drag.DragStartID != "source-row" {
		t.Errorf("ActionDrag.DragStartID = %q, want %q", drag.DragStartID, "source-row")
	}
	if drag.X != 4 || drag.Y != 5 {
		t.Errorf("ActionDrag coords = (%d, %d), want (4, 5)", drag.X, drag.Y)
	}
	if drag.DragDX != 2 || drag.DragDY != 5 {
		t.Errorf("ActionDrag delta = (%d, %d), want (2, 5)", drag.DragDX, drag.DragDY)
	}

	end := h.HandleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 4, Y: 5}))
	if end.Type != ActionDragEnd {
		t.Fatalf("release while dragging = %v, want ActionDragEnd", end.Type)
	}
	if end.X != 4 || end.Y != 5 {
		t.Errorf("ActionDragEnd coords = (%d, %d), want (4, 5)", end.X, end.Y)
	}
	if end.Region == nil || end.Region.ID != "target-row" {
		t.Fatalf("ActionDragEnd.Region = %#v, want the region under the release point", end.Region)
	}
	// The source region has to survive EndDrag, which clears handler state.
	if end.DragStartID != "source-row" {
		t.Errorf("ActionDragEnd.DragStartID = %q, want %q", end.DragStartID, "source-row")
	}
	if end.DragDX != 2 || end.DragDY != 5 {
		t.Errorf("ActionDragEnd delta = (%d, %d), want (2, 5)", end.DragDX, end.DragDY)
	}
	if h.IsDragging() {
		t.Error("should not be dragging after release")
	}
}

// Releasing over empty space is a legitimate (invalid) drop: the action still
// reports where it happened, with a nil region.
func TestHandleMouse_DragEndOverNoRegion(t *testing.T) {
	h := NewHandler()
	h.HitMap.Add("source-row", Rect{X: 0, Y: 0, W: 20, H: 1}, 3)
	h.StartDrag(2, 0, "source-row", 3)

	end := h.HandleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 50, Y: 40}))
	if end.Type != ActionDragEnd {
		t.Fatalf("release = %v, want ActionDragEnd", end.Type)
	}
	if end.Region != nil {
		t.Errorf("Region = %#v, want nil over empty space", end.Region)
	}
	if end.X != 50 || end.Y != 40 {
		t.Errorf("coords = (%d, %d), want (50, 40)", end.X, end.Y)
	}
	if end.DragStartID != "source-row" {
		t.Errorf("DragStartID = %q, want %q", end.DragStartID, "source-row")
	}
}

// If a release is lost (released outside the terminal, focus stolen), the
// handler is left dragging. All-motion mode then delivers button-less motion,
// which must cancel the stale gesture instead of being reported as a drag.
func TestHandleMouse_ButtonlessMotionCancelsStaleDrag(t *testing.T) {
	h := NewHandler()
	h.HitMap.Add("row", Rect{X: 0, Y: 0, W: 20, H: 10}, 1)
	h.StartDrag(2, 0, "row", 1)

	got := h.HandleMouse(tea.MouseMotionMsg(tea.Mouse{X: 4, Y: 5})) // no button held
	if got.Type != ActionHover {
		t.Errorf("button-less motion = %v, want ActionHover", got.Type)
	}
	if h.IsDragging() {
		t.Error("handler still dragging after button-less motion")
	}
	if h.DragRegion() != "" {
		t.Errorf("DragRegion = %q, want empty", h.DragRegion())
	}

	// And a later release must not be reported as a drag end.
	end := h.HandleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 4, Y: 5}))
	if end.Type != ActionNone {
		t.Errorf("release after cancelled drag = %v, want ActionNone", end.Type)
	}
}

// A release with no drag in progress must stay a no-op.
func TestHandleMouse_ReleaseWithoutDragIsNoAction(t *testing.T) {
	h := NewHandler()
	h.HitMap.Add("row", Rect{X: 0, Y: 0, W: 20, H: 1}, 1)

	got := h.HandleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 2, Y: 0}))
	if got.Type != ActionNone {
		t.Errorf("release without drag = %v, want ActionNone", got.Type)
	}
}

func TestHandler_Clear(t *testing.T) {
	h := NewHandler()
	h.HitMap.Add("a", Rect{X: 0, Y: 0, W: 10, H: 10}, nil)
	h.Clear()

	if region := h.HitMap.Test(5, 5); region != nil {
		t.Error("expected nil after Clear()")
	}
}

// Wheel actions carry their modifiers. Consumers that forward notches to a
// terminal application need them to tell "the app's wheel event" from "scroll
// the viewer's own scrollback" — with the fields always false the escape hatch
// was unreachable.
func TestHandleMouse_WheelCarriesModifiers(t *testing.T) {
	h := NewHandler()
	h.HitMap.Add("preview", Rect{X: 0, Y: 0, W: 100, H: 40}, nil)

	plain := h.HandleMouse(tea.MouseWheelMsg(tea.Mouse{X: 10, Y: 5, Button: tea.MouseWheelUp}))
	if plain.Type != ActionScrollUp || plain.Alt || plain.Shift {
		t.Fatalf("plain wheel-up = %#v", plain)
	}

	alt := h.HandleMouse(tea.MouseWheelMsg(tea.Mouse{X: 10, Y: 5, Button: tea.MouseWheelUp, Mod: tea.ModAlt}))
	if alt.Type != ActionScrollUp || !alt.Alt {
		t.Fatalf("alt+wheel-up = %#v, want ActionScrollUp with Alt set", alt)
	}
	altDown := h.HandleMouse(tea.MouseWheelMsg(tea.Mouse{X: 10, Y: 5, Button: tea.MouseWheelDown, Mod: tea.ModAlt}))
	if altDown.Type != ActionScrollDown || !altDown.Alt {
		t.Fatalf("alt+wheel-down = %#v, want ActionScrollDown with Alt set", altDown)
	}

	// Shift+wheel stays a horizontal scroll — it is not a vertical-scroll
	// modifier, so nothing downstream should expect ActionScrollUp with Shift.
	shift := h.HandleMouse(tea.MouseWheelMsg(tea.Mouse{X: 10, Y: 5, Button: tea.MouseWheelUp, Mod: tea.ModShift}))
	if shift.Type != ActionScrollLeft || !shift.Shift {
		t.Fatalf("shift+wheel-up = %#v, want ActionScrollLeft with Shift set", shift)
	}
}
