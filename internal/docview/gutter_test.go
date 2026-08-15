package docview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestGutterWidthAdaptsAboveFourDigits(t *testing.T) {
	tests := []struct {
		lineCount int
		width     int
		number    string
	}{
		{lineCount: 0, width: 5, number: "   1 "},
		{lineCount: 1, width: 5, number: "   1 "},
		{lineCount: 9999, width: 5, number: "9999 "},
		{lineCount: 12000, width: 6, number: "12000 "},
		{lineCount: 1234567, width: 8, number: "1234567 "},
	}
	for _, tt := range tests {
		g := NewGutter(tt.lineCount)
		if got := g.Width(); got != tt.width {
			t.Fatalf("NewGutter(%d).Width() = %d, want %d", tt.lineCount, got, tt.width)
		}
		if !g.Enabled() {
			t.Fatalf("NewGutter(%d) is disabled", tt.lineCount)
		}
		line := max(tt.lineCount, 1)
		if got := ansi.Strip(g.Number(line)); got != tt.number {
			t.Fatalf("NewGutter(%d).Number(%d) = %q, want %q", tt.lineCount, line, got, tt.number)
		}
		if got := g.Blank(); got != strings.Repeat(" ", tt.width) {
			t.Fatalf("NewGutter(%d).Blank() = %q", tt.lineCount, got)
		}
		if got := ansi.StringWidth(ansi.Strip(g.Number(line))); got != ansi.StringWidth(g.Blank()) {
			t.Fatalf("number and blank cells differ in width for %d lines", tt.lineCount)
		}
	}
}

func TestGutterNumberIsStyledAndRightAligned(t *testing.T) {
	g := NewGutter(100)
	cell := g.Number(7)
	if ansi.Strip(cell) != "   7 " {
		t.Fatalf("number cell = %q", ansi.Strip(cell))
	}
	if cell == ansi.Strip(cell) {
		t.Fatalf("number cell carried no style: %q", cell)
	}
}

func TestZeroGutterIsDisabled(t *testing.T) {
	var g Gutter
	if g.Enabled() || g.Width() != 0 || g.Number(3) != "" || g.Blank() != "" {
		t.Fatalf("zero Gutter is not disabled: enabled=%v width=%d number=%q blank=%q",
			g.Enabled(), g.Width(), g.Number(3), g.Blank())
	}
}

func TestNewGutterForWidthDropsGutterInNarrowBoxes(t *testing.T) {
	tests := []struct {
		total   int
		enabled bool
	}{
		{total: 5, enabled: false},
		{total: 8, enabled: false},
		{total: 12, enabled: false},
		{total: 13, enabled: true},
		{total: 80, enabled: true},
	}
	for _, tt := range tests {
		g := NewGutterForWidth(10, tt.total)
		if g.Enabled() != tt.enabled {
			t.Fatalf("NewGutterForWidth(10, %d) enabled = %v, want %v", tt.total, g.Enabled(), tt.enabled)
		}
	}
	// A wider gutter needs a wider box before it is worth showing.
	if NewGutterForWidth(1000000, 13).Enabled() {
		t.Fatal("seven-digit gutter should be dropped at width 13")
	}
}

func TestGutterWithSeparatorWidensTheColumn(t *testing.T) {
	g := NewGutter(500).WithSeparator(": ")

	if got, want := g.Width(), 6; got != want {
		t.Fatalf("Width() = %d, want %d (four digits plus %q)", got, want, ": ")
	}
	if got, want := g.Plain(12), "  12: "; got != want {
		t.Fatalf("Plain(12) = %q, want %q", got, want)
	}
	if got, want := ansi.Strip(g.Number(12)), "  12: "; got != want {
		t.Fatalf("Number(12) = %q, want %q", got, want)
	}
	if got := len(ansi.Strip(g.Blank())); got != g.Width() {
		t.Fatalf("Blank() is %d cells, want %d", got, g.Width())
	}

	// Past four digits the column grows, and the separator comes along.
	wide := NewGutter(10240).WithSeparator(": ")
	if got, want := wide.Plain(10240), "10240: "; got != want {
		t.Fatalf("Plain(10240) = %q, want %q", got, want)
	}
	if got, want := wide.Width(), 7; got != want {
		t.Fatalf("Width() = %d, want %d", got, want)
	}
}

func TestGutterDefaultSeparatorIsUnchanged(t *testing.T) {
	g := NewGutter(100)
	if got, want := g.Plain(7), "   7 "; got != want {
		t.Fatalf("Plain(7) = %q, want %q", got, want)
	}
	if got, want := g.Width(), 5; got != want {
		t.Fatalf("Width() = %d, want %d", got, want)
	}
}
