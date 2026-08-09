package screenmodel

import (
	"strconv"
	"strings"

	"github.com/rivo/uniseg"
)

// This file is the oracle side of the fidelity harness: it turns tmux's
// `capture-pane -p -e` rendering back into canonical cells.
//
// It is deliberately test-only and deliberately hand-written. Decoding the
// capture with the emulator under test would make the comparison circular —
// an SGR bug in x/vt would cancel itself out on both sides. This decoder
// shares no code with x/vt: it does its own escape scanning, its own SGR
// interpretation, and it uses rivo/uniseg for grapheme segmentation and width
// rather than the clipperhouse tables x/ansi (and therefore x/vt) uses.
//
// Known dependency: neither side can be a true oracle for *column placement*
// of wide characters, because capture-pane emits the character once with no
// padding for its continuation column, so the decoder must apply a width table
// to lay cells out. tmux's own column arithmetic is observed independently
// through the cursor_x metadata assertion instead.

// decodeCapture converts capture-pane -e output into a w x h canonical grid.
func decodeCapture(text string, w, h int) Grid {
	text = strings.TrimSuffix(text, "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	g := make(Grid, h)
	// The pen carries across lines. tmux emits capture-pane -e as one
	// continuous SGR stream and only writes the delta, so a line whose
	// predecessor left an attribute set begins with that attribute still
	// active — verified by the sgr_underline_styles fixture, where tmux emits
	// the underline colour for a row without repeating the underline attribute
	// the previous row turned on.
	var p pen
	for y := range h {
		row := make([]Cell, w)
		for x := range row {
			row[x] = BlankCell
		}
		if y < len(lines) {
			decodeCaptureLine(lines[y], row, &p)
		}
		g[y] = row
	}
	return g
}

// tabWidth is tmux's default tab stop interval. capture-pane emits a literal
// TAB where a tab advanced the cursor over untouched cells, so the decoder has
// to apply the same stops rather than treating it as a printable character.
const tabWidth = 8

// pen is the decoder's SGR/link state.
//
// tmux renders each captured line starting from default state, so the pen is
// reset per line. If that assumption were ever wrong the colored-line-followed-
// by-plain-line fixture would fail loudly rather than silently bleed.
type pen struct {
	fg, bg, ul Color
	underline  Underline
	attrs      Attr
	linkURL    string
	linkParams string
}

func (p pen) cell(grapheme string, width int) Cell {
	return Cell{
		Grapheme:       grapheme,
		Width:          width,
		Fg:             p.fg,
		Bg:             p.bg,
		UnderlineColor: p.ul,
		Underline:      p.underline,
		Attrs:          p.attrs,
		LinkURL:        p.linkURL,
		LinkParams:     p.linkParams,
	}
}

func decodeCaptureLine(line string, row []Cell, p *pen) {
	col := 0
	i := 0
	for i < len(line) {
		if line[i] == 0x1b {
			n := decodeEscape(line[i:], p)
			if n <= 0 {
				i++
				continue
			}
			i += n
			continue
		}
		if line[i] == '\t' {
			i++
			col = (col/tabWidth + 1) * tabWidth
			continue
		}
		cluster, _, width, _ := uniseg.FirstGraphemeClusterInString(line[i:], -1)
		if cluster == "" {
			i++
			continue
		}
		i += len(cluster)
		if width < 1 {
			width = 1
		}
		if col < len(row) {
			row[col] = p.cell(cluster, width)
		}
		for k := 1; k < width; k++ {
			if col+k < len(row) {
				row[col+k] = Continuation
			}
		}
		col += width
	}
}

// decodeEscape consumes one escape sequence and returns its byte length.
func decodeEscape(s string, p *pen) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		// CSI: parameter and intermediate bytes, then a final byte.
		j := 2
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			j++
		}
		if j >= len(s) {
			return len(s)
		}
		final := s[j]
		if final == 'm' {
			applySGR(s[2:j], p)
		}
		return j + 1
	case ']':
		// OSC: data up to BEL or ST.
		end := len(s)
		termLen := 0
		if k := strings.IndexByte(s[2:], 0x07); k >= 0 {
			end = 2 + k
			termLen = 1
		}
		if k := strings.Index(s[2:], "\x1b\\"); k >= 0 && 2+k < end {
			end = 2 + k
			termLen = 2
		}
		applyOSC(s[2:end], p)
		return end + termLen
	default:
		return 2
	}
}

func applyOSC(data string, p *pen) {
	// OSC 8 ; params ; URI
	if !strings.HasPrefix(data, "8;") {
		return
	}
	rest := data[2:]
	sep := strings.IndexByte(rest, ';')
	if sep < 0 {
		return
	}
	p.linkParams = rest[:sep]
	p.linkURL = rest[sep+1:]
}

// applySGR interprets one SGR parameter string. Both ';' and ':' separated
// sub-parameters are handled, since tmux emits colon forms for underline
// styles and may emit either for extended colors.
func applySGR(params string, p *pen) {
	if params == "" {
		params = "0"
	}
	groups := strings.Split(params, ";")
	// Flatten into a token stream that keeps colon groups together.
	for gi := 0; gi < len(groups); gi++ {
		g := groups[gi]
		sub := strings.Split(g, ":")
		n, err := strconv.Atoi(sub[0])
		if err != nil {
			if sub[0] == "" {
				n = 0
			} else {
				continue
			}
		}
		switch {
		case n == 0:
			*p = pen{linkURL: p.linkURL, linkParams: p.linkParams}
		case n == 1:
			p.attrs |= AttrBold
		case n == 2:
			p.attrs |= AttrFaint
		case n == 3:
			p.attrs |= AttrItalic
		case n == 4:
			if len(sub) > 1 {
				p.underline = underlineFromParam(sub[1])
			} else {
				p.underline = UnderlineSingle
			}
		case n == 5:
			p.attrs |= AttrBlink
		case n == 6:
			p.attrs |= AttrRapidBlink
		case n == 7:
			p.attrs |= AttrReverse
		case n == 8:
			p.attrs |= AttrConceal
		case n == 9:
			p.attrs |= AttrStrikethrough
		case n == 21:
			p.underline = UnderlineDouble
		case n == 22:
			p.attrs &^= AttrBold | AttrFaint
		case n == 23:
			p.attrs &^= AttrItalic
		case n == 24:
			p.underline = UnderlineNone
		case n == 25:
			p.attrs &^= AttrBlink | AttrRapidBlink
		case n == 27:
			p.attrs &^= AttrReverse
		case n == 28:
			p.attrs &^= AttrConceal
		case n == 29:
			p.attrs &^= AttrStrikethrough
		case n >= 30 && n <= 37:
			p.fg = IndexedColor(n - 30)
		case n == 38:
			c, used := extendedColor(sub, groups, gi)
			p.fg = c
			gi += used
		case n == 39:
			p.fg = ColorDefault
		case n >= 40 && n <= 47:
			p.bg = IndexedColor(n - 40)
		case n == 48:
			c, used := extendedColor(sub, groups, gi)
			p.bg = c
			gi += used
		case n == 49:
			p.bg = ColorDefault
		case n == 58:
			c, used := extendedColor(sub, groups, gi)
			p.ul = c
			gi += used
		case n == 59:
			p.ul = ColorDefault
		case n >= 90 && n <= 97:
			p.fg = IndexedColor(n - 90 + 8)
		case n >= 100 && n <= 107:
			p.bg = IndexedColor(n - 100 + 8)
		}
	}
}

func underlineFromParam(s string) Underline {
	switch s {
	case "0":
		return UnderlineNone
	case "1":
		return UnderlineSingle
	case "2":
		return UnderlineDouble
	case "3":
		return UnderlineCurly
	case "4":
		return UnderlineDotted
	case "5":
		return UnderlineDashed
	default:
		return UnderlineSingle
	}
}

// extendedColor reads a 38/48/58 color. It returns the color and how many
// extra semicolon-separated groups it consumed.
func extendedColor(sub, groups []string, gi int) (Color, int) {
	atoi := func(s string) int {
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0
		}
		return n
	}
	if len(sub) > 1 {
		// Colon form: 38:5:n or 38:2::r:g:b or 38:2:r:g:b.
		switch sub[1] {
		case "5":
			if len(sub) > 2 {
				return IndexedColor(atoi(sub[2])), 0
			}
		case "2":
			nums := sub[2:]
			// The CSS-ish form carries an empty colorspace id first.
			if len(nums) == 4 {
				nums = nums[1:]
			}
			if len(nums) >= 3 {
				return RGBColor(uint8(atoi(nums[0])), uint8(atoi(nums[1])), uint8(atoi(nums[2]))), 0 //nolint:gosec
			}
		}
		return ColorDefault, 0
	}
	// Semicolon form: 38;5;n or 38;2;r;g;b.
	if gi+1 >= len(groups) {
		return ColorDefault, 0
	}
	switch groups[gi+1] {
	case "5":
		if gi+2 < len(groups) {
			return IndexedColor(atoi(groups[gi+2])), 2
		}
		return ColorDefault, 1
	case "2":
		if gi+4 < len(groups) {
			return RGBColor(uint8(atoi(groups[gi+2])), uint8(atoi(groups[gi+3])), uint8(atoi(groups[gi+4]))), 4 //nolint:gosec
		}
		return ColorDefault, len(groups) - gi - 1
	}
	return ColorDefault, 1
}
