package screenmodel

import (
	"fmt"
	"slices"
	"strings"
)

// Mismatch is one canonical difference between two screens.
type Mismatch struct {
	// Kind is a coarse class: "cell", "cursor", "mode", "geometry", "history".
	Kind string
	// Field names the specific property, e.g. "grapheme", "bg", "attrs".
	Field string
	// Row and Col locate a cell mismatch; both are -1 for non-cell kinds.
	Row, Col int
	// Want is the oracle's value, Got the model's.
	Want, Got string
}

// String renders one mismatch for a test failure message.
func (m Mismatch) String() string {
	if m.Row >= 0 && m.Col >= 0 {
		return fmt.Sprintf("%s/%s at (%d,%d): want %s, got %s", m.Kind, m.Field, m.Row, m.Col, m.Want, m.Got)
	}
	return fmt.Sprintf("%s/%s: want %s, got %s", m.Kind, m.Field, m.Want, m.Got)
}

// CompareGrids compares two canonical grids cell by cell.
//
// This is the canonical cell comparator: it compares grapheme, cell width,
// foreground/background/underline color, attributes, underline style, and
// hyperlink. It deliberately does NOT compare rendered string spelling —
// two renderers may legitimately emit different escape byte sequences for the
// same screen. Cursor position, cursor visibility, and modes are separate
// assertions; see [CompareCursor] and [CompareModes].
//
// Both grids are normalized to w x h first, so a capture that trimmed trailing
// blanks compares equal to a fully populated model row.
func CompareGrids(want, got Grid, w, h int) []Mismatch {
	a := want.Normalize(w, h)
	b := got.Normalize(w, h)
	var out []Mismatch
	for y := range h {
		for x := range w {
			out = append(out, compareCell(y, x, a[y][x], b[y][x])...)
		}
	}
	return out
}

func compareCell(row, col int, want, got Cell) []Mismatch {
	var out []Mismatch
	add := func(field, w, g string) {
		out = append(out, Mismatch{Kind: "cell", Field: field, Row: row, Col: col, Want: w, Got: g})
	}
	if want.Grapheme != got.Grapheme {
		add("grapheme", fmt.Sprintf("%q", want.Grapheme), fmt.Sprintf("%q", got.Grapheme))
	}
	if want.Width != got.Width {
		add("width", fmt.Sprint(want.Width), fmt.Sprint(got.Width))
	}
	if want.Fg != got.Fg {
		add("fg", colorName(want.Fg), colorName(got.Fg))
	}
	if want.Bg != got.Bg {
		add("bg", colorName(want.Bg), colorName(got.Bg))
	}
	if want.UnderlineColor != got.UnderlineColor {
		add("underline_color", colorName(want.UnderlineColor), colorName(got.UnderlineColor))
	}
	if want.Underline != got.Underline {
		add("underline", want.Underline.String(), got.Underline.String())
	}
	if want.Attrs != got.Attrs {
		add("attrs", want.Attrs.String(), got.Attrs.String())
	}
	if want.LinkURL != got.LinkURL {
		add("link_url", fmt.Sprintf("%q", want.LinkURL), fmt.Sprintf("%q", got.LinkURL))
	}
	if want.LinkParams != got.LinkParams {
		add("link_params", fmt.Sprintf("%q", want.LinkParams), fmt.Sprintf("%q", got.LinkParams))
	}
	return out
}

func colorName(c Color) string {
	if c == ColorDefault {
		return "default"
	}
	return string(c)
}

// CursorState is the cursor half of a screen assertion, kept separate from the
// cell grid on purpose: a cursor-only divergence and a content divergence are
// different failures with different causes.
type CursorState struct {
	Row, Col int
	Visible  bool
}

// CompareCursor compares two cursor states.
func CompareCursor(want, got CursorState) []Mismatch {
	var out []Mismatch
	if want.Row != got.Row || want.Col != got.Col {
		out = append(out, Mismatch{
			Kind: "cursor", Field: "position", Row: -1, Col: -1,
			Want: fmt.Sprintf("(%d,%d)", want.Row, want.Col),
			Got:  fmt.Sprintf("(%d,%d)", got.Row, got.Col),
		})
	}
	if want.Visible != got.Visible {
		out = append(out, Mismatch{
			Kind: "cursor", Field: "visible", Row: -1, Col: -1,
			Want: fmt.Sprint(want.Visible), Got: fmt.Sprint(got.Visible),
		})
	}
	return out
}

// ModeState is the mode half of a screen assertion.
type ModeState struct {
	AltScreen     bool
	MouseAny      bool
	MouseSGR      bool
	BracketedPast bool
}

// CompareModes compares two mode states. Fields the caller does not want
// asserted should be equal on both sides.
func CompareModes(want, got ModeState) []Mismatch {
	var out []Mismatch
	add := func(field string, w, g bool) {
		if w != g {
			out = append(out, Mismatch{
				Kind: "mode", Field: field, Row: -1, Col: -1,
				Want: fmt.Sprint(w), Got: fmt.Sprint(g),
			})
		}
	}
	add("alt_screen", want.AltScreen, got.AltScreen)
	add("mouse_any", want.MouseAny, got.MouseAny)
	add("mouse_sgr", want.MouseSGR, got.MouseSGR)
	add("bracketed_paste", want.BracketedPast, got.BracketedPast)
	return out
}

// FormatMismatches renders up to limit mismatches for a failure message.
func FormatMismatches(ms []Mismatch, limit int) string {
	if len(ms) == 0 {
		return "no mismatches"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d mismatch(es):", len(ms))
	for i, m := range ms {
		if i >= limit {
			fmt.Fprintf(&b, "\n  ... and %d more", len(ms)-limit)
			break
		}
		b.WriteString("\n  " + m.String())
	}
	return b.String()
}

// Signature reduces a mismatch to a stable class, dropping coordinates and
// values. The corpus runner uses it to assert that the *set* of documented
// upstream gaps is exactly what is observed — so a fixed defect and a new one
// both fail the test.
func (m Mismatch) Signature() string {
	return m.Kind + "/" + m.Field
}

// Signatures returns the sorted-unique signature set of a mismatch list.
func Signatures(ms []Mismatch) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range ms {
		s := m.Signature()
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}
