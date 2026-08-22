package scroll

import "testing"

// legacyThumbMath is the pre-shared arithmetic every scrollbar surface drew
// with before ThumbLocFor; ThumbLocFor must reproduce it exactly.
func legacyThumbMath(totalItems, scrollOffset, visibleItems, trackHeight int) (size, pos int, has bool) {
	if trackHeight < 1 || totalItems <= visibleItems {
		return 0, 0, false
	}
	size = (visibleItems * trackHeight) / totalItems
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
	pos = (scrollOffset * (trackHeight - size)) / maxOffset
	if pos < 0 {
		pos = 0
	}
	if pos > trackHeight-size {
		pos = trackHeight - size
	}
	return size, pos, true
}

func TestThumbLocForMatchesLegacyMath(t *testing.T) {
	for total := 0; total <= 60; total++ {
		for visible := 0; visible <= 30; visible++ {
			for track := 0; track <= 24; track++ {
				wantSize, wantPos, wantHas := legacyThumbMath(total, 0, visible, track)
				got := ThumbLocFor(total, 0, visible, track)
				if got.Size != wantSize || got.Pos != wantPos || got.Has != wantHas {
					t.Fatalf("ThumbLocFor(%d,%d,%d,%d) = %+v, want (%d,%d,%v)",
						total, 0, visible, track, got, wantSize, wantPos, wantHas)
				}
			}
		}
	}
}

func TestThumbLocForTracksOffset(t *testing.T) {
	loc := ThumbLocFor(100, 0, 10, 10)
	if !loc.Has || loc.Pos != 0 || loc.Size != 1 {
		t.Fatalf("offset 0 = %+v, want thumb at top", loc)
	}
	mid := ThumbLocFor(100, 45, 10, 10)
	if mid.Pos != 4 { // 45*9/90
		t.Fatalf("offset 45 pos = %d, want 4", mid.Pos)
	}
	bottom := ThumbLocFor(100, 90, 10, 10)
	if bottom.Pos != 9 { // clamped to track-size
		t.Fatalf("offset 90 pos = %d, want 9", bottom.Pos)
	}
	negative := ThumbLocFor(100, -5, 10, 10)
	if negative.Pos != 0 {
		t.Fatalf("negative offset pos = %d, want 0", negative.Pos)
	}
}

func TestThumbLocForMinSize(t *testing.T) {
	loc := ThumbLocFor(10000, 5000, 1, 10)
	if !loc.Has || loc.Size != 1 {
		t.Fatalf("min-size thumb = %+v, want Has with Size 1", loc)
	}
}

func TestThumbLocForNoThumb(t *testing.T) {
	for _, tc := range []struct{ total, visible int }{
		{5, 10}, {10, 10}, {0, 0}, {0, 5},
	} {
		if loc := ThumbLocFor(tc.total, 3, tc.visible, 8); loc.Has {
			t.Errorf("total=%d visible=%d: reported thumb, want none", tc.total, tc.visible)
		}
	}
	if loc := ThumbLocFor(100, 0, 10, 0); loc.Has {
		t.Error("zero-height track: reported thumb, want none")
	}
}

func TestOffsetAtRowMonotonicAndClamped(t *testing.T) {
	for _, tc := range []struct{ total, visible, track int }{
		{100, 10, 10}, {10000, 1, 40}, {21, 20, 3}, {50, 7, 13}, {11, 10, 1},
	} {
		prev := -1
		for row := -5; row <= tc.track+5; row++ {
			got := OffsetAtRow(tc.total, tc.visible, tc.track, row)
			if got < 0 || got > tc.total-tc.visible {
				t.Fatalf("total=%d visible=%d track=%d: OffsetAtRow(%d) = %d, out of range",
					tc.total, tc.visible, tc.track, row, got)
			}
			if got < prev {
				t.Fatalf("total=%d visible=%d track=%d: OffsetAtRow not monotonic at row %d (%d < %d)",
					tc.total, tc.visible, tc.track, row, got, prev)
			}
			prev = got
		}
	}
}

func TestOffsetAtRowEndpoints(t *testing.T) {
	params := struct{ total, visible, track int }{100, 10, 91}
	if got := OffsetAtRow(params.total, params.visible, params.track, -1); got != 0 {
		t.Errorf("row below track = %d, want 0", got)
	}
	if got := OffsetAtRow(params.total, params.visible, params.track, 0); got != 0 {
		t.Errorf("top row = %d, want 0", got)
	}
	if got := OffsetAtRow(params.total, params.visible, params.track, params.track-1); got != 90 {
		t.Errorf("bottom row = %d, want max offset 90", got)
	}
	if got := OffsetAtRow(params.total, params.visible, params.track, params.track+10); got != 90 {
		t.Errorf("row past track = %d, want clamped to 90", got)
	}
}

func TestOffsetAtRowNoThumbReturnsZero(t *testing.T) {
	if got := OffsetAtRow(5, 10, 8, 4); got != 0 {
		t.Errorf("fits-without-thumb: OffsetAtRow = %d, want 0", got)
	}
	if got := OffsetAtRow(100, 10, 0, 4); got != 0 {
		t.Errorf("no track: OffsetAtRow = %d, want 0", got)
	}
}

func TestRoundTripStability(t *testing.T) {
	for _, tc := range []struct{ total, visible, track int }{
		{100, 10, 10}, {10000, 1, 40}, {50, 7, 13}, {120, 40, 25}, {11, 10, 1},
		{8, 6, 15}, // size 11, travel 4 > maxOffset 2: collapsed anchoring
	} {
		maxOffset := tc.total - tc.visible
		travel := tc.track - ThumbLocFor(tc.total, 0, tc.visible, tc.track).Size
		if travel < 1 {
			continue
		}
		band := maxOffset/travel + 1 // widest run of offsets sharing one row
		for offset := 0; offset <= maxOffset; offset++ {
			row := RowForOffset(tc.total, tc.visible, tc.track, offset)
			back := OffsetAtRow(tc.total, tc.visible, tc.track, row)
			drift := offset - back
			if drift < 0 || drift > band {
				t.Fatalf("total=%d visible=%d track=%d: round trip %d -> row %d -> %d drifts by %d",
					tc.total, tc.visible, tc.track, offset, row, back, drift)
			}
		}
		// Anchoring: rendering the offset a row maps to must place the thumb
		// back on that row without ever snapping above it. Exact re-anchor
		// holds while travel <= maxOffset; in the collapsed regime the
		// documented guarantee weakens to at-or-below, with monotonicity,
		// clamping, and endpoints still holding.
		exactAnchor := travel <= maxOffset
		prev := -1
		for row := 0; row < travel; row++ {
			back := OffsetAtRow(tc.total, tc.visible, tc.track, row)
			if back < prev {
				t.Fatalf("total=%d visible=%d track=%d: OffsetAtRow not monotonic at row %d (%d < %d)",
					tc.total, tc.visible, tc.track, row, back, prev)
			}
			if back < 0 || back > maxOffset {
				t.Fatalf("total=%d visible=%d track=%d: OffsetAtRow(%d) = %d, out of [0,%d]",
					tc.total, tc.visible, tc.track, row, back, maxOffset)
			}
			reanchored := RowForOffset(tc.total, tc.visible, tc.track, back)
			if exactAnchor && reanchored != row {
				t.Fatalf("total=%d visible=%d track=%d: row %d maps to offset %d which renders at row %d",
					tc.total, tc.visible, tc.track, row, back, reanchored)
			}
			if !exactAnchor && reanchored < row {
				t.Fatalf("total=%d visible=%d track=%d: collapsed regime snapped above: row %d -> offset %d -> row %d",
					tc.total, tc.visible, tc.track, row, back, reanchored)
			}
			prev = back
		}
		if got := OffsetAtRow(tc.total, tc.visible, tc.track, 0); got != 0 {
			t.Errorf("total=%d visible=%d track=%d: top row = %d, want 0",
				tc.total, tc.visible, tc.track, got)
		}
		if got := OffsetAtRow(tc.total, tc.visible, tc.track, travel); got != maxOffset {
			t.Errorf("total=%d visible=%d track=%d: bottom row = %d, want %d",
				tc.total, tc.visible, tc.track, got, maxOffset)
		}
	}
}

func TestRoundTripExactWhenTrackResolvesEveryOffset(t *testing.T) {
	// With TrackHeight == TotalItems the thumb is VisibleItems tall and
	// travel equals maxOffset, so one row per offset makes the pair exact
	// inverses.
	total, visible, track := 100, 10, 100
	for offset := 0; offset <= total-visible; offset++ {
		row := RowForOffset(total, visible, track, offset)
		if got := OffsetAtRow(total, visible, track, row); got != offset {
			t.Fatalf("offset %d: exact inverse broken, got %d", offset, got)
		}
	}
}

func TestRowForOffsetMatchesRendererPlacement(t *testing.T) {
	for _, tc := range []struct{ total, visible, track int }{
		{100, 10, 10}, {10000, 1, 40}, {50, 7, 13},
	} {
		for offset := -3; offset <= tc.total; offset++ {
			row := RowForOffset(tc.total, tc.visible, tc.track, offset)
			clamped := min(max(offset, 0), tc.total-tc.visible)
			want := ThumbLocFor(tc.total, clamped, tc.visible, tc.track).Pos
			if row != want {
				t.Fatalf("total=%d visible=%d track=%d: RowForOffset(%d) = %d, want renderer pos %d",
					tc.total, tc.visible, tc.track, offset, row, want)
			}
			if row < 0 || row >= tc.track {
				t.Fatalf("RowForOffset(%d) = %d, outside track of %d rows", offset, row, tc.track)
			}
		}
	}
}

func TestRowForOffsetNoThumbReturnsZero(t *testing.T) {
	if got := RowForOffset(5, 10, 8, 3); got != 0 {
		t.Errorf("fits-without-thumb: RowForOffset = %d, want 0", got)
	}
}
