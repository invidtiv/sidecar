package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
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
	if got := doc.view.View(); strings.Contains(got, "OLD CONTENT") || !strings.Contains(got, "Loading two.md") {
		t.Fatalf("stale result changed retargeted viewer: %q", got)
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
	got, ok := p.renderDocumentSplit(36, 20)
	if !ok || !strings.Contains(got, doc.view.Title()) {
		t.Fatalf("focused doc fallback not rendered full-size: ok=%v view=%q", ok, got)
	}
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
