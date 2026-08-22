package tty

import "github.com/marcus/sidecar/internal/scroll"

// A terminal window's scrollbar is one translation between two vocabularies:
// the shared renderer counts items from the top of the content, and a terminal
// window counts rows back from its live edge (or holds an absolute start while
// a gesture pins it). This file is that translation, state-free, so both hosts
// that draw an interactive bar over captured scrollback — the project's primary
// terminal and panel, and the global Sessions preview — map presses and drags
// through the same rule instead of keeping copies of it.

// WindowScrollbar is the scrollbar one drawn window reports: the renderer
// inputs the bar was drawn with, plus the buffer base those offsets were built
// against. Offset is in absolute buffer coordinates — the same number
// Viewport.AbsoluteStart carries — because a window pinned mid-gesture must not
// slide when output lands below it, and absolute coordinates do not move when
// the live edge advances.
type WindowScrollbar struct {
	Total   int // scrollback extent, which can exceed the loaded buffer
	Offset  int // absolute top row of the drawn window
	Visible int // drawn rows in the window
	Track   int // track height in rows, == Visible for every surface today
	Base    int // buffer-relative coordinate of absolute row zero
}

// WindowScrollbarFor reads the scrollbar off a drawn layout. totalItems is the
// host's history summary — how much scrollback exists, loaded or not; the bar
// never claims less than the drawn window can show.
func WindowScrollbarFor(layout Viewport, totalItems int) WindowScrollbar {
	return WindowScrollbar{
		Total:   max(totalItems, layout.EffectiveCount),
		Offset:  layout.AbsoluteStart,
		Visible: layout.DisplayHeight,
		Track:   layout.DisplayHeight,
		Base:    layout.AbsoluteStart - layout.Start,
	}
}

// HasThumb reports whether the drawn window overflows far enough to draw a
// thumb. A window that fits registers no regions: the reserved column is an
// anti-jitter spacer, not a control.
func (s WindowScrollbar) HasThumb() bool {
	return scroll.ThumbLocFor(s.Total, s.Offset, s.Visible, s.Track).Has
}

// RowForStart is the track row where the thumb top sits for a window start,
// counted in buffer-relative rows like the freeze pins.
func (s WindowScrollbar) RowForStart(start int) int {
	return scroll.RowForOffset(s.Total, s.Visible, s.Track, s.Base+start)
}

// StartAtTrackRow maps a track row to the buffer-relative window start whose
// thumb anchors there — a track press's jump-to-spot, or a drag motion's target.
// The floor keeps an offset above the buffer base from placing a negative start;
// the ceiling is the drawn bound, applied by FitViewport on placement.
func (s WindowScrollbar) StartAtTrackRow(row int) int {
	offset := scroll.OffsetAtRow(s.Total, s.Visible, s.Track, row)
	return max(offset-s.Base, 0)
}
