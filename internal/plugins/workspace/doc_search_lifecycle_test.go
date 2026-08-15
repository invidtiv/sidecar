package workspace

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/filefind"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/projectsearch"
)

// The finder's file list belongs to the root, not to one open of the finder: a
// second ctrl+p answers from the walk the first one paid for. Without that, and
// with a scan that walks up to 50k files, every ctrl+p re-walked the project and
// open/esc/open spawned walks whose results were then thrown away.
func TestDocPaneFinderReusesTheScanPerRoot(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()

	first := p.openDocFinder(doc)
	if first == nil {
		t.Fatal("the first ctrl+p issued no scan")
	}
	scanFinder(t, p, first)
	files := doc.mode.finder.Matches()
	if len(files) == 0 {
		t.Fatal("the scan found no files to reuse")
	}
	p.closeDocSearch(doc)

	if cmd := p.openDocFinder(doc); cmd != nil {
		t.Fatal("the second ctrl+p walked the project again")
	}
	if got := len(doc.mode.finder.Matches()); got != len(files) {
		t.Fatalf("the reopened finder shows %d files, want the %d the cached scan holds", got, len(files))
	}
}

// Panes on one root are looking at one tree, so they share the walk rather than
// paying for one each.
func TestDocPaneFindersShareOneCachePerRoot(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	first := p.focusedDocPane()
	scanFinder(t, p, p.openDocFinder(first))

	other := newDocPane(99, first.root, first.surface, nil)
	p.docs[99] = other
	if cmd := p.openDocFinder(other); cmd != nil {
		t.Fatal("a second pane on the same root walked the tree again")
	}
	if len(other.mode.finder.Matches()) != len(first.mode.finder.Matches()) {
		t.Fatal("the second pane did not see the first pane's file list")
	}

	// A different root is a different tree and does get its own walk.
	elsewhere := newDocPane(98, t.TempDir(), first.surface, nil)
	p.docs[98] = elsewhere
	if cmd := p.openDocFinder(elsewhere); cmd == nil {
		t.Fatal("a pane on another root reused the first root's file list")
	}
}

// Workspaces has no filesystem watcher over the project tree, so the cache
// cannot be invalidated precisely; it ages out instead. A finder opened after
// the lifetime rescans.
func TestDocPaneFinderCacheAgesOut(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()
	scanFinder(t, p, p.openDocFinder(doc))
	p.closeDocSearch(doc)

	entry := p.docFinderCaches[doc.root]
	if entry == nil {
		t.Fatal("the finder scan cached nothing for the pane's root")
	}
	entry.scanned = time.Now().Add(-docFinderCacheTTL - time.Second)

	cmd := p.openDocFinder(doc)
	if cmd == nil {
		t.Fatal("a cache older than its lifetime was not rescanned")
	}
	scanFinder(t, p, cmd)
	if time.Since(p.docFinderCaches[doc.root].scanned) > time.Minute {
		t.Fatal("the rescan did not reset the cache's age")
	}
}

// A project switch drops every cached file list: the roots it described are no
// longer the ones being browsed.
func TestInitDropsTheFinderCaches(t *testing.T) {
	p, root := docSearchPlugin(t, true)
	scanFinder(t, p, p.openDocFinder(p.focusedDocPane()))
	if len(p.docFinderCaches) == 0 {
		t.Fatal("nothing was cached to drop")
	}

	registry := keymap.NewRegistry()
	keymap.RegisterDefaults(registry)
	if err := p.Init(&plugin.Context{WorkDir: root, ProjectRoot: root, Config: config.Default(), Keymap: registry, Epoch: 18}); err != nil {
		t.Fatal(err)
	}
	if p.docFinderCaches != nil {
		t.Fatalf("a project switch kept %d cached file lists", len(p.docFinderCaches))
	}
}

// docPaneProjectSearchResults drives a pane's project search to the point where
// it is showing results, without ripgrep: the message is delivered the way the
// runtime delivers it, through the pane-tagged wrapper.
func docPaneProjectSearchResults(t *testing.T, p *Plugin, doc *docPane, query string, results []projectsearch.SearchFileResult) {
	t.Helper()
	p.openDocProjectSearch(doc)
	typeDocSearch(p, query)
	p.applyDocSearchMsg(docSearchMsg{LeafID: doc.leafID, Msg: projectsearch.ResultsMsg{
		Epoch:   p.ctx.Epoch,
		Results: results,
	}})
}

func guideResults() []projectsearch.SearchFileResult {
	return []projectsearch.SearchFileResult{{
		Path: "docs/guide.md",
		Matches: []projectsearch.SearchMatch{
			{LineNo: 12, LineText: "guide line", ColStart: 0, ColEnd: 5},
			{LineNo: 200, LineText: "guide line", ColStart: 0, ColEnd: 5},
		},
	}}
}

// The project search end to end in a pane: type a query, take results, open the
// hit, and land in the right tab on the right line.
func TestDocPaneProjectSearchOpensTheHit(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	composePaneTree(t, p, 120, 30)
	doc := p.focusedDocPane()

	docPaneProjectSearchResults(t, p, doc, "guide", guideResults())
	state := doc.mode.search.State
	if state.Query != "guide" {
		t.Fatalf("typed query landed as %q", state.Query)
	}
	if len(state.Results) != 1 {
		t.Fatalf("results = %#v, want the one file", state.Results)
	}
	// The cursor lands on the first match rather than on the file header.
	if _, _, isFile := state.FlatItem(state.Cursor); isFile {
		t.Fatal("results landed with the cursor on a file header")
	}

	// The pane header says what the pane is doing while all of this is up.
	if header := ansi.Strip(layoutDocTabStrip(doc, 60, true).Row); !strings.Contains(header, "Search") {
		t.Fatalf("header during the search = %q", header)
	}

	cmd := p.handleDocSearchKey(doc, tea.KeyPressMsg{Code: tea.KeyEnter})
	applyDocOpen(t, p, cmd)
	if doc.mode != nil {
		t.Fatal("opening a hit left the search up")
	}
	view := doc.view()
	if view == nil || view.Title() != "docs/guide.md" {
		t.Fatalf("the search opened %#v, want docs/guide.md", view)
	}

	// shift+enter puts the next hit in a tab of its own rather than over the
	// document the user is reading.
	docPaneProjectSearchResults(t, p, doc, "main", []projectsearch.SearchFileResult{{
		Path:    "cmd/main.go",
		Matches: []projectsearch.SearchMatch{{LineNo: 1, LineText: "package main"}},
	}})
	cmd = p.handleDocSearchKey(doc, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	applyDocOpen(t, p, cmd)
	if len(doc.tabs.Items) != 2 {
		t.Fatalf("shift+enter made %d tabs, want a second one", len(doc.tabs.Items))
	}
	if got := doc.view().Title(); got != "cmd/main.go" {
		t.Fatalf("the new tab shows %q, want cmd/main.go", got)
	}
}

// A result for a pane that has since closed its search is dropped, not applied
// to whatever the pane is showing now.
func TestDocSearchResultForAClosedSearchIsDropped(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()
	p.openDocProjectSearch(doc)
	typeDocSearch(p, "guide")
	p.closeDocSearch(doc)

	// The message the closed search's run is about to deliver.
	if cmd := p.applyDocSearchMsg(docSearchMsg{LeafID: doc.leafID, Msg: projectsearch.ResultsMsg{
		Epoch:   p.ctx.Epoch,
		Results: guideResults(),
	}}); cmd != nil {
		t.Fatal("a result for a closed search produced work")
	}
	if doc.mode != nil {
		t.Fatal("a late result reopened the search")
	}

	// The same for a finder scan landing after esc.
	scanFinder(t, p, p.openDocFinder(doc))
	p.closeDocSearch(doc)
	p.applyDocSearchMsg(docSearchMsg{LeafID: doc.leafID, Msg: filefind.ScannedMsg{
		Files: []string{"ghost.go"},
		Epoch: p.ctx.Epoch,
	}})
	if doc.mode != nil {
		t.Fatal("a late scan reopened the finder")
	}
}

// Two panes searching at once keep their results apart: a message is addressed
// to the pane that issued it, by leaf.
func TestDocSearchResultsLandInTheirOwnPane(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	first := p.focusedDocPane()
	second := newDocPane(99, first.root, first.surface, nil)
	p.docs[99] = second

	p.openDocProjectSearch(first)
	p.openDocProjectSearch(second)

	p.applyDocSearchMsg(docSearchMsg{LeafID: second.leafID, Msg: projectsearch.ResultsMsg{
		Epoch:   p.ctx.Epoch,
		Results: guideResults(),
	}})

	if len(second.mode.search.State.Results) != 1 {
		t.Fatalf("the addressed pane holds %#v", second.mode.search.State.Results)
	}
	if len(first.mode.search.State.Results) != 0 {
		t.Fatalf("the other pane's search took the result: %#v", first.mode.search.State.Results)
	}

	// A message for a leaf no pane owns is dropped rather than applied to the
	// first pane it finds.
	if cmd := p.applyDocSearchMsg(docSearchMsg{LeafID: 4242, Msg: projectsearch.ResultsMsg{
		Epoch:   p.ctx.Epoch,
		Results: guideResults(),
	}}); cmd != nil {
		t.Fatal("a message for an unknown pane produced work")
	}
	if len(first.mode.search.State.Results) != 0 {
		t.Fatal("a message for an unknown pane landed in a live pane")
	}
}

// A pane the frame did not draw registers nothing to click. The zoomed branch
// draws one leaf; a second pane holding an open search — which survives losing
// focus — must not leave its last regions lying over the leaf that was drawn.
func TestHiddenPaneRegistersNoSearchRegions(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	const width, height = 120, 30
	first := p.focusedDocPane()
	scanFinder(t, p, p.openDocFinder(first))
	composePaneTree(t, p, width, height)
	if len(first.modeRegions) == 0 {
		t.Fatal("the drawn pane registered nothing")
	}

	// A second pane with its own open finder, which the tree does not hold: it
	// cannot be drawn this frame.
	hidden := newDocPane(99, first.root, first.surface, nil)
	p.docs[99] = hidden
	p.openDocFinder(hidden)
	hidden.modeRegions = first.modeRegions

	composePaneTree(t, p, width, height)
	if len(hidden.modeRegions) != 0 {
		t.Fatalf("a pane that was not drawn kept %d hit regions", len(hidden.modeRegions))
	}
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if strings.HasPrefix(region.ID, filefind.RegionItem) {
			found := false
			for _, own := range first.modeRegions {
				if own.ID == region.ID && own.Rect == region.Rect {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("region %q at %+v belongs to no drawn pane", region.ID, region.Rect)
			}
		}
	}
}

// Outside the pane is the modal's backdrop. A click there dismisses the
// surface; the wheel and pointer motion are swallowed, so scrolling over a
// terminal pane cannot scroll the finder's list behind the user's back. This is
// how the file-info modal behaves — its backdrop covers the screen and eats
// everything that lands on it.
func TestDocPaneSearchIgnoresEventsOutsideThePane(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	const width, height = 120, 30
	composePaneTree(t, p, width, height)
	doc := p.focusedDocPane()
	scanFinder(t, p, p.openDocFinder(doc))
	typeDocSearch(p, "e")
	composePaneTree(t, p, width, height)
	if len(doc.mode.finder.Matches()) < 2 {
		t.Fatalf("need at least two matches to move a cursor between")
	}

	before := doc.mode.finder.Cursor()
	p.handleMouse(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelDown})
	if doc.mode == nil {
		t.Fatal("the wheel outside the pane dismissed the search")
	}
	if got := doc.mode.finder.Cursor(); got != before {
		t.Fatalf("the wheel outside the pane moved the finder cursor to %d", got)
	}

	p.handleMouse(tea.MouseMotionMsg{X: 0, Y: 0})
	if doc.mode == nil {
		t.Fatal("pointer motion outside the pane dismissed the search")
	}

	// Inside the pane the wheel is still the list's.
	p.handleMouse(tea.MouseWheelMsg{X: doc.boxX + 2, Y: doc.boxY + 2, Button: tea.MouseWheelDown})
	if doc.mode.finder.Cursor() == before {
		t.Fatal("the wheel inside the pane did not reach the finder")
	}
}

// F opens a document pane, and kanban has no pane tree to open one in, so the
// footer must not offer it there: a hint for a key that does nothing is the
// thing to avoid. In list view it is offered as before.
func TestFindHintIsListViewOnly(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	p.closeDocPane()
	p.activePane = PaneSidebar

	p.viewMode = ViewModeList
	if got := commandNameByID(p.Commands(), "find-file"); got != "Find" {
		t.Fatalf("list footer find hint = %q, want Find", got)
	}

	p.viewMode = ViewModeKanban
	if got := commandNameByID(p.Commands(), "find-file"); got != "" {
		t.Fatalf("kanban footer advertises find as %q, but F does nothing there", got)
	}
	if cmd := p.handleListKeys(tea.KeyPressMsg{Code: 'F', Text: "F"}); cmd != nil {
		t.Fatal("F in kanban did something after all; then it should be advertised")
	}
	if p.activeDocPaneOrNil() != nil {
		t.Fatal("F opened a pane in a view that draws no pane tree")
	}
}

// Footer order is a decision, not an accident: two commands sharing a priority
// sort arbitrarily against each other.
func TestListFooterPrioritiesAreUnique(t *testing.T) {
	p, _ := docSearchPlugin(t, false)
	p.closeDocPane()
	p.activePane = PaneSidebar
	p.viewMode = ViewModeList

	seen := map[int]string{}
	for _, command := range p.Commands() {
		if command.Context != "workspace-list" {
			continue
		}
		// The base block and the worktree-action block have overlapped since
		// before the file finder existed (delete/push/merge against
		// sidebar/refresh/filter); this pins the pair the finder introduced.
		switch command.ID {
		case "delete-workspace", "push", "merge-workflow":
			continue
		}
		if other, dup := seen[command.Priority]; dup {
			t.Errorf("%q and %q share footer priority %d", other, command.ID, command.Priority)
		}
		seen[command.Priority] = command.ID
	}
}
