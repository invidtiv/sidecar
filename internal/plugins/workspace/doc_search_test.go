package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/filefind"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/gitstatus"
	"github.com/marcus/sidecar/internal/state"
)

// docSearchPlugin opens README.md in a document pane beside the selected
// terminal and hands back the plugin with that pane focused. shell picks the
// scope: a shell surface (what global Workspaces browses) or a worktree.
func docSearchPlugin(t *testing.T, shell bool) (*Plugin, string) {
	t.Helper()
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "docs/guide.md", strings.Repeat("guide line\n", 400))
	writeDocPaneFixture(t, root, "cmd/main.go", "package main\n")
	p := docPaneTestPlugin(t, root, shell)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	if p.focusedDocPane() == nil {
		t.Fatal("no focused document pane to search from")
	}
	return p, root
}

// scanFinder runs the finder's file scan to completion and feeds the result
// back the way the runtime would.
func scanFinder(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("opening the finder issued no scan")
	}
	msg, ok := cmd().(docSearchMsg)
	if !ok {
		t.Fatalf("finder scan produced %T, want a pane-tagged message", cmd())
	}
	if _, ok := msg.Msg.(filefind.ScannedMsg); !ok {
		t.Fatalf("pane-tagged message carried %T, want a file scan", msg.Msg)
	}
	p.applyDocSearchMsg(msg)
}

func typeDocSearch(p *Plugin, text string) {
	doc := p.focusedDocPane()
	for _, r := range text {
		p.handleDocSearchKey(doc, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// Both surfaces open from a focused document pane, in either scope, rooted at
// the pane's own directory rather than at anything the plugin holds globally.
func TestDocPaneSearchOpensInBothScopes(t *testing.T) {
	for _, shell := range []bool{true, false} {
		name := "workspace"
		if shell {
			name = "shell"
		}
		t.Run(name, func(t *testing.T) {
			p, _ := docSearchPlugin(t, shell)
			doc := p.focusedDocPane()

			handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
			if !handled {
				t.Fatal("ctrl+p was not handled by the focused document pane")
			}
			if doc.mode == nil || doc.mode.kind != docSearchFinder {
				t.Fatalf("ctrl+p left mode %#v, want the file finder", doc.mode)
			}
			if got := doc.mode.finder.Root(); got != doc.root {
				t.Fatalf("finder root = %q, want the pane's own root %q", got, doc.root)
			}
			if p.FocusContext() != "workspace-doc-search" {
				t.Fatalf("focus context = %q, want workspace-doc-search", p.FocusContext())
			}
			if !p.ConsumesTextInput() || !p.BlocksGlobalKeys() {
				t.Fatal("an open pane search does not claim typed text and the global keys")
			}
			scanFinder(t, p, cmd)

			p.handleDocSearchKey(doc, tea.KeyPressMsg{Code: tea.KeyEsc})
			if doc.mode != nil {
				t.Fatal("esc left the finder open")
			}

			handled, _ = p.handleDocKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
			if !handled || doc.mode == nil || doc.mode.kind != docSearchProject {
				t.Fatalf("f left mode %#v, want the project search", doc.mode)
			}
			if got := doc.mode.search.Root(); got != doc.root {
				t.Fatalf("project search root = %q, want the pane's own root %q", got, doc.root)
			}
			p.handleDocSearchKey(doc, tea.KeyPressMsg{Code: tea.KeyEsc})
			if doc.mode != nil {
				t.Fatal("esc left the project search open")
			}
			if p.activeDocPaneOrNil() == nil {
				t.Fatal("closing the search closed the pane with it")
			}
			if p.FocusContext() != "workspace-doc" {
				t.Fatalf("focus context after esc = %q, want workspace-doc", p.FocusContext())
			}
		})
	}
}

// While a search is open every key is its own: nothing reaches the document's
// keys or the workspace behind the pane.
func TestDocPaneSearchAbsorbsEveryKey(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()
	scanFinder(t, p, p.openDocFinder(doc))

	for _, k := range []tea.KeyPressMsg{
		{Code: 'q', Text: "q"},
		{Code: 'x', Text: "x"},
		{Code: 'w', Text: "w"},
	} {
		handled, _ := p.handleDocKey(k)
		if !handled {
			t.Fatalf("%q leaked out of the open search", k.String())
		}
	}
	if p.activeDocPaneOrNil() == nil {
		t.Fatal("q while searching hid the pane")
	}
	if got := doc.mode.finder.Query(); got != "qxw" {
		t.Fatalf("finder query = %q, want the typed text %q", got, "qxw")
	}
	if len(doc.tabs.Items) != 1 {
		t.Fatalf("x while searching closed a tab: %d tabs left", len(doc.tabs.Items))
	}
}

// Picking a file loads it through the pane's own tab machinery: plain enter
// replaces the active tab, shift+enter opens a new one, and the line the hit
// carries is where the document lands.
func TestDocPaneSearchOpensResultInTheActiveTabAndInANewTab(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	composePaneTree(t, p, 120, 30)
	doc := p.focusedDocPane()

	cmd := p.applyDocSearchOutcome(doc, docSearchOutcome{Open: true, Path: "docs/guide.md", Line: 200}, nil)
	applyDocOpen(t, p, cmd)
	if len(doc.tabs.Items) != 1 {
		t.Fatalf("a plain pick opened %d tabs, want the active one replaced", len(doc.tabs.Items))
	}
	view := doc.view()
	if view == nil || view.Title() != "docs/guide.md" {
		t.Fatalf("active tab = %#v, want docs/guide.md", view)
	}
	if view.ScrollOffset() <= 0 {
		t.Fatalf("jump to line 200 left the document at offset %d", view.ScrollOffset())
	}

	cmd = p.applyDocSearchOutcome(doc, docSearchOutcome{Open: true, Path: "cmd/main.go", NewTab: true}, nil)
	applyDocOpen(t, p, cmd)
	if len(doc.tabs.Items) != 2 {
		t.Fatalf("shift+enter opened %d tabs, want a second one", len(doc.tabs.Items))
	}
	if got := doc.view().Title(); got != "cmd/main.go" {
		t.Fatalf("new tab shows %q, want cmd/main.go", got)
	}

	// An already-open file is focused rather than opened twice.
	cmd = p.applyDocSearchOutcome(doc, docSearchOutcome{Open: true, Path: "docs/guide.md"}, nil)
	applyDocOpen(t, p, cmd)
	if len(doc.tabs.Items) != 2 {
		t.Fatalf("reopening an open file made %d tabs", len(doc.tabs.Items))
	}
	if got := doc.view().Title(); got != "docs/guide.md" {
		t.Fatalf("reopening an open file selected %q", got)
	}
}

// Enter in the finder opens what the cursor is on, end to end.
func TestDocPaneFinderEnterLoadsTheMatch(t *testing.T) {
	p, _ := docSearchPlugin(t, false)
	composePaneTree(t, p, 120, 30)
	doc := p.focusedDocPane()
	scanFinder(t, p, p.openDocFinder(doc))

	typeDocSearch(p, "guide")
	if len(doc.mode.finder.Matches()) == 0 {
		t.Fatal("typing produced no matches to open")
	}
	cmd := p.handleDocSearchKey(doc, tea.KeyPressMsg{Code: tea.KeyEnter})
	applyDocOpen(t, p, cmd)
	if doc.mode != nil {
		t.Fatal("opening a match left the finder up")
	}
	if got := doc.view().Title(); got != "docs/guide.md" {
		t.Fatalf("enter opened %q, want docs/guide.md", got)
	}
}

// The pane keeps exactly the box it was given with a search open — the app's
// header scrolls off the moment a leaf answers with more rows than that — and
// the surface is drawn inside that box rather than over the screen.
func TestDocPaneSearchKeepsTheLeafsBox(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	const width, height = 120, 30
	doc := p.focusedDocPane()

	before := composePaneTree(t, p, width, height)
	scanFinder(t, p, p.openDocFinder(doc))
	// composePaneTree asserts the row count and every row's width, which is the
	// property a leaf that overflowed its box would break.
	after := composePaneTree(t, p, width, height)
	if strings.Join(before, "\n") == strings.Join(after, "\n") {
		t.Fatal("opening the finder changed nothing on screen")
	}

	box := docLeafBox(t, p, width, height)
	origin, ok := p.previewContentBox()
	if !ok {
		t.Fatal("preview content box is unplaced")
	}
	if len(doc.modeRegions) == 0 {
		t.Fatal("the surface registered nothing to click")
	}
	for _, region := range doc.modeRegions {
		r := region.Rect
		if r.X < origin.X+box.X || r.Y < origin.Y+box.Y ||
			r.X+r.W > origin.X+box.X+box.W || r.Y+r.H > origin.Y+box.Y+box.H {
			t.Fatalf("region %q at %+v escapes the pane box %+v", region.ID, r, box)
		}
	}
}

// A click inside the modal hits the modal, not the document leaf drawn under it.
func TestDocPaneSearchClickHitsTheModal(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	const width, height = 120, 30
	composePaneTree(t, p, width, height)
	doc := p.focusedDocPane()
	scanFinder(t, p, p.openDocFinder(doc))
	typeDocSearch(p, "g")
	if len(doc.mode.finder.Matches()) == 0 {
		t.Fatal("no matches, so no rows to click")
	}

	composePaneTree(t, p, width, height)
	origin, ok := p.previewContentBox()
	if !ok {
		t.Fatal("preview content box is unplaced")
	}
	box := docLeafBox(t, p, width, height)

	var row *mouse.Region
	for _, region := range doc.modeRegions {
		if strings.HasPrefix(region.ID, filefind.RegionItem) {
			r := region
			row = &r
			break
		}
	}
	if row == nil {
		t.Fatal("the finder registered no row regions")
	}
	if row.Rect.X < origin.X+box.X || row.Rect.Y < origin.Y+box.Y {
		t.Fatalf("row region %+v sits outside the pane at (%d,%d)", row.Rect, origin.X+box.X, origin.Y+box.Y)
	}
	hit := p.mouseHandler.HitMap.Test(row.Rect.X+1, row.Rect.Y)
	if hit == nil || hit.ID != row.ID {
		t.Fatalf("a click at pane-absolute (%d,%d) hit %#v, want the modal row", row.Rect.X+1, row.Rect.Y, hit)
	}
}

func docLeafBox(t *testing.T, p *Plugin, width, height int) Box {
	t.Helper()
	leaves, _, _ := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	for _, placement := range leaves {
		if placement.Node.Kind == PaneDoc {
			return placement.Box
		}
	}
	t.Fatal("the document leaf was not placed")
	return Box{}
}

// The pane header says what the pane is doing. A pane taking search keystrokes
// must not still read as a pane showing a file.
func TestDocPaneHeaderShowsTheSearchMode(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()
	before := ansi.Strip(layoutDocTabStrip(doc, 60, true).Row)
	if !strings.Contains(before, "README.md") {
		t.Fatalf("header without a search = %q, want the filename", before)
	}

	scanFinder(t, p, p.openDocFinder(doc))
	typeDocSearch(p, "gui")
	header := ansi.Strip(layoutDocTabStrip(doc, 60, true).Row)
	if !strings.Contains(header, "Find") || !strings.Contains(header, "gui") {
		t.Fatalf("searching header = %q, want the mode and the query", header)
	}

	p.handleDocSearchKey(doc, tea.KeyPressMsg{Code: tea.KeyEsc})
	p.openDocProjectSearch(doc)
	if header := ansi.Strip(layoutDocTabStrip(doc, 60, true).Row); !strings.Contains(header, "Search") {
		t.Fatalf("project-search header = %q, want the mode named", header)
	}
}

// F opens a document pane straight into the finder, and it degrades to a toast
// rather than a broken layout when the geometry cannot hold one.
func TestListFOpensAFinderPane(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	// Start from a terminal-only tree: F is the way to a pane, not a second one.
	p.closeDocPane()
	p.activePane = PaneSidebar
	if p.activeDocPaneOrNil() != nil {
		t.Fatal("the document pane survived the close")
	}

	cmd := p.handleListKeys(tea.KeyPressMsg{Code: 'F', Text: "F"})
	doc := p.focusedDocPane()
	if doc == nil {
		t.Fatal("F opened no document pane")
	}
	if doc.mode == nil || doc.mode.kind != docSearchFinder {
		t.Fatalf("F left mode %#v, want the file finder", doc.mode)
	}
	if len(doc.tabs.Items) != 0 {
		t.Fatalf("the finder pane opened with %d tabs, want none until a file is picked", len(doc.tabs.Items))
	}
	scanFinder(t, p, unwrapDocSearchCmd(t, cmd))
	composePaneTree(t, p, 120, 30)

	// The same key on a window too small for a second pane leaves the tree alone.
	small, _ := docSearchPlugin(t, true)
	small.closeDocPane()
	small.width, small.height = 20, 8
	before := small.paneRoot
	small.handleListKeys(tea.KeyPressMsg{Code: 'F', Text: "F"})
	if small.activeDocPaneOrNil() != nil || small.paneRoot != before {
		t.Fatal("F split a window that cannot hold the pane")
	}
	if small.toastMessage == "" {
		t.Fatal("F refused the split without saying why")
	}
}

// unwrapDocSearchCmd digs the finder's scan out of the batch F returns.
func unwrapDocSearchCmd(t *testing.T, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("F issued no command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return func() tea.Msg { return msg }
	}
	for _, child := range batch {
		if child == nil {
			continue
		}
		out := child()
		if _, ok := out.(docSearchMsg); ok {
			return func() tea.Msg { return out }
		}
	}
	t.Fatal("F issued no finder scan")
	return nil
}

// P fetches a PR and F no longer does. Both halves are asserted together so
// moving one without the other fails.
func TestFetchPRMovedToPAndFIsTheFinder(t *testing.T) {
	registry := keymap.NewRegistry()
	keymap.RegisterDefaults(registry)
	for key, want := range map[string]string{"P": "fetch-pr", "F": "find-file"} {
		got, ok := registry.CommandForContextKey("workspace-list", key)
		if !ok || got != want {
			t.Fatalf("workspace-list %q -> %q (bound=%v), want %q", key, got, ok, want)
		}
	}
	for key, want := range map[string]string{"ctrl+p": "find-file", "f": "search-project"} {
		got, ok := registry.CommandForContextKey("workspace-doc", key)
		if !ok || got != want {
			t.Fatalf("workspace-doc %q -> %q (bound=%v), want %q", key, got, ok, want)
		}
	}
	for key, want := range map[string]string{"esc": "search-cancel", "enter": "search-open"} {
		got, ok := registry.CommandForContextKey("workspace-doc-search", key)
		if !ok || got != want {
			t.Fatalf("workspace-doc-search %q -> %q (bound=%v), want %q", key, got, ok, want)
		}
	}

	p, _ := docSearchPlugin(t, true)
	p.closeDocPane()
	p.activePane = PaneSidebar
	p.handleListKeys(tea.KeyPressMsg{Code: 'P', Text: "P"})
	if p.viewMode != ViewModeFetchPR {
		t.Fatalf("P left view mode %v, want the fetch-PR modal", p.viewMode)
	}

	p.viewMode = ViewModeList
	p.clearFetchPRState()
	p.handleListKeys(tea.KeyPressMsg{Code: 'F', Text: "F"})
	if p.viewMode == ViewModeFetchPR {
		t.Fatal("F still opens the fetch-PR modal")
	}
	if p.focusedDocPane() == nil {
		t.Fatal("F did not open the finder pane")
	}
}

// The footer names both surfaces where they can be reached from, which is how
// this codebase surfaces a shortcut at all.
func TestDocPaneSearchCommandsAreAdvertised(t *testing.T) {
	registry := keymap.NewRegistry()
	keymap.RegisterDefaults(registry)
	p := New()
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return state.WorkspaceState{} },
		setWorkspaceState: func(string, state.WorkspaceState) error { return nil },
	}
	root := t.TempDir()
	if err := p.Init(&plugin.Context{WorkDir: root, ProjectRoot: root, Config: config.Default(), Keymap: registry, Epoch: 5}); err != nil {
		t.Fatal(err)
	}
	if got := commandNameByID(p.Commands(), "find-file"); got != "Find" {
		t.Fatalf("list footer find hint = %q, want Find", got)
	}

	live, _ := docSearchPlugin(t, true)
	doc := live.focusedDocPane()
	commands := live.Commands()
	if got := commandNameByID(commands, "find-file"); got != "Find" {
		t.Fatalf("document footer find hint = %q, want Find", got)
	}
	if got := commandNameByID(commands, "search-project"); got != "Search" {
		t.Fatalf("document footer search hint = %q, want Search", got)
	}

	live.openDocProjectSearch(doc)
	commands = live.Commands()
	for _, command := range commands {
		if command.Context != "workspace-doc-search" {
			t.Fatalf("an open search advertised %q in context %q", command.ID, command.Context)
		}
	}
	if got := commandNameByID(commands, "search-cancel"); got != "Cancel" {
		t.Fatalf("open-search footer cancel hint = %q, want Cancel", got)
	}
}

// A click inside the modal is the modal's; a press outside the pane dismisses
// it rather than leaving the pane stuck in search mode.
func TestDocPaneSearchMouseRouting(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	const width, height = 120, 30
	composePaneTree(t, p, width, height)
	doc := p.focusedDocPane()
	scanFinder(t, p, p.openDocFinder(doc))
	typeDocSearch(p, "g")
	composePaneTree(t, p, width, height)

	var rows []mouse.Region
	for _, region := range doc.modeRegions {
		if strings.HasPrefix(region.ID, filefind.RegionItem) {
			rows = append(rows, region)
		}
	}
	if len(rows) < 2 {
		t.Fatalf("the finder registered %d row regions, want at least two to click between", len(rows))
	}
	if doc.mode.finder.Cursor() != 0 {
		t.Fatalf("finder cursor starts at %d, want the first row", doc.mode.finder.Cursor())
	}
	second := rows[1]
	p.handleMouse(clickAt(second.Rect.X+1, second.Rect.Y))
	if doc.mode == nil {
		t.Fatal("a click on a row dismissed the finder")
	}
	if doc.mode.finder.Cursor() != 1 {
		t.Fatalf("a click on the second row left the cursor at %d", doc.mode.finder.Cursor())
	}

	p.handleMouse(clickAt(0, 0))
	if doc.mode != nil {
		t.Fatal("a click outside the pane left the finder open")
	}
}

func clickAt(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// `f` in the pane is the pane's only while the pane has the keyboard. On the
// diff tab it is still the diff's file picker.
func TestDiffTabFStillOpensTheFilePicker(t *testing.T) {
	p, _ := docSearchPlugin(t, false)
	p.closeDocPane()
	p.activePane = PanePreview
	p.previewTab = PreviewTabDiff
	p.multiFileDiff = &gitstatus.MultiFileDiff{Files: []gitstatus.FileDiffInfo{{}, {}}}

	p.handleListKeys(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if p.viewMode != ViewModeFilePicker {
		t.Fatalf("f on the diff tab left view mode %v, want the file picker", p.viewMode)
	}
}
