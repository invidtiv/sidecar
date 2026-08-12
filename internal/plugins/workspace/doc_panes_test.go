package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

func docPaneTestPlugin(t *testing.T, root string, shell bool) *Plugin {
	t.Helper()
	p := New()
	p.ctx = &plugin.Context{WorkDir: root, Epoch: 17}
	p.width, p.height = 140, 36
	p.sidebarVisible = false
	p.activePane = PanePreview
	p.viewMode = ViewModeList
	p.previewTab = PreviewTabOutput
	p.autoScrollOutput = true
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
	doc.view.SetSize(60, 4)
	if got := doc.view.View(); strings.Contains(got, "OLD CONTENT") || !strings.Contains(got, "Loading document") || !strings.Contains(got, "two.md") {
		t.Fatalf("stale result changed retargeted viewer: %q", got)
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
	if stripped := ansi.Strip(docFocused); !strings.Contains(stripped, "guide.md") || !strings.Contains(stripped, "Rendered") || !strings.Contains(stripped, "×") || !strings.Contains(stripped, "q close") {
		t.Fatalf("document header lacks identity/mode/close/hint: %q", stripped)
	}

	var closeRegion *mouse.Region
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == regionDocClose {
			regionCopy := region
			closeRegion = &regionCopy
			break
		}
	}
	if closeRegion == nil {
		t.Fatal("rendered close chip has no hit region")
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
	if cmd := p.handleMouseClick(mouse.MouseAction{Region: closeRegion}); cmd == nil || p.activeDocPaneOrNil() != nil {
		t.Fatal("close header chip did not close the document and schedule terminal resize")
	}
}

func TestDocumentCommandsDescribeCurrentMode(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# Read me\n")
	p := docPaneTestPlugin(t, root, true)
	p.openTerminalPath("README.md", 0)
	commands := p.Commands()
	if len(commands) == 0 || commands[0].Context != "workspace-doc" || commands[0].Name != "Close" {
		t.Fatalf("document commands = %#v", commands)
	}
	if commands[1].ID != "toggle-sidebar" || commands[2].Name != "Raw" {
		t.Fatalf("document sidebar/render commands = %#v", commands[:3])
	}
	doc, _ := p.activeDocPane()
	doc.view.SetRendered(false)
	if got := p.Commands()[2].Name; got != "Render" {
		t.Fatalf("raw-mode action = %q, want Render", got)
	}
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
	doc.view.SetSize(40, 2)

	p.handleListKeys(tea.KeyPressMsg{Code: 'j'})
	if got := doc.view.View(); strings.Contains(got, "line 1") {
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
	p.handleMouseClick(mouse.MouseAction{Region: &mouse.Region{ID: regionDocPane, Data: leaf.ID}})
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

func (p *Plugin) activeDocPaneOrNil() *docPane {
	doc, _ := p.activeDocPane()
	return doc
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

	// An already-open pane can become too narrow after a terminal resize. The
	// focused leaf gets the whole outer preview instead of an under-floor split.
	p.width = 140
	p.openTerminalPath("README.md", 1)
	doc, leaf := p.activeDocPane()
	p.width = 40
	p.paneFocus = leaf.ID
	p.mouseHandler.Clear()
	got, ok := p.renderDocumentSplit(36, 20)
	if !ok || !strings.Contains(got, doc.view.Title()) {
		t.Fatalf("focused doc fallback not rendered full-size: ok=%v view=%q", ok, got)
	}
	var closeRegion *mouse.Region
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == regionDocClose {
			regionCopy := region
			closeRegion = &regionCopy
			break
		}
	}
	if closeRegion == nil {
		t.Fatal("focused narrow fallback rendered a close chip without a close hit region")
	}
	if hit := p.mouseHandler.HitMap.Test(closeRegion.Rect.X, closeRegion.Rect.Y); hit == nil || hit.ID != regionDocClose {
		t.Fatalf("close chip coordinates resolve to %#v, want %s", hit, regionDocClose)
	}
	if cmd := p.handleMouseClick(mouse.MouseAction{Region: closeRegion}); cmd == nil || p.activeDocPaneOrNil() != nil {
		t.Fatal("focused narrow fallback close chip did not close the document")
	}

	// Reopen to retain the original assertion: terminal focus at the same narrow
	// size falls back to the legacy full terminal rather than an invalid split.
	p.width = 140
	p.openTerminalPath("README.md", 1)
	p.width = 40
	p.paneFocus = terminalLeafID(p.paneRoot)
	if _, ok := p.renderDocumentSplit(36, 20); ok {
		t.Fatal("terminal-focused narrow layout did not fall back to legacy full terminal")
	}
}

func TestDocPaneTargetOnlyAcceptsMarkdownInsideSelectedSurface(t *testing.T) {
	for _, tc := range []struct {
		path   string
		inside bool
		want   bool
	}{
		{"README.md", true, true},
		{"notes.MARKDOWN", true, true},
		{"README.md", false, false},
		{"main.go", true, false},
		{"archive.md.txt", true, false},
	} {
		if got := docPaneTarget(tc.path, tc.inside); got != tc.want {
			t.Errorf("docPaneTarget(%q, %v) = %v, want %v", tc.path, tc.inside, got, tc.want)
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
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("feature-disabled route = %T, want focus plus navigation", cmd())
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
	if saved.PaneLayout == nil || saved.PaneLayout.Split == nil || saved.PaneLayout.Split.Ratio != 45 {
		t.Fatalf("saved layout after grow = %#v", saved.PaneLayout)
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
	if doc == nil || doc.view.Title() != "docs/valid.md" || doc.view.Rendered() || p.paneRoot.Split.Ratio != 64 {
		t.Fatalf("restored pane doc=%#v tree=%#v", doc, p.paneRoot)
	}
	if p.paneRestoreCmd == nil {
		t.Fatal("valid restored document did not schedule its load")
	}
	if saved.PaneLayout == nil || saved.PaneLayout.Split == nil || len(saved.PaneLayout.Split.B.Tabs) != 1 || saved.PaneLayout.Split.B.Tabs[0].Path != "docs/valid.md" {
		t.Fatalf("stale tabs were not pruned from persisted layout: %#v", saved.PaneLayout)
	}

	other := t.TempDir()
	saved.PaneLayout.Root = other
	p.resetPaneTreeToTerminal()
	p.docs = make(map[int]*docPane)
	p.paneRestoreCmd = nil
	p.restoreSelectionState()
	if p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil || p.paneRestoreCmd != nil {
		t.Fatal("layout from another terminal root was restored")
	}
}

func TestRestorePaneLayoutRejectsUnsupportedNestedTree(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "one.md", "one")
	writeDocPaneFixture(t, root, "two.md", "two")
	p := docPaneTestPlugin(t, root, true)
	layout := &state.PaneLayoutJSON{Root: root, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: "terminal"},
		B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
			Axis: "rows", Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "one.md"}}},
			B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "two.md"}}},
		}},
	}}
	p.restorePaneLayout(layout)
	if p.paneRoot.Split != nil || p.paneRoot.Kind != PaneTerminal || len(p.docs) != 0 {
		t.Fatalf("unsupported nested tree was retained: root=%#v docs=%d", p.paneRoot, len(p.docs))
	}
}

func TestRestorePaneLayoutCollapsesEscapingDocument(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	layout := &state.PaneLayoutJSON{Root: root, Surface: "shell:test-shell", Split: &state.PaneSplitJSON{
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
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# shell A\n")
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	p.shells = append(p.shells, &ShellSession{Name: "Shell B", TmuxName: "test-shell-b", Agent: &Agent{TmuxPane: "%903", OutputBuf: tty.NewOutputBuffer(20)}})
	var saved state.WorkspaceState
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	p.openTerminalPath("README.md", 0)
	doc, _ := p.activeDocPane()
	if doc == nil || doc.surface != "shell:test-shell" {
		t.Fatalf("opened doc surface = %#v", doc)
	}

	// Selection handlers save immediately before loadSelectedContent performs
	// its reset. That early save must already describe shell B as terminal-only.
	p.selectedShellIdx = 1
	p.saveSelectionState()
	if saved.ShellTmuxName != "test-shell-b" || saved.PaneLayout == nil || saved.PaneLayout.Surface != "shell:test-shell-b" || saved.PaneLayout.Split != nil || saved.PaneLayout.Kind != "terminal" {
		t.Fatalf("shell B early save retained shell A layout: %#v", saved)
	}
	p.loadSelectedContent()
	if p.activeDocPaneOrNil() != nil || p.paneRoot.Split != nil {
		t.Fatal("shell A document survived switch to same-root shell B")
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
	p.shellSelected = false
	p.selectedIdx = 0
	p.saveSelectionState()
	if saved.PaneLayout == nil || saved.PaneLayout.Surface != "workspace:"+stablePathKey(root) || saved.PaneLayout.Split != nil {
		t.Fatalf("same-root workspace save retained shell layout: %#v", saved.PaneLayout)
	}
	p.loadSelectedContent()
	if p.activeDocPaneOrNil() != nil {
		t.Fatal("shell document survived switch to workspace at the same root")
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
