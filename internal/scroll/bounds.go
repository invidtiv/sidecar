// Package scroll contains the small, state-free rules shared by Sidecar's
// scrollable surfaces. Rendering and content ownership stay with each surface;
// this package only answers movement and boundary questions in rows from the
// top, where negative deltas move up and positive deltas move down.
package scroll

// Bounds describes a vertical viewport position and its inclusive maximum.
type Bounds struct {
	Position int
	Maximum  int
}

// AtBoundary reports whether delta points out of the scrollable range.
func (b Bounds) AtBoundary(delta int) bool {
	maximum := max(b.Maximum, 0)
	position := min(max(b.Position, 0), maximum)
	switch {
	case delta < 0:
		return position == 0
	case delta > 0:
		return position == maximum
	default:
		return true
	}
}

// Move applies delta and clamps the result to the scrollable range. Changed is
// false at either boundary, which lets callers avoid downstream work.
func (b Bounds) Move(delta int) (position int, changed bool) {
	maximum := max(b.Maximum, 0)
	before := min(max(b.Position, 0), maximum)
	after := min(max(before+delta, 0), maximum)
	return after, after != before
}
