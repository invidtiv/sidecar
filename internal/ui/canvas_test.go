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

func TestCanvasIgnoresBlitsOutsideItsBounds(t *testing.T) {
	c := NewCanvas(4, 2)
	c.Blit(mouse.Rect{X: 4, Y: 0, W: 2, H: 2}, "xx\nxx")
	c.Blit(mouse.Rect{X: 0, Y: 2, W: 2, H: 1}, "yy")
	c.Blit(mouse.Rect{X: 2, Y: 1, W: 4, H: 4}, "zzzz\nzzzz\nzzzz")
	if got, want := c.String(), "    \n  zz"; got != want {
		t.Fatalf("canvas = %q, want %q", got, want)
	}
}
