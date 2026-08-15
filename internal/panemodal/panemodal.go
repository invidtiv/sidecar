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

// dimMargin is the number of cells of the pane's own content that must remain
// visible on every side for the modal to read as an overlay rather than as the
// pane's content. Below it there is not enough surviving context for the dimmed
// ring to mean anything — a one- or two-cell frame of greyed characters reads
// as noise around the modal — so the modal takes the box instead and the pane
// content behind it is dropped entirely.
const dimMargin = 2

// Render draws m inside box on top of background (the pane's own content,
// rendered at box.W x box.H) and registers m's hit regions at their true screen
// positions. The result is exactly box.H lines of exactly box.W cells, ready to
// blit at (box.X, box.Y).
//
// When the box is roomy the modal is centred in it with the pane's content
// dimmed around it. When the box is tight (see dimMargin) the modal fills the
// box and the pane's content is not shown at all.
//
// handler may be nil, in which case no regions are registered. Note that
// modal.Modal.Render clears the handler's hit map, so the modal's regions are
// the only ones in it afterwards — anything the pane registered before this
// call is gone, exactly as it is for a full-screen modal.
func Render(m *modal.Modal, box Box, background string, handler *mouse.Handler) string {
	if box.W <= 0 || box.H <= 0 {
		return ""
	}
	if m == nil {
		return ui.OverlayModal(background, "", box.W, box.H)
	}

	// The modal sizes itself to the surface it is given, so handing it the box
	// dimensions is what keeps it inside the pane.
	content := m.Render(box.W, box.H, handler)
	translateRegions(handler, box.X, box.Y)

	if !roomy(content, box) {
		background = ""
	}
	return ui.OverlayModal(background, content, box.W, box.H)
}

// roomy reports whether the box has room for a dimmed ring of pane content
// around the rendered modal.
func roomy(content string, box Box) bool {
	return lipgloss.Width(content)+2*dimMargin <= box.W &&
		lipgloss.Height(content)+2*dimMargin <= box.H
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
