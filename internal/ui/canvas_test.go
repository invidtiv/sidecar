package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
)

func TestCanvasClipsAndPadsEveryBlockToItsBox(t *testing.T) {
	c := NewCanvas(10, 3)
	c.Blit(mouse.Rect{X: 0, Y: 0, W: 4, H: 3}, "ab\ncdefgh")
	c.Blit(mouse.Rect{X: 4, Y: 0, W: 1, H: 3}, "|\n|\n|")
	c.Blit(mouse.Rect{X: 5, Y: 1, W: 5, H: 1}, "right")

	want := strings.Join([]string{
		"ab  |     ",
		"cdef|right",
		"    |     ",
	}, "\n")
	if got := c.String(); got != want {
		t.Fatalf("canvas =\n%q\nwant\n%q", got, want)
	}
}

func TestCanvasKeepsStyledContentByteIdenticalWhenItFillsItsBox(t *testing.T) {
	styled := "\x1b[38;5;99mexact\x1b[m"
	c := NewCanvas(5, 1)
	c.Blit(mouse.Rect{X: 0, Y: 0, W: 5, H: 1}, styled)
	if got := c.String(); got != styled {
		t.Fatalf("canvas rewrote a block that already fit its box: %q, want %q", got, styled)
	}
}

func TestCanvasMeasuresCellsNotBytes(t *testing.T) {
	c := NewCanvas(6, 1)
	c.Blit(mouse.Rect{X: 0, Y: 0, W: 3, H: 1}, "\x1b[31mab\x1b[m")
	c.Blit(mouse.Rect{X: 3, Y: 0, W: 3, H: 1}, "\x1b[32mcd\x1b[m")
	row := c.String()
	if cells := ansi.StringWidth(row); cells != 6 {
		t.Fatalf("row width = %d cells, want 6: %q", cells, row)
	}
	if got := ansi.Strip(row); got != "ab cd " {
		t.Fatalf("stripped row = %q, want %q", got, "ab cd ")
	}
}

// Isolating cells is not isolating styling: a block clipped mid-attribute, or
// one that never closed the attribute it opened, would paint the divider and
// every block to its right on that row.
func TestCanvasClosesAStyleABlockLeftOpen(t *testing.T) {
	for name, block := range map[string]string{
		"clipped mid-style": "\x1b[41mlong red run",
		"unclosed style":    "\x1b[41mred",
		"short and open":    "\x1b[41mr",
		"extended colour":   "\x1b[38;5;0mzero",
	} {
		t.Run(name, func(t *testing.T) {
			c := NewCanvas(9, 1)
			c.Blit(mouse.Rect{X: 0, Y: 0, W: 4, H: 1}, block)
			c.Blit(mouse.Rect{X: 4, Y: 0, W: 1, H: 1}, "|")
			c.Blit(mouse.Rect{X: 5, Y: 0, W: 4, H: 1}, "tail")

			row := c.String()
			divider := strings.Index(row, "|")
			if divider < 0 {
				t.Fatalf("row lost its divider: %q", row)
			}
			if !strings.Contains(row[:divider], ResetSequence) {
				t.Fatalf("row carries an open style past the block that opened it: %q", row)
			}
			if cells := ansi.StringWidth(row); cells != 9 {
				t.Fatalf("row width = %d cells, want 9: %q", cells, row)
			}
		})
	}
}

// A block that closed its own styling is written through untouched: a reset
// after every span would rewrite rows that were already correct.
func TestCanvasLeavesAClosedStyleAlone(t *testing.T) {
	c := NewCanvas(8, 1)
	c.Blit(mouse.Rect{X: 0, Y: 0, W: 4, H: 1}, "\x1b[41mred\x1b[0m ")
	c.Blit(mouse.Rect{X: 4, Y: 0, W: 4, H: 1}, "tail")
	if got, want := c.String(), "\x1b[41mred\x1b[0m tail"; got != want {
		t.Fatalf("canvas = %q, want %q", got, want)
	}
}

func TestCanvasIgnoresBlitsOutsideItsBounds(t *testing.T) {
	c := NewCanvas(4, 2)
	c.Blit(mouse.Rect{X: 4, Y: 0, W: 2, H: 2}, "xx\nxx")
	c.Blit(mouse.Rect{X: 0, Y: 2, W: 2, H: 1}, "yy")
	c.Blit(mouse.Rect{X: 2, Y: 1, W: 4, H: 4}, "zzzz\nzzzz\nzzzz")
	if got, want := c.String(), "    \n  zz"; got != want {
		t.Fatalf("canvas = %q, want %q", got, want)
	}
}
