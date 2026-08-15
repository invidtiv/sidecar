package ui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestTruncateStartKeepsFilenameEnd(t *testing.T) {
	const path = "internal/plugins/workspace/plugin.go"
	if got := TruncateStart(path, 100); got != path {
		t.Fatalf("wide = %q", got)
	}
	if got := TruncateStart(path, 11); got != "…/plugin.go" {
		t.Fatalf("filename budget = %q, want …/plugin.go", got)
	}
	if got := TruncateStart("ab", 1); got != "…" {
		t.Fatalf("width 1 = %q", got)
	}
	if got := TruncateStart("ab", 0); got != "" {
		t.Fatalf("width 0 = %q", got)
	}
}

func TestElidePathSpansMapRangesOntoTheElidedText(t *testing.T) {
	path := ".claude/skills/create-modal/SKILL.md"
	out, spans := ElidePath(path, 26)
	if out != ".c/s/create-modal/SKILL.md" {
		t.Fatalf("ElidePath = %q", out)
	}
	// "modal" in the original must land on "modal" in the elided text.
	src := strings.Index(path, "modal")
	start, end, ok := MapSpans(spans, src, src+len("modal"))
	if !ok {
		t.Fatal("the range did not survive an elision that kept the text it names")
	}
	if got := out[start:end]; got != "modal" {
		t.Errorf("mapped range spells %q, want %q", got, "modal")
	}

	// A range in a segment that was abbreviated away is reported as gone rather
	// than mapped onto the wrong characters.
	src = strings.Index(path, "skills")
	if _, _, ok := MapSpans(spans, src+2, src+5); ok {
		t.Error("a range whose characters were dropped was mapped anyway")
	}

	// A segment cut in the middle keeps both ends verbatim, and a range in
	// either end still maps onto the characters it names.
	out, spans = ElidePath(path, 22)
	src = strings.Index(path, "modal")
	if start, end, ok := MapSpans(spans, src+2, src+5); ok {
		if got := out[start:end]; got != "dal" {
			t.Errorf("tail range spells %q, want %q in %q", got, "dal", out)
		}
	} else {
		t.Errorf("the tail of a middle-elided segment did not map: %q", out)
	}
}

// A path one cell over budget must lose a cell, not its head. The old fallback
// was all-or-nothing: a 23-cell path in a 22-cell row threw the leading segment
// away, which is the only thing that told `.claude/...` from `.agents/...`.
func TestElidePathDegradesGradually(t *testing.T) {
	const path = ".claude/skills/drag-pane/SKILL.md"
	for width := runewidth.StringWidth(path) - 1; width >= 12; width-- {
		got, _ := ElidePath(path, width)
		if w := runewidth.StringWidth(got); w > width {
			t.Fatalf("width %d: %q is %d cells", width, got, w)
		}
		if !strings.HasPrefix(got, ".c") {
			t.Errorf("width %d: the leading segment was thrown away: %q", width, got)
		}
		if !strings.HasSuffix(got, "SKILL.md") {
			t.Errorf("width %d: the filename did not survive: %q", width, got)
		}
	}
}

// The property that matters in a list: sibling paths must not render as the
// same string. The budgets are the ones a real workspace file pane produces —
// a 100x30 terminal gives a 30-column pane, whose finder rows have 22 cells and
// whose search headers have less.
func TestElidePathKeepsSiblingsDistinguishable(t *testing.T) {
	families := [][]string{{
		".claude/skills/create-modal/SKILL.md",
		".claude/skills/create-plugin/SKILL.md",
		".claude/skills/create-theme/SKILL.md",
		".claude/skills/drag-pane/SKILL.md",
		".claude/skills/ui-features/SKILL.md",
		".agents/skills/drag-pane/SKILL.md",
		".agents/skills/release-sidecar/SKILL.md",
	}, {
		"internal/plugins/workspace/plugin.go",
		"internal/plugins/filebrowser/plugin.go",
		"internal/plugins/gitstatus/plugin.go",
		"internal/plugins/git/plugin.go",
	}}

	// Below about 18 cells the SKILL.md family cannot be told apart without
	// cutting the shared filename, which is a worse trade; these are the widths
	// a real pane actually produces.
	for _, width := range []int{22, 18} {
		for _, family := range families {
			seen := map[string]string{}
			for _, path := range family {
				got, _ := ElidePath(path, width)
				if w := runewidth.StringWidth(got); w > width {
					t.Errorf("width %d: %q is %d cells", width, got, w)
				}
				if other, dup := seen[got]; dup {
					t.Errorf("width %d: %q and %q both render as %q", width, other, path, got)
				}
				seen[got] = path
			}
		}
	}
}

func TestTruncateAnchoredKeepsTheMatchAndTrailingContext(t *testing.T) {
	line := "if p.displayMode == modeSplit && other != nil {"
	start := strings.Index(line, "displayMode")
	runeStart := len([]rune(line[:start]))
	runeEnd := runeStart + len([]rune("displayMode"))

	for _, width := range []int{20, 30, 45} {
		got, hlStart, hlEnd := TruncateAnchored(line, width, runeStart, runeEnd)
		if w := runewidth.StringWidth(got); w > width {
			t.Errorf("width %d: got %d cells: %q", width, w, got)
		}
		if !strings.Contains(got, "display") {
			t.Errorf("width %d: the match was clipped away: %q", width, got)
		}
		runes := []rune(got)
		if hlStart < 0 || hlEnd > len(runes) || hlStart > hlEnd {
			t.Errorf("width %d: highlight %d..%d is outside %q", width, hlStart, hlEnd, got)
		}
	}

	// A line that fits is returned untouched.
	if got, s, e := TruncateAnchored("short", 40, 0, 5); got != "short" || s != 0 || e != 5 {
		t.Errorf("a fitting line was rewritten: %q %d %d", got, s, e)
	}
}

// Match rows differ from each other at the end of the line as often as in the
// middle. Eight hits on the same boilerplate line rendered as eight copies of
// "… `internal/pl…" — the two cells of leading ellipsis bought nothing and the
// window never reached the part that differed. The budgets here are a real
// pane's: a 30-column workspace file pane leaves a match row about 16 cells,
// and the narrower terminal less again.
func TestTruncateAnchoredKeepsRowsDistinguishable(t *testing.T) {
	lines := []string{
		"**File:** `internal/plugins/workspace/plugin.go`",
		"**File:** `internal/plugins/workspace/view_modals.go`",
		"**File:** `internal/plugins/workspace/keys.go`",
		"**File:** `internal/plugins/workspace/doc_search.go`",
	}
	const term = "internal"

	for _, width := range []int{16, 20} {
		seen := map[string]string{}
		for _, line := range lines {
			start := len([]rune(line[:strings.Index(line, term)]))
			got, hlStart, hlEnd := TruncateAnchored(line, width, start, start+len([]rune(term)))
			if w := runewidth.StringWidth(got); w > width {
				t.Errorf("width %d: %q is %d cells", width, got, w)
			}
			if other, dup := seen[got]; dup {
				t.Errorf("width %d: %q and %q both render as %q", width, other, line, got)
			}
			seen[got] = line
			if hl := string([]rune(got)[hlStart:hlEnd]); hl != term {
				t.Errorf("width %d: highlight covers %q, want %q in %q", width, hl, term, got)
			}
		}
	}
}

// A match that fits is never cut, and never pays for leading context it cannot
// afford: at 19 cells "TruncateString" fitted twice over, and the row still
// rendered "…TruncateStrin…".
func TestTruncateAnchoredNeverTruncatesAMatchThatFits(t *testing.T) {
	line := "func TestTruncateStringHandlesWideRunes(t *testing.T) {"
	term := "TruncateString"
	start := len([]rune(line[:strings.Index(line, term)]))

	for _, width := range []int{19, 24, 40} {
		got, hlStart, hlEnd := TruncateAnchored(line, width, start, start+len([]rune(term)))
		if w := runewidth.StringWidth(got); w > width {
			t.Fatalf("width %d: %q is %d cells", width, got, w)
		}
		if !strings.Contains(got, term) {
			t.Errorf("width %d: the match was truncated although it fits: %q", width, got)
		}
		if hl := string([]rune(got)[hlStart:hlEnd]); hl != term {
			t.Errorf("width %d: highlight covers %q, want %q in %q", width, hl, term, got)
		}
	}
}
