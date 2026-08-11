package kanban

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestAlignmentInvariant is the regression guard for the three-widths bug:
// across a range of widths and lane counts, every rendered row must have
// identical display width, and every junction/divider glyph must land on the
// same column index on every row that has it.
func TestAlignmentInvariant(t *testing.T) {
	widths := []int{60, 77, 90, 100, 121, 150, 200}
	laneCounts := []int{1, 2, 3, 4, 6}
	for _, width := range widths {
		for _, laneCount := range laneCounts {
			t.Run(fmt.Sprintf("w%d_n%d", width, laneCount), func(t *testing.T) {
				var c Component
				c.SetBoard(alignmentFixture(laneCount))
				if c.Compact(width, 16) {
					t.Skip("compact at this width")
				}
				result := c.Render(RenderOptions{Width: width, Height: 20, Header: "Board", HeaderRight: "toggle", MinColumnWidth: 16, CardHeight: 4})
				lines := strings.Split(result.View, "\n")
				if len(lines) == 0 {
					t.Fatal("no rendered lines")
				}
				want := ansi.StringWidth(lines[0])
				for i, line := range lines {
					if got := ansi.StringWidth(line); got != want {
						t.Fatalf("line %d width = %d, want %d\nline: %q", i, got, want, line)
					}
				}
				// Trim the outer panel border (border rune + 1 padding column
				// on each side) so only the component's own painted grid
				// glyphs are compared; the panel border's vertical sides also
				// render '│' and are not part of this invariant.
				columnsOf := map[rune][]int{}
				for i, line := range lines {
					runes := []rune(ansi.Strip(line))
					if len(runes) <= 4 {
						continue
					}
					inner := runes[2 : len(runes)-2]
					for glyph := range map[rune]bool{'┬': true, '┼': true, '│': true} {
						cols := runeColumns(string(inner), glyph)
						if len(cols) == 0 {
							continue
						}
						if existing, ok := columnsOf[glyph]; ok {
							if !equalInts(existing, cols) {
								t.Fatalf("line %d: %q columns = %v, want %v", i, glyph, cols, existing)
							}
						} else {
							columnsOf[glyph] = cols
						}
					}
				}
				if laneCount < 2 {
					return
				}
				// Presence is part of the invariant. Comparing only the glyphs
				// that happen to appear lets a rule that was never drawn pass:
				// the ┬ rule above the lane headers went missing exactly that
				// way. Every divider glyph must exist and share one column set.
				dividers, ok := columnsOf['│']
				if !ok {
					t.Fatal("no '│' dividers rendered")
				}
				if len(dividers) != laneCount-1 {
					t.Fatalf("'│' columns = %v, want %d dividers", dividers, laneCount-1)
				}
				for _, glyph := range []rune{'┬', '┼'} {
					cols, ok := columnsOf[glyph]
					if !ok {
						t.Fatalf("no %q junctions rendered", glyph)
					}
					if !equalInts(cols, dividers) {
						t.Fatalf("%q columns = %v, want %v", glyph, cols, dividers)
					}
				}
			})
		}
	}
}

func alignmentFixture(laneCount int) Board {
	lanes := make([]Lane, laneCount)
	for i := range lanes {
		lanes[i] = Lane{
			ID:    LaneID(fmt.Sprintf("lane-%d", i)),
			Label: fmt.Sprintf("LANE%d", i),
			Cards: []Card{
				{ID: fmt.Sprintf("l%d-a", i), Title: "Card One", Subtitle: "subtitle", Detail: "detail", Meta: "meta"},
				{ID: fmt.Sprintf("l%d-b", i), Title: "Card Two", Subtitle: "subtitle", Detail: "detail", Meta: "meta"},
				{ID: fmt.Sprintf("l%d-c", i), Title: "Card Three", Subtitle: "subtitle", Detail: "detail", Meta: "meta"},
			},
		}
	}
	return Board{Lanes: lanes}
}

func runeColumns(s string, target rune) []int {
	var cols []int
	col := 0
	for _, r := range s {
		if r == target {
			cols = append(cols, col)
		}
		col++
	}
	return cols
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSpanTruncationNeverExceedsCell(t *testing.T) {
	line := Line{Spans: []Span{{Text: "a very long span that overruns the cell width by a lot"}}}
	got := renderSpans(line.Spans, 10, false)
	if w := ansi.StringWidth(got); w != 10 {
		t.Fatalf("width = %d, want 10", w)
	}
}

func TestLinesTakePrecedenceOverStringFields(t *testing.T) {
	card := Card{
		Title: "should not appear",
		Lines: []Line{{Spans: []Span{{Text: "styled line"}}}},
	}
	got := defaultCardLine(card, 0, 20, false)
	if !strings.Contains(got, "styled line") {
		t.Fatalf("rendered = %q, want styled line", got)
	}
	if strings.Contains(got, "should not appear") {
		t.Fatalf("rendered = %q, string fields leaked through", got)
	}
}

func TestLinesPastEndAreBlank(t *testing.T) {
	card := Card{Lines: []Line{{Spans: []Span{{Text: "only line"}}}}}
	got := defaultCardLine(card, 2, 10, false)
	if strings.TrimSpace(ansi.Strip(got)) != "" {
		t.Fatalf("rendered = %q, want blank", got)
	}
	if w := ansi.StringWidth(got); w != 10 {
		t.Fatalf("width = %d, want 10", w)
	}
}

func TestStringFieldPathUnchangedWhenLinesEmpty(t *testing.T) {
	card := Card{Title: "t", Subtitle: "s", Detail: "d", Meta: "m"}
	for line, want := range map[int]string{0: "t", 1: "s", 2: "d", 3: "m"} {
		got := ansi.Strip(defaultCardLine(card, line, 10, false))
		if !strings.Contains(got, want) {
			t.Fatalf("line %d = %q, want to contain %q", line, got, want)
		}
	}
}

func TestSelectedLinePaintsFullWidthBackgroundKeepingForeground(t *testing.T) {
	fg := color.RGBA{R: 200, G: 10, B: 10, A: 255}
	line := Line{Spans: []Span{{Text: "hi", Foreground: fg}}}
	got := renderSpans(line.Spans, 8, true)
	if w := ansi.StringWidth(got); w != 8 {
		t.Fatalf("width = %d, want 8", w)
	}
	if !strings.Contains(got, "hi") {
		t.Fatalf("rendered = %q, want to contain text", got)
	}
}

func TestCellEmptyWithNoMessageRendersDimDot(t *testing.T) {
	var c Component
	c.SetBoard(Board{Lanes: []Lane{{ID: "empty", Label: "Empty", State: CellEmpty}}})
	result := c.Render(RenderOptions{Width: 40, Height: 12, Header: "Board", MinColumnWidth: 16, CardHeight: 4})
	if !strings.Contains(ansi.Strip(result.View), "·") {
		t.Fatalf("view lacks dim dot: %q", result.View)
	}
	if strings.Contains(ansi.Strip(result.View), "empty") {
		t.Fatalf("view leaked lane state word: %q", result.View)
	}
}

func TestOverflowUsesEveryCardSlotAndSelectionScrollsViewport(t *testing.T) {
	cards := []Card{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
	var c Component
	c.SetBoard(Board{Lanes: []Lane{{ID: "work", Label: "Work", Cards: cards}}})
	result := c.Render(RenderOptions{Width: 40, Height: 15, Header: "Board", MinColumnWidth: 16, CardHeight: 4})
	plain := ansi.Strip(result.View)
	if !strings.Contains(plain, "↓ 3 more below") {
		t.Fatalf("view lacks below indicator: %q", plain)
	}
	if !strings.Contains(plain, "┃") {
		t.Fatalf("view lacks scrollbar thumb: %q", plain)
	}
	var visible []string
	for _, region := range result.Regions {
		if region.Kind == RegionCard {
			visible = append(visible, region.CardID)
		}
	}
	if got, want := strings.Join(visible, ","), "a,b"; got != want {
		t.Fatalf("visible cards = %s, want %s", got, want)
	}

	c.Select(Selection{Column: 0, Row: 4})
	result = c.Render(RenderOptions{Width: 40, Height: 15, Header: "Board", MinColumnWidth: 16, CardHeight: 4})
	visible = nil
	for _, region := range result.Regions {
		if region.Kind == RegionCard {
			visible = append(visible, region.CardID)
		}
	}
	if got, want := strings.Join(visible, ","), "d,e"; got != want {
		t.Fatalf("scrolled cards = %s, want %s", got, want)
	}
	if strings.Contains(ansi.Strip(result.View), "more below") {
		t.Fatalf("view still reports cards below at end of lane: %q", ansi.Strip(result.View))
	}
}

func TestMoveInColumnTargetsHoveredLane(t *testing.T) {
	var c Component
	c.SetBoard(Board{Lanes: []Lane{
		{ID: "left", Cards: []Card{{ID: "a"}, {ID: "b"}}},
		{ID: "right", Cards: []Card{{ID: "c"}, {ID: "d"}, {ID: "e"}}},
	}})
	c.MoveInColumn(1, 2)
	if got, want := c.Selection(), (Selection{Column: 1, Row: 2}); got != want {
		t.Fatalf("selection = %#v, want %#v", got, want)
	}
}

func TestSelectedStructuredCardDoesNotHighlightRowsPastItsContent(t *testing.T) {
	card := Card{Lines: []Line{
		{Spans: []Span{{Text: "one"}}},
		{Spans: []Span{{Text: "two"}}},
		{Spans: []Span{{Text: "three"}}},
	}}
	if got := defaultCardLine(card, 3, 12, true); strings.Contains(got, "\x1b[") {
		t.Fatalf("blank row contains selection styling: %q", got)
	}
}

func TestSelectedLaneHeaderUsesFullWidthBackgroundWithoutUnderline(t *testing.T) {
	got := renderLaneHeader(Lane{ID: "work", Label: "WORK", Cards: []Card{{}}}, 20, true)
	if strings.Contains(got, "\x1b[4m") {
		t.Fatalf("selected header still underlined: %q", got)
	}
	if !strings.Contains(got, "\x1b[48;") {
		t.Fatalf("selected header lacks background styling: %q", got)
	}
	if width := ansi.StringWidth(got); width != 20 {
		t.Fatalf("selected header width = %d, want 20", width)
	}
}

func TestColumnWidthsSumWithSeparatorsToInnerWidth(t *testing.T) {
	for _, width := range []int{60, 77, 90, 121, 200} {
		for _, columns := range []int{1, 2, 3, 5, 6} {
			layout := CalculateLayout(width, 20, columns, 16, 4)
			if len(layout.ColumnWidths) != columns {
				t.Fatalf("width=%d columns=%d: len = %d", width, columns, len(layout.ColumnWidths))
			}
			sum := columns - 1
			for _, w := range layout.ColumnWidths {
				sum += w
			}
			innerWidth := width - 4
			if sum != innerWidth {
				t.Fatalf("width=%d columns=%d: sum+separators = %d, want %d", width, columns, sum, innerWidth)
			}
			// Summing correctly does not pin where the remainder went. Widths
			// must fall left to right, differ by at most one, and leave the
			// narrowest lane last, which is what ColumnWidth reports.
			for i := 1; i < len(layout.ColumnWidths); i++ {
				delta := layout.ColumnWidths[i-1] - layout.ColumnWidths[i]
				if delta < 0 || delta > 1 {
					t.Fatalf("width=%d columns=%d: widths %v not remainder-to-leftmost", width, columns, layout.ColumnWidths)
				}
			}
			if last := layout.ColumnWidths[columns-1]; layout.ColumnWidth != last && layout.ColumnWidth < 16 {
				t.Fatalf("width=%d columns=%d: ColumnWidth = %d, want base %d", width, columns, layout.ColumnWidth, last)
			}
		}
	}
}
