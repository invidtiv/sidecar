package projectsearch

import (
	"fmt"
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

// paneBoxes are the boxes a workspace file pane really hands these surfaces.
// A 100x30 terminal leaves the plugin 100x27, whose document pane is 30x25, of
// which the pane's own header row keeps one: 30x24. An 80x24 terminal gives the
// same 30 columns and 18 rows. Deriving the sizes rather than guessing them is
// the difference between a test that fails when the app does and one that
// passes while the user looks at a broken box.
var paneBoxes = []struct {
	name string
	w, h int
}{
	{"100x30", 30, 24},
	{"80x24", 30, 18},
}

// The modal must be exactly as tall as it budgeted for, with its bottom border
// on its last row, in every state.
//
// Measuring newline-separated lines and then rendering into a box that wraps
// them is how the border went missing: the first frame after pressing `f`
// wrapped the placeholder, every wrapping stats line cost another row, and the
// box grew past the surface it was allocated. The assertion is on the final
// rendered height for that reason — an intermediate string measurement is
// exactly what was already wrong.
func TestSearchFillsItsBoxExactlyInEveryState(t *testing.T) {
	states := []struct {
		name  string
		setup func(*State)
	}{
		{"placeholder", func(*State) {}},
		{"searching", func(st *State) {
			st.Query = "internal"
			st.IsSearching = true
		}},
		{"results", func(st *State) {
			st.Query = "internal"
			st.Results = wrappingStatsResults()
			st.Cursor = 1
		}},
		{"nomatch", func(st *State) { st.Query = "zzzzzz" }},
	}

	for _, box := range paneBoxes {
		for _, state := range states {
			s := New("/Users/someone/code/sidecar", 1)
			s.SetFill(true)
			state.setup(s.State)

			out := ansi.Strip(s.View(box.w, box.h, mouse.NewHandler()))
			lines := strings.Split(out, "\n")
			if len(lines) != box.h {
				t.Errorf("%s/%s: %d rows in a %d-row box:\n%s", box.name, state.name, len(lines), box.h, out)
			}
			for i, line := range lines {
				if w := ansi.StringWidth(line); w != box.w {
					t.Errorf("%s/%s: row %d is %d cells, want %d", box.name, state.name, i, w, box.w)
				}
			}
			last := lines[len(lines)-1]
			if !strings.HasPrefix(last, "╰") || !strings.HasSuffix(last, "╯") {
				t.Errorf("%s/%s: the last row is not the bottom border: %q", box.name, state.name, last)
			}
		}
	}
}

// wrappingStatsResults is a result set whose counts line is longer than a
// narrow pane's content column, which is the state the missing border showed up
// in most often.
func wrappingStatsResults() []SearchFileResult {
	var results []SearchFileResult
	for i := range 117 {
		results = append(results, SearchFileResult{
			Path: fmt.Sprintf("internal/plugins/workspace/file%d.go", i),
			Matches: []SearchMatch{
				{LineNo: 50, LineText: "**File:** `internal/plugins/workspace/plugin.go`", ColStart: 11, ColEnd: 19},
				{LineNo: 62, LineText: "**File:** `internal/plugins/workspace/view_modals.go`", ColStart: 11, ColEnd: 19},
			},
		})
	}
	return results
}

// Rows that share a long prefix must stay distinguishable in a real pane, both
// the file headers and the match lines under them.
func TestNarrowRowsStayDistinguishable(t *testing.T) {
	paths := []string{
		".claude/skills/create-modal/SKILL.md",
		".claude/skills/create-plugin/SKILL.md",
		".claude/skills/create-theme/SKILL.md",
		".claude/skills/ui-features/SKILL.md",
		".claude/skills/drag-pane/SKILL.md",
		".agents/skills/release-sidecar/SKILL.md",
		".agents/skills/drag-pane/SKILL.md",
	}
	// A 30-column pane leaves 24 cells of modal content, of which the icon and
	// the match count take six; 40 is a wider pane. Below about 16 cells of
	// path the family cannot be told apart at all without cutting the shared
	// filename, which is a worse trade.
	files := make([]SearchFileResult, len(paths))
	for i, path := range paths {
		files[i] = SearchFileResult{Path: path, Matches: []SearchMatch{{}}}
	}
	// The headers are fitted as a list, which is the only way the property can
	// hold: one header cannot know what the header above it came out as.
	for _, width := range []int{24, 40} {
		fitted := elideFileHeaders(files, width)
		seen := map[string]string{}
		for i, file := range files {
			row := ansi.Strip(renderFileHeader(file, fitted[i], false, false, width))
			if ansi.StringWidth(row) > width {
				t.Errorf("width %d: header is %d cells: %q", width, ansi.StringWidth(row), row)
			}
			if other, dup := seen[row]; dup {
				t.Errorf("width %d: %q and %q both render as %q", width, other, paths[i], row)
			}
			seen[row] = paths[i]
		}
	}
}

// The eight rows the proof run found identical: same file, same boilerplate
// line, differing only in the filename at the end of it.
func TestNarrowMatchRowsStayDistinguishable(t *testing.T) {
	lines := []string{
		"**File:** `internal/plugins/workspace/plugin.go`",
		"**File:** `internal/plugins/workspace/view_modals.go`",
		"**File:** `internal/plugins/workspace/keys.go`",
		"**File:** `internal/plugins/workspace/doc_search.go`",
	}
	gutter := matchGutter([]SearchFileResult{{Matches: []SearchMatch{{LineNo: 90}}}})

	for _, width := range []int{24, 40} {
		seen := map[string]string{}
		for i, line := range lines {
			start := strings.Index(line, "internal")
			match := SearchMatch{LineNo: 50 + i, LineText: line, ColStart: start, ColEnd: start + len("internal")}
			row := ansi.Strip(renderMatchLine(match, false, false, width, gutter))
			if ansi.StringWidth(row) > width {
				t.Errorf("width %d: row is %d cells: %q", width, ansi.StringWidth(row), row)
			}
			// The line number differs between rows; compare what it says about
			// the line itself.
			text := strings.TrimSpace(row[strings.Index(row, ": ")+2:])
			if other, dup := seen[text]; dup {
				t.Errorf("width %d: %q and %q both render as %q", width, other, line, text)
			}
			seen[text] = line
			if !strings.Contains(row, "internal") {
				t.Errorf("width %d: the match itself was clipped away: %q", width, row)
			}
		}
	}
}

// Selection is marked the way the finder marks it, on both kinds of row. A
// background highlight alone is not enough: a proof run at a real terminal
// concluded that clicking a result did nothing, when the click had in fact
// moved the cursor onto the row it clicked. The gutter is the same two cells
// whether or not the row is selected, so nothing shifts as the cursor moves.
func TestSelectedRowsCarryTheFindersMarker(t *testing.T) {
	file := SearchFileResult{Path: "internal/app/update.go", Matches: []SearchMatch{
		{LineNo: 12, LineText: "return p.update(msg)", ColStart: 9, ColEnd: 15},
	}}

	for _, width := range []int{24, 40, 80} {
		header := ansi.Strip(renderFileHeader(file, file.Path, true, false, width))
		if !strings.HasPrefix(header, "> ") {
			t.Errorf("width %d: selected header does not carry the marker: %q", width, header)
		}
		if idle := ansi.Strip(renderFileHeader(file, file.Path, false, false, width)); !strings.HasPrefix(idle, "  ") {
			t.Errorf("width %d: unselected header does not hold the gutter open: %q", width, idle)
		}

		gutter := matchGutter([]SearchFileResult{file})
		match := ansi.Strip(renderMatchLine(file.Matches[0], true, false, width, gutter))
		if !strings.HasPrefix(match, "> ") {
			t.Errorf("width %d: selected match line does not carry the marker: %q", width, match)
		}
		if idle := ansi.Strip(renderMatchLine(file.Matches[0], false, false, width, gutter)); !strings.HasPrefix(idle, "  ") {
			t.Errorf("width %d: unselected match line does not hold the gutter open: %q", width, idle)
		}
	}
}

// A pane's search is rooted at that pane's directory, which in a global
// workspace is often not the checkout on screen. The box says which one it is,
// in every state — including the one where it found nothing.
func TestSearchNamesTheRootItIsSearching(t *testing.T) {
	for _, box := range paneBoxes {
		s := New("/Users/someone/code/sidecar", 1)
		s.SetFill(true)
		s.State.Query = "zzzz"

		out := ansi.Strip(s.View(box.w, box.h, mouse.NewHandler()))
		if !strings.Contains(out, "sidecar") {
			t.Errorf("%s: the root is not named anywhere in:\n%s", box.name, out)
		}
	}
}

// A label that disappears is worse than a label that is short. This checkout is
// named "sidecar-files-panel-improvements" — 32 cells against the counts row's
// 28 — and the root vanished from the box entirely at every size and in every
// state while the goldens, rooted at "/golden/sidecar", stayed green.
func TestSearchNamesALongRootToo(t *testing.T) {
	const root = "/Users/someone/code/sidecar-files-panel-improvements"
	for _, size := range []struct{ w, h int }{{200, 50}, {100, 30}, {56, 20}} {
		s := New(root, 1)
		s.State.Query = "zzzz"
		out := ansi.Strip(s.View(size.w, size.h, mouse.NewHandler()))
		if !strings.Contains(out, "sidecar-") {
			t.Errorf("%dx%d: the long root is not named anywhere in:\n%s", size.w, size.h, out)
		}
	}
}

// One match in one file is not "1 matches in 1 files".
func TestCountsLinePluralises(t *testing.T) {
	s := New("/root", 1)
	s.State.Query = "update"
	s.State.Results = []SearchFileResult{{
		Path:    "internal/app/update.go",
		Matches: []SearchMatch{{LineNo: 12, LineText: "update", ColStart: 0, ColEnd: 6}},
	}}

	got := s.countsText(60)
	if !strings.Contains(got, "1 match in 1 file") || strings.Contains(got, "matches") || strings.Contains(got, "files") {
		t.Errorf("counts line reads %q", got)
	}
}

// A capped result set says it is capped, at every width, and never renders as
// "1000/107" — which reads as "item 1000 of 107", the one thing the row never
// means.
func TestCountsLineSignalsTheCap(t *testing.T) {
	s := New("/root", 1)
	s.State.Query = "e"
	s.State.Truncated = true
	s.State.Results = []SearchFileResult{{
		Path:    "internal/app/update.go",
		Matches: []SearchMatch{{LineNo: 12, LineText: "e", ColStart: 0, ColEnd: 1}},
	}}

	for _, width := range []int{60, 30, 20, 12, 8} {
		got := s.countsText(width)
		if !strings.Contains(got, "+") {
			t.Errorf("width %d: counts read %q, which does not say the set was cut", width, got)
		}
		if strings.Count(got, "/") > 0 && !strings.Contains(got, "  ") {
			t.Errorf("width %d: counts read %q, which reads as a position", width, got)
		}
	}
}

// The whole-word chip is \b, not a doubled backslash.
func TestWholeWordChipLabel(t *testing.T) {
	s := New("/root", 1)
	out := ansi.Strip(s.View(80, 24, mouse.NewHandler()))
	if strings.Contains(out, `\\b`) {
		t.Errorf("the whole-word chip is doubled:\n%s", out)
	}
	if !strings.Contains(out, `\b`) {
		t.Errorf("the whole-word chip is missing:\n%s", out)
	}
}

// A file header on the last row with no match under it spends a row saying a
// file has hits without showing one.
func TestNoOrphanedFileHeader(t *testing.T) {
	s := New("/root", 1)
	s.SetFill(true)
	s.State.Query = "update"
	for i := range 12 {
		s.State.Results = append(s.State.Results, SearchFileResult{
			Path: fmt.Sprintf("internal/app/file%d.go", i),
			Matches: []SearchMatch{
				{LineNo: 1, LineText: "update()", ColStart: 0, ColEnd: 6},
				{LineNo: 2, LineText: "update()", ColStart: 0, ColEnd: 6},
			},
		})
	}
	s.State.Cursor = 0

	for h := 12; h <= 26; h++ {
		out := ansi.Strip(s.View(60, h, mouse.NewHandler()))
		lines := strings.Split(out, "\n")
		for i, line := range lines {
			if !strings.Contains(line, "▼ ") {
				continue
			}
			// The row under a header is either another row of the list or the
			// blank the list pads with — never the box's own bottom.
			if i+1 < len(lines) && strings.Contains(lines[i+1], "╰") {
				t.Errorf("height %d: a file header is the last row of the list:\n%s", h, out)
			}
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

		if empty != searching || empty != results {
			t.Errorf("%dx%d: box jitters: empty=%v searching=%v results=%v",
				size.w, size.h, empty, searching, results)
		}
		// A dead end is the one state that shrinks: a query matching nothing
		// keeps a one-row list rather than a page of rows reserved for results
		// that are not coming.
		if nomatch[0] != empty[0] {
			t.Errorf("%dx%d: a dead-end query changed the box width: %v vs %v", size.w, size.h, nomatch, empty)
		}
		if nomatch[1] >= empty[1] {
			t.Errorf("%dx%d: a dead-end query kept the tall box: nomatch=%v answering=%v",
				size.w, size.h, nomatch, empty)
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
