package tty

// ViewportInput contains every value needed to lay out a captured terminal
// buffer inside a viewport. It is deliberately value-only: layout must not
// mutate the buffer or any host state.
type ViewportInput struct {
	Buffer *OutputBuffer
	Width  int
	Height int

	// Offset is absolute from the top unless OffsetFromBottom is set.
	Offset           int
	OffsetFromBottom bool
	Follow           bool
	TrimTrailing     bool

	Interactive   bool
	CursorRow     int
	CursorCol     int
	CursorVisible bool
	PaneHeight    int
	PaneWidth     int
	AbsoluteBase  int

	// Scrollbar reserves the viewport's final column. It is explicit because it
	// moves every column the user can click on, so a host that draws one and a
	// host that does not must not be distinguished by an implicit width test.
	Scrollbar bool
}

// TrimsTrailingRows reports whether a surface's window ends at the last line
// with text on it rather than at the end of the buffer.
//
// A window that is not mirroring a live grid is a scrollback window, and
// tmux's trailing blank rows are padding rather than content there. A live
// grid's blank rows are the application's own — full-screen programs draw
// chrome against them — so they stay addressable, which is why FitViewport
// exempts any buffer with a known live grid from this answer regardless of
// where its window currently sits. A host must not add a second condition of
// its own: the two surfaces would then answer a scrolled-back pane differently.
//
// What this costs a watched pane the user has scrolled back on: nothing that
// moves the rows. A scrollback window is placed by an explicit Start (or by a
// distance back from the live bottom), so trimming changes EffectiveCount,
// MaxOffset and where the window may stop — never which buffer line lands on
// which screen row at a given offset. The visible effect is that the window
// cannot be scrolled into blank padding below the last row there is anything to
// see on, which is the behaviour every surface wants of a history window. A
// live grid gets that for free from its own bound: no window sits below the
// live edge, so its trailing rows are exactly as reachable as they are now.
func TrimsTrailingRows(interactive bool) bool { return !interactive }

// ShouldOverlayCursor reports whether a surface may draw the pane's cursor.
// A window scrolled off the live edge is showing history, and a cursor painted
// over history sits on a row the pane is not writing to.
func ShouldOverlayCursor(interactive, cursorVisible, atLiveEdge bool) bool {
	return interactive && cursorVisible && atLiveEdge
}

// Viewport is where a buffer's lines land inside a viewport: which lines are
// drawn, how wide and tall the drawn area is, and how the pane's own geometry
// was projected onto it.
type Viewport struct {
	Start          int
	End            int
	EffectiveCount int
	DisplayWidth   int
	DisplayHeight  int
	MaxOffset      int
	AbsoluteStart  int
	ShowScrollbar  bool

	// PadWidth is the column the content block is padded to before a scrollbar
	// is joined to it. It tracks the viewport rather than the pane so the
	// scrollbar stays at the viewport's edge.
	PadWidth int

	// Fit records how the pane's observed geometry was projected onto the
	// viewport: letterboxed when the pane is smaller, clipped (with ColOffset
	// as the first visible column) when it is larger.
	Fit PaneFit

	// PaneClipped reports that the pane itself does not fit the viewport, as
	// opposed to Fit.ClippedWidth, which also trips when the scrollbar takes a
	// column off an otherwise perfectly sized pane.
	PaneClipped bool

	// PaneTop is the buffer index of pane row 0, so a rendered row maps back to
	// the pane row tmux would report for it, and so the native cursor lands on
	// the row it belongs to.
	//
	// It comes from the buffer, which was told the split by whoever published
	// the content — a screen-model frame knows its loaded history rows and its
	// grid height at the instant it is built, and a capture knows its own row
	// count and the pane height read with it. Both alternatives were tried and
	// both drift. The absolute-coordinate form (history_size + cursor_row -
	// buffer base) mixes two independently observed quantities: display-message
	// and capture-pane are separate writes, so N lines can scroll into history
	// between them and the cursor is drawn N rows too high until the pane is
	// re-seeded. Re-deriving it as "the buffer's last PaneHeight lines" instead
	// assumes a serialization detail that does not hold: a grid whose final row
	// is blank ends in a newline that reads as a terminator, the buffer is a row
	// short, and the cursor is drawn one row too high (td-d29821). Absolute
	// coordinates are still what scrollback, search, and selection use, where a
	// transient off-by-N is harmless.
	PaneTop int
}

// PaneRowAt maps a 0-indexed rendered row to a 0-indexed pane row.
func (v Viewport) PaneRowAt(relY int) int {
	return v.Start + relY - v.PaneTop
}

// Rows is the number of buffer lines the viewport draws.
func (v Viewport) Rows() int { return v.End - v.Start }

// FitViewport resolves the drawn window for one terminal surface.
func FitViewport(in ViewportInput) Viewport {
	layout := Viewport{
		DisplayWidth:  max(in.Width, 0),
		DisplayHeight: max(in.Height, 0),
	}
	if layout.DisplayWidth == 0 || layout.DisplayHeight == 0 {
		return layout
	}

	// Project the pane's observed geometry onto the viewport (td-73fa86). The
	// pane can be any size — another sidecar instance may own the session — so
	// the requested size is only a request.
	fit := FitPane(PaneFitInput{
		ViewWidth:  layout.DisplayWidth,
		ViewHeight: layout.DisplayHeight,
		PaneWidth:  in.PaneWidth,
		PaneHeight: in.PaneHeight,
		CursorCol:  in.CursorCol,
		HasCursor:  in.Interactive && in.CursorVisible,
	})
	layout.DisplayWidth = fit.Width
	// A shorter pane only letterboxes while the viewport mirrors the live pane.
	// Outside interactive mode the viewport is a scrollback window, so the extra
	// rows show more history rather than stretching the pane.
	if !in.Interactive && fit.LetterboxedHeight {
		fit.Height = layout.DisplayHeight
		fit.LetterboxedHeight = false
	}
	layout.DisplayHeight = fit.Height
	layout.PadWidth = layout.DisplayWidth
	// Record the pane-vs-viewport verdict before the scrollbar steals a column:
	// losing a column to chrome is not a geometry mismatch and must not read as
	// one (td-73fa86).
	layout.PaneClipped = fit.Clipped()
	if in.Scrollbar && layout.DisplayWidth > 1 {
		// The scrollbar owns the viewport's final column even when all content
		// fits; RenderScrollbar draws a spacer in that state. Keeping the chrome
		// stable prevents a newly published frame from clipping the application's
		// last column while the corresponding tmux resize is still in flight
		// (td-0818ef). A pane already sized to the remaining content area fits
		// as-is; subtracting again would clip its own final column (td-e8bdcf).
		contentWidth := max(in.Width, 0) - 1
		layout.DisplayWidth = min(layout.DisplayWidth, contentWidth)
		fit = fit.WithWidth(layout.DisplayWidth, in.PaneWidth, in.CursorCol, in.Interactive && in.CursorVisible)
		// Keep the scrollbar pinned to the viewport edge even when a narrower
		// pane letterboxes the content.
		layout.PadWidth = max(layout.DisplayWidth, contentWidth)
		layout.ShowScrollbar = true
	}
	layout.Fit = fit
	// Geometry is settled above so hit testing can ask for it without a buffer;
	// only the scroll window needs one.
	if in.Buffer == nil {
		return layout
	}

	lineCount, paneTop, paneKnown := in.Buffer.PaneWindow()
	// Pane row 0, in buffer coordinates. Settled against the full line count
	// before any trailing-blank trim: the producer split describes the live
	// grid including blank final rows (td-d29821).
	//
	// Between a resize and the capture that follows it, paneTop still describes
	// the old pane height while in.PaneHeight is already the new one, so the
	// cursor sits off by the delta for one poll. That is the accepted cost of
	// taking the split from the producer: the alternative — inferring it from
	// the buffer's tail — was self-consistent across a resize but wrong whenever
	// the grid's last row was blank, which is every prompt at the bottom of a
	// screen rather than the instant after a drag (td-d29821).
	switch {
	case paneKnown:
		layout.PaneTop = min(paneTop, lineCount)
	case in.PaneHeight > 0:
		layout.PaneTop = max(lineCount-in.PaneHeight, 0)
	}

	// A live grid is a buffer whose tail is a pane tmux is still drawing into,
	// which is knowable without asking where the window currently sits.
	//
	// Full-screen programs (Grok, Claude, …) keep intentional blank rows in that
	// pane, so trimming them is wrong for the whole buffer rather than only
	// while following: the trim used to be lifted at the live edge alone, which
	// left offset 0 placed by the untrimmed grid and offset 1 by the trimmed
	// count — one notch back jumped by however many blank rows the pane ended
	// with (measured at 38 rows on a watched agent pane, td-bbbbfe). Nothing is
	// lost by keeping them: a window can never sit below the live edge, so the
	// pane's trailing padding is exactly as reachable as it is on screen now.
	liveGrid := paneKnown || in.PaneHeight > 0

	layout.EffectiveCount = lineCount
	if in.TrimTrailing && !liveGrid {
		layout.EffectiveCount = max(in.Buffer.LastNonEmptyLine()+1, 0)
		if layout.PaneTop > layout.EffectiveCount {
			layout.PaneTop = layout.EffectiveCount
		}
	}

	// MaxOffset is the live-edge start, which makes it both the window's origin
	// and its bound: offset 0 is the live edge, offset N is N rows back from it,
	// and the furthest back a window can go is the top of the buffer. Deriving
	// the two from one number is what keeps the first notch off the live edge
	// worth exactly one scroll step on every surface (td-bbbbfe).
	layout.MaxOffset = max(layout.EffectiveCount-layout.DisplayHeight, 0)
	if liveGrid {
		// A pane shorter than the viewport is letterboxed at the live edge —
		// it starts at PaneTop and render pads below it — so that, not a window
		// full of history, is where offset 0 sits.
		if paneRows := lineCount - layout.PaneTop; paneRows <= layout.DisplayHeight {
			layout.MaxOffset = max(layout.PaneTop, 0)
		}
	}

	switch {
	case in.Follow:
		layout.Start = layout.MaxOffset
	case in.OffsetFromBottom:
		layout.Start = layout.MaxOffset - min(max(in.Offset, 0), layout.MaxOffset)
	default:
		layout.Start = min(max(in.Offset, 0), layout.MaxOffset)
	}
	// A pane taller than the viewport is clipped, so pin the window to the
	// cursor when following: the live row matters more than the pane's last
	// row, which is usually blank padding below it (td-73fa86).
	// Gated on interactive mode, which is the only state that carries a cursor
	// row at all; ClippedHeight implies observed geometry, so PaneTop is already
	// set. Deliberately not gated on the cursor being *visible*: a full-screen
	// app that hides it (less, some TUIs) still has a live row, and anchoring on
	// the pane's tail instead would clip exactly what the user is looking at.
	//
	// This is the one adjustment that moves the live edge without moving the
	// bound, so it is deliberately confined to the window that follows: applying
	// it to a scrolled-back window instead would slide history under the reader
	// every time the application moved its cursor.
	if fit.ClippedHeight && in.Follow && in.Interactive {
		cursorLine := layout.PaneTop + in.CursorRow
		layout.Start = min(layout.Start, max(cursorLine-layout.DisplayHeight+1, 0))
	}
	layout.End = min(layout.Start+layout.DisplayHeight, layout.EffectiveCount)
	layout.AbsoluteStart = in.AbsoluteBase + layout.Start
	if !paneKnown && in.PaneHeight <= 0 {
		layout.PaneTop = layout.Start
	}
	return layout
}

// WindowBound is the furthest back a surface's window can be placed, in rows
// back from its live edge. It is the one derivation every host asks — the
// global preview, the project's primary surface and its terminal panel — so
// that a bound and the window it bounds can never come from two different
// measurements of the same surface.
//
// It is measured off the drawn layout rather than off a line count, because the
// drawn window is what a reader can actually reach: a pane letterboxed into a
// taller viewport, or one whose trailing rows are trimmed, stops somewhere a
// count does not know about. Taking it off the live edge is deliberate — that
// is the only state a bound is asked about, and it is where offset 0 sits.
func WindowBound(in ViewportInput) int {
	in.Follow = false
	return max(FitViewport(in).MaxOffset, 0)
}

// PaneCoordsAt maps a position inside a drawn viewport, relative to its first
// content cell, to the 1-indexed pane coordinates tmux's mouse protocol expects.
//
// Horizontal placement comes from the fit — a pane wider than the viewport is
// drawn scrolled — while vertical placement comes from the buffer window, because
// the viewport scrolls history as well as the live grid.
func PaneCoordsAt(v Viewport, relX, relY, paneWidth, paneHeight int) (col, row int, ok bool) {
	if v.DisplayWidth <= 0 || v.DisplayHeight <= 0 || paneWidth <= 0 || paneHeight <= 0 {
		return 0, 0, false
	}
	if relX < 0 || relY < 0 || relX >= v.DisplayWidth || relY >= v.DisplayHeight {
		return 0, 0, false
	}
	col = min(relX+v.Fit.ColOffset+1, paneWidth)
	row = min(max(v.PaneRowAt(relY)+1, 1), paneHeight)
	return col, row, true
}

// ViewportCursor is where the pane's cursor lands inside a drawn viewport, and
// false when it is scrolled out of the drawn window. A rendered row maps back
// through PaneTop, so the drawn cursor and the pane row tmux reports for a click
// on it can never disagree (td-d29821).
func ViewportCursor(v Viewport, in ViewportInput) (x, y int, ok bool) {
	if in.Buffer == nil || v.EffectiveCount == 0 || v.DisplayWidth <= 0 || v.DisplayHeight <= 0 {
		return 0, 0, false
	}
	visibleRows := v.Rows()
	if in.PaneHeight > 0 && (in.Interactive || in.Follow) {
		visibleRows = v.DisplayHeight
	}
	if visibleRows <= 0 {
		return 0, 0, false
	}
	y = v.PaneTop + in.CursorRow - v.Start
	if y < 0 || y >= visibleRows {
		return 0, 0, false
	}
	x = min(max(in.CursorCol-v.Fit.ColOffset, 0), v.DisplayWidth-1)
	return x, y, true
}
