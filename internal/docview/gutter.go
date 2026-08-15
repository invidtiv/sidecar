package docview

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/styles"
)

const (
	// minGutterDigits keeps short files looking the way they always have: a
	// four-digit number plus a single trailing space.
	minGutterDigits = 4
	// minGutterContentWidth is the narrowest text column worth keeping. Below
	// it the gutter would crowd out the document it is annotating, so it is
	// dropped entirely rather than shown over an empty text column.
	minGutterContentWidth = 8
)

// Gutter renders the line-number column beside a document's text. It is a
// value type with no state beyond its width, so callers can build one per
// render pass.
//
// The zero Gutter is disabled: it has zero width and renders empty cells, so
// a caller that suppresses numbering (rendered markdown, placeholder text) can
// keep one code path.
type Gutter struct {
	digits int
}

// NewGutter returns a gutter wide enough to number a document of lineCount
// lines, never narrower than the historical four digits.
func NewGutter(lineCount int) Gutter {
	digits := len(strconv.Itoa(max(lineCount, 1)))
	return Gutter{digits: max(digits, minGutterDigits)}
}

// NewGutterForWidth is NewGutter plus the "is there room?" decision: it
// returns a disabled gutter when numbering would leave less than
// minGutterContentWidth cells for the text itself.
func NewGutterForWidth(lineCount, totalWidth int) Gutter {
	g := NewGutter(lineCount)
	if totalWidth-g.Width() < minGutterContentWidth {
		return Gutter{}
	}
	return g
}

// Enabled reports whether this gutter renders anything.
func (g Gutter) Enabled() bool { return g.digits > 0 }

// Width is the cell width of every cell this gutter renders, including the
// separating space. It is 0 when the gutter is disabled.
func (g Gutter) Width() int {
	if g.digits == 0 {
		return 0
	}
	return g.digits + 1
}

// Number renders the cell for a 1-based source line number.
func (g Gutter) Number(line int) string {
	if g.digits == 0 {
		return ""
	}
	cell := fmt.Sprintf("%*d ", g.digits, line)
	return styles.FileBrowserLineNumber.Width(g.Width()).Render(cell)
}

// Blank renders an equal-width empty cell, for wrapped continuation rows and
// for lines that have no source line number of their own.
func (g Gutter) Blank() string {
	if g.digits == 0 {
		return ""
	}
	return strings.Repeat(" ", g.Width())
}
