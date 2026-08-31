package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacecreate"
)

func docPaneTestPlugin(t *testing.T, root string, shell bool) *Plugin {
	t.Helper()
	p := New()
	p.ctx = &plugin.Context{WorkDir: root, Epoch: 17}
	p.width, p.height = 140, 36
	p.sidebarVisible = false
	p.activePane = PanePreview
	p.viewMode = ViewModeList
	p.SetFocused(true)
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.paneFocus = 1
	p.paneNextID = 2
	p.docs = make(map[int]*docPane)
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return state.WorkspaceState{} },
		setWorkspaceState: func(string, state.WorkspaceState) error { return nil },
	}
	if shell {
		p.shellSelected = true
		p.shells = []*ShellSession{{
			Name: "Shell", TmuxName: "test-shell",
			Agent: &Agent{TmuxPane: "%901", OutputBuf: tty.NewOutputBuffer(20)},
		}}
	} else {
		p.worktrees = []*Worktree{{
			Name: "selected", Path: root,
			Agent: &Agent{TmuxPane: "%902", OutputBuf: tty.NewOutputBuffer(20)},
		}}
	}
	return p
}

func workspacePaneLayout(s state.WorkspaceState, surface string) *state.PaneLayoutJSON {
	return s.PaneLayoutFor(surface)
}

func countLeavesOfKind(n *PaneNode, kind PaneKind) int {
	if n == nil {
		return 0
	}
	if n.Split != nil {
		return countLeavesOfKind(n.Split.A, kind) + countLeavesOfKind(n.Split.B, kind)
	}
	if n.Kind == kind {
		return 1
	}
	return 0
}

func layoutHasDocPath(layout *state.PaneLayoutJSON, path string) bool {
	if layout == nil {
		return false
	}
	for _, tab := range layout.Tabs {
		if tab.Path == path {
			return true
		}
	}
	if layout.Split == nil {
		return false
	}
	return layoutHasDocPath(layout.Split.A, path) || layoutHasDocPath(layout.Split.B, path)
}

func writeDocPaneFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenTerminalMarkdownUsesSelectedSurfaceAndStaysInWorkspace(t *testing.T) {
	for _, shell := range []bool{true, false} {
		name := "workspace"
		if shell {
			name = "shell"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeDocPaneFixture(t, root, "docs/README.md", "# selected surface\n")
			p := docPaneTestPlugin(t, root, shell)

			cmd := p.openTerminalPath("docs/README.md", 1)
			if cmd == nil {
				t.Fatal("markdown path did not return a load command")
			}
			doc, leaf := p.activeDocPane()
			resolvedRoot, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			if doc == nil || leaf == nil || doc.root != filepath.Clean(resolvedRoot) {
				t.Fatalf("opened doc = %#v leaf=%#v, want selected root %q", doc, leaf, root)
			}
			if p.paneRoot.Split == nil || p.paneRoot.Split.Axis != SplitCols || p.paneRoot.Split.Ratio != 50 {
				t.Fatalf("pane tree = %#v, want fixed 50/50 column split", p.paneRoot)
			}
			if p.FocusContext() != "workspace-doc" {
				t.Fatalf("focus context = %q, want workspace-doc", p.FocusContext())
			}
			if batch, ok := cmd().(tea.BatchMsg); !ok || len(batch) != 2 {
				t.Fatalf("open command = %T, want load plus exactly one selected-terminal resize", cmd())
			}
		})
	}
}

func TestDocPaneRetargetRejectsStaleLoadAndDoesNotResizeAgain(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "one.md", "OLD CONTENT\n")
	writeDocPaneFixture(t, root, "two.md", "NEW CONTENT\n")
	p := docPaneTestPlugin(t, root, true)

	first := p.openTerminalPath("one.md", 1)
	batch := first().(tea.BatchMsg)
	var stale docview.LoadedMsg
	for _, child := range batch {
		if msg, ok := child().(docview.LoadedMsg); ok {
			stale = msg
		}
	}
	if stale.Path == "" {
		t.Fatal("did not find doc load in open batch")
	}
	second := p.openTerminalPath("two.md", 1)
	if _, ok := second().(tea.BatchMsg); ok {
		t.Fatal("retarget unexpectedly emitted another resize batch")
	}
	p.applyDocLoaded(stale)
	doc, _ := p.activeDocPane()
	if titles := docTabTitles(doc); len(titles) != 2 || titles[1] != "two.md" {
		t.Fatalf("second open did not append a tab: %v", titles)
	}
	doc.view().SetSize(60, 4)
	if got := doc.view().View(); strings.Contains(got, "OLD CONTENT") || !strings.Contains(got, "Loading document") || !strings.Contains(got, "two.md") {
		t.Fatalf("stale result changed the active tab: %q", got)
	}
}

func TestDocumentSplitFocusChromeCloseRegionAndExactBox(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "docs/guide.md", "# Guide\n\nbody\n")
	p := docPaneTestPlugin(t, root, true)
	open := p.openTerminalPath("docs/guide.md", 0)
	for _, child := range open().(tea.BatchMsg) {
		if msg, ok := child().(docview.LoadedMsg); ok {
			p.applyDocLoaded(msg)
		}
	}

	_, leaf := p.activeDocPane()
	const width, height = 100, 12
	docFocused, ok := p.renderDocumentSplit(width, height)
	if !ok {
		t.Fatal("document split was not rendered")
	}
	lines := strings.Split(docFocused, "\n")
	if len(lines) != height {
		t.Fatalf("rendered rows = %d, want %d", len(lines), height)
	}
	for row, line := range lines {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("row %d width = %d, want %d: %q", row, got, width, line)
		}
	}
	if stripped := ansi.Strip(docFocused); !strings.Contains(stripped, "guide.md") || strings.Contains(stripped, "Rendered") || strings.Contains(stripped, "q close") || strings.Contains(stripped, "m raw") {
		t.Fatalf("document header is not a path tab strip: %q", stripped)
	}
	if !strings.Contains(ansi.Strip(docFocused), ui.CloseButtonLabel) {
		t.Fatalf("document header has no close button: %q", ansi.Strip(docFocused))
	}
	if docPaneRegion(p, regionDocTab) == nil {
		t.Fatal("drawn tab has no hit region")
	}
	if docPaneRegion(p, regionPaneClose) == nil {
		t.Fatal("header has no close hit region")
	}
	if docPaneRegion(p, "doc-mode") != nil || docPaneRegion(p, "doc-close") != nil {
		t.Fatal("header still registered old mode/close hit regions")
	}

	p.paneFocus = terminalLeafID(p.paneRoot)
	terminalFocused, ok := p.renderDocumentSplit(width, height)
	if !ok || terminalFocused == docFocused {
		t.Fatal("moving focus did not change header/divider treatment")
	}
	if !strings.Contains(ansi.Strip(terminalFocused), "▸ Shell") {
		t.Fatalf("terminal focus is not named in its header: %q", ansi.Strip(terminalFocused))
	}

	p.paneFocus = leaf.ID
	if cmd := p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyEscape}); cmd == nil || p.activeDocPaneOrNil() != nil {
		t.Fatal("escape did not close the document and schedule terminal resize")
	}
}

func TestDocPaneMAndModeChipToggleRender(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# Read me\n\nbody\n")
	p := docPaneTestPlugin(t, root, true)
	open := p.openTerminalPath("README.md", 0)
	for _, child := range open().(tea.BatchMsg) {
		if msg, ok := child().(docview.LoadedMsg); ok {
			p.applyDocLoaded(msg)
		}
	}
	doc, _ := p.activeDocPane()
	if doc == nil || !doc.view().Rendered() {
		t.Fatalf("opened doc = %#v, want rendered markdown", doc)
	}

	handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if !handled || doc.view().Rendered() {
		t.Fatalf("m did not toggle to raw: handled=%v rendered=%v", handled, doc.view().Rendered())
	}
	p.mouseHandler.Clear()
	rawView, ok := p.renderDocumentSplit(100, 12)
	if !ok {
		t.Fatal("document split was not rendered")
	}
	if stripped := ansi.Strip(rawView); strings.Contains(stripped, "Raw") || strings.Contains(stripped, "m render") {
		t.Fatalf("header still paints mode/hints: %q", stripped)
	}

	handled, _ = p.handleDocKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if !handled || doc.view().Rendered() {
		t.Fatalf("r should be absorbed without toggling: handled=%v rendered=%v", handled, doc.view().Rendered())
	}

	p.handleListKeys(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if !doc.view().Rendered() {
		t.Fatal("second m did not restore rendered mode")
	}

	p.paneFocus = terminalLeafID(p.paneRoot)
	p.handleListKeys(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if !doc.view().Rendered() {
		t.Fatal("m on the terminal pane toggled the document")
	}
	if p.viewMode != ViewModeList {
		t.Fatalf("m on the terminal pane changed view mode to %v", p.viewMode)
	}
}

func docPaneRegion(p *Plugin, id string) *mouse.Region {
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == id {
			regionCopy := region
			return &regionCopy
		}
	}
	return nil
}

func TestMOnFocusedDocTogglesRenderNotMerge(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# Read me\n")
	p := docPaneTestPlugin(t, root, false)
	open := p.openTerminalPath("README.md", 0)
	for _, child := range open().(tea.BatchMsg) {
		if msg, ok := child().(docview.LoadedMsg); ok {
			p.applyDocLoaded(msg)
		}
	}
	doc, leaf := p.activeDocPane()
	if doc == nil || leaf == nil || !doc.view().Rendered() {
		t.Fatalf("opened doc = %#v rendered=%v", doc, doc != nil && doc.view().Rendered())
	}
	if p.FocusContext() != "workspace-doc" {
		t.Fatalf("context = %q, want workspace-doc", p.FocusContext())
	}

	handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if !handled || doc.view().Rendered() {
		t.Fatalf("doc m: handled=%v rendered=%v", handled, doc.view().Rendered())
	}
}

func TestDocPaneTabStripLeftTruncatesDeepPath(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "internal/plugins/workspace/plugin.go", "package workspace\n")
	p := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, p, p.openTerminalPath("internal/plugins/workspace/plugin.go", 0))
	doc := p.activeDocPaneOrNil()
	if doc == nil {
		t.Fatal("doc did not open")
	}

	const width = 22
	got := ansi.Strip(layoutDocTabStrip(doc, width, true).Row)
	if !strings.Contains(got, "plugin.go") {
		t.Fatalf("narrow header dropped the filename: %q", got)
	}
	if strings.Contains(got, "internal/plugins") {
		t.Fatalf("narrow header did not left-truncate: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("narrow header has no start ellipsis: %q", got)
	}
}

func TestDocPaneTabStripShowsTwoFilenames(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "internal/plugins/workspace/plugin.go", "package workspace\n")
	p := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("internal/plugins/workspace/plugin.go", 0))
	doc := p.activeDocPaneOrNil()

	const width = 36
	strip := layoutDocTabStrip(doc, width, true)
	got := ansi.Strip(strip.Row)
	if !strings.Contains(got, "README.md") || !strings.Contains(got, "plugin.go") {
		t.Fatalf("two-tab header dropped a filename: %q", got)
	}
	if strings.Contains(got, "Rendered") || strings.Contains(got, "q close") {
		t.Fatalf("two-tab header still has chips/hints: %q", got)
	}
	if strings.Count(got, "×") != 2 {
		t.Fatalf("two-tab header = %q, want one × per tab", got)
	}
	if len(strip.Tabs) != 2 {
		t.Fatalf("visible tabs = %d, want 2: %q", len(strip.Tabs), got)
	}
	if cells := ansi.StringWidth(strip.Row); cells != width {
		t.Fatalf("header width = %d, want %d", cells, width)
	}
}

func TestDocPaneTabCloseClickClosesThatTab(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, true)
	p.width, p.height = 100, 20
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	doc := p.activeDocPaneOrNil()
	if doc.view().Title() != "main.go" {
		t.Fatalf("active = %q, want main.go", doc.view().Title())
	}

	p.mouseHandler.Clear()
	_ = p.renderListView(p.width, p.height)
	var closeHit *mouse.Region
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionDocTab {
			continue
		}
		hit, ok := region.Data.(docTabHit)
		if !ok || !hit.Close || hit.Index != 0 {
			continue
		}
		r := region
		closeHit = &r
		break
	}
	if closeHit == nil {
		t.Fatal("README tab has no close hit region")
	}
	x, y := closeHit.Rect.X, closeHit.Rect.Y
	resolved := p.mouseHandler.HitMap.Test(x, y)
	if resolved == nil || resolved.ID != regionDocTab {
		t.Fatalf("close cell (%d,%d) resolves to %#v, want %s", x, y, resolved, regionDocTab)
	}
	hit, ok := resolved.Data.(docTabHit)
	if !ok || !hit.Close || hit.Index != 0 {
		t.Fatalf("close cell (%d,%d) resolves to %#v, want README close", x, y, resolved.Data)
	}
	_ = p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	if titles := docTabTitles(doc); len(titles) != 1 || titles[0] != "main.go" {
		t.Fatalf("close click left %v, want [main.go]", titles)
	}
}

func TestDocPaneTabStripOverflowKeepsActiveFilename(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	for _, name := range []string{"one.md", "two.md", "three.md", "four.md", "five.md", "six.md"} {
		writeDocPaneFixture(t, root, name, "# "+name+"\n")
		applyDocOpen(t, p, p.openTerminalPath(name, 0))
	}
	doc := p.activeDocPaneOrNil()
	strip := layoutDocTabStrip(doc, 40, true)
	got := ansi.Strip(strip.Row)
	if !strings.Contains(got, "six.md") {
		t.Fatalf("overflow header dropped the active filename: %q", got)
	}
	if !strings.Contains(got, "<") {
		t.Fatalf("overflow header has no left marker: %q", got)
	}
	if strings.Contains(got, "one.md") {
		t.Fatalf("overflow window did not drop an offscreen tab: %q", got)
	}
}

func TestDocPaneTabRawCoordinateClickSelectsTab(t *testing.T) {
	for _, width := range []int{48, 100} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			root := t.TempDir()
			writeDocPaneFixture(t, root, "README.md", "# readme\n")
			writeDocPaneFixture(t, root, "main.go", "package main\n")
			p := docPaneTestPlugin(t, root, true)
			p.width, p.height = 140, 20
			applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
			applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
			p.width, p.height = width, 20
			doc := p.activeDocPaneOrNil()
			if doc.view().Title() != "main.go" {
				t.Fatalf("active = %q, want main.go", doc.view().Title())
			}

			p.mouseHandler.Clear()
			_ = p.renderListView(width, p.height)
			drawn := docPaneTabRegion(p, 0)
			if drawn == nil {
				t.Fatal("README tab has no rendered hit region")
			}
			x, y := drawn.Rect.X+drawn.Rect.W/2, drawn.Rect.Y
			resolved := p.mouseHandler.HitMap.Test(x, y)
			if resolved == nil || resolved.ID != regionDocTab {
				t.Fatalf("raw coordinate (%d,%d) resolves to %#v, want %s", x, y, resolved, regionDocTab)
			}
			if hit, ok := resolved.Data.(docTabHit); !ok || hit.Index != 0 {
				t.Fatalf("raw coordinate (%d,%d) resolves to %#v, want tab 0", x, y, resolved.Data)
			}

			_ = p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
			if doc.view().Title() != "README.md" {
				t.Fatalf("raw click did not select README: %q", doc.view().Title())
			}
			if docPaneRegion(p, "doc-close") != nil {
				t.Fatal("header registered a close hit region")
			}
		})
	}
}

func docPaneTabRegion(p *Plugin, index int) *mouse.Region {
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionDocTab {
			continue
		}
		if hit, ok := region.Data.(docTabHit); ok && hit.Index == index {
			regionCopy := region
			return &regionCopy
		}
	}
	return nil
}

func clickDrawnDocTab(t *testing.T, p *Plugin, index int) {
	t.Helper()
	doc, leaf := p.activeDocPane()
	if doc == nil || leaf == nil {
		t.Fatal("no document pane")
	}
	pane := docPaneRegion(p, regionPaneLeaf)
	if pane == nil {
		t.Fatal("document pane has no hit region")
	}
	inner := insetPanelChrome(pane.Rect)
	// The same reserve the renderer used, or this helper models a strip that is
	// not the one on screen: the header now carries the layout ⊞ beside the ×.
	strip := layoutDocTabStrip(doc, p.reserveHeader(inner.W, true).TabsWidth, p.paneFocus == leaf.ID)
	var tab *docTabPlacement
	for i := range strip.Tabs {
		if strip.Tabs[i].Index == index {
			tab = &strip.Tabs[i]
			break
		}
	}
	if tab == nil {
		t.Fatalf("tab %d is not drawn: %+v", index, strip.Tabs)
	}
	x := inner.X + tab.Col + tab.Width/2
	y := inner.Y
	resolved := p.mouseHandler.HitMap.Test(x, y)
	if resolved == nil || resolved.ID != regionDocTab {
		t.Fatalf("visual tab %d at (%d,%d) resolves to %#v, want %s", index, x, y, resolved, regionDocTab)
	}
	if hit, ok := resolved.Data.(docTabHit); !ok || hit.Index != index {
		t.Fatalf("visual tab %d at (%d,%d) resolves to %#v", index, x, y, resolved.Data)
	}
	_ = p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
}

func TestDocPaneVisualTabClickSelectsOnShellAndWorktree(t *testing.T) {
	for _, tc := range []struct {
		name  string
		shell bool
		side  bool
		width int
	}{
		{"shell full preview", true, false, 100},
		{"shell with sidebar", true, true, 120},
		{"worktree chips overlay", false, true, 120},
		{"worktree narrow", false, true, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeDocPaneFixture(t, root, "README.md", "# readme\n")
			writeDocPaneFixture(t, root, "main.go", "package main\n")
			p := docPaneTestPlugin(t, root, tc.shell)
			p.sidebarVisible = tc.side
			p.width, p.height = 140, 24
			applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
			applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
			p.width, p.height = tc.width, 24
			doc := p.activeDocPaneOrNil()
			if doc.view().Title() != "main.go" {
				t.Fatalf("active = %q, want main.go", doc.view().Title())
			}

			_ = p.View(tc.width, p.height)
			clickDrawnDocTab(t, p, 0)
			if doc.view().Title() != "README.md" {
				t.Fatalf("clicking README selected %q", doc.view().Title())
			}
			_ = p.View(tc.width, p.height)
			clickDrawnDocTab(t, p, 1)
			if doc.view().Title() != "main.go" {
				t.Fatalf("clicking main.go selected %q", doc.view().Title())
			}
		})
	}
}

func TestDocPaneClickOnRenderedFilenameSelectsTab(t *testing.T) {
	for _, tc := range []struct {
		name  string
		shell bool
		side  bool
		width int
	}{
		{"shell full preview", true, false, 100},
		{"shell with sidebar", true, true, 140},
		{"worktree with sidebar", false, true, 140},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeDocPaneFixture(t, root, "README.md", "# readme\n")
			writeDocPaneFixture(t, root, "main.go", "package main\n")
			p := docPaneTestPlugin(t, root, tc.shell)
			p.sidebarVisible = tc.side
			p.width, p.height = tc.width, 24
			applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
			applyDocOpen(t, p, p.openTerminalPath("main.go", 0))

			view := p.View(tc.width, p.height)
			x, y, ok := renderedFilenameCell(view, "README.md")
			if !ok {
				t.Fatalf("README.md not on the header row:\n%s", ansi.Strip(view))
			}
			resolved := p.mouseHandler.HitMap.Test(x, y)
			if resolved == nil || resolved.ID != regionDocTab {
				t.Fatalf("rendered README.md at (%d,%d) hits %#v, want %s\nheader=%q\nregions=%s",
					x, y, resolved, regionDocTab, headerRow(view), dumpRegions(p, y))
			}
			_ = p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
			if got := p.activeDocPaneOrNil().view().Title(); got != "README.md" {
				t.Fatalf("clicking rendered README.md selected %q", got)
			}
		})
	}
}

func headerRow(view string) string {
	lines := strings.Split(view, "\n")
	if len(lines) <= previewBorderRows {
		return ""
	}
	return ansi.Strip(lines[previewBorderRows])
}

func renderedFilenameCell(view, name string) (x, y int, ok bool) {
	lines := strings.Split(view, "\n")
	if len(lines) <= previewBorderRows {
		return 0, 0, false
	}
	plain := ansi.Strip(lines[previewBorderRows])
	at := strings.LastIndex(plain, name)
	if at < 0 {
		return 0, 0, false
	}
	return ansi.StringWidth(plain[:at]) + ansi.StringWidth(name)/2, previewBorderRows, true
}

func dumpRegions(p *Plugin, y int) string {
	var b strings.Builder
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if y < region.Rect.Y || y >= region.Rect.Y+region.Rect.H {
			continue
		}
		fmt.Fprintf(&b, " %s[%d:%d] data=%#v", region.ID, region.Rect.X, region.Rect.X+region.Rect.W, region.Data)
	}
	return b.String()
}

func TestDocPaneTabRowClickWinsOverPreviewPaneAndDivider(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, false)
	p.sidebarVisible = true
	p.width, p.height = 140, 24
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	view := p.View(p.width, p.height)
	x, y, ok := renderedFilenameCell(view, "README.md")
	if !ok {
		t.Fatal("README.md not on the header row")
	}

	// The live steal: Test() names the preview pane or the widened divider,
	// which used to start a terminal gesture and ignore the tab.
	_ = p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		X:      x,
		Y:      y,
		Region: &mouse.Region{ID: regionPreviewPane},
	})
	if got := p.activeDocPaneOrNil().view().Title(); got != "README.md" {
		t.Fatalf("preview-pane steal at (%d,%d) selected %q", x, y, got)
	}

	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	_ = p.View(p.width, p.height)
	tab := docPaneTabRegion(p, 0)
	if tab == nil {
		t.Fatal("README tab missing after re-render")
	}
	beforeRatio := p.paneRoot.Split.Ratio
	_ = p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		X:      tab.Rect.X,
		Y:      tab.Rect.Y + 1,
		Region: &mouse.Region{ID: regionPaneTreeDivider, Data: p.paneRoot.ID},
	})
	if p.mouseHandler.DragRegion() != regionPaneTreeDivider || p.paneRoot.Split.Ratio != beforeRatio {
		t.Fatalf("divider press was stolen by tab fallback: drag=%q ratio=%d", p.mouseHandler.DragRegion(), p.paneRoot.Split.Ratio)
	}
	if got := p.activeDocPaneOrNil().view().Title(); got != "main.go" {
		t.Fatalf("divider press selected file tab %q", got)
	}
}

func TestDocPaneTabRowClickDoesNotStealPreviewChips(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, false)
	p.sidebarVisible = true
	p.width, p.height = 140, 24
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	_ = p.View(p.width, p.height)

	var chip *mouse.Region
	for _, region := range p.mouseHandler.HitMap.Regions() {
		hit, ok := region.Data.(previewActionHit)
		if region.ID == regionPreviewAction && ok && hit == previewActionDiff {
			copy := region
			chip = &copy
			break
		}
	}
	if chip == nil {
		t.Fatal("worktree Diff chip was not registered")
	}
	_ = p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		X:      chip.Rect.X + chip.Rect.W/2,
		Y:      chip.Rect.Y,
		Region: chip,
	})
	if diff, _ := p.activeDiffPane(); diff == nil {
		t.Fatal("Diff chip did not open a Diff leaf")
	}
	if got := p.activeDocPaneOrNil().view().Title(); got != "main.go" {
		t.Fatalf("file tab changed to %q on a Diff chip click", got)
	}
}

func TestZoomedDocumentDoesNotRegisterPreviewActionChips(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, false)
	p.sidebarVisible = false
	p.width, p.height = 120, 24
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))

	p.width, p.height = 40, 24
	_ = p.View(p.width, p.height)
	if p.terminalSurfaceGeometry(false).OK {
		t.Fatal("expected the split to zoom away from the terminal")
	}
	if docPaneRegion(p, regionPreviewAction) != nil {
		t.Fatal("zoomed document still registered Diff/Task action chip targets")
	}
	clickDrawnDocTab(t, p, 0)
	if got := p.activeDocPaneOrNil().view().Title(); got != "README.md" {
		t.Fatalf("zoomed file-tab click selected %q", got)
	}
}

func TestDocumentCommandsDescribeCurrentMode(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# Read me\n")
	p := docPaneTestPlugin(t, root, true)
	p.openTerminalPath("README.md", 0)
	commands := p.Commands()
	if len(commands) == 0 || commands[0].Context != "workspace-doc" || commands[0].Name != "Close" || commands[0].ID != "close" {
		t.Fatalf("document commands = %#v", commands)
	}
	if commandNameByID(commands, "close-tab") != "Tab×" || commandNameByID(commands, "prev-tab") != "Tab←" || commandNameByID(commands, "next-tab") != "Tab→" {
		t.Fatalf("document tab commands = %#v", commands)
	}
	if commandNameByID(commands, "toggle-sidebar") != "Sidebar" || commandNameByID(commands, "render") != "Raw" {
		t.Fatalf("document sidebar/render commands = %#v", commands)
	}
	if commandNameByID(commands, "reload") != "Reload" {
		t.Fatalf("document reload command = %#v", commands)
	}
	if commandNameByID(commands, "toggle-wrap") != "Wrap" || commandNameByID(commands, "info") != "Info" || commandNameByID(commands, "reveal") != "Reveal" {
		t.Fatalf("document path-action commands = %#v", commands)
	}
	doc, _ := p.activeDocPane()
	doc.view().SetRendered(false)
	if got := commandNameByID(p.Commands(), "render"); got != "Render" {
		t.Fatalf("raw-mode action = %q, want Render", got)
	}

	writeDocPaneFixture(t, root, "main.go", "package main\n")
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	if got := commandNameByID(p.Commands(), "render"); got != "" {
		t.Fatalf("non-markdown footer still lists render: %q", got)
	}
}

func commandNameByID(commands []plugin.Command, id string) string {
	for _, command := range commands {
		if command.ID == id {
			return command.Name
		}
	}
	return ""
}

func TestDocumentBackslashHidesAndRestoresWorkspaceSidebar(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# Read me\n")
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = true
	p.openTerminalPath("README.md", 0)
	if p.FocusContext() != "workspace-doc" {
		t.Fatalf("focus context = %q, want workspace-doc", p.FocusContext())
	}

	handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: '\\', Text: "\\"})
	if !handled || cmd == nil || p.sidebarVisible || p.activePane != PanePreview {
		t.Fatalf("hide handled=%v cmd=%v visible=%v pane=%v", handled, cmd != nil, p.sidebarVisible, p.activePane)
	}
	if p.FocusContext() != "workspace-doc" {
		t.Fatalf("hidden document context = %q, want workspace-doc", p.FocusContext())
	}
	handled, _ = p.handleDocKey(tea.KeyPressMsg{Code: '\\', Text: "\\"})
	if !handled || !p.sidebarVisible || p.activePane != PaneSidebar {
		t.Fatalf("restore handled=%v visible=%v pane=%v", handled, p.sidebarVisible, p.activePane)
	}
}

func TestDocPaneFocusKeysMouseAndCloseLifecycle(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "line 1\nline 2\nline 3\nline 4\nline 5\n")
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = true
	open := p.openTerminalPath("README.md", 1)
	for _, child := range open().(tea.BatchMsg) {
		if msg, ok := child().(docview.LoadedMsg); ok {
			p.applyDocLoaded(msg)
		}
	}
	doc, leaf := p.activeDocPane()
	doc.view().SetSize(40, 2)

	p.handleListKeys(tea.KeyPressMsg{Code: 'j'})
	if got := doc.view().View(); strings.Contains(got, "line 1") {
		t.Fatalf("document did not scroll: %q", got)
	}
	p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.activePane != PaneSidebar {
		t.Fatalf("tab from doc focus = pane %v, want sidebar", p.activePane)
	}
	p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.activePane != PanePreview || p.paneFocus != terminalLeafID(p.paneRoot) {
		t.Fatalf("tab from sidebar did not focus terminal: pane=%v focus=%d", p.activePane, p.paneFocus)
	}
	p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.paneFocus != leaf.ID {
		t.Fatalf("tab from terminal focus = %d, want doc %d", p.paneFocus, leaf.ID)
	}

	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, PaneOnEntry: PanePreview}
	p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: regionPaneLeaf, Data: leaf.ID},
	})
	if p.viewMode != ViewModeList || p.interactiveState != nil || p.paneFocus != leaf.ID {
		t.Fatalf("doc click did not exit interactive and focus doc: mode=%v interactive=%#v focus=%d", p.viewMode, p.interactiveState, p.paneFocus)
	}

	close := p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyEscape})
	if close == nil || p.activeDocPaneOrNil() != nil || p.paneFocus != terminalLeafID(p.paneRoot) {
		t.Fatalf("escape did not close back to terminal: root=%#v focus=%d", p.paneRoot, p.paneFocus)
	}
	if got := len(p.docTerminalResizeCmds()); got != 1 {
		t.Fatalf("close resize fan-out = %d, want exactly one selected-terminal resize", got)
	}
}

func TestDocPaneNarrowRefusalAndFocusedLeafFallback(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# narrow\n")
	p := docPaneTestPlugin(t, root, true)
	p.width = 40
	if cmd := p.openTerminalPath("README.md", 1); cmd != nil {
		t.Fatal("narrow open returned a command")
	}
	if p.activeDocPaneOrNil() != nil || p.toastMessage == "" || p.paneFocus != 1 {
		t.Fatalf("narrow refusal left state: doc=%#v toast=%q focus=%d", p.activeDocPaneOrNil(), p.toastMessage, p.paneFocus)
	}

	// 80 cols with a 40% sidebar leaves a 48-col preview. Outer floors are
	// 14+1+34=49, so a 2-col markdown split must refuse rather than clip.
	p.width = 80
	p.sidebarVisible = true
	p.sidebarWidth = 40
	p.toastMessage = ""
	if cmd := p.openTerminalPath("README.md", 1); cmd != nil {
		t.Fatal("80/40% sidebar 2-col open returned a command")
	}
	if p.activeDocPaneOrNil() != nil || p.toastMessage == "" {
		t.Fatalf("80/40%% sidebar 2-col left state: doc=%#v toast=%q", p.activeDocPaneOrNil(), p.toastMessage)
	}
	p.sidebarVisible = false
	p.toastMessage = ""

	// An already-open pane can become too narrow after a terminal resize. The
	// focused leaf gets the whole outer preview instead of an under-floor split.
	p.width = 140
	p.openTerminalPath("README.md", 1)
	doc, leaf := p.activeDocPane()
	p.width = 40
	p.paneFocus = leaf.ID
	p.mouseHandler.Clear()
	got, ok := p.renderDocumentSplit(36, 20)
	if !ok || !strings.Contains(got, doc.view().Title()) {
		t.Fatalf("focused doc fallback not rendered full-size: ok=%v view=%q", ok, got)
	}
	if docPaneRegion(p, regionDocTab) == nil {
		t.Fatal("focused narrow fallback rendered no tab hit region")
	}
	if cmd := p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyEscape}); cmd == nil || p.activeDocPaneOrNil() != nil {
		t.Fatal("focused narrow fallback escape did not close the document")
	}

	// Reopen to retain the terminal fallback assertion: terminal focus at the
	// same narrow size composes one full-box leaf through the shared frame.
	p.width = 140
	p.openTerminalPath("README.md", 1)
	p.width = 40
	p.paneFocus = terminalLeafID(p.paneRoot)
	if _, ok := p.renderDocumentSplit(36, 20); !ok {
		t.Fatal("terminal-focused narrow layout did not compose the full terminal leaf")
	}
}

func TestDocPaneTargetAcceptsAnyReadablePath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"README.md", true},
		{"notes.MARKDOWN", true},
		{"main.go", true},
		{"archive.json", true},
		{"", false},
		{"   ", false},
	} {
		if got := docPaneTarget(tc.path); got != tc.want {
			t.Errorf("docPaneTarget(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestFeatureDisabledMarkdownKeepsFileBrowserRoute(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# file browser\n")
	p := New()
	p.ctx = &plugin.Context{WorkDir: root}
	p.shellSelected = true
	cmd := p.openTerminalPath("README.md", 7)
	if cmd == nil {
		t.Fatal("feature-disabled markdown path returned no command")
	}
	// The shell owns the focus-plus-navigate dispatch now; this host asks for
	// the jump and stops there.
	activation, ok := cmd().(app.ActivateTargetMsg)
	if !ok {
		t.Fatalf("feature-disabled route = %T, want an activation request", cmd())
	}
	if activation.Target.Kind != uirequest.TargetKindFile || activation.Target.Value != "README.md" || activation.Target.Line != 7 {
		t.Fatalf("feature-disabled route target = %+v", activation.Target)
	}
}

// TestForeignRootFileLinkAsksForACrossProjectJump: a terminal scanned against
// another checkout used to resolve the path and then silently do nothing. The
// host still owns the one thing the shell cannot know — which root the terminal
// was scanned against — but now says so instead of swallowing the jump.
func TestForeignRootFileLinkAsksForACrossProjectJump(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	p := New()
	p.ctx = &plugin.Context{WorkDir: root}

	cmd := p.activateFileForRoot(other, "README.md", 7)
	if cmd == nil {
		t.Fatal("foreign-root link returned no command")
	}
	activation, ok := cmd().(app.ActivateTargetMsg)
	if !ok {
		t.Fatalf("foreign-root route = %T, want an activation request", cmd())
	}
	if activation.Project != other {
		t.Fatalf("activation project = %q, want %q", activation.Project, other)
	}
	if activation.Target.Value != "README.md" || activation.Target.Line != 7 {
		t.Fatalf("activation target = %+v", activation.Target)
	}

	sameRoot := p.activateFileForRoot(root, "README.md", 7)
	if sameRoot == nil {
		t.Fatal("same-root link returned no command")
	}
	if got := sameRoot().(app.ActivateTargetMsg); got.Project != "" {
		t.Fatalf("same-root activation carried a project qualifier: %q", got.Project)
	}
}

func TestSelectionChangeClosesDocWithoutStealingSidebarFocus(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeDocPaneFixture(t, first, "README.md", "# first\n")
	p := docPaneTestPlugin(t, first, false)
	p.worktrees = append(p.worktrees, &Worktree{Name: "second", Path: second})
	p.openTerminalPath("README.md", 1)
	p.activePane = PaneSidebar
	p.selectedIdx = 1
	p.loadSelectedContent()
	if p.activeDocPaneOrNil() != nil {
		t.Fatal("selection change retained old-root document")
	}
	if p.activePane != PaneSidebar {
		t.Fatalf("selection reset stole focus: active pane = %v", p.activePane)
	}
}

func TestDocFocusedResizePersistsAndEmitsOneResize(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# resize\n")
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	var saved state.WorkspaceState
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	p.openTerminalPath("README.md", 0)

	cmd := p.handleListKeys(tea.KeyPressMsg{Code: '+'})
	if cmd == nil || p.paneRoot.Split.Ratio != 45 {
		t.Fatalf("grow doc ratio=%d cmd=%v, want 45 and resize", p.paneRoot.Split.Ratio, cmd != nil)
	}
	if layout := workspacePaneLayout(saved, "shell:test-shell"); layout == nil || layout.Split == nil || layout.Split.Ratio != 45 {
		t.Fatalf("saved layout after grow = %#v", saved.PaneLayouts)
	}
	if _, ok := cmd().(paneResizedMsg); !ok {
		t.Fatalf("keyboard resize command = %T, want one direct terminal resize", cmd())
	}

	for range 20 {
		p.handleListKeys(tea.KeyPressMsg{Code: '-'})
	}
	if p.paneRoot.Split.Ratio != paneMaxRatio {
		t.Fatalf("shrinking doc clamp ratio=%d, want %d", p.paneRoot.Split.Ratio, paneMaxRatio)
	}
}

func TestPaneTreeDividerDragIsLocalUntilSourceAwareRelease(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# drag\n")
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	p.sidebarWidth = 37
	var saves int
	var saved state.WorkspaceState
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saves++; saved = next; return nil },
	}
	p.openTerminalPath("README.md", 0)
	saves = 0
	p.selection.SelectRange(ui.SelectionPoint{Line: 1}, ui.SelectionPoint{Line: 2, Col: 2}, false)
	splitID := p.paneRoot.ID
	p.handleMouseClick(mouse.MouseAction{Region: &mouse.Region{ID: regionPaneTreeDivider, Data: splitID}, X: 70, Y: 5})

	if cmd := p.handleMouseDrag(mouse.MouseAction{DragStartID: regionPaneTreeDivider, DragDX: 14}); cmd != nil {
		t.Fatal("drag motion emitted a tmux resize command")
	}
	if p.paneRoot.Split.Ratio == 50 || saves != 0 {
		t.Fatalf("drag motion ratio=%d saves=%d, want live change without persistence", p.paneRoot.Split.Ratio, saves)
	}
	cmd := p.handleMouseDragEnd(mouse.MouseAction{
		DragStartID: regionPaneTreeDivider,
		Region:      &mouse.Region{ID: regionPreviewPane},
	})
	if cmd == nil || saves != 1 || p.sidebarWidth != 37 {
		t.Fatalf("release cmd=%v saves=%d sidebar=%d", cmd != nil, saves, p.sidebarWidth)
	}
	if !p.selection.HasSelection() {
		t.Fatal("pane divider release finalized or cleared terminal selection")
	}
	if _, ok := cmd().(paneResizedMsg); !ok {
		t.Fatalf("divider release command = %T, want one direct terminal resize", cmd())
	}
}

func TestRestorePaneLayoutPrunesStaleTabsAndRejectsOtherRoot(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "docs/valid.md", "# restored\n")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	saved := state.WorkspaceState{
		ShellTmuxName: "test-shell",
		PaneLayout: &state.PaneLayoutJSON{Root: resolvedRoot, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
			Axis: "cols", Ratio: 64,
			A: &state.PaneLayoutJSON{Kind: "terminal"},
			B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{
				{Path: "missing.md", Mode: "rendered"},
				{Path: "docs/valid.md", Mode: "raw"},
			}},
		}},
	}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	if !p.restoreSelectionState() {
		t.Fatal("saved shell selection was not restored")
	}
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view().Title() != "docs/valid.md" || doc.view().Rendered() || p.paneRoot.Split.Ratio != 64 {
		t.Fatalf("restored pane doc=%#v tree=%#v", doc, p.paneRoot)
	}
	if p.paneRestoreCmd == nil {
		t.Fatal("valid restored document did not schedule its load")
	}
	layout := workspacePaneLayout(saved, "shell:test-shell")
	if layout == nil || layout.Split == nil || len(layout.Split.B.Tabs) != 1 || layout.Split.B.Tabs[0].Path != "docs/valid.md" {
		t.Fatalf("stale tabs were not pruned from persisted layout: %#v", saved.PaneLayouts)
	}
	if saved.PaneLayout != nil {
		t.Fatalf("legacy paneLayout was still written: %#v", saved.PaneLayout)
	}

	other := t.TempDir()
	layout.Root = other
	p.resetPaneTreeToTerminal()
	p.docs = make(map[int]*docPane)
	p.paneRestoreCmd = nil
	p.restoreSelectionState()
	if p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil || p.paneRestoreCmd != nil {
		t.Fatal("layout from another terminal root was restored")
	}
}

// A nested stack beside the terminal used to be refused on restore, from a
// renderer that could compose exactly two leaves. The compositor places any
// tree the layout returns, so the refusal now costs the user the layout they
// left rather than protecting anything.
func TestRestorePaneLayoutAcceptsNestedDocumentStack(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "one.md", "one")
	writeDocPaneFixture(t, root, "two.md", "two")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	layout := &state.PaneLayoutJSON{Root: resolvedRoot, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: "terminal"},
		B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
			Axis: "rows", Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "one.md"}}},
			B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "two.md"}}},
		}},
	}}
	if cmd := p.restorePaneLayout(layout); cmd == nil {
		t.Fatal("nested stack restored without scheduling its loads")
	}
	// contentpanes allows at most one leaf per kind, so the second document is
	// dropped and its split collapses. A surviving empty doc leaf is a ghost.
	if p.paneRoot.Split == nil {
		t.Fatalf("terminal+doc split was lost: root=%#v", p.paneRoot)
	}
	if n := countLeavesOfKind(p.paneRoot, PaneDoc); n != 1 {
		t.Fatalf("doc leaves = %d, want 1 (no ghost second pane)", n)
	}
	if len(p.docs) != 1 {
		t.Fatalf("docs map = %d, want 1", len(p.docs))
	}
	if p.paneFocus != terminalLeafID(p.paneRoot) {
		t.Fatalf("restored focus = %d, want the terminal leaf", p.paneFocus)
	}
}

func TestSplitAfterRestoredDocumentLoadsTheVisibleFile(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# restored body\n")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	layout := &state.PaneLayoutJSON{Root: resolvedRoot, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: "terminal"},
		B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "README.md"}}},
	}}
	applyDocOpen(t, p, p.restorePaneLayout(layout))
	if p.contentDeck == nil {
		t.Fatal("restore did not adopt through the content deck; a later split would re-arm the viewers")
	}
	doc := p.activeDocPaneOrNil()
	if doc == nil || doc.view() == nil {
		t.Fatal("restore lost the document")
	}
	doc.view().SetSize(40, 8)
	if strings.Contains(ansi.Strip(doc.view().View()), "Loading document") {
		t.Fatalf("restore load did not complete: %q", doc.view().View())
	}

	stubTd(t)
	surfaceRoot, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("no selected terminal surface")
	}
	applyDocOpen(t, p, p.openIssuePaneForSurface(surfaceRoot, surface, "td-1111aa"))
	if len(p.docs) != 1 {
		t.Fatalf("split dropped the document pane: docs=%d", len(p.docs))
	}
	for _, d := range p.docs {
		if d.view() == nil {
			t.Fatal("document pane has no view after split")
		}
		d.view().SetSize(40, 8)
		got := ansi.Strip(d.view().View())
		if strings.Contains(got, "Loading document") {
			t.Fatalf("document pane stuck loading after split: %q", got)
		}
	}
}

// A leaf of a kind this build has never heard of is what the restore guard is
// still for: it would size a terminal against a box nothing draws into.
func TestRestorePaneLayoutRejectsUnknownLeafKind(t *testing.T) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	unknown := &PaneNode{ID: 3, Kind: PaneKind(99)}
	if supportedPaneTree(&PaneNode{ID: 1, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
		A: &PaneNode{ID: 2, Kind: PaneTerminal}, B: unknown}}) {
		t.Fatal("a leaf of an unknown kind was accepted")
	}
	layout := &state.PaneLayoutJSON{Root: resolvedRoot, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: "terminal"},
		B: &state.PaneLayoutJSON{Kind: "hologram"},
	}}
	// The decoder drops the leaf it cannot build and the split collapses onto
	// the terminal, which is the layout the user can still work in.
	if cmd := p.restorePaneLayout(layout); cmd != nil {
		t.Fatal("an unknown leaf scheduled a load")
	}
	if p.paneRoot.Split != nil || p.paneRoot.Kind != PaneTerminal {
		t.Fatalf("unknown leaf did not collapse to the terminal: root=%#v", p.paneRoot)
	}
}

func TestRestorePaneLayoutCollapsesNestedUnknownBesideDocument(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "one.md", "one")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	layout := &state.PaneLayoutJSON{Root: resolvedRoot, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: "terminal"},
		B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
			Axis: "rows", Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: "hologram"},
			B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "one.md"}}},
		}},
	}}
	if cmd := p.restorePaneLayout(layout); cmd == nil {
		t.Fatal("the surviving document did not schedule its load")
	}
	if p.paneRoot.Split == nil || countLeavesOfKind(p.paneRoot, PaneDoc) != 1 || countLeavesOfKind(p.paneRoot, PaneTerminal) != 1 {
		t.Fatalf("nested unknown did not collapse onto terminal|doc: root=%#v", p.paneRoot)
	}
	if p.paneRoot.Split.B != nil && p.paneRoot.Split.B.Split != nil {
		t.Fatalf("inner split around the unknown leaf survived: root=%#v", p.paneRoot)
	}
}

func TestRestorePaneLayoutCollapsesEscapingDocument(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	layout := &state.PaneLayoutJSON{Root: resolvedRoot, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: "terminal"},
		B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: outside}}},
	}}
	if cmd := p.restorePaneLayout(layout); cmd != nil {
		t.Fatal("escaping doc scheduled a load")
	}
	if p.paneRoot == nil || p.paneRoot.Split != nil || p.paneRoot.Kind != PaneTerminal || len(p.docs) != 0 {
		t.Fatalf("escaping doc did not collapse to terminal: root=%#v docs=%d", p.paneRoot, len(p.docs))
	}
}

func TestShellSelectionIdentityClosesSameRootDocument(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# shell A\n"+strings.Repeat("line\n", 40))
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	p.shells = append(p.shells, &ShellSession{Name: "Shell B", TmuxName: "test-shell-b", Agent: &Agent{TmuxPane: "%903", OutputBuf: tty.NewOutputBuffer(20)}})
	var saved state.WorkspaceState
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	doc, _ := p.activeDocPane()
	if doc == nil || doc.surface != "shell:test-shell" {
		t.Fatalf("opened doc surface = %#v", doc)
	}
	doc.view().SetSize(30, 4)
	doc.view().Scroll(6)
	wantScroll := doc.view().ScrollOffset()
	selectedRoot, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("selected shell has no terminal surface")
	}
	issueCmd := p.openIssuePaneForSurface(selectedRoot, surface, "td-1a2b3c")
	if issueCmd == nil {
		t.Fatal("issue did not open beside document")
	}
	if batch, ok := issueCmd().(tea.BatchMsg); ok {
		for _, child := range batch {
			if child == nil {
				continue
			}
			if loaded, ok := child().(issueview.LoadedMsg); ok {
				p.applyIssueLoaded(loaded)
			}
		}
	}
	issue, _ := p.activeIssuePane()
	if issue == nil || issue.view().Data() == nil {
		t.Fatal("issue load did not land")
	}
	issue.view().SetSize(30, 4)
	issue.view().Scroll(5)
	wantIssueScroll := issue.view().ScrollOffset()

	// Encode A before the index changes. A test that only inspects the map
	// after a finished switch would miss writing B's empty tree onto A's key.
	p.selectTopShellAt(1)
	p.saveSelectionState()
	aLayout := workspacePaneLayout(saved, "shell:test-shell")
	if saved.ShellTmuxName != "test-shell-b" || !layoutHasDocPath(aLayout, "README.md") {
		t.Fatalf("shell A layout missing after selecting B: %#v", saved)
	}
	if layoutHasDocPath(workspacePaneLayout(saved, "shell:test-shell-b"), "README.md") {
		t.Fatalf("shell B stole shell A's document: %#v", saved.PaneLayouts)
	}
	if p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil {
		t.Fatal("shell B live tree is not terminal-only")
	}
	if saved.PaneLayout != nil {
		t.Fatalf("legacy paneLayout was still written: %#v", saved.PaneLayout)
	}

	p.selectTopShellAt(0)
	reopened, _ := p.activeDocPane()
	if reopened == nil || reopened.view().Title() != "README.md" || reopened.surface != "shell:test-shell" || reopened.view().ScrollOffset() != wantScroll {
		t.Fatalf("selecting A again did not reopen README: %#v", reopened)
	}
	reopenedIssue, _ := p.activeIssuePane()
	if reopenedIssue == nil || reopenedIssue.view().IssueID() != "td-1a2b3c" || reopenedIssue.view().ScrollOffset() != wantIssueScroll {
		t.Fatalf("selecting A again did not restore issue scroll: %#v", reopenedIssue)
	}
}

func TestSurfaceKindIdentityClosesSameRootDocument(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# shell\n")
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	p.worktrees = []*Worktree{{Name: "same-root", Path: root, Agent: &Agent{TmuxPane: "%904", OutputBuf: tty.NewOutputBuffer(20)}}}
	var saved state.WorkspaceState
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	p.openTerminalPath("README.md", 0)
	p.selectWorktreeAt(0)
	p.saveSelectionState()
	aLayout := workspacePaneLayout(saved, "shell:test-shell")
	wsSurface := "workspace:" + stablePathKey(root)
	if !layoutHasDocPath(aLayout, "README.md") {
		t.Fatalf("shell A layout missing after selecting same-root workspace: %#v", saved)
	}
	if layoutHasDocPath(workspacePaneLayout(saved, wsSurface), "README.md") {
		t.Fatalf("workspace stole shell A's document: %#v", saved.PaneLayouts)
	}
	if p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil {
		t.Fatal("same-root workspace live tree is not terminal-only")
	}

	p.selectTopShellAt(0)
	reopened, _ := p.activeDocPane()
	if reopened == nil || reopened.view().Title() != "README.md" || reopened.surface != "shell:test-shell" {
		t.Fatalf("selecting the shell again did not reopen README: %#v", reopened)
	}
}

func TestKillingSelectedShellDoesNotWipeTheNextSurface(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# A\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	p.shells = append(p.shells, &ShellSession{Name: "Shell B", TmuxName: "test-shell-b", Agent: &Agent{TmuxPane: "%903", OutputBuf: tty.NewOutputBuffer(20)}})
	var saved state.WorkspaceState
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	p.openTerminalPath("README.md", 0)
	p.selectTopShellAt(1)
	p.openTerminalPath("main.go", 0)
	p.selectTopShellAt(0)
	p.saveSelectionState()
	if !layoutHasDocPath(workspacePaneLayout(saved, "shell:test-shell-b"), "main.go") {
		t.Fatalf("B layout missing before kill: %#v", saved.PaneLayouts)
	}

	updated, _ := p.Update(ShellKilledMsg{SessionName: "test-shell"})
	p = updated.(*Plugin)
	p.saveSelectionState()
	bLayout := workspacePaneLayout(saved, "shell:test-shell-b")
	if workspacePaneLayout(saved, "shell:test-shell") != nil {
		t.Fatalf("killing A retained its owned layout: %#v", saved.PaneLayouts)
	}
	if !layoutHasDocPath(bLayout, "main.go") {
		t.Fatalf("killing A wiped B: %#v", saved.PaneLayouts)
	}
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view().Title() != "main.go" || doc.surface != "shell:test-shell-b" {
		t.Fatalf("live tree after killing A = %#v, want B's main.go", doc)
	}
}

func TestManifestSyncPreservesPaneLayoutsByShellIdentity(t *testing.T) {
	for _, selected := range []string{"removed A", "surviving B after earlier removal"} {
		t.Run(selected, func(t *testing.T) {
			root := t.TempDir()
			writeDocPaneFixture(t, root, "README.md", "# A\n")
			writeDocPaneFixture(t, root, "main.go", "package main\n")
			p, saved := persistDocPanePlugin(t, root)
			applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
			p.selectTopShellAt(1)
			applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
			if selected == "removed A" {
				p.selectTopShellAt(0)
			}
			p.saveSelectionState()

			// A real path in a temp dir, not the zero value: the sync path
			// writes restore markers through this handle, and a pathless
			// manifest used to resolve its lock file against the process's
			// working directory — the source tree, under `go test`.
			manifest := &ShellManifest{Version: manifestVersion, path: filepath.Join(t.TempDir(), "shells.json"),
				Shells: []ShellDefinition{{
					TmuxName: "test-shell-b", DisplayName: "Shell B", WorkDir: root,
				}}}
			p.shellManifest = manifest
			p.applyManifestSync(shellManifestSyncMsg{
				Manifest: manifest,
				Running:  map[string]bool{"test-shell-b": true},
				PaneIDs:  map[string]string{"test-shell-b": "%903"},
			})

			if !p.shellSelected || p.selectedShellIdx != 0 || p.getSelectedShell() == nil || p.getSelectedShell().TmuxName != "test-shell-b" {
				t.Fatalf("selection after sync = shell:%v index:%d selected:%#v", p.shellSelected, p.selectedShellIdx, p.getSelectedShell())
			}
			doc, _ := p.activeDocPane()
			if doc == nil || doc.surface != "shell:test-shell-b" || doc.view().Title() != "main.go" {
				t.Fatalf("live tree after sync = %#v, want B's main.go", doc)
			}
			if workspacePaneLayout(*saved, "shell:test-shell") != nil {
				t.Fatalf("dropped A layout survived: %#v", saved.PaneLayouts)
			}

			// Stop is the exact path that exposed the identity bug: it must save
			// B's restored/live tree back under B, never A's tree or terminal-only.
			p.Stop()
			bLayout := workspacePaneLayout(*saved, "shell:test-shell-b")
			if !layoutHasDocPath(bLayout, "main.go") || layoutHasDocPath(bLayout, "README.md") {
				t.Fatalf("Stop corrupted B after manifest sync: %#v", saved.PaneLayouts)
			}
		})
	}
}

func TestRemovingSelectedWorktreeForgetsOnlyItsOwnedSurfacesAndRetargets(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	p := docPaneTestPlugin(t, rootA, false)
	p.ctx.ProjectRoot = rootA
	p.worktrees = []*Worktree{
		{Key: "A-key", Name: "A", Path: rootA},
		{Key: "B-key", Name: "B", Path: rootB},
	}
	p.selectedIdx = 0
	p.paneLayoutSurface = workspaceSurfaceIdentity(p.worktrees[0])
	ownedNested := "shell:nested-A"
	siblingShell := "shell:unrelated"
	p.nestedByWorkDir = map[string][]*ShellSession{
		filepath.Clean(rootA): {{TmuxName: "nested-A", WorkDir: rootA}},
	}
	saved := state.WorkspaceState{PaneLayouts: map[string]*state.PaneLayoutJSON{
		workspaceSurfaceIdentity(p.worktrees[0]):       {Surface: workspaceSurfaceIdentity(p.worktrees[0]), Kind: contentKindTerminal, Open: true},
		legacyWorkspaceSurfaceIdentity(p.worktrees[0]): {Surface: legacyWorkspaceSurfaceIdentity(p.worktrees[0]), Kind: contentKindTerminal, Open: true},
		workspaceSurfaceIdentity(p.worktrees[1]):       {Root: rootB, Surface: workspaceSurfaceIdentity(p.worktrees[1]), Kind: contentKindTerminal, Open: true},
		ownedNested:                                    {Surface: ownedNested, Kind: contentKindTerminal, Open: true},
		siblingShell:                                   {Surface: siblingShell, Kind: contentKindTerminal, Open: true},
	}}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	p.removeWorktreeByName("A")
	if len(p.worktrees) != 1 || p.selectedWorktree() != p.worktrees[0] || p.worktrees[0].Name != "B" {
		t.Fatalf("retarget after removal: index=%d worktrees=%#v", p.selectedIdx, p.worktrees)
	}
	for _, removed := range []string{"workspace:A-key", legacyWorkspaceSurfaceIdentity(&Worktree{Path: rootA}), ownedNested} {
		if saved.PaneLayouts[removed] != nil {
			t.Fatalf("removed owner retained %q: %#v", removed, saved.PaneLayouts)
		}
	}
	if saved.PaneLayouts["workspace:B-key"] == nil || saved.PaneLayouts[siblingShell] == nil {
		t.Fatalf("removing A damaged siblings: %#v", saved.PaneLayouts)
	}
}

func TestRestorePaneLayoutsMapWithoutLegacyField(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# from map\n")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	saved := state.WorkspaceState{
		ShellTmuxName: "test-shell",
		PaneLayouts: map[string]*state.PaneLayoutJSON{
			"shell:test-shell": {Root: resolvedRoot, Surface: "shell:test-shell", Open: true, Split: &state.PaneSplitJSON{
				Axis: "cols", Ratio: 58,
				A: &state.PaneLayoutJSON{Kind: "terminal"},
				B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "README.md", Mode: "rendered"}}},
			}},
		},
	}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	if !p.restoreSelectionState() {
		t.Fatal("saved shell selection was not restored")
	}
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view().Title() != "README.md" || p.paneRoot.Split == nil || p.paneRoot.Split.Ratio != 58 {
		t.Fatalf("map layout did not restore: doc=%#v tree=%#v", doc, p.paneRoot)
	}
	if saved.PaneLayout != nil {
		t.Fatalf("legacy paneLayout was written from a map-only record: %#v", saved.PaneLayout)
	}
}

func docTabTitles(doc *docPane) []string {
	if doc == nil {
		return nil
	}
	out := make([]string, 0, len(doc.tabs.Items))
	for _, item := range doc.tabs.Items {
		if item.View != nil {
			out = append(out, item.View.Title())
		}
	}
	return out
}

func firstDocLeafTabs(layout *state.PaneLayoutJSON) (tabs []state.PaneDocTabJSON, active int) {
	if layout == nil {
		return nil, 0
	}
	if len(layout.Tabs) > 0 {
		return layout.Tabs, layout.Active
	}
	if layout.Split == nil {
		return nil, 0
	}
	if tabs, active = firstDocLeafTabs(layout.Split.B); len(tabs) > 0 {
		return tabs, active
	}
	return firstDocLeafTabs(layout.Split.A)
}

func applyDocOpen(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	applyDocOpenMsg(t, p, cmd())
}

func applyDocOpenMsg(t *testing.T, p *Plugin, msg tea.Msg) {
	t.Helper()
	switch m := msg.(type) {
	case tea.BatchMsg:
		for _, child := range m {
			if child != nil {
				applyDocOpenMsg(t, p, child())
			}
		}
	case contentpanes.Result:
		applyDocOpen(t, p, p.applyWorkspaceDeckResult(m))
	case docview.LoadedMsg:
		p.applyDocLoaded(m)
	}
}

func TestDocPaneOpenAppendsAndSelectsExisting(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\none\ntwo\nthree\nfour\nfive\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	var saved state.WorkspaceState
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}

	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	doc, _ := p.activeDocPane()
	if titles := docTabTitles(doc); len(titles) != 2 || titles[0] != "README.md" || titles[1] != "main.go" {
		t.Fatalf("open tabs = %v", titles)
	}
	if doc.view().Title() != "main.go" {
		t.Fatalf("active after append = %q", doc.view().Title())
	}

	again := p.openTerminalPath("README.md", 0)
	if again != nil {
		t.Fatal("selecting an already-open tab issued a load")
	}
	if titles := docTabTitles(doc); len(titles) != 2 || doc.view().Title() != "README.md" {
		t.Fatalf("select existing = %v active=%q", titles, doc.view().Title())
	}

	doc.view().SetSize(40, 2)
	p.openTerminalPath("README.md", 4)
	if doc.view().Rendered() || doc.view().ScrollOffset() != 3 {
		t.Fatalf("line target on existing tab: rendered=%v scroll=%d", doc.view().Rendered(), doc.view().ScrollOffset())
	}

	tabs, active := firstDocLeafTabs(workspacePaneLayout(saved, "shell:test-shell"))
	if len(tabs) != 2 || active != 0 || tabs[0].Path != "README.md" || tabs[1].Path != "main.go" {
		t.Fatalf("persisted tabs = %#v active=%d", tabs, active)
	}
}

func TestDocPaneCloseTabAndCycle(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))

	handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: '{', Text: "{"})
	doc, _ := p.activeDocPane()
	if !handled || doc.view().Title() != "README.md" {
		t.Fatalf("{ did not select README: handled=%v title=%q", handled, doc.view().Title())
	}
	handled, _ = p.handleDocKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	if !handled || doc.view().Title() != "main.go" {
		t.Fatalf("} did not select main.go: handled=%v title=%q", handled, doc.view().Title())
	}

	handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !handled || p.activeDocPaneOrNil() == nil || docTabTitles(p.activeDocPaneOrNil())[0] != "README.md" {
		t.Fatalf("x did not leave README: handled=%v titles=%v cmd=%v", handled, docTabTitles(p.activeDocPaneOrNil()), cmd != nil)
	}
	if p.paneRoot.Split == nil {
		t.Fatal("x on the non-last tab closed the pane")
	}

	handled, close := p.handleDocKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !handled || close == nil || p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil {
		t.Fatalf("last x did not forget the pane: handled=%v root=%#v", handled, p.paneRoot)
	}
}

func persistDocPanePlugin(t *testing.T, root string) (*Plugin, *state.WorkspaceState) {
	t.Helper()
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	p.shells = append(p.shells, &ShellSession{Name: "Shell B", TmuxName: "test-shell-b", Agent: &Agent{TmuxPane: "%903", OutputBuf: tty.NewOutputBuffer(20)}})
	saved := state.WorkspaceState{}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	return p, &saved
}

func TestDocPaneQHidesAndRestoresAcrossSwitch(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p, saved := persistDocPanePlugin(t, root)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))

	handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if !handled || cmd == nil || p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil {
		t.Fatalf("q did not hide to full-width terminal: handled=%v root=%#v", handled, p.paneRoot)
	}
	hidden := workspacePaneLayout(*saved, "shell:test-shell")
	tabs, active := firstDocLeafTabs(hidden)
	if state.PaneLayoutOpen(hidden) || len(tabs) != 2 || active != 1 || tabs[0].Path != "README.md" || tabs[1].Path != "main.go" {
		t.Fatalf("q persist = %#v tabs=%#v active=%d", hidden, tabs, active)
	}

	p.selectTopShellAt(1)
	p.saveSelectionState()
	if p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil {
		t.Fatal("B live tree is not terminal-only")
	}
	if !layoutHasDocPath(workspacePaneLayout(*saved, "shell:test-shell"), "README.md") {
		t.Fatalf("switch-away dropped A's hidden tabs: %#v", saved.PaneLayouts)
	}

	p.selectTopShellAt(0)
	reopened := p.activeDocPaneOrNil()
	if titles := docTabTitles(reopened); len(titles) != 2 || titles[0] != "README.md" || titles[1] != "main.go" || reopened.view().Title() != "main.go" {
		t.Fatalf("switch-back after q = %v active=%q", titles, titleOrEmpty(reopened))
	}
}

func TestDocPaneEscHidesLikeQ(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	p, saved := persistDocPanePlugin(t, root)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))

	handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled || cmd == nil || p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil {
		t.Fatalf("esc did not hide: handled=%v root=%#v", handled, p.paneRoot)
	}
	if state.PaneLayoutOpen(workspacePaneLayout(*saved, "shell:test-shell")) || !layoutHasDocPath(workspacePaneLayout(*saved, "shell:test-shell"), "README.md") {
		t.Fatalf("esc persist = %#v", saved.PaneLayouts)
	}
}

func TestDocPaneLastXForgetsAcrossSwitch(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p, saved := persistDocPanePlugin(t, root)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))

	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'x', Text: "x"}); !handled || p.paneRoot.Split == nil {
		t.Fatal("first x closed the pane")
	}
	if !state.PaneLayoutOpen(workspacePaneLayout(*saved, "shell:test-shell")) {
		t.Fatal("x on a non-last tab hid the pane")
	}
	handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !handled || cmd == nil || p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil {
		t.Fatalf("last x did not forget: handled=%v root=%#v", handled, p.paneRoot)
	}
	forgotten := workspacePaneLayout(*saved, "shell:test-shell")
	if layoutHasDocPath(forgotten, "README.md") || layoutHasDocPath(forgotten, "main.go") {
		t.Fatalf("last x kept tabs: %#v", forgotten)
	}

	p.selectTopShellAt(1)
	p.saveSelectionState()
	p.selectTopShellAt(0)
	if p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil {
		t.Fatalf("forgotten pane came back: %#v", p.paneRoot)
	}
}

func TestDocPaneClickWhileHiddenReopensRememberedSet(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	writeDocPaneFixture(t, root, "notes.md", "# notes\n")
	p, saved := persistDocPanePlugin(t, root)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	if handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: '+'}); !handled || cmd == nil || p.paneRoot.Split.Ratio != 45 {
		t.Fatalf("grow before hide ratio=%d", p.paneRoot.Split.Ratio)
	}

	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); !handled || p.paneRoot.Split != nil {
		t.Fatal("q did not hide")
	}
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	reopened := p.activeDocPaneOrNil()
	if titles := docTabTitles(reopened); len(titles) != 2 || titles[0] != "README.md" || titles[1] != "main.go" || reopened.view().Title() != "README.md" {
		t.Fatalf("click existing while hidden = %v active=%q", titles, titleOrEmpty(reopened))
	}
	if p.paneRoot.Split == nil || p.paneRoot.Split.Ratio != 45 {
		t.Fatalf("reopen ratio = %#v, want 45", p.paneRoot.Split)
	}
	if !state.PaneLayoutOpen(workspacePaneLayout(*saved, "shell:test-shell")) {
		t.Fatal("click while hidden left Open=false")
	}

	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); !handled || p.paneRoot.Split != nil {
		t.Fatal("second q did not hide")
	}
	applyDocOpen(t, p, p.openTerminalPath("notes.md", 0))
	appended := p.activeDocPaneOrNil()
	if titles := docTabTitles(appended); len(titles) != 3 || titles[2] != "notes.md" || appended.view().Title() != "notes.md" {
		t.Fatalf("click new while hidden = %v active=%q", titles, titleOrEmpty(appended))
	}
}

func TestRestoreHiddenPaneLayoutKeepsTabsWithoutSplit(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# A\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	saved := state.WorkspaceState{
		ShellTmuxName: "test-shell",
		PaneLayouts: map[string]*state.PaneLayoutJSON{
			"shell:test-shell": {Root: resolvedRoot, Surface: "shell:test-shell", Open: false, Split: &state.PaneSplitJSON{
				Axis: "cols", Ratio: 41,
				A: &state.PaneLayoutJSON{Kind: "terminal"},
				B: &state.PaneLayoutJSON{Kind: "doc", Active: 1, Tabs: []state.PaneDocTabJSON{
					{Path: "README.md", Mode: "rendered"},
					{Path: "main.go", Mode: "raw"},
				}},
			}},
		},
	}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	if !p.restoreSelectionState() {
		t.Fatal("saved shell selection was not restored")
	}
	if p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil {
		t.Fatalf("relaunch restored a hidden split: root=%#v", p.paneRoot)
	}
	if !layoutHasDocPath(workspacePaneLayout(saved, "shell:test-shell"), "main.go") {
		t.Fatalf("relaunch dropped hidden tabs: %#v", saved.PaneLayouts)
	}

	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	doc := p.activeDocPaneOrNil()
	if titles := docTabTitles(doc); len(titles) != 2 || titles[0] != "README.md" || titles[1] != "main.go" || doc.view().Title() != "main.go" {
		t.Fatalf("click after relaunch hide = %v active=%q", titles, titleOrEmpty(doc))
	}
	if p.paneRoot.Split == nil || p.paneRoot.Split.Ratio != 41 {
		t.Fatalf("relaunch reopen ratio = %#v, want 41", p.paneRoot.Split)
	}
}

func titleOrEmpty(doc *docPane) string {
	if doc == nil || doc.view() == nil {
		return ""
	}
	return doc.view().Title()
}

func TestDocPaneTabsPersistAcrossShellSwitch(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# A\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	p.shells = append(p.shells, &ShellSession{Name: "Shell B", TmuxName: "test-shell-b", Agent: &Agent{TmuxPane: "%903", OutputBuf: tty.NewOutputBuffer(20)}})
	var saved state.WorkspaceState
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))

	p.selectTopShellAt(1)
	p.saveSelectionState()
	tabs, active := firstDocLeafTabs(workspacePaneLayout(saved, "shell:test-shell"))
	if len(tabs) != 2 || active != 1 || tabs[0].Path != "README.md" || tabs[1].Path != "main.go" {
		t.Fatalf("A tabs missing after selecting B: %#v active=%d", tabs, active)
	}
	if p.activeDocPaneOrNil() != nil {
		t.Fatal("B live tree is not terminal-only")
	}

	p.selectTopShellAt(0)
	reopened := p.activeDocPaneOrNil()
	if titles := docTabTitles(reopened); len(titles) != 2 || titles[0] != "README.md" || titles[1] != "main.go" || reopened.view().Title() != "main.go" {
		t.Fatalf("selecting A again = %v active=%q", titles, reopened.view().Title())
	}
}

func TestWorkspaceSurfaceRekeysLegacySymlinkIdentityOnRestore(t *testing.T) {
	realRoot := t.TempDir()
	writeDocPaneFixture(t, realRoot, "README.md", "# canonical\n")
	resolvedRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, link, false)
	p.ctx.ProjectRoot = realRoot
	canonical := workspaceSurfaceIdentity(p.worktrees[0])
	legacy := legacyWorkspaceSurfaceIdentity(p.worktrees[0])
	if canonical == legacy {
		t.Fatalf("test needs distinct identities, both were %q", canonical)
	}
	saved := state.WorkspaceState{WorkspaceName: p.worktrees[0].Name, PaneLayouts: map[string]*state.PaneLayoutJSON{
		legacy: {
			Root: resolvedRoot, Surface: legacy, Open: true,
			Split: &state.PaneSplitJSON{Axis: "cols", Ratio: 50,
				A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
				B: &state.PaneLayoutJSON{Kind: contentKindDoc, Tabs: []state.PaneDocTabJSON{{Path: "README.md"}}},
			},
		},
	}}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	p.restoreIncomingPaneLayoutHonoringOpen()
	if saved.PaneLayouts[legacy] != nil || saved.PaneLayouts[canonical] == nil || saved.PaneLayouts[canonical].Surface != canonical {
		t.Fatalf("surface migration = %#v", saved.PaneLayouts)
	}
	if p.activeDocPaneOrNil() == nil {
		root, surface, ok := p.selectedTerminalSurface()
		t.Fatalf("canonical restore did not rebuild the document: root=%q surface=%q ok=%v layout=%#v tree=%#v", root, surface, ok, saved.PaneLayouts[canonical], p.paneRoot)
	}
}

func TestRestorePaneLayoutLoadsOnlyActiveTab(t *testing.T) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	tabs := make([]state.PaneDocTabJSON, 8)
	for i := range tabs {
		rel := "docs/f" + string(rune('0'+i)) + ".md"
		writeDocPaneFixture(t, root, rel, "# "+rel+"\nline 2\nline 3\nline 4\nline 5\n")
		tabs[i] = state.PaneDocTabJSON{Path: rel, Mode: "raw", Scroll: i + 1}
	}
	p := docPaneTestPlugin(t, root, true)
	layout := &state.PaneLayoutJSON{Root: resolvedRoot, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: "terminal"},
		B: &state.PaneLayoutJSON{Kind: "doc", Active: 3, Tabs: tabs},
	}}
	cmd := p.restorePaneLayout(layout)
	if cmd == nil {
		t.Fatal("restore scheduled no load")
	}
	doc := p.activeDocPaneOrNil()
	if doc == nil || len(doc.tabs.Items) != 8 || doc.tabs.Active != 3 {
		t.Fatalf("restored tabs = %#v", doc)
	}
	for i, item := range doc.tabs.Items {
		if item.View == nil {
			t.Fatalf("tab %d has no model", i)
		}
		if item.View.NeedsLoad() == (i == 3) {
			t.Fatalf("tab %d NeedsLoad=%v, want only the active tab loaded", i, item.View.NeedsLoad())
		}
		if item.View.Title() != tabs[i].Path {
			t.Fatalf("tab %d path = %q, want %q", i, item.View.Title(), tabs[i].Path)
		}
	}
	msg := cmd()
	if _, ok := msg.(tea.BatchMsg); ok {
		t.Fatalf("restore issued a batch, want one Load: %T", msg)
	}
	loaded, ok := msg.(docview.LoadedMsg)
	if !ok || loaded.Path != tabs[3].Path {
		t.Fatalf("restore load = %#v, want %q", msg, tabs[3].Path)
	}
	p.applyDocLoaded(loaded)
	if doc.view().ScrollOffset() != 4 {
		t.Fatalf("active scroll = %d, want 4", doc.view().ScrollOffset())
	}

	p.activePane = PanePreview
	if leaf := firstPaneLeafOfKind(p.paneRoot, PaneDoc); leaf != nil {
		p.paneFocus = leaf.ID
	}
	handled, load := p.handleDocKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	if !handled || load == nil || doc.tabs.Active != 4 || doc.tabs.Items[4].View.NeedsLoad() {
		t.Fatalf("cycle onto a lazy tab: handled=%v load=%v active=%d needsLoad=%v",
			handled, load != nil, doc.tabs.Active, doc.tabs.Items[4].View != nil && doc.tabs.Items[4].View.NeedsLoad())
	}
}

func TestDocPanePersistsScrollOnEveryTab(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "one\ntwo\nthree\nfour\nfive\nsix\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n\nfunc main() {}\n")
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	var saved state.WorkspaceState
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	doc, _ := p.activeDocPane()
	doc.view().SetRendered(false)
	doc.view().SetSize(40, 2)
	doc.view().Scroll(3)
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	p.saveSelectionState()

	tabs, active := firstDocLeafTabs(workspacePaneLayout(saved, "shell:test-shell"))
	if len(tabs) != 2 || active != 1 || tabs[0].Scroll != 3 {
		t.Fatalf("persisted scroll = %#v active=%d", tabs, active)
	}
}

func TestDocScrollMutationAndStopPersistTheLatestOffset(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "one\ntwo\nthree\nfour\nfive\nsix\n")
	p, saved := persistDocPanePlugin(t, root)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	doc, leaf := p.activeDocPane()
	doc.view().SetRendered(false)
	doc.view().SetSize(40, 2)
	p.paneFocus = leaf.ID

	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'j'}); !handled {
		t.Fatal("j was not handled")
	}
	tabs, _ := firstDocLeafTabs(workspacePaneLayout(*saved, "shell:test-shell"))
	if len(tabs) != 1 || tabs[0].Scroll != 1 {
		t.Fatalf("keyboard scroll persisted as %#v", tabs)
	}

	// A final model mutation can happen after the last input save. Stop is the
	// quit and project-switch boundary that must capture it.
	doc.view().Scroll(2)
	p.Stop()
	tabs, _ = firstDocLeafTabs(workspacePaneLayout(*saved, "shell:test-shell"))
	if len(tabs) != 1 || tabs[0].Scroll != 3 {
		t.Fatalf("Stop persisted scroll as %#v", tabs)
	}
}

func TestDocPanePersistsWrapPerTab(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "one two three four five six seven eight nine ten\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	var saved state.WorkspaceState
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'w', Text: "w"}); !handled {
		t.Fatal("w was not handled")
	}
	if doc := p.activeDocPaneOrNil(); doc == nil || !doc.view().Wrap() {
		t.Fatal("w did not enable wrap on the active tab")
	}
	applyDocOpen(t, p, p.openTerminalPath("main.go", 0))
	if p.activeDocPaneOrNil().view().Wrap() {
		t.Fatal("new tab inherited wrap")
	}
	p.saveSelectionState()

	tabs, active := firstDocLeafTabs(workspacePaneLayout(saved, "shell:test-shell"))
	if len(tabs) != 2 || active != 1 || !tabs[0].Wrap || tabs[1].Wrap {
		t.Fatalf("persisted wrap = %#v active=%d", tabs, active)
	}
}

func TestRestorePaneLayoutHonorsWrap(t *testing.T) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	writeDocPaneFixture(t, root, "README.md", "wrap me\n")
	writeDocPaneFixture(t, root, "main.go", "package main\n")
	p := docPaneTestPlugin(t, root, true)
	cmd := p.restorePaneLayout(&state.PaneLayoutJSON{Root: resolvedRoot, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: "terminal"},
		B: &state.PaneLayoutJSON{Kind: "doc", Active: 1, Tabs: []state.PaneDocTabJSON{
			{Path: "README.md", Mode: "rendered", Wrap: true},
			{Path: "main.go", Mode: "raw"},
		}},
	}})
	if cmd == nil {
		t.Fatal("restore scheduled no load")
	}
	doc := p.activeDocPaneOrNil()
	if doc == nil || len(doc.tabs.Items) != 2 {
		t.Fatalf("restored tabs = %#v", doc)
	}
	if !doc.tabs.Items[0].View.Wrap() {
		t.Fatal("README wrap was not restored")
	}
	if doc.tabs.Items[1].View.Wrap() {
		t.Fatal("main.go wrap should stay off")
	}
}

func TestDocPanePathActions(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	p := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	p.activePane = PanePreview
	if leaf := firstPaneLeafOfKind(p.paneRoot, PaneDoc); leaf != nil {
		p.paneFocus = leaf.ID
	}

	if handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'I', Text: "I"}); !handled || p.docInfo == nil {
		t.Fatalf("I info: handled=%v info=%v cmd=%v", handled, p.docInfo != nil, cmd != nil)
	}
	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); !handled || p.docInfo != nil {
		t.Fatal("q did not close info")
	}
	if p.paneRoot.Split == nil {
		t.Fatal("q on info hid the pane")
	}

	if handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}); !handled || cmd == nil {
		t.Fatalf("ctrl+r: handled=%v cmd=%v", handled, cmd != nil)
	}
	if handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'Y', Text: "Y"}); !handled || cmd == nil {
		t.Fatalf("Y: handled=%v cmd=%v", handled, cmd != nil)
	}
}

func TestFeatureDisabledPreservesPaneLayoutForReenable(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# dormant\n")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	saved := state.WorkspaceState{
		ShellTmuxName: "test-shell",
		PaneLayout: &state.PaneLayoutJSON{Root: resolvedRoot, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
			Axis: "cols", Ratio: 61,
			A: &state.PaneLayoutJSON{Kind: "terminal"},
			B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "README.md", Mode: "rendered"}}},
		}},
	}
	wantJSON, err := json.Marshal(saved.PaneLayout)
	if err != nil {
		t.Fatal(err)
	}
	hooks := shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	cfg := config.Default()
	cfg.Features.Flags[features.WorkspaceDocPanes.Name] = false
	cfg.Features.Flags[features.WorkspaceTerminalPanel.Name] = false
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	p := New()
	p.shellStartupHooks = hooks
	if err := p.Init(&plugin.Context{WorkDir: root, ProjectRoot: root, Config: cfg, Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	p.shells = []*ShellSession{{Name: "Shell", TmuxName: "test-shell", Agent: &Agent{TmuxPane: "%905", OutputBuf: tty.NewOutputBuffer(20)}}}
	if !p.restoreSelectionState() {
		t.Fatal("disabled feature did not restore ordinary shell selection")
	}
	gotJSON, err := json.Marshal(saved.PaneLayout)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) || p.paneRoot != nil || p.paneRestoreCmd != nil {
		t.Fatalf("disabled feature changed dormant layout: got=%s want=%s root=%#v", gotJSON, wantJSON, p.paneRoot)
	}

	cfg.Features.Flags[features.WorkspaceDocPanes.Name] = true
	features.Init(cfg)
	if err := p.Init(&plugin.Context{WorkDir: root, ProjectRoot: root, Config: cfg, Epoch: 2}); err != nil {
		t.Fatal(err)
	}
	p.shells = []*ShellSession{{Name: "Shell", TmuxName: "test-shell", Agent: &Agent{TmuxPane: "%905", OutputBuf: tty.NewOutputBuffer(20)}}}
	if !p.restoreSelectionState() || p.activeDocPaneOrNil() == nil || p.paneRoot.Split == nil || p.paneRoot.Split.Ratio != 61 {
		t.Fatalf("re-enabled feature did not restore preserved layout: root=%#v doc=%#v", p.paneRoot, p.activeDocPaneOrNil())
	}
}

// TestDocPaneAcceptanceJourney is the Phase 6 / §7 path: two files on A, tab
// strip only, viewer keys, hide vs forget, switch B and back, relaunch, last x.
func TestDocPaneAcceptanceJourney(t *testing.T) {
	const goPath = "internal/plugins/workspace/plugin.go"
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n\nbody\n")
	writeDocPaneFixture(t, root, goPath, "package workspace\n")
	p, saved := persistDocPanePlugin(t, root)

	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	applyDocOpen(t, p, p.openTerminalPath(goPath, 0))
	doc := p.activeDocPaneOrNil()
	if titles := docTabTitles(doc); len(titles) != 2 || titles[0] != "README.md" || titles[1] != goPath {
		t.Fatalf("open tabs = %v", titles)
	}
	if doc.view().Title() != goPath || doc.view().Rendered() {
		t.Fatalf("active Go tab = %q rendered=%v", titleOrEmpty(doc), doc != nil && doc.view().Rendered())
	}

	wide := ansi.Strip(layoutDocTabStrip(doc, 80, true).Row)
	narrow := ansi.Strip(layoutDocTabStrip(doc, 36, true).Row)
	for _, header := range []string{wide, narrow} {
		if !strings.Contains(header, "README.md") || !strings.Contains(header, "plugin.go") {
			t.Fatalf("header dropped a filename: %q", header)
		}
		if strings.Contains(header, "Raw") || strings.Contains(header, "Rendered") || strings.Contains(header, "q close") {
			t.Fatalf("header is not a path-only tab strip: %q", header)
		}
		if strings.Count(header, "×") != 2 {
			t.Fatalf("header = %q, want one × per tab", header)
		}
	}
	if strings.Contains(narrow, "internal/plugins") {
		t.Fatalf("narrow Go tab did not left-truncate: %q", narrow)
	}
	if !strings.Contains(narrow, "plugin.go") {
		t.Fatalf("narrow Go tab dropped the filename: %q", narrow)
	}

	commands := p.Commands()
	if commands[0].ID != "close" || commands[0].Name != "Close" || commandNameByID(commands, "close-tab") != "Tab×" {
		t.Fatalf("footer close/close-tab = %#v", commands)
	}
	if commandNameByID(commands, "prev-tab") != "Tab←" || commandNameByID(commands, "next-tab") != "Tab→" {
		t.Fatalf("footer cycle = %#v", commands)
	}
	if commandNameByID(commands, "toggle-wrap") != "Wrap" || commandNameByID(commands, "info") != "Info" || commandNameByID(commands, "reveal") != "Reveal" {
		t.Fatalf("footer path actions = %#v", commands)
	}
	if commandNameByID(commands, "render") != "" {
		t.Fatalf("Go tab footer listed render: %#v", commands)
	}

	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'm', Text: "m"}); !handled || doc.view().Rendered() {
		t.Fatalf("m on Go changed render: handled=%v rendered=%v", handled, doc.view().Rendered())
	}
	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'w', Text: "w"}); !handled || !doc.view().Wrap() {
		t.Fatal("w did not wrap the Go tab")
	}
	if handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'I', Text: "I"}); !handled || p.docInfo == nil {
		t.Fatalf("I info: handled=%v info=%v cmd=%v", handled, p.docInfo != nil, cmd != nil)
	}
	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); !handled || p.docInfo != nil || p.paneRoot.Split == nil {
		t.Fatal("q on info hid the pane")
	}
	if handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}); !handled || cmd == nil {
		t.Fatalf("ctrl+r: handled=%v cmd=%v", handled, cmd != nil)
	}

	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'x', Text: "x"}); !handled || p.paneRoot.Split == nil {
		t.Fatal("x closed the pane instead of the Go tab")
	}
	readme := p.activeDocPaneOrNil()
	if titles := docTabTitles(readme); len(titles) != 1 || titles[0] != "README.md" || !readme.view().Rendered() {
		t.Fatalf("after x = %v rendered=%v", titles, readme != nil && readme.view().Rendered())
	}
	if commandNameByID(p.Commands(), "render") != "Raw" {
		t.Fatalf("README footer render = %q", commandNameByID(p.Commands(), "render"))
	}
	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'm', Text: "m"}); !handled || readme.view().Rendered() {
		t.Fatal("m on README did not toggle to raw")
	}
	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'm', Text: "m"}); !handled || !readme.view().Rendered() {
		t.Fatal("m on README did not restore rendered")
	}

	if handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); !handled || cmd == nil || p.paneRoot.Split != nil {
		t.Fatalf("q did not hide README: root=%#v", p.paneRoot)
	}
	p.selectTopShellAt(1)
	if p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil {
		t.Fatal("B live tree is not terminal-only")
	}
	p.selectTopShellAt(0)
	restored := p.activeDocPaneOrNil()
	if titles := docTabTitles(restored); len(titles) != 1 || titles[0] != "README.md" || !restored.view().Rendered() {
		t.Fatalf("switch-back after hide = %v rendered=%v", titles, restored != nil && restored.view().Rendered())
	}

	applyDocOpen(t, p, p.openTerminalPath(goPath, 0))
	both := p.activeDocPaneOrNil()
	if titles := docTabTitles(both); len(titles) != 2 || both.view().Title() != goPath || both.view().Rendered() {
		t.Fatalf("reopen both = %v active=%q", titles, titleOrEmpty(both))
	}
	p.saveSelectionState()

	relaunch := docPaneTestPlugin(t, root, true)
	relaunch.ctx.ProjectRoot = root
	relaunch.shells = p.shells
	relaunch.shellStartupHooks = p.shellStartupHooks
	if !relaunch.restoreSelectionState() {
		t.Fatal("relaunch did not restore shell A")
	}
	again := relaunch.activeDocPaneOrNil()
	if titles := docTabTitles(again); len(titles) != 2 || titles[0] != "README.md" || titles[1] != goPath {
		t.Fatalf("relaunch tabs = %v", titles)
	}
	if again.view().Title() != goPath || again.view().Rendered() || again.tabs.Items[0].View.Rendered() != true {
		t.Fatalf("relaunch active=%q goRendered=%v readmeRendered=%v", titleOrEmpty(again), again.view().Rendered(), again.tabs.Items[0].View.Rendered())
	}
	relaunch.activePane = PanePreview
	if leaf := firstPaneLeafOfKind(relaunch.paneRoot, PaneDoc); leaf != nil {
		relaunch.paneFocus = leaf.ID
	}

	if handled, _ := relaunch.handleDocKey(tea.KeyPressMsg{Code: 'x', Text: "x"}); !handled || relaunch.paneRoot.Split == nil {
		t.Fatal("first x after relaunch closed the pane")
	}
	if handled, cmd := relaunch.handleDocKey(tea.KeyPressMsg{Code: 'x', Text: "x"}); !handled || cmd == nil || relaunch.paneRoot.Split != nil {
		t.Fatalf("last x did not forget: root=%#v", relaunch.paneRoot)
	}
	relaunch.selectTopShellAt(1)
	relaunch.saveSelectionState()
	relaunch.selectTopShellAt(0)
	if relaunch.activeDocPaneOrNil() != nil || relaunch.paneRoot.Split != nil {
		t.Fatalf("forgotten pane came back: %#v", relaunch.paneRoot)
	}
	if layoutHasDocPath(workspacePaneLayout(*saved, "shell:test-shell"), "README.md") {
		t.Fatalf("forgotten A still has tabs: %#v", saved.PaneLayouts)
	}
}

// The reported journey: with a document beside the terminal, dragging the
// divider moves the terminal's box, and the terminal has to be drawn and
// captured at that box without the user clicking into it first. Activation was
// the only thing correcting it, which is why the pane visibly jumped on the
// click rather than on the release.
//
// The transport half of that defect is pinned in internal/tty, where a
// control-owned pane was resized by restarting the transport and never given
// the geometry. This pins the half this plugin owns: the release moves the box,
// every sizer follows it, and the geometry assertion it emits is what schedules
// the recapture — with the terminal still passive.
func TestDividerReleaseRecapturesTheTerminalWithoutActivation(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# recapture\n")
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	p.openTerminalPath("README.md", 0)

	before, ok := p.terminalLeafBox()
	if !ok {
		t.Fatal("no terminal leaf before the drag")
	}

	splitID := p.paneRoot.ID
	p.handleMouseClick(mouse.MouseAction{Region: &mouse.Region{ID: regionPaneTreeDivider, Data: splitID}, X: 70, Y: 5})
	p.handleMouseDrag(mouse.MouseAction{DragStartID: regionPaneTreeDivider, DragDX: 14})
	cmd := p.handleMouseDragEnd(mouse.MouseAction{
		DragStartID: regionPaneTreeDivider,
		Region:      &mouse.Region{ID: regionPreviewPane},
	})

	after, ok := p.terminalLeafBox()
	if !ok || after.W == before.W {
		t.Fatalf("release left the terminal's box at %d columns, want the dragged one", after.W)
	}
	if width, _ := p.calculateAgentPaneDimensions(); width != after.W {
		t.Fatalf("the surface is sized at %d columns, want the leaf's %d", width, after.W)
	}

	if cmd == nil {
		t.Fatal("release asserted no geometry for the terminal's new box")
	}
	msg := cmd()
	if _, ok := msg.(paneResizedMsg); !ok {
		t.Fatalf("release produced %T, want one pane-geometry assertion", msg)
	}
	if _, poll := p.update(msg); poll == nil {
		t.Fatal("the geometry assertion scheduled no capture, so the terminal keeps drawing the old pane")
	}

	// A sizer reporting the new width is half the claim; the other half is the
	// grid. The composition after the release has to place the terminal — and the
	// divider beside it — at the dragged geometry, with no activation in between.
	peer, ok := p.previewPeerBox()
	if !ok {
		t.Fatal("preview peer box is unplaced after the release")
	}
	rows := composePaneTree(t, p, peer.W, peer.H)
	leaves, dividers, fits := LayoutPanes(p.paneRoot, peer, paneTreeFloors())
	if !fits || len(dividers) != 1 {
		t.Fatalf("post-release layout = %d dividers fits=%v, want one divider", len(dividers), fits)
	}
	for _, placement := range leaves {
		if placement.Node.Kind == PaneTerminal && insetPanelChrome(placement.Box).W != after.W {
			t.Fatalf("the composed terminal is %d columns, want the released %d", insetPanelChrome(placement.Box).W, after.W)
		}
	}
	assertDividersDrawn(t, rows, dividers)

	if p.viewMode != ViewModeList || p.interactiveState != nil {
		t.Fatal("the divider gesture activated the terminal; the refresh must not need a click")
	}
}

// The `w` and `ctrl+r` document keys are only reachable if the keymap routes
// them to this plugin's commands in the workspace-doc context, and only
// discoverable if Commands() names them for the footer. Both halves are
// asserted here so removing either one fails.
func TestDocWrapAndRevealAreBoundAndAdvertised(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	registry := keymap.NewRegistry()
	keymap.RegisterDefaults(registry)
	p := New()
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return state.WorkspaceState{} },
		setWorkspaceState: func(string, state.WorkspaceState) error { return nil },
	}
	if err := p.Init(&plugin.Context{WorkDir: root, ProjectRoot: root, Config: config.Default(), Keymap: registry, Epoch: 5}); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"w": "toggle-wrap", "ctrl+r": "reveal"} {
		got, ok := registry.CommandForContextKey("workspace-doc", key)
		if !ok || got != want {
			t.Fatalf("workspace-doc %q -> %q (bound=%v), want %q", key, got, ok, want)
		}
	}

	p.ctx.WorkDir = root
	p.width, p.height = 140, 36
	p.shellSelected = true
	p.shells = []*ShellSession{{Name: "Shell", TmuxName: "test-shell", Agent: &Agent{TmuxPane: "%911", OutputBuf: tty.NewOutputBuffer(20)}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.paneFocus = 1
	p.paneNextID = 2
	p.activePane = PanePreview
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))

	commands := p.Commands()
	if got := commandNameByID(commands, "toggle-wrap"); got != "Wrap" {
		t.Fatalf("footer wrap hint = %q, want Wrap", got)
	}
	if got := commandNameByID(commands, "reveal"); got != "Reveal" {
		t.Fatalf("footer reveal hint = %q, want Reveal", got)
	}
	for _, id := range []string{"toggle-wrap", "reveal"} {
		for _, command := range commands {
			if command.ID == id && command.Context != "workspace-doc" {
				t.Fatalf("%s command context = %q, want workspace-doc", id, command.Context)
			}
		}
	}
}

// `w` belongs to the focused document, not to the workspace behind it, and the
// flip has to reach persisted state without a later save doing the work.
func TestDocWrapKeyRequiresDocFocusAndPersistsImmediately(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "one two three four five six seven eight\n")
	p, saved := persistDocPanePlugin(t, root)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	doc, leaf := p.activeDocPane()
	if doc == nil || doc.view().Wrap() {
		t.Fatalf("opened doc = %#v", doc)
	}

	p.paneFocus = terminalLeafID(p.paneRoot)
	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'w', Text: "w"}); handled {
		t.Fatal("w was handled as a document key while the terminal was focused")
	}
	if doc.view().Wrap() {
		t.Fatal("w wrapped a document that did not have focus")
	}

	p.paneFocus = leaf.ID
	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'w', Text: "w"}); !handled || !doc.view().Wrap() {
		t.Fatalf("w did not toggle wrap on the focused document: wrap=%v", doc.view().Wrap())
	}
	tabs, _ := firstDocLeafTabs(workspacePaneLayout(*saved, "shell:test-shell"))
	if len(tabs) != 1 || !tabs[0].Wrap {
		t.Fatalf("w did not persist wrap: %#v", tabs)
	}

	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'w', Text: "w"}); !handled || doc.view().Wrap() {
		t.Fatalf("second w did not unwrap: wrap=%v", doc.view().Wrap())
	}
	tabs, _ = firstDocLeafTabs(workspacePaneLayout(*saved, "shell:test-shell"))
	if len(tabs) != 1 || tabs[0].Wrap {
		t.Fatalf("unwrap did not persist: %#v", tabs)
	}
}

// ctrl+r reveals the focused document's own path, and asks for nothing when the
// pane has no path to reveal.
func TestDocRevealKeyNeedsAFocusedPath(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n")
	p := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, p, p.openTerminalPath("README.md", 0))
	doc, leaf := p.activeDocPane()
	p.activePane = PanePreview
	p.paneFocus = leaf.ID

	handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if !handled || cmd == nil {
		t.Fatalf("ctrl+r on a document with a path: handled=%v cmd=%v", handled, cmd != nil)
	}
	if got := p.revealActiveDoc(); got == nil {
		t.Fatal("revealActiveDoc returned no command for a document with a path")
	}

	// A pathless tab has nothing to hand the file manager.
	doc.tabs = docview.Tabs{}
	doc.tabs.Append(docview.New(nil))
	if doc.view().Title() != "" {
		t.Fatalf("replacement tab has path %q", doc.view().Title())
	}
	handled, cmd = p.handleDocKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if !handled || cmd != nil {
		t.Fatalf("ctrl+r without a path: handled=%v cmd=%v, want handled with no command", handled, cmd != nil)
	}
}

// The switcher must be reachable from the pane you are reading. Every content
// pane absorbs the keys it does not own, so before this `n` did nothing there
// and opening a second pane meant tabbing back to the sidebar first.
func TestPaneSwitcherOpensFromEveryFocusedContentPane(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# Read me\n")
	p := docPaneTestPlugin(t, root, true)
	p.openTerminalPath("README.md", 0)
	if !p.docFocused() {
		t.Fatal("premise: the document leaf should own the keyboard")
	}

	handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if !handled || cmd == nil {
		t.Fatalf("n on a focused document: handled=%v cmd=%v", handled, cmd != nil)
	}
	if p.viewMode != ViewModeCreate {
		t.Fatalf("n did not open the switcher: mode=%v", p.viewMode)
	}
	if p.createForm == nil || p.createForm.Step() != workspacecreate.StepKind {
		t.Fatal("the switcher did not open on its kind list")
	}
	// The pane it was opened from is still there: this adds a pane, it does
	// not replace the one being read.
	if panelayout.FirstOfKind(p.paneRoot, panelayout.Document) == nil {
		t.Fatal("opening the switcher closed the document it was opened from")
	}
}

// A committed in-file search owns n for its next-match while it is up. The
// switcher is asked after the pane's own input surfaces have declined, so the
// two cannot fight over the key.
func TestPaneSwitcherYieldsToACommittedInFileSearch(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "alpha\nbeta\nalpha\n")
	p := docPaneTestPlugin(t, root, true)
	p.openTerminalPath("README.md", 0)
	doc := p.focusedDocPane()
	if doc == nil || doc.view() == nil {
		t.Fatal("premise: a focused document with a view")
	}
	doc.view().StartSearch()
	for _, r := range "alpha" {
		p.handleDocKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	p.handleDocKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !doc.view().SearchActive() {
		t.Fatal("premise: the in-file search should still own the keyboard")
	}

	if handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'n', Text: "n"}); !handled {
		t.Fatal("n was not handled during a committed search")
	}
	if p.viewMode == ViewModeCreate {
		t.Fatal("n opened the switcher while a search owned the keyboard")
	}
}
