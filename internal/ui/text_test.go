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
	if out != ".c…/s…/creat…odal/SKILL.md" {
		t.Fatalf("ElidePath = %q", out)
	}
	// "odal" in the original must land on "odal" in the elided text: the
	// segment was cut in the middle, and the tail it kept is verbatim.
	src := strings.Index(path, "odal")
	start, end, ok := MapSpans(spans, src, src+len("odal"))
	if !ok {
		t.Fatal("the range did not survive an elision that kept the text it names")
	}
	if got := out[start:end]; got != "odal" {
		t.Errorf("mapped range spells %q, want %q", got, "odal")
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
	if start, end, ok := MapSpans(spans, src+3, src+5); ok {
		if got := out[start:end]; got != "al" {
			t.Errorf("tail range spells %q, want %q in %q", got, "al", out)
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

// Every cut an elision makes is marked, at every width. An unmarked
// abbreviation is not a short path, it is a different path: `i/p/t/x.go` names
// directories that do not exist, and a reader has no way to tell it from one
// that does. The narrowest budgets — where a segment collapses to a single
// letter — are where the lie used to be told and where it is most convincing.
func TestElidePathMarksEveryAbbreviation(t *testing.T) {
	paths := []string{
		"internal/plugins/tasks/plugin_test.go",
		".claude/skills/project-switching/SKILL.md",
		"internal/agents/tools/browse/narrow-global-agents.txt",
	}
	for _, path := range paths {
		for width := 8; width < runewidth.StringWidth(path); width++ {
			got, _ := ElidePath(path, width)
			for i, seg := range strings.Split(got, "/") {
				if seg == "…" || seg == "" || strings.Contains(seg, "…") {
					continue
				}
				// A segment drawn verbatim must actually be one of the path's,
				// in the same position from the end.
				src := strings.Split(path, "/")
				if !containsSegment(src, seg) {
					t.Errorf("width %d: %q renders %q, which is not a segment of the path (%d)", width, got, seg, i)
				}
			}
		}
	}
}

func containsSegment(segs []string, want string) bool {
	for _, seg := range segs {
		if seg == want {
			return true
		}
	}
	return false
}

// The property that matters in a list: two different paths must never render
// as the same row. No single-path elision can promise that — every budget
// eventually forces a choice of characters, and the neighbour may have made the
// same one — so the promise belongs to ElidePathSet, which can see the list.
//
// The families are the shapes that actually break it: same depth, shared
// prefixes, names differing only at position N, names differing only in length,
// and the plugin_test.go family that rendered two files as one row on screen.
func TestElidePathSetKeepsRowsDistinct(t *testing.T) {
	cases := []struct {
		name string
		// floor is the narrowest width at which every row of this family can
		// still be told apart. Below it the discriminating characters do not
		// fit beside the shared filename at any spending, and the honest answer
		// is that the list cannot be separated rather than that it was.
		floor  int
		family []string
	}{{
		name:  "same depth, one directory apart, one character in",
		floor: 18,
		family: []string{
			"internal/plugins/tasks/plugin_test.go",
			"internal/plugins/tdmonitor/plugin_test.go",
			"internal/plugins/filebrowser/plugin_test.go",
			"internal/plugins/workspace/plugin_test.go",
			"internal/plugins/gitstatus/plugin_test.go",
		},
	}, {
		name:  "shared prefix, shared filename, two roots",
		floor: 18,
		family: []string{
			".claude/skills/create-modal/SKILL.md",
			".claude/skills/create-plugin/SKILL.md",
			".claude/skills/create-theme/SKILL.md",
			".claude/skills/drag-pane/SKILL.md",
			".claude/skills/ui-features/SKILL.md",
			".agents/skills/drag-pane/SKILL.md",
			".agents/skills/release-sidecar/SKILL.md",
		},
	}, {
		name:  "names differing only in length",
		floor: 20,
		family: []string{
			"internal/plugins/git/plugin.go",
			"internal/plugins/gitstatus/plugin.go",
			"internal/plugins/gitstatusbar/plugin.go",
			"internal/plugins/workspace/plugin.go",
		},
	}, {
		name:  "names differing only at the last character",
		floor: 20,
		family: []string{
			"a/alpha1/notes.md",
			"a/alpha2/notes.md",
			"a/alpha3/notes.md",
		},
	}, {
		name:  "different depths under one root",
		floor: 16,
		family: []string{
			"src/main.go",
			"src/app/main.go",
			"src/app/cmd/main.go",
			"src/app/cmd/run/main.go",
		},
	}, {
		name:  "the same file listed twice is not a collision",
		floor: 12,
		family: []string{
			"internal/plugins/workspace/plugin.go",
			"internal/plugins/workspace/plugin.go",
		},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			widest := 0
			for _, path := range tc.family {
				if w := runewidth.StringWidth(path); w > widest {
					widest = w
				}
			}
			for width := tc.floor; width <= widest+2; width++ {
				got, _ := ElidePathSet(tc.family, width)
				if len(got) != len(tc.family) {
					t.Fatalf("width %d: %d rows for %d paths", width, len(got), len(tc.family))
				}
				seen := map[string]string{}
				for i, row := range got {
					if w := runewidth.StringWidth(row); w > width {
						t.Errorf("width %d: %q is %d cells", width, row, w)
					}
					if other, dup := seen[row]; dup && other != tc.family[i] {
						t.Errorf("width %d: %q and %q both render as %q", width, other, tc.family[i], row)
					}
					seen[row] = tc.family[i]
				}
			}
		})
	}
}

// Seeing the list may never make a row worse: at every width, eliding the set
// together produces at least as many distinct rows as eliding each path alone.
func TestElidePathSetNeverLosesToElidingAlone(t *testing.T) {
	family := []string{
		"internal/plugins/tasks/plugin_test.go",
		"internal/plugins/tdmonitor/plugin_test.go",
		"internal/plugins/tdwatch/plugin_test.go",
		".claude/skills/create-modal/SKILL.md",
		".claude/skills/create-plugin/SKILL.md",
		"docs/guides/active/releasing.md",
		"docs/guides/active/headless-testing.md",
		"README.md",
	}
	for width := 6; width <= 60; width++ {
		alone := map[string]bool{}
		for _, path := range family {
			got, _ := ElidePath(path, width)
			alone[got] = true
		}
		set, _ := ElidePathSet(family, width)
		together := map[string]bool{}
		for _, row := range set {
			together[row] = true
		}
		if len(together) < len(alone) {
			t.Errorf("width %d: %d distinct rows as a set, %d eliding alone: %q",
				width, len(together), len(alone), set)
		}
	}
}

// Spans have to survive the list-aware passes, or a repaired row loses the
// highlight that says why it matched.
func TestElidePathSetKeepsSpansOnTheRepairedRows(t *testing.T) {
	family := []string{
		"internal/plugins/tasks/plugin_test.go",
		"internal/plugins/tdmonitor/plugin_test.go",
	}
	rows, spans := ElidePathSet(family, 22)
	for i, row := range rows {
		src := strings.Index(family[i], "plugin_test.go")
		start, end, ok := MapSpans(spans[i], src, src+len("plugin_test.go"))
		if !ok {
			t.Fatalf("row %d (%q) dropped the filename span", i, row)
		}
		if got := row[start:end]; got != "plugin_test.go" {
			t.Errorf("row %d span spells %q in %q", i, got, row)
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

// A root label is only useful if it always appears. The budget the counts row
// gives it is 28 cells, and this checkout's own directory name is 32 — so the
// label was absent from the Files plugin at every size and in every state while
// the unit tests were green. Every candidate must fit its budget, and none may
// be empty.
func TestShortRootAlwaysNamesTheDirectory(t *testing.T) {
	roots := []string{
		"/Users/marcus/code/sidecar-files-panel-improvements",
		"/Users/marcus/code/sidecar",
		"/var/folders/T/some-temp-dir-with-a-long-name",
		"/",
		"relative-dir",
	}
	for _, root := range roots {
		for width := 40; width >= 1; width-- {
			got := ShortRoot(root, width)
			if got == "" {
				t.Fatalf("root %q at width %d rendered nothing", root, width)
			}
			if w := runewidth.StringWidth(got); w > width {
				t.Fatalf("root %q at width %d rendered %q (%d cells)", root, width, got, w)
			}
		}
	}
	if got := ShortRoot("", 20); got != "" {
		t.Errorf("no root rendered %q, want nothing", got)
	}
}

// The truncated last resort keeps both ends of the name: a project's worktrees
// share their prefix and differ in their suffix, so a head-only cut renders
// them all alike.
func TestShortRootTruncationKeepsSiblingsDistinguishable(t *testing.T) {
	const budget = 28 // what the counts row allows
	siblings := []string{
		"/Users/marcus/code/sidecar-files-panel-improvements",
		"/Users/marcus/code/sidecar-files-panel-regressions",
		"/Users/marcus/code/sidecar-workspace-tabs-and-more",
	}
	seen := map[string]string{}
	for _, root := range siblings {
		got := ShortRoot(root, budget)
		if other, dup := seen[got]; dup {
			t.Errorf("%q and %q both render as %q", other, root, got)
		}
		seen[got] = root
	}
	if got := ShortRoot("/Users/marcus/code/sidecar-files-panel-improvements", budget); !strings.HasSuffix(got, "improvements") {
		t.Errorf("the end of the name did not survive: %q", got)
	}
}

// A shortened directory must never read as a real one. `.c/skills/...` names a
// directory that could exist — this repo has both `.claude` and `.codex` — and
// every other cut on the row is marked, so an unmarked one is the only thing on
// the line a reader has no way to tell from the truth.
func TestElidePathMarksTheAbbreviatedLeadingSegment(t *testing.T) {
	paths := []string{
		".claude/skills/inline-editor/SKILL.md",
		".agents/skills/drag-pane/SKILL.md",
		"internal/plugins/workspace/doc_search.go",
	}
	for _, path := range paths {
		head := path[:strings.Index(path, "/")]
		for width := 30; width >= 12; width-- {
			got, _ := ElidePath(path, width)
			slash := strings.Index(got, "/")
			if slash < 0 {
				continue // too narrow for a directory at all
			}
			rendered := got[:slash]
			if rendered == head || strings.HasPrefix(rendered, "…") {
				continue // whole, or a fallback that has no head to mark
			}
			if !strings.Contains(rendered, "…") {
				t.Errorf("width %d: %q renders its head as %q, which reads as a real directory",
					width, path, rendered)
			}
		}
	}
}

// One list, one elision. Rows that lost their directory entirely and began with
// "…" sat next to rows that had abbreviated theirs, so the column read as three
// different renderings of the same kind of thing. Every row keeps a directory
// in front of the name, and the name is cut from its front.
func TestElidePathRendersAColumnOneWay(t *testing.T) {
	column := []string{
		"internal/plugins/tasks/plugin.go",
		"internal/plugins/gitstatus/plugin.go",
		"docs/plans/implemented/conversations-plugin.md",
		"plans/conversations-plugin.md",
		".claude/skills/create-modal/SKILL.md",
	}
	for _, width := range []int{22, 18} {
		seen := map[string]string{}
		for _, path := range column {
			got, _ := ElidePath(path, width)
			if w := runewidth.StringWidth(got); w > width {
				t.Errorf("width %d: %q is %d cells", width, got, w)
			}
			if strings.HasPrefix(got, "…") {
				t.Errorf("width %d: %q lost its directory: %q", width, path, got)
			}
			if other, dup := seen[got]; dup {
				t.Errorf("width %d: %q and %q both render as %q", width, other, path, got)
			}
			seen[got] = path
		}
	}
}
