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

// HeaderRowSplit is HeaderRow with a second, right-aligned group of chips.
//
// The row reads: identity chips left, then space, then the right chips, then
// the hints hard against the right edge. Action chips belong there because they
// are the same two buttons on every row — putting them immediately after a name
// of unpredictable length left them at a different column on every row, and
// nothing about them is part of the row's identity.
//
// Priority under pressure is hints, then right chips, then left chips, and the
// first left chip is the row's name: it is truncated to make room rather than
// dropped, so a narrow row keeps a name and its buttons instead of losing both.
//
// The returned placements are where the right chips landed, in columns relative
// to the row's first column. Hit regions must come from these rather than a
// second calculation: a right-aligned chip's column depends on the hints beside
// it, which only this function has seen.
func HeaderRowSplit(left, right []string, hints string, width, hintFloor int, truncate func(string, int) string) (string, []ChipPlacement) {
	placements := make([]ChipPlacement, len(right))
	if !anyChip(right) {
		return HeaderRow(left, hints, width, hintFloor, truncate), placements
	}
	if width <= 0 {
		return "", placements
	}
	if truncate == nil {
		truncate = TruncateANSI
	}

	drawn := make([]int, 0, len(right))
	for i, chip := range right {
		if chip != "" {
			drawn = append(drawn, i)
		}
	}
	// Give the hints what is left after the right chips, then drop right chips
	// from the end while the pair still cannot fit the row.
	fullHints := hints
	hintsWidth := 0
	for {
		hintsWidth = 0
		if fullHints != "" {
			budget := width - groupWidth(right, drawn)
			if len(drawn) > 0 {
				budget -= ChipGap
			}
			hints = truncate(fullHints, max(budget, 0))
			hintsWidth = ansi.StringWidth(hints)
		}
		if regionWidth(groupWidth(right, drawn), hintsWidth) <= width || len(drawn) == 0 {
			break
		}
		drawn = drawn[:len(drawn)-1]
	}

	rightWidth := groupWidth(right, drawn)
	regionW := regionWidth(rightWidth, hintsWidth)
	leftBudget := max(width-regionW-ChipGap, 0)
	if regionW == 0 {
		leftBudget = width
	}

	leftChips := fitNameChip(left, leftBudget, truncate)
	var leftText strings.Builder
	leftUsed := 0
	for i, placement := range LayoutChips(leftChips, leftBudget, 0) {
		if !placement.Drawn {
			continue
		}
		if leftUsed > 0 {
			leftText.WriteString(strings.Repeat(" ", ChipGap))
		}
		leftText.WriteString(leftChips[i])
		leftUsed = placement.Col + placement.Width
	}

	var row strings.Builder
	row.WriteString(leftText.String())
	if regionW == 0 {
		return row.String(), placements
	}
	if gap := width - regionW - leftUsed; gap > 0 {
		row.WriteString(strings.Repeat(" ", gap))
	}
	col := max(width-regionW, leftUsed)
	for n, i := range drawn {
		if n > 0 {
			row.WriteString(strings.Repeat(" ", ChipGap))
			col += ChipGap
		}
		row.WriteString(right[i])
		placements[i] = ChipPlacement{Col: col, Width: ansi.StringWidth(right[i]), Drawn: true}
		col += ansi.StringWidth(right[i])
	}
	if hintsWidth > 0 {
		if rightWidth > 0 {
			row.WriteString(strings.Repeat(" ", ChipGap))
		}
		row.WriteString(hints)
	}
	return row.String(), placements
}

// fitNameChip shrinks the row's name — the first chip — to the columns the row
// can spare, so the chips beside it survive a narrow window.
func fitNameChip(chips []string, budget int, truncate func(string, int) string) []string {
	if len(chips) == 0 || chips[0] == "" || budget <= 0 {
		return chips
	}
	if ansi.StringWidth(chips[0]) <= budget {
		return chips
	}
	out := append([]string(nil), chips...)
	out[0] = truncate(out[0], budget)
	return out
}

func anyChip(chips []string) bool {
	for _, chip := range chips {
		if chip != "" {
			return true
		}
	}
	return false
}

// groupWidth is the drawn width of the chips named by idx, gaps included.
func groupWidth(chips []string, idx []int) int {
	total := 0
	for n, i := range idx {
		if n > 0 {
			total += ChipGap
		}
		total += ansi.StringWidth(chips[i])
	}
	return total
}

func regionWidth(chipsWidth, hintsWidth int) int {
	if chipsWidth > 0 && hintsWidth > 0 {
		return chipsWidth + ChipGap + hintsWidth
	}
	return chipsWidth + hintsWidth
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
