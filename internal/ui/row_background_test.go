package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// cellBackgrounds returns the background sequence active at each visible cell of
// a rendered row, which is the only thing that decides whether a highlight has
// holes in it.
func cellBackgrounds(t *testing.T, row string) []string {
	t.Helper()
	var bgs []string
	current := ""
	state := ansi.NormalState
	remaining := row
	for len(remaining) > 0 {
		seq, width, n, next := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			break
		}
		if width > 0 {
			for range width {
				bgs = append(bgs, current)
			}
		} else if bg, touches := SGRBackground(seq); touches {
			if bg == RowBackgroundDefault {
				current = ""
			} else {
				current = bg
			}
		}
		state = next
		remaining = remaining[n:]
	}
	return bgs
}

func assertRowFilled(t *testing.T, row string, width int, bgSeq string) {
	t.Helper()
	bgs := cellBackgrounds(t, row)
	if len(bgs) != width {
		t.Fatalf("row occupies %d cells, want %d (%q)", len(bgs), width, row)
	}
	for i, bg := range bgs {
		if bg != bgSeq {
			t.Fatalf("cell %d has background %q, want %q (row %q)", i, bg, bgSeq, row)
		}
	}
	if !strings.HasSuffix(row, "\x1b[m") {
		t.Fatalf("row does not end in a reset, background can bleed: %q", row)
	}
}

func testBgSeq() string { return styles.BgANSISeqFor(styles.BgTertiary) }

func TestRowBackgroundCoversEveryCellOfAdversarialRows(t *testing.T) {
	bgSeq := testBgSeq()
	hue := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff8800"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

	cases := map[string]string{
		"plain":                 "a note",
		"lipgloss spans":        hue.Render("◆") + " " + muted.Render("title") + "  " + muted.Render("4m"),
		"explicit reset 0m":     "a\x1b[0mb",
		"implicit reset m":      "a\x1b[mb",
		"inner truecolour bg":   "a\x1b[48;2;10;20;30mb\x1b[0mc",
		"inner 256 bg":          "a\x1b[48;5;12mb\x1b[0mc",
		"legacy bg code":        "a\x1b[41mb\x1b[49mc",
		"compound reset":        "\x1b[0;38;2;200;200;200mstyled\x1b[0m tail",
		"fg containing 49":      "\x1b[38;2;49;0;0mtrap\x1b[0m",
		"underline and bold":    "\x1b[1mbold\x1b[22m \x1b[4munder\x1b[24m",
		"wide runes":            "日本語 note",
		"emoji":                 "✅ done",
		"nested lipgloss":       lipgloss.NewStyle().Bold(true).Render(hue.Render("x") + "y"),
		"already backgrounded":  lipgloss.NewStyle().Background(lipgloss.Color("#123456")).Render("row"),
		"trailing reset only":   "text\x1b[0m",
		"leading unstyled pad":  "  > title",
		"empty":                 "",
		"only escape sequences": "\x1b[1m\x1b[0m",
	}

	const width = 24
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			assertRowFilled(t, RowBackground(in, width, styles.BgTertiary), width, bgSeq)
		})
	}
}

func TestRowBackgroundPreservesForegroundStyling(t *testing.T) {
	hue := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff8800"))
	in := hue.Render("◆") + " title"
	out := RowBackground(in, 20, styles.BgTertiary)

	fgSeq := ""
	if i := strings.Index(hue.Render("\x01"), "\x01"); i > 0 {
		fgSeq = hue.Render("\x01")[:i]
	}
	if fgSeq == "" {
		t.Skip("theme renders no colour in this environment")
	}
	if !strings.Contains(out, fgSeq) {
		t.Fatalf("highlighted row dropped the source hue %q: %q", fgSeq, out)
	}
	if got := ansi.Strip(out); !strings.HasPrefix(got, "◆ title") {
		t.Fatalf("text changed: %q", got)
	}
}

func TestRowBackgroundTruncatesAndPadsToExactlyWidth(t *testing.T) {
	bgSeq := testBgSeq()

	long := lipgloss.NewStyle().Bold(true).Render("a very long note title that will not fit")
	out := RowBackground(long, 10, styles.BgTertiary)
	assertRowFilled(t, out, 10, bgSeq)
	if got := ansi.Strip(out); got != "a very lon" {
		t.Fatalf("truncated text = %q", got)
	}

	short := RowBackground("hi", 6, styles.BgTertiary)
	assertRowFilled(t, short, 6, bgSeq)
	if got := ansi.Strip(short); got != "hi    " {
		t.Fatalf("padded text = %q", got)
	}
}

func TestRowBackgroundTruncatesWideRunesOnACellBoundary(t *testing.T) {
	// Cutting "日" in half must drop the whole grapheme rather than leave the
	// row one cell short or one cell over.
	out := RowBackground("日本語", 3, styles.BgTertiary)
	assertRowFilled(t, out, 3, testBgSeq())
}

func TestRowBackgroundZeroWidthIsEmpty(t *testing.T) {
	if got := RowBackground("anything", 0, styles.BgTertiary); got != "" {
		t.Fatalf("zero width row = %q, want empty", got)
	}
	if got := RowBackground("anything", -3, styles.BgTertiary); got != "" {
		t.Fatalf("negative width row = %q, want empty", got)
	}
}

func TestRowBackgroundSeqWithoutABackgroundStillNormalizesWidth(t *testing.T) {
	if got := RowBackgroundSeq("hi", 5, ""); got != "hi   " {
		t.Fatalf("got %q", got)
	}
	if got := RowBackgroundSeq("hello there", 5, ""); ansi.Strip(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestRowBackgroundHandlesEachLineOfAMultiLineBlock(t *testing.T) {
	bgSeq := testBgSeq()
	out := RowBackground("title\n  body\x1b[0m tail", 12, styles.BgTertiary)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}
	for _, line := range lines {
		assertRowFilled(t, line, 12, bgSeq)
	}
}

func TestSelectedRowBackgroundUsesTheThemeSelectionColour(t *testing.T) {
	out := SelectedRowBackground("a\x1b[0mb", 8)
	assertRowFilled(t, out, 8, GetSelectionBgANSI())
}
