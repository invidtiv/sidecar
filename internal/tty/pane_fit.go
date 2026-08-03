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

	// CursorCol is the pane-relative cursor column used to anchor horizontal
	// clipping. Ignored unless HasCursor is set.
	CursorCol int
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
