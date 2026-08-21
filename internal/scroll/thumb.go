package scroll

// ThumbLoc is the proportional placement of a vertical scrollbar thumb
// within its track. All scrollbar surfaces share this math so the drawn
// thumb and the hit-tested one can never disagree.
type ThumbLoc struct {
	Size int  // Thumb height in rows; always >= 1 when Has is set.
	Pos  int  // Top row of the thumb, in [0, TrackHeight-Size].
	Has  bool // False when all content fits and no thumb should be drawn.
}

// ThumbLocFor computes proportional thumb placement from list geometry,
// matching the renderer's arithmetic exactly: size is the visible fraction
// of the track with a floor of one row, and position tracks the scroll
// offset across the remaining travel. Has is false when totalItems does not
// exceed visibleItems.
func ThumbLocFor(totalItems, scrollOffset, visibleItems, trackHeight int) ThumbLoc {
	if trackHeight < 1 || totalItems <= visibleItems {
		return ThumbLoc{}
	}

	size := (visibleItems * trackHeight) / totalItems
	if size < 1 {
		size = 1
	}
	if size > trackHeight {
		size = trackHeight
	}

	maxOffset := totalItems - visibleItems
	if maxOffset < 1 {
		maxOffset = 1
	}
	pos := (scrollOffset * (trackHeight - size)) / maxOffset
	if pos < 0 {
		pos = 0
	}
	if pos > trackHeight-size {
		pos = trackHeight - size
	}

	return ThumbLoc{Size: size, Pos: pos, Has: true}
}

// OffsetAtRow maps a track row to the smallest scroll offset whose thumb
// top renders on or below that row, clamped to
// [0, totalItems-visibleItems]. Rows on a track with no thumb (content
// fits) map to 0. The mapping is monotonic in row. Re-rendering the
// returned offset pins the thumb back on the queried row exactly while
// travel (trackHeight minus thumb size) does not exceed maxOffset
// (totalItems minus visibleItems); beyond that ratio rows outnumber
// offsets, and re-rendering lands below the queried row instead, never
// above it.
func OffsetAtRow(totalItems, visibleItems, trackHeight, row int) int {
	loc := ThumbLocFor(totalItems, 0, visibleItems, trackHeight)
	if !loc.Has {
		return 0
	}
	maxOffset := totalItems - visibleItems
	travel := trackHeight - loc.Size
	if travel < 1 {
		// Thumb spans the whole track; the renderer pins it at the top.
		return 0
	}
	row = min(max(row, 0), travel)
	offset := (row*maxOffset + travel - 1) / travel
	return min(max(offset, 0), maxOffset)
}

// RowForOffset maps a scroll offset to the track row where the renderer
// places the thumb top, clamped to [0, trackHeight-1]. Tracks with no thumb
// map every offset to 0.
func RowForOffset(totalItems, visibleItems, trackHeight, offset int) int {
	maxOffset := totalItems - visibleItems
	offset = min(max(offset, 0), max(0, maxOffset))
	loc := ThumbLocFor(totalItems, offset, visibleItems, trackHeight)
	return loc.Pos
}
