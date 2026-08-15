package filefind

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

func testFinder() *Finder {
	f := NewFinder(&Cache{Files: []string{"main.go", "src/app.go", "src/app_test.go"}, OK: true}, "/root", 7)
	f.Open()
	return f
}

func typeQuery(f *Finder, s string) {
	for _, r := range s {
		f.HandleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestFinderTypingFiltersAndEnterOpens(t *testing.T) {
	f := testFinder()
	typeQuery(f, "app")

	if len(f.Matches()) == 0 {
		t.Fatal("no matches for \"app\"")
	}
	if got := f.Query(); got != "app" {
		t.Fatalf("query = %q, want %q", got, "app")
	}

	res, _ := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Outcome != OutcomeOpen {
		t.Fatalf("enter outcome = %v, want OutcomeOpen", res.Outcome)
	}
	if res.Path != "src/app.go" && res.Path != "src/app_test.go" {
		t.Errorf("opened %q, want one of the app files", res.Path)
	}
	if f.Query() != "" || len(f.Matches()) != 0 {
		t.Error("opening a file should clear the finder")
	}
}

func TestFinderEscCancels(t *testing.T) {
	f := testFinder()
	typeQuery(f, "app")

	res, _ := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if res.Outcome != OutcomeCancelled {
		t.Fatalf("esc outcome = %v, want OutcomeCancelled", res.Outcome)
	}
	if f.Query() != "" {
		t.Errorf("query survived cancel: %q", f.Query())
	}
}

func TestFinderEnterWithoutMatchesDoesNothing(t *testing.T) {
	f := testFinder()
	typeQuery(f, "zzzzzz")

	res, _ := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Outcome != OutcomeNone {
		t.Fatalf("enter on no matches = %v, want OutcomeNone", res.Outcome)
	}
}

func TestFinderNavigationClampsToMatches(t *testing.T) {
	f := testFinder()

	f.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if f.Cursor() != 0 {
		t.Errorf("cursor went above the first match: %d", f.Cursor())
	}
	for i := 0; i < 10; i++ {
		f.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if want := len(f.Matches()) - 1; f.Cursor() != want {
		t.Errorf("cursor = %d, want %d", f.Cursor(), want)
	}
}

func TestFinderUpdateAppliesScanAndRejectsStrangers(t *testing.T) {
	f := NewFinder(nil, "/root", 7)
	f.Open()

	// A scan for another epoch is somebody else's answer.
	f.Update(ScannedMsg{Files: []string{"stale.go"}, Epoch: 6})
	if len(f.Cache.Files) != 0 {
		t.Errorf("stale scan applied: %v", f.Cache.Files)
	}

	// So is a directory scan, which belongs to path auto-complete.
	f.Update(ScannedMsg{Dirs: true, Files: []string{"src"}, Epoch: 7})
	if len(f.Cache.Files) != 0 {
		t.Errorf("directory scan applied to the file finder: %v", f.Cache.Files)
	}

	f.Update(ScannedMsg{Files: []string{"main.go", "src/app.go"}, Epoch: 7})
	if len(f.Cache.Files) != 2 {
		t.Fatalf("cache = %v, want 2 files", f.Cache.Files)
	}
	if len(f.Matches()) != 2 {
		t.Errorf("matches were not recomputed against the landed scan: %v", f.Matches())
	}
}

// rowRegions returns the registered row regions in row order, ignoring the
// modal's own backdrop and body absorbers.
func rowRegions(t *testing.T, handler *mouse.Handler) []mouse.Region {
	t.Helper()
	var rows []mouse.Region
	for _, r := range handler.HitMap.Regions() {
		if _, ok := ParseItemID(r.ID); ok {
			rows = append(rows, r)
		}
	}
	return rows
}

func TestFinderDoubleClickOpensTheRowUnderIt(t *testing.T) {
	f := testFinder()
	handler := mouse.NewHandler()
	f.View(100, 30, handler)

	regions := rowRegions(t, handler)
	if len(regions) != len(f.Matches()) {
		t.Fatalf("registered %d regions for %d matches", len(regions), len(f.Matches()))
	}

	// Click the second row, then double-click it.
	r := regions[1]
	click := tea.MouseClickMsg{X: r.Rect.X, Y: r.Rect.Y, Button: tea.MouseLeft}
	f.HandleMouse(click, handler)
	if f.Cursor() != 1 {
		t.Fatalf("cursor = %d after clicking the second row", f.Cursor())
	}

	res, _ := f.HandleMouse(click, handler)
	if res.Outcome != OutcomeOpen {
		t.Fatalf("double click outcome = %v, want OutcomeOpen", res.Outcome)
	}
}

// TestFinderClickLandsOnTheRowItIsDrawnOn composites the finder the way a host
// does and asserts that the row region at a screen row really is the row drawn
// there. The hand-rolled modal this replaced registered rows at fixed
// coordinates while the caller drew the box vertically centred, so on a short
// screen every click missed by the centring offset.
func TestFinderClickLandsOnTheRowItIsDrawnOn(t *testing.T) {
	for _, height := range []int{40, 30, 24, 18} {
		f := testFinder()
		handler := mouse.NewHandler()

		width := 100
		box := f.View(width, height, handler)
		background := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", width)+"\n", height), "\n")
		screen := strings.Split(ui.OverlayModal(background, box, width, height), "\n")

		rows := rowRegions(t, handler)
		if len(rows) != len(f.Matches()) {
			t.Fatalf("height %d: %d row regions for %d matches", height, len(rows), len(f.Matches()))
		}

		for i, r := range rows {
			if r.Rect.Y < 0 || r.Rect.Y >= len(screen) {
				t.Fatalf("height %d: row %d registered off screen at y=%d", height, i, r.Rect.Y)
			}
			drawn := ansi.Strip(screen[r.Rect.Y])
			if want := f.Matches()[i].Path; !strings.Contains(drawn, want) {
				t.Fatalf("height %d: row %d registered at y=%d, which draws %q, not %q",
					height, i, r.Rect.Y, strings.TrimSpace(drawn), want)
			}

			f.SetCursor(0)
			f.HandleMouse(tea.MouseClickMsg{X: r.Rect.X, Y: r.Rect.Y, Button: tea.MouseLeft}, handler)
			if f.Cursor() != i {
				t.Fatalf("height %d: clicking the row drawn at y=%d selected %d, want %d",
					height, r.Rect.Y, f.Cursor(), i)
			}
		}
	}
}

func TestFinderViewSizesToWhatItIsGiven(t *testing.T) {
	f := testFinder()

	wide := f.View(120, 40, mouse.NewHandler())
	narrow := f.View(50, 14, mouse.NewHandler())

	if lineWidth(wide) <= lineWidth(narrow) {
		t.Errorf("modal did not shrink with the surface: %d vs %d", lineWidth(wide), lineWidth(narrow))
	}
	if lineWidth(narrow) > 50 {
		t.Errorf("modal is %d cells wide in a 50-cell surface", lineWidth(narrow))
	}
}

func lineWidth(s string) int {
	widest := 0
	for _, line := range strings.Split(s, "\n") {
		if w := ansi.StringWidth(line); w > widest {
			widest = w
		}
	}
	return widest
}

func TestElidePathKeepsWhatDiffers(t *testing.T) {
	tests := []struct {
		path  string
		width int
		want  string
	}{
		// Outermost directories are spent first, and only as far as the budget
		// demands: the parent keeps both of its ends, which is where sibling
		// names differ.
		// Every abbreviated segment carries an ellipsis, not only the leading
		// one: ".c" alone reads as a directory that could exist next to
		// ".claude" and ".codex", and "s" reads as one next to "skills".
		{".claude/skills/create-modal/SKILL.md", 26, ".c…/s…/creat…odal/SKILL.md"},
		{".claude/skills/create-modal/SKILL.md", 22, ".c…/s…/cre…al/SKILL.md"},
		{"internal/plugins/filebrowser/view.go", 23, "i…/p…/file…wser/view.go"},
		// Too deep to abbreviate its way down: the middle collapses, and the
		// leading segment and the filename are what survive.
		{"a/very/deeply/nested/path/that/goes/on/file.go", 20, "a/…/on/file.go"},
		{"a/very/deeply/nested/path/that/goes/on/file.go", 12, "a/…/file.go"},
		// The filename comes before the head: a name cut in half places nothing.
		{".claude/skills/create-modal/SKILL.md", 12, ".c…/SKILL.md"},
		{"dir/an_extremely_long_filename_indeed.go", 12, "…e_indeed.go"},
		{"short.go", 20, "short.go"},
		{"no_directory_but_far_too_long.go", 10, "…o_long.go"},
	}
	for _, tt := range tests {
		got, _ := ui.ElidePath(tt.path, tt.width)
		if got != tt.want {
			t.Errorf("ElidePath(%q, %d) = %q, want %q", tt.path, tt.width, got, tt.want)
		}
		if ansi.StringWidth(got) > tt.width {
			t.Errorf("ElidePath(%q, %d) = %q, which is %d cells wide",
				tt.path, tt.width, got, ansi.StringWidth(got))
		}
	}
}

// TestNarrowRowsStayDistinguishable is the property that actually matters in a
// tight pane: two different files must never render as the same row.
//
// It is asserted against the drawn list, not against the elision of one path,
// because the list is where the property lives. Twice now a per-path test has
// passed while the pane on screen showed a pair of byte-identical rows: no
// elision of `internal/plugins/tasks/plugin_test.go` can know that
// `internal/plugins/tdmonitor/plugin_test.go` is the row above it. The finder
// fits its whole visible window as a set (ui.ElidePathSet) for exactly that
// reason, and this test drives the real render so a future change that goes
// back to per-row fitting fails here rather than on a screenshot.
//
// The widths are the ones the app really produces: a 100x30 terminal gives a
// 30-column workspace file pane, whose rows have 22 cells of path budget; 200x50
// gives a pane wide enough that nothing should be abbreviated at all.
func TestNarrowRowsStayDistinguishable(t *testing.T) {
	families := [][]string{{
		".claude/skills/create-modal/SKILL.md",
		".claude/skills/create-plugin/SKILL.md",
		".claude/skills/create-theme/SKILL.md",
		".claude/skills/ui-features/SKILL.md",
		".claude/skills/drag-pane/SKILL.md",
		".agents/skills/release-sidecar/SKILL.md",
		".agents/skills/drag-pane/SKILL.md",
		".agents/skills/shell-integration/SKILL.md",
	}, {
		// The family that shipped two identical rows to a real screen.
		"internal/plugins/tasks/plugin_test.go",
		"internal/plugins/tdmonitor/plugin_test.go",
		"internal/plugins/filebrowser/plugin_test.go",
		"internal/plugins/gitstatus/plugin_test.go",
		"internal/plugins/workspace/plugin_test.go",
		"internal/plugins/git/plugin_test.go",
	}}

	// 30x24 is the pane a 100x30 terminal gives; 60x40 is the roomy one.
	for _, size := range []struct{ w, h int }{{30, 24}, {60, 40}} {
		for _, family := range families {
			rows := drawnResultRows(t, family, size.w, size.h)
			if len(rows) != len(family) {
				t.Fatalf("%dx%d: drew %d rows for %d files: %q", size.w, size.h, len(rows), len(family), rows)
			}
			seen := map[string]string{}
			for i, row := range rows {
				if other, dup := seen[row]; dup {
					t.Errorf("%dx%d: %q and %q both render as %q", size.w, size.h, other, family[i], row)
				}
				seen[row] = family[i]
			}
		}
	}
}

// Nothing a row draws may name a directory that is not there. An abbreviation
// without its ellipsis is not a shorter path, it is a different one, and the
// narrowest pane is where that used to happen.
func TestNarrowRowsMarkEveryAbbreviation(t *testing.T) {
	family := []string{
		"internal/plugins/tasks/plugin_test.go",
		"internal/plugins/tdmonitor/plugin_test.go",
		".claude/skills/project-switching/SKILL.md",
	}
	real := map[string]bool{}
	for _, path := range family {
		for _, seg := range strings.Split(path, "/") {
			real[seg] = true
		}
	}
	for _, row := range drawnResultRows(t, family, 30, 24) {
		for _, seg := range strings.Split(row, "/") {
			if seg == "" || seg == "\u2026" || strings.Contains(seg, "\u2026") {
				continue
			}
			if !real[seg] {
				t.Errorf("row %q draws %q, which is not a directory in the list", row, seg)
			}
		}
	}
}

// drawnResultRows renders the finder at a real pane size against a file list
// and returns the result rows as they appear, stripped of styling and of the
// selection gutter.
func drawnResultRows(t *testing.T, files []string, width, height int) []string {
	t.Helper()
	finder := NewFinder(&Cache{Files: files, OK: true}, "/tmp/project", 0)
	// A query every path matches, so the window holds the whole family.
	finder.SetQuery(".")
	if len(finder.Matches()) != len(files) {
		finder.SetQuery("")
	}
	if len(finder.Matches()) != len(files) {
		t.Fatalf("the query matched %d of %d files", len(finder.Matches()), len(files))
	}
	view := finder.View(width, height, nil)

	var rows []string
	bases := map[string]bool{}
	for _, f := range files {
		bases[f[strings.LastIndex(f, "/")+1:]] = true
	}
	for _, line := range strings.Split(view, "\n") {
		plain := strings.TrimRight(ansi.Strip(line), " ")
		trimmed := strings.TrimLeft(plain, " │")
		trimmed = strings.TrimPrefix(trimmed, "> ")
		trimmed = strings.TrimRight(trimmed, "│ ")
		// A result row ends with a filename from the list; the query row and
		// the counts row do not.
		if !bases[trimmed[strings.LastIndex(trimmed, "/")+1:]] {
			continue
		}
		rows = append(rows, trimmed)
	}
	return rows
}

// A path that fits is not touched: the 200x50 pane shows whole paths, where the
// old one-step abbreviation crushed `shell-integration` to `s` to save cells it
// did not need.
func TestWidePaneDoesNotAbbreviateWhatFits(t *testing.T) {
	const path = ".agents/skills/shell-integration/references/tmux-notes.md"
	row := ansi.Strip(RenderMatch(Match{Path: path}, 60))
	if !strings.Contains(row, path) {
		t.Errorf("a path that fits was abbreviated: %q", row)
	}
}

// paneBoxes are the boxes a workspace file pane really hands the finder: a
// 100x30 terminal leaves a 30x24 box below the pane's header row, an 80x24
// terminal a 30x18 one.
var paneBoxes = []struct {
	name string
	w, h int
}{
	{"100x30", 30, 24},
	{"80x24", 30, 18},
}

// Filling a pane, the box is exactly the box it was given, with its bottom
// border on the last row, in every state. Heights measured before the box wraps
// its content are heights the border falls off the bottom of.
func TestFinderFillsItsBoxExactlyInEveryState(t *testing.T) {
	states := []struct {
		name  string
		setup func(*Finder, *Cache)
	}{
		{"placeholder", func(*Finder, *Cache) {}},
		{"scanning", func(_ *Finder, c *Cache) { c.Scanning = true; c.Files = nil }},
		{"results", func(f *Finder, _ *Cache) { f.SetQuery("skill") }},
		{"nomatch", func(f *Finder, _ *Cache) { f.SetQuery("zzzzzz") }},
		{"error", func(_ *Finder, c *Cache) { c.ErrText = "scan timed out after 5s" }},
	}

	for _, box := range paneBoxes {
		for _, state := range states {
			cache := &Cache{Files: distinguishablePaths(), OK: true}
			f := NewFinder(cache, "/Users/someone/code/sidecar", 1)
			f.Open()
			f.SetFill(true)
			state.setup(f, cache)

			out := ansi.Strip(f.View(box.w, box.h, mouse.NewHandler()))
			lines := strings.Split(out, "\n")
			if len(lines) != box.h {
				t.Errorf("%s/%s: %d rows in a %d-row box:\n%s", box.name, state.name, len(lines), box.h, out)
			}
			for i, line := range lines {
				if w := ansi.StringWidth(line); w != box.w {
					t.Errorf("%s/%s: row %d is %d cells, want %d", box.name, state.name, i, w, box.w)
				}
			}
			if last := lines[len(lines)-1]; !strings.HasPrefix(last, "╰") || !strings.HasSuffix(last, "╯") {
				t.Errorf("%s/%s: the last row is not the bottom border: %q", box.name, state.name, last)
			}
		}
	}
}

// The finder says which directory it walked and how many files it found, in
// every state: a pane-rooted finder is often not rooted where the user is
// reading, and "No matches" about an unnamed directory cannot be answered.
func TestFinderNamesItsRootAndFileCount(t *testing.T) {
	for _, box := range paneBoxes {
		f := NewFinder(&Cache{Files: distinguishablePaths(), OK: true}, "/Users/someone/code/sidecar", 1)
		f.Open()
		f.SetFill(true)
		f.SetQuery("zzzzzz")

		out := ansi.Strip(f.View(box.w, box.h, mouse.NewHandler()))
		if !strings.Contains(out, "sidecar") {
			t.Errorf("%s: the root is not named anywhere in:\n%s", box.name, out)
		}
		if !strings.Contains(out, "files") {
			t.Errorf("%s: the file count is missing from:\n%s", box.name, out)
		}
	}
}

// A root too long for the counts row's budget must still be named. This
// checkout is "sidecar-files-panel-improvements" — 32 cells against 28 — and
// the label was absent from the Files plugin at every size while the tests,
// rooted at short paths, stayed green.
func TestFinderNamesALongRootToo(t *testing.T) {
	const root = "/Users/someone/code/sidecar-files-panel-improvements"
	for _, size := range []struct{ w, h int }{{200, 50}, {100, 30}, {56, 20}} {
		f := NewFinder(&Cache{Files: distinguishablePaths(), OK: true}, root, 1)
		f.Open()
		f.SetQuery("zzzzzz")

		out := ansi.Strip(f.View(size.w, size.h, mouse.NewHandler()))
		if !strings.Contains(out, "sidecar-") {
			t.Errorf("%dx%d: the long root is not named anywhere in:\n%s", size.w, size.h, out)
		}
	}
}

// A query that matched more files than the list keeps says so, rather than
// presenting the best fifty as all there were.
func TestFinderSignalsTheMatchCap(t *testing.T) {
	files := make([]string, 0, MaxMatches+10)
	for i := range MaxMatches + 10 {
		files = append(files, fmt.Sprintf("internal/pkg%d/view.go", i))
	}
	f := NewFinder(&Cache{Files: files, OK: true}, "/root", 1)
	f.Open()
	f.SetQuery("view")

	if len(f.Matches()) != MaxMatches {
		t.Fatalf("kept %d matches, want the cap of %d", len(f.Matches()), MaxMatches)
	}
	out := ansi.Strip(f.View(100, 30, mouse.NewHandler()))
	if !strings.Contains(out, fmt.Sprintf("/%d+", MaxMatches)) {
		t.Errorf("the counts row does not say the list was cut:\n%s", out)
	}

	f.SetQuery("pkg1/view")
	out = ansi.Strip(f.View(100, 30, mouse.NewHandler()))
	if strings.Contains(out, "+") {
		t.Errorf("an uncapped query still claims to be cut:\n%s", out)
	}
}

func distinguishablePaths() []string {
	return []string{
		".claude/skills/create-modal/SKILL.md",
		".claude/skills/create-plugin/SKILL.md",
		".agents/skills/drag-pane/SKILL.md",
		"internal/plugins/workspace/plugin.go",
		"README.md",
	}
}

// TestFinderFitsANarrowPane is the pane case: a box far smaller than any screen
// the finder used to be drawn on. Nothing may spill out of it, the list must
// still show rows, and a long path must still show its filename.
func TestFinderFitsANarrowPane(t *testing.T) {
	long := "internal/plugins/filebrowser/search_surfaces.go"
	f := NewFinder(&Cache{Files: []string{long}, OK: true}, "/root", 1)
	f.Open()

	for _, size := range []struct{ w, h int }{{34, 12}, {28, 9}, {24, 8}} {
		handler := mouse.NewHandler()
		out := f.View(size.w, size.h, handler)

		if w := lineWidth(out); w > size.w {
			t.Errorf("%dx%d: modal is %d cells wide", size.w, size.h, w)
		}
		if h := len(strings.Split(out, "\n")); h > size.h {
			t.Errorf("%dx%d: modal is %d rows tall", size.w, size.h, h)
		}
		if rows := rowRegions(t, handler); len(rows) != 1 {
			t.Errorf("%dx%d: %d row regions, want 1", size.w, size.h, len(rows))
		}
		if !strings.Contains(ansi.Strip(out), "surfaces.go") {
			t.Errorf("%dx%d: the filename was truncated away:\n%s", size.w, size.h, ansi.Strip(out))
		}
	}
}

// TestFinderLongQueryDoesNotWrapTheHeader keeps a typed query that outgrows the
// box from pushing the whole modal a row taller.
func TestFinderLongQueryDoesNotWrapTheHeader(t *testing.T) {
	f := testFinder()
	f.SetQuery(strings.Repeat("q", 200))

	out := f.View(40, 16, mouse.NewHandler())
	if w := lineWidth(out); w > 40 {
		t.Fatalf("modal is %d cells wide with a long query", w)
	}
	if h := len(strings.Split(out, "\n")); h > 16 {
		t.Fatalf("modal is %d rows tall with a long query", h)
	}
}

// The box must not breathe as the user types: an empty finder, a scanning one,
// and one with results all occupy exactly the same rows.
//
// A dead end is the exception, and the only one: a query matching nothing keeps
// a one-row list rather than a dozen blank rows under the words "No matches".
// The box is not being refined at that point, and a page of reserved rows for
// results that are not coming reads as broken rather than steady.
func TestFinderHeightIsStableAcrossStates(t *testing.T) {
	for _, size := range []struct{ w, h int }{{100, 30}, {80, 24}, {56, 20}} {
		f := NewFinder(&Cache{}, "/root", 1)
		f.Open()
		empty := renderSize(f, size.w, size.h)

		f.Cache.Scanning = true
		scanning := renderSize(f, size.w, size.h)

		f.Cache.Scanning = false
		f.Cache.Files = []string{"internal/app/view.go", "README.md"}
		f.Cache.OK = true
		f.Refilter()
		results := renderSize(f, size.w, size.h)

		f.SetQuery("zzzz")
		nomatch := renderSize(f, size.w, size.h)

		if empty != scanning || empty != results {
			t.Errorf("%dx%d: box height jitters: empty=%v scanning=%v results=%v",
				size.w, size.h, empty, scanning, results)
		}
		if nomatch[0] != empty[0] {
			t.Errorf("%dx%d: a dead-end query changed the box width: %v vs %v", size.w, size.h, nomatch, empty)
		}
		if nomatch[1] >= empty[1] {
			t.Errorf("%dx%d: a dead-end query kept the tall box: nomatch=%v results=%v",
				size.w, size.h, nomatch, empty)
		}
	}
}

// renderSize is the rendered box's size, which is what a host composites.
func renderSize(f *Finder, width, height int) [2]int {
	out := f.View(width, height, mouse.NewHandler())
	return [2]int{lineWidth(out), len(strings.Split(out, "\n"))}
}

// An empty or short state is empty space, not a pattern: every reserved row is
// drawn the same way, so a run of them cannot read as banding.
func TestEmptyStateRowsAreUniform(t *testing.T) {
	f := NewFinder(&Cache{Files: []string{"README.md"}, OK: true}, "/root", 1)
	f.Open()
	f.SetQuery("zzzz")

	spellings := map[string]bool{}
	for _, line := range strings.Split(f.View(100, 30, mouse.NewHandler()), "\n") {
		if plain := ansi.Strip(line); strings.Trim(plain, " \u2502") == "" {
			spellings[line] = true
		}
	}
	// The box's own padding rows and the list's filler rows are the only two
	// blank spellings there may be; a third means the run is patterned.
	if len(spellings) > 2 {
		t.Errorf("%d different blank-row spellings; a run of them will read as banding", len(spellings))
	}
}

// The finder must fill its box exactly when a host asks it to own the pane.
func TestFillModeIsExactlyTheBox(t *testing.T) {
	for _, size := range []struct{ w, h int }{{56, 20}, {80, 24}, {40, 12}, {30, 8}} {
		f := NewFinder(&Cache{Files: []string{"README.md"}, OK: true}, "/root", 1)
		f.Open()
		f.SetFill(true)
		out := f.View(size.w, size.h, mouse.NewHandler())
		if w, h := lineWidth(out), len(strings.Split(out, "\n")); w != size.w || h != size.h {
			t.Errorf("fill at %dx%d rendered %dx%d", size.w, size.h, w, h)
		}
	}
}
