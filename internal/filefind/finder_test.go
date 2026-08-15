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

func TestElidePathKeepsTheFilename(t *testing.T) {
	tests := []struct {
		path  string
		width int
		want  string
	}{
		{"a/very/deeply/nested/path/that/goes/on/file.go", 20, "a/very/deep…/file.go"},
		{"a/very/deeply/nested/path/that/goes/on/file.go", 12, "a/v…/file.go"},
		// A filename that cannot fit on its own keeps its end rather than
		// pretending a directory prefix is the useful part.
		{"dir/an_extremely_long_filename_indeed.go", 12, "…e_indeed.go"},
		{"short.go", 20, "short.go"},
		{"no_directory_but_far_too_long.go", 10, "…o_long.go"},
	}
	for _, tt := range tests {
		got := elidePath(tt.path, tt.width)
		if got != tt.want {
			t.Errorf("elidePath(%q, %d) = %q, want %q", tt.path, tt.width, got, tt.want)
		}
		if ansi.StringWidth(got) > tt.width {
			t.Errorf("elidePath(%q, %d) = %q, which is %d cells wide",
				tt.path, tt.width, got, ansi.StringWidth(got))
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
