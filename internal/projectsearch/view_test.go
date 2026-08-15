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

// A narrow pane must still show what a match line says. Centring the window on
// the match clipped both sides down to the query itself, so every row read as
// the same handful of characters; anchoring keeps the match and the text after
// it, and gives up the leading context first.
func TestNarrowMatchLinesKeepTheMatchAndWhatFollows(t *testing.T) {
	gutter := matchGutter([]SearchFileResult{{Matches: []SearchMatch{{LineNo: 27}}}})
	line := "\t\t\tif p.displayMode == modeSplit && other != nil {"
	match := SearchMatch{
		LineNo:   27,
		LineText: line,
		ColStart: strings.Index(line, "displayMode"),
		ColEnd:   strings.Index(line, "displayMode") + len("displayMode"),
	}

	for _, width := range []int{30, 45} {
		got := ansi.Strip(renderMatchLine(match, false, false, width, gutter))
		if ansi.StringWidth(got) > width {
			t.Errorf("width %d: row is %d cells: %q", width, ansi.StringWidth(got), got)
		}
		if !strings.Contains(got, "displayMode") {
			t.Errorf("width %d: the match itself was clipped away: %q", width, got)
		}
		// Something after the match survived: the row says more than the query.
		after := got[strings.Index(got, "displayMode")+len("displayMode"):]
		if strings.TrimSpace(strings.Trim(after, "…")) == "" {
			t.Errorf("width %d: nothing after the match survived: %q", width, got)
		}
	}
}

// Rows that share a long prefix must stay distinguishable in a narrow pane.
func TestNarrowFileHeadersStayDistinguishable(t *testing.T) {
	paths := []string{
		".claude/skills/create-modal/SKILL.md",
		".claude/skills/create-plugin/SKILL.md",
		".claude/skills/ui-features/SKILL.md",
		".agents/skills/release-sidecar/SKILL.md",
	}
	for _, width := range []int{30, 45} {
		seen := map[string]string{}
		for _, path := range paths {
			row := ansi.Strip(renderFileHeader(SearchFileResult{Path: path, Matches: []SearchMatch{{}}}, false, false, width))
			if ansi.StringWidth(row) > width {
				t.Errorf("width %d: header is %d cells: %q", width, ansi.StringWidth(row), row)
			}
			if other, dup := seen[row]; dup {
				t.Errorf("width %d: %q and %q both render as %q", width, other, path, row)
			}
			seen[row] = path
		}
	}
}

// The box must not breathe as the user types.
func TestSearchHeightIsStableAcrossStates(t *testing.T) {
	for _, size := range []struct{ w, h int }{{100, 30}, {80, 24}, {56, 20}} {
		s := New("/root", 1)
		size := size
		measure := func() [2]int {
			out := s.View(size.w, size.h, mouse.NewHandler())
			widest := 0
			for _, line := range strings.Split(out, "\n") {
				if w := ansi.StringWidth(line); w > widest {
					widest = w
				}
			}
			return [2]int{widest, len(strings.Split(out, "\n"))}
		}

		empty := measure()
		s.State.Query = "update"
		s.State.IsSearching = true
		searching := measure()

		s.State.IsSearching = false
		s.State.Results = []SearchFileResult{{
			Path:    "internal/app/update.go",
			Matches: []SearchMatch{{LineNo: 12, LineText: "update", ColStart: 0, ColEnd: 6}},
		}}
		results := measure()

		s.State.Results = nil
		s.State.Query = "zzzz"
		nomatch := measure()

		if empty != searching || empty != results || empty != nomatch {
			t.Errorf("%dx%d: box jitters: empty=%v searching=%v results=%v nomatch=%v",
				size.w, size.h, empty, searching, results, nomatch)
		}
	}
}

// Filling, the search is exactly the box a host handed it.
func TestSearchFillModeIsExactlyTheBox(t *testing.T) {
	for _, size := range []struct{ w, h int }{{56, 20}, {80, 24}, {40, 12}, {30, 8}} {
		s := New("/root", 1)
		s.SetFill(true)
		out := s.View(size.w, size.h, mouse.NewHandler())
		lines := strings.Split(out, "\n")
		widest := 0
		for _, line := range lines {
			if w := ansi.StringWidth(line); w > widest {
				widest = w
			}
		}
		if widest != size.w || len(lines) != size.h {
			t.Errorf("fill at %dx%d rendered %dx%d", size.w, size.h, widest, len(lines))
		}
	}
}
