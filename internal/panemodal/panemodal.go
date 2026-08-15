// Package panemodal renders a modal inside an arbitrary rectangular box rather
// than over the whole screen, and puts the modal's mouse hit regions where the
// box actually is on screen.
//
// A modal.Modal renders and registers its hit regions against the surface it is
// handed: coordinates run from (0,0) of that surface. Handed a pane, the modal
// has no idea the pane starts partway down the screen, so every region it
// registers is wrong by the pane's origin. Render fixes that by translating the
// regions the modal just registered by the box origin.
package panemodal

import (
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// Box is a rectangle in screen coordinates: the pane the modal is drawn into.
// The field names mirror the workspace pane layout's Box.
type Box struct {
	X, Y, W, H int
}

// dimMarginX and dimMarginY are how much of the pane's own content must survive
// on each side for the modal to read as an overlay rather than as the pane's
// content. They differ because a horizontal sliver and a horizontal strip are
// not the same thing to read: two rows of full-width pane text above and below
// the box are legible context, while a one- or two-cell column of greyed
// characters down each side is vertical confetti — the fragments are too narrow
// to be words. Below these thresholds the modal takes the box instead and the
// pane content behind it is dropped entirely.
const (
	dimMarginX = 8
	dimMarginY = 2
)

// Draw renders a surface into a box of the given size and registers its hit
// regions on h (which may be nil). fill asks the surface to *be* the box —
// exactly width by height, no margin — rather than to size itself to its own
// content; see RenderFunc for when it is set.
type Draw func(width, height int, fill bool, h *mouse.Handler) string

// Render draws m inside box on top of background (the pane's own content,
// rendered at box.W x box.H) and registers m's hit regions at their true screen
// positions. The result is exactly box.H lines of exactly box.W cells, ready to
// blit at (box.X, box.Y).
//
// When the box is roomy the modal is centred in it with the pane's content
// dimmed around it. When the box is tight (see dimMarginX/dimMarginY) the modal
// fills the box and the pane's content is not shown at all.
//
// handler may be nil, in which case no regions are registered. Note that
// modal.Modal.Render clears the handler's hit map, so the modal's regions are
// the only ones in it afterwards — anything the pane registered before this
// call is gone, exactly as it is for a full-screen modal.
func Render(m *modal.Modal, box Box, background string, handler *mouse.Handler) string {
	if m == nil {
		if box.W <= 0 || box.H <= 0 {
			return ""
		}
		return ui.OverlayModal(background, "", box.W, box.H)
	}
	return RenderFunc(box, background, handler, func(width, height int, _ bool, h *mouse.Handler) string {
		// The modal sizes itself to the surface it is given, so handing it the
		// box dimensions is what keeps it inside the pane. A plain modal.Modal
		// has no fill mode; it is centred either way.
		return m.Render(width, height, h)
	})
}

// RenderFunc is Render for anything that draws itself into a given size and
// registers its hit regions on a handler — a modal.Modal, a filefind.Finder, a
// projectsearch.Search. draw is called with the box's own dimensions and with
// handler; whatever regions it registers are then translated by the box origin,
// so a surface that knows only its own coordinates still lands where it is
// drawn.
//
// The compositing rules are Render's: a surface sized to its own content,
// centred with the pane dimmed around it, when the box has room for a readable
// margin; otherwise a second pass with fill set, which asks the surface to take
// the whole box, and no pane content behind it. The two passes are why draw
// must be cheap and free of side effects beyond its own layout — it may be
// called twice for one frame. The result is always exactly box.H lines of
// exactly box.W cells.
//
// handler may be nil, in which case draw is called with nil and registers
// nothing. A surface that clears the handler (modal.Modal does) leaves the
// pane's earlier regions gone, exactly as a full-screen modal does; a surface
// that only adds to it leaves them in place, so hosts that care about ordering
// hand RenderFunc a scratch handler and merge its regions themselves.
func RenderFunc(box Box, background string, handler *mouse.Handler, draw Draw) string {
	if box.W <= 0 || box.H <= 0 {
		return ""
	}
	if draw == nil {
		return ui.OverlayModal(background, "", box.W, box.H)
	}

	content := draw(box.W, box.H, false, handler)
	if !roomy(content, box) {
		// The pane cannot show enough of itself around the modal to be worth
		// dimming, so the modal owns the box.
		content = draw(box.W, box.H, true, handler)
		background = ""
	}
	translateRegions(handler, box.X, box.Y)

	return ui.OverlayModal(background, content, box.W, box.H)
}

// roomy reports whether the box has room for a dimmed ring of pane content
// around the rendered modal that is wide enough to read as context.
func roomy(content string, box Box) bool {
	return lipgloss.Width(content)+2*dimMarginX <= box.W &&
		lipgloss.Height(content)+2*dimMarginY <= box.H
}

// translateRegions shifts every region currently in the handler's hit map by
// (dx, dy). Order is preserved, so the hit-test priority the modal relies on
// (later regions win) survives the shift.
func translateRegions(handler *mouse.Handler, dx, dy int) {
	if handler == nil || (dx == 0 && dy == 0) {
		return
	}
	regions := handler.HitMap.Regions()
	handler.HitMap.Clear()
	for _, r := range regions {
		handler.HitMap.Add(r.ID, mouse.Rect{
			X: r.Rect.X + dx,
			Y: r.Rect.Y + dy,
			W: r.Rect.W,
			H: r.Rect.H,
		}, r.Data)
	}
}
