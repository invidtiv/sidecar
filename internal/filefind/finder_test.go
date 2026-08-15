package filefind

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
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

func TestFinderDoubleClickOpensTheRowUnderIt(t *testing.T) {
	f := testFinder()
	handler := mouse.NewHandler()
	f.View(100, 30, handler)

	regions := handler.HitMap.Regions()
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
