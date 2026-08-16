package overlay

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// An overlay covers the cells it is placed on and leaves everything around them
// alone, which is what makes it a floating list rather than an insertion.
func TestCompositeCoversWithoutMovingAnything(t *testing.T) {
	base := strings.Join([]string{
		"aaaaaaaaaa",
		"bbbbbbbbbb",
		"cccccccccc",
	}, "\n")
	got := Composite(base, "XX\nYY", 2, 1)
	want := strings.Join([]string{
		"aaaaaaaaaa",
		"bbXXbbbbbb",
		"ccYYcccccc",
	}, "\n")
	if got != want {
		t.Fatalf("Composite = %q, want %q", got, want)
	}
}

// Anything outside the base is clipped: an overlay must never change how many
// lines a caller's pane has.
func TestCompositeClipsToTheBase(t *testing.T) {
	base := "aaaa\nbbbb"
	got := Composite(base, "XX\nYY\nZZ", 3, 1)
	if lines := strings.Count(got, "\n") + 1; lines != 2 {
		t.Fatalf("Composite produced %d lines, want 2", lines)
	}
	if !strings.HasSuffix(got, "bbbXX") {
		t.Fatalf("Composite = %q", got)
	}
}

// A line shorter than the placement is padded, so the overlay still lands where
// it was asked to rather than sliding left.
func TestLinePadsShortLines(t *testing.T) {
	if got := Line("ab", "XY", 4); got != "ab  XY" {
		t.Fatalf("Line = %q, want %q", got, "ab  XY")
	}
	if got := Line("abcdef", "XY", -1); got != "Ybcdef" {
		t.Fatalf("Line with negative x = %q, want %q", got, "Ybcdef")
	}
}

// The point of this package is that slicing is ANSI- and width-aware. A base
// line whose colour run spans the overlay, and a wide-rune overlay straddling
// the right edge, are the two cases that a "simplification" to []rune slicing
// would silently break.
func TestLineIsAnsiAndWidthAware(t *testing.T) {
	const (
		red   = "\x1b[31m"
		reset = "\x1b[0m"
	)
	cases := []struct {
		name      string
		base      string
		over      string
		x         int
		wantWidth int
		wantHas   string
	}{
		{
			name:      "colour run spans the overlay",
			base:      red + "aaaaaaaaaa" + reset,
			over:      "XX",
			x:         4,
			wantWidth: 10,
			wantHas:   "XX",
		},
		{
			name:      "wide runes are measured in cells",
			base:      "..........",
			over:      "日本",
			x:         6,
			wantWidth: 10,
			wantHas:   "日本",
		},
		{
			name:      "a wide overlay straddling the right edge is not widened",
			base:      "........",
			over:      "日本語",
			x:         4,
			wantWidth: 10, // 4 cells of base + 6 cells of overlay, nothing dropped
			wantHas:   "日本語",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Line(tc.base, tc.over, tc.x)
			if width := ansi.StringWidth(got); width != tc.wantWidth {
				t.Fatalf("Line width = %d, want %d (%q)", width, tc.wantWidth, got)
			}
			if !strings.Contains(got, tc.wantHas) {
				t.Fatalf("Line = %q, want it to contain %q", got, tc.wantHas)
			}
			// The overlay starts exactly at x, measured in cells.
			if prefix := ansi.Cut(got, 0, tc.x); ansi.StringWidth(prefix) != tc.x {
				t.Fatalf("the overlay did not start at cell %d: %q", tc.x, got)
			}
		})
	}
}

// A wide rune the overlay only half-covers must not leave the line wider than
// it was: compositing writes over cells, it does not insert them.
func TestCompositeOverWideRunesKeepsTheWidth(t *testing.T) {
	base := "日本語です"                    // ten cells
	got := Composite(base, "ab", 3, 0) // lands mid-rune
	if width := ansi.StringWidth(got); width > 10 {
		t.Fatalf("compositing grew the line to %d cells: %q", width, got)
	}
	if !strings.Contains(got, "ab") {
		t.Fatalf("Composite = %q, want it to contain the overlay", got)
	}
}
