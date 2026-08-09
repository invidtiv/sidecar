package screenmodel

import (
	"fmt"
	"image/color"

	"github.com/charmbracelet/x/ansi"
)

// Attr is a bitmask of the SGR text attributes Sidecar cares about. The values
// are Sidecar's own; they are translated from the emulator's representation so
// that a dependency swap cannot silently renumber them.
type Attr uint16

// Text attributes.
const (
	AttrBold Attr = 1 << iota
	AttrFaint
	AttrItalic
	AttrBlink
	AttrRapidBlink
	AttrReverse
	AttrConceal
	AttrStrikethrough
)

// String renders the attribute set as a stable, human-readable list. It is
// used by the comparator's diff output, never as a wire format.
func (a Attr) String() string {
	if a == 0 {
		return "none"
	}
	names := []struct {
		bit  Attr
		name string
	}{
		{AttrBold, "bold"},
		{AttrFaint, "faint"},
		{AttrItalic, "italic"},
		{AttrBlink, "blink"},
		{AttrRapidBlink, "rapidblink"},
		{AttrReverse, "reverse"},
		{AttrConceal, "conceal"},
		{AttrStrikethrough, "strike"},
	}
	out := ""
	for _, n := range names {
		if a&n.bit != 0 {
			if out != "" {
				out += "+"
			}
			out += n.name
		}
	}
	return out
}

// Underline is the underline style of a cell.
type Underline uint8

// Underline styles.
const (
	UnderlineNone Underline = iota
	UnderlineSingle
	UnderlineDouble
	UnderlineCurly
	UnderlineDotted
	UnderlineDashed
)

// String names the underline style for diff output.
func (u Underline) String() string {
	switch u {
	case UnderlineNone:
		return "none"
	case UnderlineSingle:
		return "single"
	case UnderlineDouble:
		return "double"
	case UnderlineCurly:
		return "curly"
	case UnderlineDotted:
		return "dotted"
	case UnderlineDashed:
		return "dashed"
	default:
		return fmt.Sprintf("underline(%d)", uint8(u))
	}
}

// Color is a canonical, comparable color value.
//
// The empty string means "terminal default" (SGR 39/49/59). Palette colors —
// however they were spelled on the wire, whether SGR 31, SGR 90, or
// SGR 38;5;1 — normalize to "iN". Direct colors normalize to "#rrggbb".
// Canonicalizing here is what makes a cell comparison independent of the
// spelling tmux or the emulator happens to choose when rendering.
type Color string

// ColorDefault is the terminal's default color.
const ColorDefault Color = ""

// IndexedColor returns the canonical form of a 256-color palette entry.
func IndexedColor(i int) Color {
	if i < 0 || i > 255 {
		return ColorDefault
	}
	return Color(fmt.Sprintf("i%d", i))
}

// RGBColor returns the canonical form of a direct color.
func RGBColor(r, g, b uint8) Color {
	return Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
}

// canonicalColor converts an emulator color into Sidecar's canonical form.
func canonicalColor(c color.Color) Color {
	if c == nil {
		return ColorDefault
	}
	switch v := c.(type) {
	case ansi.BasicColor:
		return IndexedColor(int(v))
	case ansi.IndexedColor:
		return IndexedColor(int(v))
	case ansi.TrueColor: //nolint:staticcheck // kept for any Color implementation still constructing it directly
		return RGBColor(uint8(v>>16), uint8(v>>8), uint8(v)) //nolint:gosec
	}
	r, g, b, _ := c.RGBA()
	return RGBColor(uint8(r>>8), uint8(g>>8), uint8(b>>8)) //nolint:gosec
}

// Cell is one canonical terminal cell.
//
// This is the unit the fidelity harness compares. It deliberately excludes
// anything about how a cell would be *spelled* back out as escape bytes: two
// renderers that disagree about whether to emit SGR 0 before a run are not in
// disagreement about the screen.
type Cell struct {
	// Grapheme is the cell's content as a single grapheme cluster. An empty
	// string with Width 0 is the continuation column of a wide cell.
	Grapheme string
	// Width is the cell's mono-spaced width: 1 for ordinary cells, 2 for wide
	// ones, 0 for a wide cell's continuation column.
	Width int

	Fg             Color
	Bg             Color
	UnderlineColor Color
	Underline      Underline
	Attrs          Attr

	// LinkURL and LinkParams carry an OSC 8 hyperlink. LinkParams holds the
	// raw parameter list (typically "id=...").
	LinkURL    string
	LinkParams string
}

// BlankCell is an unwritten cell: a default-styled space.
var BlankCell = Cell{Grapheme: " ", Width: 1}

// Continuation is the second column of a wide cell.
var Continuation = Cell{Grapheme: "", Width: 0}

// IsBlank reports whether the cell is an unstyled, unlinked space.
func (c Cell) IsBlank() bool {
	return c.Grapheme == " " && c.Width == 1 && c.Fg == ColorDefault && c.Bg == ColorDefault &&
		c.UnderlineColor == ColorDefault && c.Underline == UnderlineNone && c.Attrs == 0 &&
		c.LinkURL == "" && c.LinkParams == ""
}

// Describe renders a cell for diff output. It quotes the grapheme and lists
// only the non-default properties, so a mismatch report stays readable.
func (c Cell) Describe() string {
	s := fmt.Sprintf("%q w=%d", c.Grapheme, c.Width)
	if c.Fg != ColorDefault {
		s += " fg=" + string(c.Fg)
	}
	if c.Bg != ColorDefault {
		s += " bg=" + string(c.Bg)
	}
	if c.UnderlineColor != ColorDefault {
		s += " ul=" + string(c.UnderlineColor)
	}
	if c.Underline != UnderlineNone {
		s += " ulstyle=" + c.Underline.String()
	}
	if c.Attrs != 0 {
		s += " attrs=" + c.Attrs.String()
	}
	if c.LinkURL != "" || c.LinkParams != "" {
		s += fmt.Sprintf(" link=%q params=%q", c.LinkURL, c.LinkParams)
	}
	return s
}

// Grid is a rectangular grid of canonical cells, row-major.
type Grid [][]Cell

// Width returns the width of the widest row, or 0 for an empty grid.
func (g Grid) Width() int {
	w := 0
	for _, row := range g {
		if len(row) > w {
			w = len(row)
		}
	}
	return w
}

// Height returns the number of rows.
func (g Grid) Height() int { return len(g) }

// Normalize returns a copy of the grid padded to exactly h rows of w cells,
// filling with blanks.
//
// Padding matters because tmux's capture-pane trims trailing blank cells from
// every line: a short captured row and a full model row can describe exactly
// the same screen. Normalizing both sides before comparison keeps that
// formatting difference from reading as a fidelity failure.
func (g Grid) Normalize(w, h int) Grid {
	out := make(Grid, h)
	for y := range out {
		row := make([]Cell, w)
		for x := range row {
			if y < len(g) && x < len(g[y]) {
				row[x] = g[y][x]
				continue
			}
			row[x] = BlankCell
		}
		out[y] = row
	}
	return out
}
