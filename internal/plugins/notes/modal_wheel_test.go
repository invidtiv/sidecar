package notes

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
)

// modalBodyPoint returns a point over modal-body that no control covers.
func modalBodyPoint(t *testing.T, h *mouse.Handler) (int, int) {
	t.Helper()
	for _, r := range h.HitMap.Regions() {
		if r.ID != "modal-body" {
			continue
		}
		x := r.Rect.X + r.Rect.W/2
		for y := r.Rect.Y; y < r.Rect.Y+r.Rect.H; y++ {
			if hit := h.HitMap.Test(x, y); hit != nil && hit.ID == "modal-body" {
				return x, y
			}
		}
	}
	t.Fatal("no free modal-body point found")
	return 0, 0
}

// modalRegionPoint returns the centre of the named hit region.
func modalRegionPoint(t *testing.T, h *mouse.Handler, id string) (int, int) {
	t.Helper()
	for _, r := range h.HitMap.Regions() {
		if r.ID == id {
			return r.Rect.X + r.Rect.W/2, r.Rect.Y + r.Rect.H/2
		}
	}
	t.Fatalf("region %q not registered", id)
	return 0, 0
}

// backdropPoint returns a point the modal absorbs outside its box.
func backdropPoint(t *testing.T, h *mouse.Handler) (int, int) {
	t.Helper()
	if hit := h.HitMap.Test(0, 0); hit == nil || hit.ID == "modal-body" {
		t.Fatalf("expected an absorbing region at the top-left corner, got %v", hit)
	}
	return 0, 0
}

// notesModalPlugin opens one of the Notes modals over a mid-list cursor and
// renders it, so the query has trustworthy geometry.
func notesModalPlugin(t *testing.T, family string, height int) *Plugin {
	t.Helper()
	p := wheelTestPlugin(t, 20)
	p.height = height
	p.cursor = 5 // mid-list: the pane underneath would report movable
	p.notes[5].Content = strings.Repeat("detail line\n", 60)
	switch family {
	case "info":
		p.openInfoModal()
		p.ensureInfoModal()
		p.infoModal.Render(p.width, p.height, p.infoModalMouseHandler)
	case "delete":
		p.openDeleteModal()
		p.ensureDeleteModal()
		p.deleteModal.Render(p.width, p.height, p.deleteModalMouseHandler)
	case "task":
		p.openTaskModal()
		p.ensureTaskModal()
		p.taskModal.Render(p.width, p.height, p.taskModalMouseHandler)
	default:
		t.Fatalf("unknown modal family %q", family)
	}
	return p
}

func notesModalHandler(p *Plugin) *mouse.Handler {
	switch {
	case p.showInfoModal:
		return p.infoModalMouseHandler
	case p.showDeleteModal:
		return p.deleteModalMouseHandler
	case p.showTaskModal:
		return p.taskModalMouseHandler
	}
	return p.mouseHandler
}

func TestNotesModalWheelAbsorbedOverChromeAndBackdrop(t *testing.T) {
	for _, family := range []string{"info", "delete", "task"} {
		t.Run(family, func(t *testing.T) {
			p := notesModalPlugin(t, family, 40)
			h := notesModalHandler(p)
			bx, by := backdropPoint(t, h)
			for _, up := range []bool{true, false} {
				if !p.WheelAtBoundary(wheelMsg(bx, by, up)) {
					t.Errorf("backdrop wheel (up=%v) was not absorbed", up)
				}
			}
			// On a tall screen these modals fit, so the body is bounded too.
			x, y := modalBodyPoint(t, h)
			for _, up := range []bool{true, false} {
				if !p.WheelAtBoundary(wheelMsg(x, y, up)) {
					t.Errorf("non-scrollable body wheel (up=%v) was not bounded", up)
				}
			}
		})
	}
}

func TestNotesDeleteConfirmationAbsorbsWholeWheelStream(t *testing.T) {
	p := notesModalPlugin(t, "delete", 40)
	h := notesModalHandler(p)
	cx, cy := modalRegionPoint(t, h, "cancel")
	for range 50 {
		if !p.WheelAtBoundary(wheelMsg(cx, cy, false)) {
			t.Fatal("a short confirmation must drop its whole wheel stream")
		}
	}
}

func TestNotesModalWheelScrollsLongBody(t *testing.T) {
	// A short screen forces the task modal's body to overflow.
	p := notesModalPlugin(t, "task", 14)
	h := notesModalHandler(p)
	x, y := modalBodyPoint(t, h)

	if !p.WheelAtBoundary(wheelMsg(x, y, true)) {
		t.Fatal("expected bounded at the top of the body")
	}
	if p.WheelAtBoundary(wheelMsg(x, y, false)) {
		t.Fatal("expected movable downward at the top of a long body")
	}

	// Middle: movable both ways.
	p.taskModal.ScrollBy(3)
	p.taskModal.Render(p.width, p.height, h)
	if p.WheelAtBoundary(wheelMsg(x, y, true)) || p.WheelAtBoundary(wheelMsg(x, y, false)) {
		t.Fatal("expected movable in both directions mid-body")
	}

	// Bottom: bounded downward, and the first reverse event passes.
	p.taskModal.ScrollToBottom()
	p.taskModal.Render(p.width, p.height, h)
	if !p.WheelAtBoundary(wheelMsg(x, y, false)) {
		t.Fatal("expected bounded at the bottom of the body")
	}
	if p.WheelAtBoundary(wheelMsg(x, y, true)) {
		t.Fatal("reverse event after the boundary must pass")
	}
}

func TestNotesModalAnswersInsteadOfPaneUnderneath(t *testing.T) {
	p := notesModalPlugin(t, "info", 40)
	// The list underneath is mid-cursor, so it would report movable.
	if p.listBounds().AtBoundary(3) {
		t.Fatal("precondition: the list pane should be movable")
	}
	// A wheel over the list pane's coordinates is answered by the modal.
	if !p.WheelAtBoundary(wheelMsg(listX, 5, false)) {
		t.Fatal("an open modal must answer instead of the pane underneath")
	}
}

func TestNotesModalUnknownAfterInvalidate(t *testing.T) {
	p := notesModalPlugin(t, "info", 40)
	h := notesModalHandler(p)
	x, y := modalBodyPoint(t, h)
	if !p.WheelAtBoundary(wheelMsg(x, y, false)) {
		t.Fatal("precondition: bounded before invalidation")
	}

	// An async content change makes the cached geometry untrustworthy.
	p.infoModal.Invalidate()
	if p.WheelAtBoundary(wheelMsg(x, y, false)) {
		t.Fatal("expected unknown (false) between invalidation and the next render")
	}
	p.infoModal.Render(p.width, p.height, h)
	if !p.WheelAtBoundary(wheelMsg(x, y, false)) {
		t.Fatal("expected an exact answer again after re-rendering")
	}
}

func TestNotesModalHorizontalWheelIsUnknown(t *testing.T) {
	p := notesModalPlugin(t, "info", 40)
	h := notesModalHandler(p)
	x, y := modalBodyPoint(t, h)
	if p.WheelAtBoundary(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelLeft}) {
		t.Fatal("horizontal wheel must stay unknown")
	}
}

func TestNotesExitConfirmationIsAbsorbed(t *testing.T) {
	p := wheelTestPlugin(t, 20)
	p.cursor = 5
	p.edit.ShowExitConfirm = true
	for _, x := range []int{listX, editorX} {
		for _, up := range []bool{true, false} {
			if !p.WheelAtBoundary(wheelMsg(x, 5, up)) {
				t.Errorf("exit confirmation wheel (x=%d, up=%v) was not absorbed", x, up)
			}
		}
	}
}
