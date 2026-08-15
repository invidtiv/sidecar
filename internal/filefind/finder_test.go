package filefind

import (
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
		// Leading directories are spent first; the parent and the filename —
		// the pair that differs between rows — survive.
		{"a/very/deeply/nested/path/that/goes/on/file.go", 20, "…hat/goes/on/file.go"},
		{".claude/skills/create-modal/SKILL.md", 30, ".c/s/create-modal/SKILL.md"},
		{"internal/plugins/filebrowser/view.go", 30, "i/p/filebrowser/view.go"},
		// Too narrow even for that: keep the tail, never the shared head.
		{"a/very/deeply/nested/path/that/goes/on/file.go", 12, "…/on/file.go"},
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
// tight pane: rows that share a long prefix and a filename must not all render
// as the same string. The old middle elision kept the shared head and threw the
// discriminating segment away, so thirteen rows read ".claude/…/SKILL.md".
func TestNarrowRowsStayDistinguishable(t *testing.T) {
	paths := []string{
		".claude/skills/create-modal/SKILL.md",
		".claude/skills/create-plugin/SKILL.md",
		".claude/skills/create-theme/SKILL.md",
		".claude/skills/ui-features/SKILL.md",
		".agents/skills/release-sidecar/SKILL.md",
		".agents/skills/drag-pane/SKILL.md",
	}
	for _, width := range []int{30, 45} {
		seen := map[string]string{}
		for _, path := range paths {
			row := ansi.Strip(RenderMatch(Match{Path: path}, width))
			if ansi.StringWidth(row) > width {
				t.Errorf("width %d: row %q is %d cells", width, row, ansi.StringWidth(row))
			}
			if other, dup := seen[row]; dup {
				t.Errorf("width %d: %q and %q both render as %q", width, other, path, row)
			}
			seen[row] = path
		}
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

		if empty != scanning || empty != results || empty != nomatch {
			t.Errorf("%dx%d: box height jitters: empty=%v scanning=%v results=%v nomatch=%v",
				size.w, size.h, empty, scanning, results, nomatch)
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
