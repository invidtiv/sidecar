package tty

import "fmt"

// PaneFitInput describes a tmux pane's real geometry alongside the local
// viewport it has to be shown in.
//
// Sidecar never owns a tmux session exclusively: the same session can be driven
// by another sidecar instance on a differently sized terminal, so the pane's
// actual size routinely disagrees with the size this instance last requested
// (td-73fa86). Rendering therefore has to derive from the observed size, not
// the requested one.
//
// PaneWidth/PaneHeight of 0 mean "unknown" — the viewport is used as-is.
type PaneFitInput struct {
	ViewWidth  int
	ViewHeight int
	PaneWidth  int
	PaneHeight int

	// CursorCol and CursorRow are the pane-relative cursor coordinates used to
	// anchor clipping. Ignored unless HasCursor is set.
	CursorCol int
	CursorRow int
	HasCursor bool
}

// PaneFit is the projection of a pane onto a viewport: the region actually
// drawn, plus the horizontal scroll needed to keep the cursor in view.
type PaneFit struct {
	// Width and Height are the display region, never larger than the viewport
	// and never larger than the pane.
	Width  int
	Height int

	// ColOffset is the first pane column rendered. Non-zero only when the pane
	// is wider than the viewport and the cursor sits past the right edge.
	ColOffset int

	// RowOffset is the first pane row rendered. Non-zero only when the pane is
	// taller than the viewport; it anchors the window on the cursor so the row
	// being typed into stays on screen, and falls back to the pane's tail when
	// there is no cursor to anchor to.
	RowOffset int

	// ClippedWidth/ClippedHeight report that the pane is larger than the
	// viewport on that axis, so part of it is not shown.
	ClippedWidth  bool
	ClippedHeight bool

	// LetterboxedWidth/LetterboxedHeight report that the pane is smaller than
	// the viewport on that axis, so the pane is padded rather than stretched.
	LetterboxedWidth  bool
	LetterboxedHeight bool
}

// Clipped reports whether any part of the pane is hidden.
func (f PaneFit) Clipped() bool { return f.ClippedWidth || f.ClippedHeight }

// Letterboxed reports whether the pane is smaller than the viewport on either
// axis.
func (f PaneFit) Letterboxed() bool { return f.LetterboxedWidth || f.LetterboxedHeight }

// FitPane projects a pane of the observed size onto the viewport. It is a pure
// function so both the workspace viewport and tty.Model share one rule.
func FitPane(in PaneFitInput) PaneFit {
	fit := PaneFit{
		Width:  max(in.ViewWidth, 0),
		Height: max(in.ViewHeight, 0),
	}
	if in.PaneWidth > 0 {
		switch {
		case in.PaneWidth < fit.Width:
			fit.Width = in.PaneWidth
			fit.LetterboxedWidth = true
		case in.PaneWidth > fit.Width:
			fit.ClippedWidth = true
		}
	}
	if in.PaneHeight > 0 {
		switch {
		case in.PaneHeight < fit.Height:
			fit.Height = in.PaneHeight
			fit.LetterboxedHeight = true
		case in.PaneHeight > fit.Height:
			fit.ClippedHeight = true
		}
	}
	fit.ColOffset = paneColOffset(fit.ClippedWidth, fit.Width, in.PaneWidth, in.CursorCol, in.HasCursor)
	fit.RowOffset = paneRowOffset(fit.ClippedHeight, fit.Height, in.PaneHeight, in.CursorRow, in.HasCursor)
	return fit
}

// WithWidth returns the fit re-derived for a narrower display region, which is
// how a caller that steals a column (a scrollbar) keeps ColOffset honest.
func (f PaneFit) WithWidth(width int, paneWidth, cursorCol int, hasCursor bool) PaneFit {
	f.Width = max(width, 0)
	if paneWidth > 0 {
		f.ClippedWidth = paneWidth > f.Width
		f.LetterboxedWidth = paneWidth < f.Width
	}
	f.ColOffset = paneColOffset(f.ClippedWidth, f.Width, paneWidth, cursorCol, hasCursor)
	return f
}

// paneColOffset anchors a clipped pane so the cursor stays visible: the window
// only slides right once the cursor would fall past its right edge, and never
// past the pane's last column.
func paneColOffset(clipped bool, width, paneWidth, cursorCol int, hasCursor bool) int {
	if !clipped || !hasCursor || width <= 0 {
		return 0
	}
	offset := cursorCol - width + 1
	if offset <= 0 {
		return 0
	}
	if maxOffset := paneWidth - width; offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		return 0
	}
	return offset
}

// paneRowOffset anchors a clipped pane vertically. With a cursor the window
// only slides down once the cursor would fall past its bottom edge, so a user
// editing near the top of a taller pane still sees the line they are on
// (td-73fa86). Without a cursor the pane's tail — the latest output — wins.
func paneRowOffset(clipped bool, height, paneHeight, cursorRow int, hasCursor bool) int {
	if !clipped || height <= 0 {
		return 0
	}
	maxOffset := paneHeight - height
	if maxOffset <= 0 {
		return 0
	}
	if !hasCursor {
		return maxOffset
	}
	offset := cursorRow - height + 1
	if offset <= 0 {
		return 0
	}
	return min(offset, maxOffset)
}

// PaneCoords maps a 0-indexed cell of the rendered region to 1-indexed pane
// coordinates, which is what tmux's mouse protocol speaks. It reports false for
// a cell outside the rendered region, so hit testing moves with the pixels
// instead of assuming the viewport and the pane are aligned (td-73fa86).
func (f PaneFit) PaneCoords(relX, relY, paneWidth, paneHeight int) (col, row int, ok bool) {
	if relX < 0 || relY < 0 || relX >= f.Width || relY >= f.Height {
		return 0, 0, false
	}
	col = relX + f.ColOffset + 1
	row = relY + f.RowOffset + 1
	if paneWidth > 0 {
		col = min(col, paneWidth)
	}
	if paneHeight > 0 {
		row = min(row, paneHeight)
	}
	return col, row, true
}

// PaneSizeIndicator describes a clipped pane as "200x50, showing 120x40". It
// returns "" when nothing is hidden, so callers can append it unconditionally.
func PaneSizeIndicator(paneWidth, paneHeight, shownWidth, shownHeight int) string {
	if paneWidth <= 0 || paneHeight <= 0 {
		return ""
	}
	if paneWidth <= shownWidth && paneHeight <= shownHeight {
		return ""
	}
	return fmt.Sprintf("%dx%d, showing %dx%d", paneWidth, paneHeight, shownWidth, shownHeight)
}
