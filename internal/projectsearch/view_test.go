package projectsearch

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
)

// TestMatchLineNumbersFitTheWidestLineNumber pins the fix for a five-digit
// match wrapping onto a second row: the number column sizes itself to the
// widest number in the result set, and every row in the set uses that width.
func TestMatchLineNumbersFitTheWidestLineNumber(t *testing.T) {
	results := []SearchFileResult{{
		Path: "big.go",
		Matches: []SearchMatch{
			{LineNo: 7, LineText: "seven", ColStart: 0, ColEnd: 5},
			{LineNo: 10240, LineText: "ten thousand", ColStart: 0, ColEnd: 3},
		},
	}}
	gutter := matchGutter(results)

	for _, m := range results[0].Matches {
		line := renderMatchLine(m, false, false, 60, gutter)
		if strings.Contains(line, "\n") {
			t.Fatalf("line %d wrapped onto a second row: %q", m.LineNo, line)
		}
		plain := ansi.Strip(line)
		if !strings.Contains(plain, "10240: ") && m.LineNo == 10240 {
			t.Fatalf("five-digit number was clipped: %q", plain)
		}
	}

	seven := ansi.Strip(renderMatchLine(results[0].Matches[0], false, false, 60, gutter))
	if !strings.Contains(seven, "    7: ") {
		t.Fatalf("short number was not padded to the column width: %q", seven)
	}
}

// TestOrdinaryLineNumbersKeepTheFourDigitColumn guards the "looks exactly as it
// does today" half of the adaptive width.
func TestOrdinaryLineNumbersKeepTheFourDigitColumn(t *testing.T) {
	results := []SearchFileResult{{
		Path:    "small.go",
		Matches: []SearchMatch{{LineNo: 12, LineText: "twelve", ColStart: 0, ColEnd: 6}},
	}}

	got := ansi.Strip(renderMatchLine(results[0].Matches[0], false, false, 60, matchGutter(results)))
	if !strings.HasPrefix(got, "      12: ") {
		t.Fatalf("four-digit column changed: %q", got)
	}
}

// TestWideLineNumbersDoNotOverflowTheModal renders the whole surface with
// five-digit matches and asserts nothing spills past the modal's width.
func TestWideLineNumbersDoNotOverflowTheModal(t *testing.T) {
	s := New(t.TempDir(), 0)
	s.State.Query = "update"
	s.State.Results = []SearchFileResult{{
		Path:    "internal/app/update.go",
		Matches: []SearchMatch{{LineNo: 98765, LineText: "\treturn p.update(msg)", ColStart: 10, ColEnd: 16}},
	}}
	s.State.Cursor = s.State.FirstMatchIndex()

	out := s.View(100, 30, mouse.NewHandler())
	widest := 0
	for _, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > widest {
			widest = w
		}
	}
	if widest > 100 {
		t.Fatalf("modal is %d cells wide in a 100-cell surface", widest)
	}
	if !strings.Contains(ansi.Strip(out), "98765: ") {
		t.Fatalf("five-digit line number missing from the render:\n%s", ansi.Strip(out))
	}
}
