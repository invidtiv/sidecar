package projectsearch

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
)

func testResults() []SearchFileResult {
	return []SearchFileResult{
		{
			Path: "internal/app/update.go",
			Matches: []SearchMatch{
				{LineNo: 12, LineText: "p.update(msg)", ColStart: 2, ColEnd: 8},
				{LineNo: 40, LineText: "return p.update(msg)", ColStart: 9, ColEnd: 15},
			},
		},
		{
			Path:    "cmd/main.go",
			Matches: []SearchMatch{{LineNo: 3, LineText: "update()", ColStart: 0, ColEnd: 6}},
		},
	}
}

func testSearch() *Search {
	s := New("/root", 7)
	s.SetSize(100, 30)
	s.State.Query = "update"
	s.State.Results = testResults()
	s.State.Cursor = s.State.FirstMatchIndex()
	return s
}

func TestSearchEnterOpensTheSelectedMatch(t *testing.T) {
	s := testSearch()

	res, _ := s.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Outcome != OutcomeOpen {
		t.Fatalf("enter outcome = %v, want OutcomeOpen", res.Outcome)
	}
	if res.Path != "internal/app/update.go" || res.Line != 12 {
		t.Errorf("opened %s:%d, want internal/app/update.go:12", res.Path, res.Line)
	}
	if res.NewTab {
		t.Error("plain enter should not ask for a new tab")
	}
	if res.Query != "update" {
		t.Errorf("result query = %q, want the search query", res.Query)
	}
}

func TestSearchShiftEnterAsksForANewTab(t *testing.T) {
	s := testSearch()

	res, _ := s.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	if res.Outcome != OutcomeOpen || !res.NewTab {
		t.Fatalf("shift+enter = %+v, want an open in a new tab", res)
	}
}

func TestSearchCtrlEAsksForTheEditor(t *testing.T) {
	s := testSearch()

	res, _ := s.HandleKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if res.Outcome != OutcomeOpenExternal {
		t.Fatalf("ctrl+e outcome = %v, want OutcomeOpenExternal", res.Outcome)
	}
	if res.Line != 12 {
		t.Errorf("ctrl+e line = %d, want 12", res.Line)
	}
}

func TestSearchEscCancels(t *testing.T) {
	s := testSearch()

	res, _ := s.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if res.Outcome != OutcomeCancelled {
		t.Fatalf("esc outcome = %v, want OutcomeCancelled", res.Outcome)
	}
}

func TestSearchTypingSchedulesADebouncedRun(t *testing.T) {
	s := New("/root", 7)
	s.SetSize(100, 30)

	res, cmd := s.HandleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if res.Outcome != OutcomeNone {
		t.Fatalf("typing outcome = %v, want OutcomeNone", res.Outcome)
	}
	if cmd == nil {
		t.Fatal("typing should schedule a search")
	}
	if s.State.Query != "x" || !s.State.IsSearching {
		t.Errorf("state after typing: query=%q searching=%v", s.State.Query, s.State.IsSearching)
	}

	// The tick it scheduled runs the search only while it is still the newest.
	tick, ok := cmd().(DebounceMsg)
	if !ok {
		t.Fatalf("scheduled %T, want DebounceMsg", cmd())
	}
	if s.Update(tick) == nil {
		t.Error("the newest debounce tick should start a run")
	}
	s.State.DebounceVersion++
	if s.Update(tick) != nil {
		t.Error("a superseded debounce tick should be dropped")
	}
}

func TestSearchNavigationSkipsFileHeaders(t *testing.T) {
	s := testSearch()

	// Every stop between the first match and the last is a match, never one of
	// the file headers the flattened list interleaves with them.
	for i := 0; i < 4; i++ {
		s.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
		if _, _, isFile := s.State.FlatItem(s.State.Cursor); isFile {
			t.Fatalf("down %d landed on a file header (cursor %d)", i+1, s.State.Cursor)
		}
	}
	if path, line := s.State.GetSelectedFile(); path != "cmd/main.go" || line != 3 {
		t.Errorf("walked to %s:%d, want cmd/main.go:3", path, line)
	}
}

func TestSearchToggleRerunsTheQuery(t *testing.T) {
	s := testSearch()

	if cmd := s.ToggleOption(&s.State.UseRegex); cmd == nil {
		t.Error("toggling an option with a query should re-run the search")
	}
	if !s.State.UseRegex {
		t.Error("regex toggle did not flip")
	}

	s.State.Query = ""
	if cmd := s.ToggleOption(&s.State.CaseSensitive); cmd != nil {
		t.Error("toggling with no query should not run ripgrep")
	}
}

func TestSearchUpdateRejectsAnotherEpochsResults(t *testing.T) {
	s := testSearch()
	s.State.IsSearching = true

	s.Update(ResultsMsg{Epoch: 6, Results: nil, Error: errors.New("gone")})
	if s.State.Error != "" || len(s.State.Results) != 2 {
		t.Errorf("stale results applied: err=%q results=%d", s.State.Error, len(s.State.Results))
	}

	s.Update(ResultsMsg{Epoch: 7, Results: testResults()[:1]})
	if len(s.State.Results) != 1 {
		t.Errorf("current results not applied: %d files", len(s.State.Results))
	}
	if s.State.IsSearching {
		t.Error("landed results should clear the in-flight flag")
	}
}

func TestSearchClickOnAFileHeaderCollapsesIt(t *testing.T) {
	s := testSearch()
	handler := mouse.NewHandler()
	s.View(100, 30, handler)

	var header *mouse.Region
	for _, r := range handler.HitMap.Regions() {
		if _, ok := ParseFileID(r.ID); ok {
			region := r
			header = &region
			break
		}
	}
	if header == nil {
		t.Fatal("no file header region registered")
	}

	res, _ := s.HandleMouse(tea.MouseClickMsg{X: header.Rect.X, Y: header.Rect.Y, Button: tea.MouseLeft}, handler)
	if res.Outcome != OutcomeNone {
		t.Fatalf("clicking a header = %v, want OutcomeNone", res.Outcome)
	}
	if !s.State.Results[0].Collapsed {
		t.Error("clicking a file header should collapse it")
	}
}

func TestSearchViewSizesToWhatItIsGiven(t *testing.T) {
	s := testSearch()

	wide := s.View(140, 40, mouse.NewHandler())
	narrow := s.View(60, 20, mouse.NewHandler())

	if widest(wide) <= widest(narrow) {
		t.Errorf("modal did not shrink with the surface: %d vs %d", widest(wide), widest(narrow))
	}
	if widest(narrow) > 60 {
		t.Errorf("modal is %d cells wide in a 60-cell surface", widest(narrow))
	}
}

func widest(s string) int {
	w := 0
	for _, line := range strings.Split(s, "\n") {
		if lw := ansi.StringWidth(line); lw > w {
			w = lw
		}
	}
	return w
}
