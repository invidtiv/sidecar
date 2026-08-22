package ui

import (
	"image"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/mattn/go-runewidth"
)

func TestRenderScrollbar_SpacerWhenAllVisible(t *testing.T) {
	result := RenderScrollbar(ScrollbarParams{
		TotalItems:   5,
		ScrollOffset: 0,
		VisibleItems: 10,
		TrackHeight:  5,
	})
	lines := strings.Split(result, "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}
	for i, line := range lines {
		if line != " " {
			t.Errorf("line %d: expected single space, got %q", i, line)
		}
	}
}

func TestRenderScrollbar_SpacerWhenEqual(t *testing.T) {
	result := RenderScrollbar(ScrollbarParams{
		TotalItems:   10,
		ScrollOffset: 0,
		VisibleItems: 10,
		TrackHeight:  5,
	})
	lines := strings.Split(result, "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}
	for i, line := range lines {
		if line != " " {
			t.Errorf("line %d: expected single space, got %q", i, line)
		}
	}
}

func TestRenderScrollbar_ThumbAtTop(t *testing.T) {
	result := RenderScrollbar(ScrollbarParams{
		TotalItems:   100,
		ScrollOffset: 0,
		VisibleItems: 10,
		TrackHeight:  10,
	})
	lines := strings.Split(result, "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines, got %d", len(lines))
	}
	// Thumb should be at position 0 (top). Thumb size = (10*10)/100 = 1.
	// First line should contain the thumb char (┃), rest should be track (│).
	if !strings.Contains(lines[0], "┃") {
		t.Errorf("expected thumb at line 0, got %q", lines[0])
	}
	for i := 1; i < 10; i++ {
		if !strings.Contains(lines[i], "│") {
			t.Errorf("expected track at line %d, got %q", i, lines[i])
		}
	}
}

func TestRenderScrollbar_ThumbAtBottom(t *testing.T) {
	result := RenderScrollbar(ScrollbarParams{
		TotalItems:   100,
		ScrollOffset: 90, // TotalItems - VisibleItems
		VisibleItems: 10,
		TrackHeight:  10,
	})
	lines := strings.Split(result, "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines, got %d", len(lines))
	}
	// Thumb should be at last line.
	if !strings.Contains(lines[9], "┃") {
		t.Errorf("expected thumb at last line, got %q", lines[9])
	}
	for i := 0; i < 9; i++ {
		if !strings.Contains(lines[i], "│") {
			t.Errorf("expected track at line %d, got %q", i, lines[i])
		}
	}
}

func TestRenderScrollbar_ThumbAtMiddle(t *testing.T) {
	result := RenderScrollbar(ScrollbarParams{
		TotalItems:   100,
		ScrollOffset: 45,
		VisibleItems: 10,
		TrackHeight:  10,
	})
	lines := strings.Split(result, "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines, got %d", len(lines))
	}
	// thumbSize = 1, thumbPos = 45 * 9 / 90 = 4 (middle-ish)
	thumbFound := -1
	for i, line := range lines {
		if strings.Contains(line, "┃") {
			thumbFound = i
			break
		}
	}
	if thumbFound < 1 || thumbFound > 8 {
		t.Errorf("expected thumb in middle range, found at %d", thumbFound)
	}
}

func TestRenderScrollbar_MinThumbSize(t *testing.T) {
	result := RenderScrollbar(ScrollbarParams{
		TotalItems:   10000,
		ScrollOffset: 0,
		VisibleItems: 1,
		TrackHeight:  10,
	})
	lines := strings.Split(result, "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines, got %d", len(lines))
	}
	// With 10000 items and 1 visible, thumb would be tiny but min is 1.
	thumbCount := 0
	for _, line := range lines {
		if strings.Contains(line, "┃") {
			thumbCount++
		}
	}
	if thumbCount < 1 {
		t.Error("expected at least 1 thumb line")
	}
}

func TestRenderScrollbar_ExactLineCount(t *testing.T) {
	for _, h := range []int{1, 5, 20, 50} {
		result := RenderScrollbar(ScrollbarParams{
			TotalItems:   100,
			ScrollOffset: 0,
			VisibleItems: 10,
			TrackHeight:  h,
		})
		lines := strings.Split(result, "\n")
		if len(lines) != h {
			t.Errorf("TrackHeight=%d: expected %d lines, got %d", h, h, len(lines))
		}
	}
}

func TestRenderScrollbar_SingleColumnWide(t *testing.T) {
	result := RenderScrollbar(ScrollbarParams{
		TotalItems:   100,
		ScrollOffset: 0,
		VisibleItems: 10,
		TrackHeight:  10,
	})
	lines := strings.Split(result, "\n")
	for i, line := range lines {
		w := runewidth.StringWidth(stripAnsi(line))
		if w != 1 {
			t.Errorf("line %d: expected width 1, got %d (line=%q)", i, w, line)
		}
	}
}

func TestRenderScrollbar_ZeroTrackHeight(t *testing.T) {
	result := RenderScrollbar(ScrollbarParams{
		TotalItems:   100,
		ScrollOffset: 0,
		VisibleItems: 10,
		TrackHeight:  0,
	})
	if result != "" {
		t.Errorf("expected empty string for TrackHeight=0, got %q", result)
	}
}

func TestRenderScrollbar_NegativeTrackHeight(t *testing.T) {
	result := RenderScrollbar(ScrollbarParams{
		TotalItems:   100,
		ScrollOffset: 0,
		VisibleItems: 10,
		TrackHeight:  -5,
	})
	if result != "" {
		t.Errorf("expected empty string for negative TrackHeight, got %q", result)
	}
}

// renderScrollbarLegacy is a verbatim copy of the renderer before geometry
// reporting was added. Every public entry point must stay byte-identical
// to it.
func renderScrollbarLegacy(params ScrollbarParams) string {
	if params.TrackHeight < 1 {
		return ""
	}

	if params.TotalItems <= params.VisibleItems {
		lines := make([]string, params.TrackHeight)
		for i := range lines {
			lines[i] = " "
		}
		return strings.Join(lines, "\n")
	}

	thumbSize := (params.VisibleItems * params.TrackHeight) / params.TotalItems
	if thumbSize < 1 {
		thumbSize = 1
	}
	if thumbSize > params.TrackHeight {
		thumbSize = params.TrackHeight
	}

	maxOffset := params.TotalItems - params.VisibleItems
	if maxOffset < 1 {
		maxOffset = 1
	}
	thumbPos := (params.ScrollOffset * (params.TrackHeight - thumbSize)) / maxOffset
	if thumbPos < 0 {
		thumbPos = 0
	}
	if thumbPos > params.TrackHeight-thumbSize {
		thumbPos = params.TrackHeight - thumbSize
	}

	trackStyle := lipgloss.NewStyle().Foreground(styles.ScrollbarTrackColor)
	thumbStyle := lipgloss.NewStyle().Foreground(styles.ScrollbarThumbColor)

	trackChar := trackStyle.Render("│")
	thumbChar := thumbStyle.Render("┃")

	lines := make([]string, params.TrackHeight)
	for i := range params.TrackHeight {
		if i >= thumbPos && i < thumbPos+thumbSize {
			lines[i] = thumbChar
		} else {
			lines[i] = trackChar
		}
	}

	return strings.Join(lines, "\n")
}

func TestRenderScrollbarWithGeometry_ByteParityWithLegacy(t *testing.T) {
	params := []ScrollbarParams{
		{TotalItems: 2, ScrollOffset: 0, VisibleItems: 1, TrackHeight: 1},
		{TotalItems: 2, ScrollOffset: 1, VisibleItems: 1, TrackHeight: 2},
		{TotalItems: 3, ScrollOffset: 1, VisibleItems: 2, TrackHeight: 3},
		{TotalItems: 100, ScrollOffset: 0, VisibleItems: 10, TrackHeight: 10},
		{TotalItems: 100, ScrollOffset: 45, VisibleItems: 10, TrackHeight: 10},
		{TotalItems: 100, ScrollOffset: 90, VisibleItems: 10, TrackHeight: 10},
		{TotalItems: 101, ScrollOffset: 91, VisibleItems: 10, TrackHeight: 7},
		{TotalItems: 11, ScrollOffset: 5, VisibleItems: 10, TrackHeight: 4},
		{TotalItems: 10000, ScrollOffset: 9999, VisibleItems: 1, TrackHeight: 40},
		{TotalItems: 10000, ScrollOffset: 5000, VisibleItems: 1, TrackHeight: 40},
		{TotalItems: 50, ScrollOffset: 25, VisibleItems: 25, TrackHeight: 50},
		{TotalItems: 10, ScrollOffset: 0, VisibleItems: 10, TrackHeight: 5},
		{TotalItems: 5, ScrollOffset: 99, VisibleItems: 10, TrackHeight: 8},
		{TotalItems: 10, ScrollOffset: -3, VisibleItems: 5, TrackHeight: 6},
		{TotalItems: 100, ScrollOffset: 9999, VisibleItems: 10, TrackHeight: 12},
		{TotalItems: 100, ScrollOffset: -9999, VisibleItems: 10, TrackHeight: 12},
		{TotalItems: 0, ScrollOffset: 0, VisibleItems: 0, TrackHeight: 4},
		{TotalItems: 100, ScrollOffset: 0, VisibleItems: 10, TrackHeight: 0},
		{TotalItems: 100, ScrollOffset: 0, VisibleItems: 10, TrackHeight: -5},
	}
	for i, p := range params {
		want := renderScrollbarLegacy(p)
		got, _ := RenderScrollbarWithGeometry(p)
		if got != want {
			t.Errorf("case %d %+v: output differs from legacy renderer", i, p)
		}
		if RenderScrollbar(p) != want {
			t.Errorf("case %d %+v: RenderScrollbar differs from legacy renderer", i, p)
		}
		idle, _ := RenderScrollbarWithState(p, ScrollbarStyle{})
		if idle != want {
			t.Errorf("case %d %+v: idle state differs from legacy renderer", i, p)
		}
	}
}

func TestGeometry_HasThumbAndRects(t *testing.T) {
	got, geom := RenderScrollbarWithGeometry(ScrollbarParams{
		TotalItems: 100, ScrollOffset: 45, VisibleItems: 10, TrackHeight: 10,
	})
	_ = got
	if !geom.HasThumb {
		t.Fatal("expected HasThumb for scrollable content")
	}
	if geom.TrackRect != (image.Rectangle{Min: image.Pt(0, 0), Max: image.Pt(1, 10)}) {
		t.Errorf("TrackRect = %v, want one column spanning rows 0..9", geom.TrackRect)
	}
	// Thumb size = (10*10)/100 = 1, pos = 45*9/90 = 4.
	wantThumb := image.Rect(0, 4, 1, 5)
	if geom.ThumbRect != wantThumb {
		t.Errorf("ThumbRect = %v, want %v", geom.ThumbRect, wantThumb)
	}
	if !geom.ThumbRect.In(geom.TrackRect) {
		t.Error("thumb escapes its track")
	}
}

func TestGeometry_FitsWithoutThumb(t *testing.T) {
	for _, p := range []ScrollbarParams{
		{TotalItems: 5, ScrollOffset: 0, VisibleItems: 10, TrackHeight: 8},
		{TotalItems: 10, ScrollOffset: 4, VisibleItems: 10, TrackHeight: 3},
		{TotalItems: 100, ScrollOffset: 0, VisibleItems: 10, TrackHeight: 0},
	} {
		_, geom := RenderScrollbarWithGeometry(p)
		if geom.HasThumb {
			t.Errorf("%+v: reported HasThumb, want false", p)
		}
		if geom.TrackRect != (image.Rectangle{}) || geom.ThumbRect != (image.Rectangle{}) {
			t.Errorf("%+v: rects not zero, handlers could misread them: %v", p, geom)
		}
	}
}

func TestGeometry_MinSizeThumb(t *testing.T) {
	_, geom := RenderScrollbarWithGeometry(ScrollbarParams{
		TotalItems: 10000, ScrollOffset: 0, VisibleItems: 1, TrackHeight: 40,
	})
	if !geom.HasThumb {
		t.Fatal("expected HasThumb")
	}
	if h := geom.ThumbRect.Dy(); h != 1 {
		t.Errorf("thumb height = %d, want min-size 1", h)
	}
}

func TestOffsetAtRow_ClampsAndEndpoints(t *testing.T) {
	p := ScrollbarParams{TotalItems: 100, ScrollOffset: 0, VisibleItems: 10, TrackHeight: 10}
	cases := []struct {
		row, want int
	}{
		{-5, 0},
		{0, 0},
		{9, 90}, // bottom row of travel resolves max offset
		{50, 90},
	}
	for _, tc := range cases {
		if got := OffsetAtRow(p, tc.row); got != tc.want {
			t.Errorf("OffsetAtRow(%d) = %d, want %d", tc.row, got, tc.want)
		}
	}
}

func TestOffsetAtRow_MonotonicAcrossTrack(t *testing.T) {
	for _, p := range []ScrollbarParams{
		{TotalItems: 100, VisibleItems: 10, TrackHeight: 10},
		{TotalItems: 10000, VisibleItems: 1, TrackHeight: 40},
		{TotalItems: 21, VisibleItems: 20, TrackHeight: 3},
		{TotalItems: 50, VisibleItems: 7, TrackHeight: 13},
		{TotalItems: 11, VisibleItems: 10, TrackHeight: 1},
	} {
		prev := -1
		for row := -3; row <= p.TrackHeight+3; row++ {
			got := OffsetAtRow(p, row)
			if got < prev {
				t.Fatalf("%+v: OffsetAtRow(%d) = %d below previous %d", p, row, got, prev)
			}
			prev = got
		}
	}
}

func TestOffsetAtRow_NoThumbReturnsZero(t *testing.T) {
	if got := OffsetAtRow(ScrollbarParams{TotalItems: 5, VisibleItems: 10, TrackHeight: 8}, 4); got != 0 {
		t.Errorf("fits-without-thumb: OffsetAtRow = %d, want 0", got)
	}
}

func TestInverseRoundTrip(t *testing.T) {
	for _, p := range []ScrollbarParams{
		{TotalItems: 100, VisibleItems: 10, TrackHeight: 10},
		{TotalItems: 10000, VisibleItems: 1, TrackHeight: 40},
		{TotalItems: 120, VisibleItems: 40, TrackHeight: 25},
	} {
		for offset := 0; offset <= p.TotalItems-p.VisibleItems; offset++ {
			row := RowForOffset(p, offset)
			back := OffsetAtRow(p, row)
			reanchored := RowForOffset(p, back)
			if reanchored != row {
				t.Fatalf("%+v: offset %d -> row %d -> offset %d renders at row %d",
					p, offset, row, back, reanchored)
			}
		}
	}
}

func TestRowForOffset_ClampsAndMatchesGeometry(t *testing.T) {
	p := ScrollbarParams{TotalItems: 100, ScrollOffset: 0, VisibleItems: 10, TrackHeight: 10}
	for _, tc := range []struct{ offset, want int }{
		{-5, 0}, {0, 0}, {45, 4}, {90, 9}, {95, 9},
	} {
		if got := RowForOffset(p, tc.offset); got != tc.want {
			t.Errorf("RowForOffset(%d) = %d, want %d", tc.offset, got, tc.want)
		}
	}
	_, geom := RenderScrollbarWithGeometry(ScrollbarParams{
		TotalItems: 100, ScrollOffset: 45, VisibleItems: 10, TrackHeight: 10,
	})
	if row := RowForOffset(p, 45); row != geom.ThumbRect.Min.Y {
		t.Errorf("RowForOffset(45) = %d, want rendered thumb top %d", row, geom.ThumbRect.Min.Y)
	}
}

func TestRenderScrollbarWithState_HoverAndDragDifferFromIdle(t *testing.T) {
	p := ScrollbarParams{TotalItems: 100, ScrollOffset: 30, VisibleItems: 10, TrackHeight: 12}
	idle, idleGeom := RenderScrollbarWithState(p, ScrollbarStyle{})
	hover, hoverGeom := RenderScrollbarWithState(p, ScrollbarStyle{Thumb: HandleHover})
	drag, dragGeom := RenderScrollbarWithState(p, ScrollbarStyle{Track: HandleDrag, Thumb: HandleDrag})

	if hover == idle {
		t.Error("hover thumb should change rendering")
	}
	if drag == idle || drag == hover {
		t.Error("drag styling should differ from idle and hover")
	}
	for name, g := range map[string]Geometry{"hover": hoverGeom, "drag": dragGeom} {
		if g != idleGeom {
			t.Errorf("%s geometry = %+v, want identical to idle %+v", name, g, idleGeom)
		}
	}
	for name, out := range map[string]string{"hover": hover, "drag": drag} {
		if strings.Count(out, "\n") != strings.Count(idle, "\n") {
			t.Errorf("%s changed line count", name)
		}
		if runewidth.StringWidth(stripAnsi(out)) != runewidth.StringWidth(stripAnsi(idle)) {
			t.Errorf("%s changed column width; anti-jitter contract broken", name)
		}
	}
}

// stripAnsi removes ANSI escape sequences for width measurement.
func stripAnsi(s string) string {
	var out strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
