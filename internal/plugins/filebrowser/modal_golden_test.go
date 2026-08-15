package filebrowser

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/filefind"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/projectsearch"
)

// The goldens in testdata pin the exact rendering of the quick-open finder and
// the project search, down to the ANSI. They serve two purposes:
//
//   - they proved that moving both surfaces out of this plugin into
//     internal/filefind and internal/projectsearch changed no pixels, and
//   - each case renders twice, once through the Files plugin and once through
//     the shared type driven directly at the same size, and asserts the two are
//     byte-identical. That is what stops the Files plugin and the workspace
//     panes from drifting apart once the panes host the same two surfaces.
var updateGolden = flag.Bool("update-golden", false, "rewrite modal golden files")

// goldenSizes are the surfaces each modal is rendered at: one comfortably wide,
// one narrow enough to exercise every clamp in the layout.
var goldenSizes = []struct {
	name          string
	width, height int
}{
	{"wide", 100, 30},
	{"narrow", 60, 20},
}

// goldenQuickOpenFiles is the fixed file list the finder golden renders from.
func goldenQuickOpenFiles() []string {
	return []string{
		"cmd/sidecar/main.go",
		"internal/app/update.go",
		"internal/app/view.go",
		"internal/plugins/filebrowser/plugin.go",
		"internal/plugins/filebrowser/view.go",
		"internal/modal/modal.go",
		"README.md",
		"a/very/deeply/nested/path/that/goes/on/and/on/for/quite/a/while/file.go",
	}
}

// goldenSearchResults is the fixed result set the project-search golden renders
// from: a collapsed file, an expanded one, and a line long enough to truncate.
func goldenSearchResults() []projectsearch.SearchFileResult {
	return []projectsearch.SearchFileResult{
		{
			Path: "internal/app/update.go",
			Matches: []projectsearch.SearchMatch{
				{LineNo: 12, LineText: "\tif err := p.update(msg); err != nil {", ColStart: 13, ColEnd: 19},
				{LineNo: 480, LineText: "        return p.update(msg)", ColStart: 18, ColEnd: 24},
			},
		},
		{
			Path:      "internal/plugins/filebrowser/plugin.go",
			Collapsed: true,
			Matches: []projectsearch.SearchMatch{
				{LineNo: 7, LineText: "// update applies the message", ColStart: 3, ColEnd: 9},
			},
		},
		{
			Path: "a/very/deeply/nested/path/that/goes/on/and/on/for/a/while/handler.go",
			Matches: []projectsearch.SearchMatch{
				{LineNo: 10240, LineText: strings.Repeat("x", 60) + " update " + strings.Repeat("y", 60), ColStart: 61, ColEnd: 67},
			},
		},
	}
}

// newGoldenPlugin builds a plugin with no disk dependencies at all, so the
// golden output is a pure function of the fixtures above.
func newGoldenPlugin(width, height int) *Plugin {
	return &Plugin{
		width:        width,
		height:       height,
		mouseHandler: mouse.NewHandler(),
	}
}

// regionDump renders the handler's hit regions as stable text, so the golden
// pins where clicks land as well as what is drawn.
func regionDump(handler *mouse.Handler) string {
	var sb strings.Builder
	for _, r := range handler.HitMap.Regions() {
		fmt.Fprintf(&sb, "%s x=%d y=%d w=%d h=%d data=%v\n",
			r.ID, r.Rect.X, r.Rect.Y, r.Rect.W, r.Rect.H, r.Data)
	}
	return sb.String()
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run: go test ./internal/plugins/filebrowser -run Golden -update-golden)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s: rendering changed.\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

// keyPress builds a printable keypress, so the parity half of each case reaches
// its state through the same public key handler a user would.
func keyPress(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// quickOpenGoldenCases are the finder states worth pinning: an empty query, a
// query that matches, a query that matches nothing, a cursor scrolled past the
// visible window, an in-flight scan, and a scan that reported an error.
var quickOpenGoldenCases = []struct {
	name     string
	query    string
	cursor   int
	scanning bool
	errText  string
	noFiles  bool
}{
	{name: "empty"},
	{name: "query", query: "view", cursor: 1},
	{name: "nomatch", query: "zzzzz"},
	{name: "scrolled", query: "o", cursor: 6},
	{name: "scanning", scanning: true, noFiles: true},
	{name: "error", query: "o", errText: "scan timed out"},
}

func TestQuickOpenModalGolden(t *testing.T) {
	for _, size := range goldenSizes {
		for _, tc := range quickOpenGoldenCases {
			t.Run(size.name+"/"+tc.name, func(t *testing.T) {
				// Through the Files plugin.
				p := newGoldenPlugin(size.width, size.height)
				if !tc.noFiles {
					p.quickOpen.Files = goldenQuickOpenFiles()
					p.quickOpen.OK = true
				}
				p.quickOpen.Scanning = tc.scanning
				p.quickOpen.ErrText = tc.errText
				p.openQuickOpen()
				finder := p.fileFinder()
				for _, r := range tc.query {
					finder.HandleKey(keyPress(r))
				}
				for i := 0; i < tc.cursor; i++ {
					finder.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
				}
				p.mouseHandler.Clear()
				got := p.renderQuickOpenModalContent() + "\n--- regions ---\n" + regionDump(p.mouseHandler)

				// Through the shared type, driven directly at the same size.
				cache := &filefind.Cache{Scanning: tc.scanning, ErrText: tc.errText}
				if !tc.noFiles {
					cache.Files = goldenQuickOpenFiles()
					cache.OK = true
				}
				direct := filefind.NewFinder(cache, t.TempDir(), 0)
				direct.Open()
				for _, r := range tc.query {
					direct.HandleKey(keyPress(r))
				}
				for i := 0; i < tc.cursor; i++ {
					direct.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
				}
				handler := mouse.NewHandler()
				bare := direct.View(size.width, size.height, handler) + "\n--- regions ---\n" + regionDump(handler)

				if bare != got {
					t.Errorf("filefind.Finder and the Files plugin disagree.\n--- shared ---\n%s\n--- plugin ---\n%s", bare, got)
				}
				checkGolden(t, "quickopen_"+size.name+"_"+tc.name+".golden", got)
			})
		}
	}
}

// projectSearchGoldenCases pin the empty, searching, error, results and
// results-focused states.
var projectSearchGoldenCases = []struct {
	name    string
	mutate  func(*projectsearch.State)
	focused string // modal element to focus before rendering
}{
	{name: "empty", mutate: func(s *projectsearch.State) {}},
	{name: "searching", mutate: func(s *projectsearch.State) {
		s.Query = "update"
		s.IsSearching = true
	}},
	{name: "error", mutate: func(s *projectsearch.State) {
		s.Query = "update"
		s.Error = "ripgrep (rg) not found - install with: brew install ripgrep"
	}},
	{name: "nomatch", mutate: func(s *projectsearch.State) {
		s.Query = "zzzz"
	}},
	{name: "results", mutate: func(s *projectsearch.State) {
		s.Query = "update"
		s.Results = goldenSearchResults()
		s.Cursor = s.FirstMatchIndex()
	}},
	{name: "results-focused", mutate: func(s *projectsearch.State) {
		s.Query = "update"
		s.Results = goldenSearchResults()
		s.Cursor = s.FirstMatchIndex()
		s.ResultsFocused = true
	}},
	{name: "results-toggles", mutate: func(s *projectsearch.State) {
		s.Query = "update"
		s.Results = goldenSearchResults()
		s.Cursor = 4
		s.UseRegex = true
		s.WholeWord = true
	}},
	{name: "results-focus-regex", focused: projectsearch.ToggleRegexID, mutate: func(s *projectsearch.State) {
		s.Query = "update"
		s.Results = goldenSearchResults()
		s.Cursor = s.FirstMatchIndex()
	}},
}

func TestProjectSearchModalGolden(t *testing.T) {
	for _, size := range goldenSizes {
		for _, tc := range projectSearchGoldenCases {
			t.Run(size.name+"/"+tc.name, func(t *testing.T) {
				// Through the Files plugin.
				p := newGoldenPlugin(size.width, size.height)
				p.openProjectSearch()
				tc.mutate(p.projectSearch.State)
				if tc.focused != "" {
					p.projectSearch.SetFocus(tc.focused)
				}
				p.mouseHandler.Clear()
				got := p.renderProjectSearchModalContent() + "\n--- regions ---\n" + regionDump(p.mouseHandler)

				// Through the shared type, driven directly at the same size.
				direct := projectsearch.New(t.TempDir(), 0)
				tc.mutate(direct.State)
				if tc.focused != "" {
					direct.SetFocus(tc.focused)
				}
				handler := mouse.NewHandler()
				bare := direct.View(size.width, size.height, handler) + "\n--- regions ---\n" + regionDump(handler)

				if bare != got {
					t.Errorf("projectsearch.Search and the Files plugin disagree.\n--- shared ---\n%s\n--- plugin ---\n%s", bare, got)
				}
				checkGolden(t, "projectsearch_"+size.name+"_"+tc.name+".golden", got)
			})
		}
	}
}

// TestFuzzyMatchHighlightGolden pins the per-row rendering of a finder match,
// which the modal golden only exercises at whatever widths it happens to use.
func TestFuzzyMatchHighlightGolden(t *testing.T) {
	matches := filefind.FuzzyFilter(goldenQuickOpenFiles(), "iav", 10)
	var sb strings.Builder
	for _, m := range matches {
		for _, w := range []int{80, 30, 12} {
			fmt.Fprintf(&sb, "%d|%s\n", w, filefind.RenderMatch(m, w))
		}
	}
	checkGolden(t, "quickopen_match_rows.golden", sb.String())
}
