// Package overlay composites a floating block of text over already-rendered
// lines without changing their number.
//
// It is the mechanism a dropdown needs and nothing more: the modal layout
// engine uses it to paint a Combo's list over the sections underneath, and the
// Configuration surface uses it to paint a select control's list over the page
// behind it. Both want the same thing — "draw this rectangle on top, clip it to
// what is already there" — so both call the same code rather than each carrying
// a copy of the cell arithmetic that ANSI-aware slicing requires.
package overlay

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Composite draws over onto base at (x, y), measured in cells from base's top
// left. It never adds, removes, or lengthens lines: anything that would fall
// outside base is clipped, so a caller's height contract is unaffected.
func Composite(base, over string, x, y int) string {
	if over == "" {
		return base
	}
	baseLines := strings.Split(base, "\n")
	overLines := strings.Split(strings.TrimRight(over, "\n"), "\n")
	for i, line := range overLines {
		dest := y + i
		if dest < 0 || dest >= len(baseLines) {
			continue
		}
		baseLines[dest] = Line(baseLines[dest], line, x)
	}
	return strings.Join(baseLines, "\n")
}

// Line draws over onto one line of base starting at cell x. A negative x clips
// the left of over rather than shifting it, and a base line shorter than x is
// padded with spaces so the overlay still lands where it was asked to.
//
// Everything here is measured in cells, not runes or bytes: a base line carrying
// SGR sequences must not have them counted as width, and a wide rune the overlay
// only half-covers is replaced by a space rather than allowed to shift the rest
// of the line sideways. An overlay that fits inside the base leaves the line
// exactly as wide as it found it.
func Line(base, over string, x int) string {
	if x < 0 {
		over = ansi.Cut(over, -x, ansi.StringWidth(over))
		x = 0
	}
	overWidth := ansi.StringWidth(over)
	baseWidth := ansi.StringWidth(base)
	left := ""
	if x > 0 {
		left = cutTo(base, 0, x, x)
	}
	right := ""
	if target := baseWidth - x - overWidth; target > 0 {
		right = cutTo(base, x+overWidth, baseWidth, target)
	}
	return left + over + right
}

// cutTo slices base from start to end and makes the result exactly width cells
// wide. A cut that lands inside a wide rune yields a slice one cell too wide, so
// the boundary is walked inward and the difference made up with spaces — the
// half-covered rune becomes blank, which is what it looks like on a terminal.
func cutTo(base string, start, end, width int) string {
	cut := ansi.Cut(base, start, end)
	if start == 0 {
		for ansi.StringWidth(cut) > width && end > start {
			end--
			cut = ansi.Cut(base, start, end)
		}
		if pad := width - ansi.StringWidth(cut); pad > 0 {
			cut += strings.Repeat(" ", pad)
		}
		return cut
	}
	for ansi.StringWidth(cut) > width && start < end {
		start++
		cut = ansi.Cut(base, start, end)
	}
	if pad := width - ansi.StringWidth(cut); pad > 0 {
		cut = strings.Repeat(" ", pad) + cut
	}
	return cut
}
