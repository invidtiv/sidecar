package termpreview

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// ChipGap is the columns between two chips, and the minimum gap between a
// header row's left and right regions.
const ChipGap = 1

// ChipPlacement is where one chip landed on a header row: Col is its first
// column, relative to the row's own first column, and Drawn is false for a chip
// the row had no columns for.
type ChipPlacement struct {
	Col   int
	Width int
	Drawn bool
}

// LayoutChips places chips left to right in the columns a header row can give
// them, dropping whole chips rather than clipping one. It is the single
// authority on which chips a row drew and where: HeaderRow renders from it and
// hit regions are registered from it, so a chip dropped for want of columns
// cannot keep a live click target.
//
// hintFloor is the columns the row's right region has claimed; zero leaves the
// chips the whole width.
func LayoutChips(chips []string, width, hintFloor int) []ChipPlacement {
	placements := make([]ChipPlacement, len(chips))
	if width <= 0 {
		return placements
	}
	budget := width
	if hintFloor > 0 {
		budget = max(width-hintFloor-ChipGap, 0)
	}

	used := 0
	for i, chip := range chips {
		if chip == "" {
			continue
		}
		chipWidth := ansi.StringWidth(chip)
		col, cost := used, chipWidth
		if used > 0 {
			col += ChipGap
			cost += ChipGap
		}
		if used+cost > budget {
			break
		}
		placements[i] = ChipPlacement{Col: col, Width: chipWidth, Drawn: true}
		used += cost
	}
	return placements
}

// HeaderRow composes the one row above an embedded terminal: identity chips on
// the left, hints right-aligned on the right.
//
// Truncation is deliberately asymmetric. The chips say what the surface is and
// carry the only hit regions on the row, so they are kept whole — a chip either
// fits or is dropped entirely, never clipped mid-chip — while the hints are
// advisory and give way first. The result is always exactly one row and never
// wider than width, so the terminal below can never lose a row to overflow.
//
// hintFloor inverts that priority for the columns it names: the right region
// keeps at least that many, dropping chips to find them.
//
// truncate is passed in rather than reached for so this stays a plain function
// over strings: the caller supplies its own ANSI-aware truncation cache.
func HeaderRow(chips []string, hints string, width, hintFloor int, truncate func(string, int) string) string {
	if width <= 0 {
		return ""
	}
	if hints == "" {
		hintFloor = 0
	}
	if truncate == nil {
		truncate = TruncateANSI
	}

	var left strings.Builder
	leftWidth := 0
	for i, placement := range LayoutChips(chips, width, hintFloor) {
		if !placement.Drawn {
			continue
		}
		if leftWidth > 0 {
			left.WriteString(strings.Repeat(" ", ChipGap))
		}
		left.WriteString(chips[i])
		leftWidth = placement.Col + placement.Width
	}

	if hints == "" {
		return left.String()
	}
	available := width - leftWidth - ChipGap
	if available < 1 {
		return left.String()
	}
	hints = truncate(hints, available)
	gap := width - leftWidth - ansi.StringWidth(hints)
	if gap < ChipGap {
		// truncate reported a narrower string than it produced; keep the row
		// exactly one row wide rather than risk a wrap.
		return left.String()
	}
	return left.String() + strings.Repeat(" ", gap) + hints
}

// TruncateANSI is the default ANSI-safe truncation: no ellipsis, so a clipped
// terminal line looks clipped rather than annotated.
func TruncateANSI(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "")
}
