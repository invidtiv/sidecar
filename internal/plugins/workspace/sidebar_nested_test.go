package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/workspacelist"
)

func nestedSidebarPlugin(t *testing.T) *Plugin {
	t.Helper()
	p := New()
	workDirA := filepath.Join(t.TempDir(), "sidecar")
	workDirB := filepath.Join(t.TempDir(), "sidecar-feature")
	p.ctx = &plugin.Context{WorkDir: workDirA, ProjectRoot: workDirA, Epoch: 3}
	p.width, p.height = 140, 40
	p.focused = true
	p.viewMode = ViewModeList
	p.sidebarVisible = true
	p.sidebarWidth = 30
	p.activePane = PaneSidebar
	p.shells = []*ShellSession{
		{Name: "here", TmuxName: "sidecar-sh-sidecar-1", WorkDir: workDirA},
	}
	p.worktrees = []*Worktree{
		{Name: "main", Path: workDirA, IsMain: true, Key: "main"},
		{Name: "feature", Path: workDirB, Key: "feature"},
	}
	p.nestedByWorkDir = map[string][]*ShellSession{
		filepath.Clean(workDirB): {
			{Name: "sibling", TmuxName: "sidecar-sh-sidecar-feature-1", WorkDir: workDirB},
		},
	}
	p.selectTopShellAt(0)
	return p
}

func TestNestedShellsAppearUnderSiblingWorktreeOnly(t *testing.T) {
	p := nestedSidebarPlugin(t)
	view := ansi.Strip(p.renderSidebarContent(48, 24))
	if !strings.Contains(view, "here") {
		t.Fatalf("current worktree shell missing from Shells section:\n%s", view)
	}
	if !strings.Contains(view, "sibling") {
		t.Fatalf("sibling shell missing from Workspaces nest:\n%s", view)
	}
	if !strings.Contains(view, workspacelist.KindGlyph(workspacelist.KindShell)) {
		t.Fatalf("nested shell row missing shell kind glyph:\n%s", view)
	}

	// Current worktree row has no nested copy of "here".
	if strings.Count(view, "here") != 1 {
		t.Fatalf("current worktree shell appeared more than once:\n%s", view)
	}

	items := p.visibleSidebarItems()
	if len(items) != 4 { // here, main, feature, sibling
		t.Fatalf("visible items = %d, want 4: %+v", len(items), items)
	}
	if items[0].kind != navKindShell || items[1].kind != navKindWorktree || items[2].kind != navKindWorktree || items[3].kind != navKindNestedShell {
		t.Fatalf("item kinds = %+v", items)
	}
	if items[3].shell == nil || items[3].shell.TmuxName != "sidecar-sh-sidecar-feature-1" {
		t.Fatalf("nested item = %+v", items[3])
	}
	if items[1].worktreeIdx != 0 || len(p.visibleNestedShells(p.worktrees[0])) != 0 {
		t.Fatal("current worktree grew a nest")
	}
}

func TestNestedShellsStayOutOfTopShellsSection(t *testing.T) {
	p := nestedSidebarPlugin(t)
	if len(p.shells) != 1 || p.shells[0].TmuxName != "sidecar-sh-sidecar-1" {
		t.Fatalf("top Shells = %v, want only the current workDir shell", shellNames(p.shells))
	}
	for _, idx := range p.visibleShellIndices() {
		if p.shells[idx].TmuxName == "sidecar-sh-sidecar-feature-1" {
			t.Fatal("sibling shell leaked into the top Shells section")
		}
	}
}

func TestNestedShellKeyboardAndMouseSelect(t *testing.T) {
	p := nestedSidebarPlugin(t)
	var walk []string
	walk = append(walk, selectionLabel(p))
	for i := 0; i < 3; i++ {
		p.moveCursor(1)
		walk = append(walk, selectionLabel(p))
	}
	want := []string{"shell:here", "worktree:main", "worktree:feature", "nested:sibling"}
	if strings.Join(walk, ",") != strings.Join(want, ",") {
		t.Fatalf("j walk = %v, want %v", walk, want)
	}
	if !p.selectingShell() || p.getSelectedShell() == nil || p.getSelectedShell().TmuxName != "sidecar-sh-sidecar-feature-1" {
		t.Fatalf("nested selection did not resolve: shellSelected=%v nested=%q selected=%v",
			p.shellSelected, p.selectedNestedTmux, p.getSelectedShell())
	}

	p.moveCursor(-1)
	if selectionLabel(p) != "worktree:feature" {
		t.Fatalf("k from nested = %q, want parent worktree", selectionLabel(p))
	}

	p.selectTopShellAt(0)
	p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: regionWorktreeItem, Data: nestedShellHit{TmuxName: "sidecar-sh-sidecar-feature-1"}},
	})
	if selectionLabel(p) != "nested:sibling" {
		t.Fatalf("mouse click = %q, want nested:sibling", selectionLabel(p))
	}
}

func TestNestedShellEnterAttachesByTmuxName(t *testing.T) {
	p := nestedSidebarPlugin(t)
	var attached string
	p.attachSession = func(sessionName, displayName string) tea.Cmd {
		attached = sessionName
		return nil
	}
	p.selectNestedShell(1, "sidecar-sh-sidecar-feature-1")
	cmd := p.handleListKeys(key("enter"))
	if cmd != nil {
		cmd()
	}
	if attached != "sidecar-sh-sidecar-feature-1" {
		t.Fatalf("enter attached %q, want sidecar-sh-sidecar-feature-1", attached)
	}

	attached = ""
	cmd = p.handleListKeys(key("t"))
	if cmd != nil {
		cmd()
	}
	if attached != "sidecar-sh-sidecar-feature-1" {
		t.Fatalf("t attached %q, want sidecar-sh-sidecar-feature-1", attached)
	}

	attached = ""
	cmd = p.handleMouseDoubleClick(mouse.MouseAction{
		Type:   mouse.ActionDoubleClick,
		Region: &mouse.Region{ID: regionWorktreeItem, Data: nestedShellHit{TmuxName: "sidecar-sh-sidecar-feature-1"}},
	})
	if cmd != nil {
		cmd()
	}
	if attached != "sidecar-sh-sidecar-feature-1" {
		t.Fatalf("double-click attached %q", attached)
	}
}

func TestNewShellPersistsWorkDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shells.json")
	manifest := &ShellManifest{Version: manifestVersion, path: path}
	p := New()
	p.ctx = &plugin.Context{WorkDir: "/work/sidecar", ProjectRoot: "/work/sidecar", Epoch: 1}
	p.shellManifest = manifest

	_, cmd := p.Update(ShellCreatedMsg{
		SessionName: "sidecar-sh-sidecar-1",
		DisplayName: "Shell 1",
		PaneID:      "%3",
	})
	_ = cmd

	if len(p.shells) != 1 || p.shells[0].WorkDir != "/work/sidecar" {
		t.Fatalf("in-memory WorkDir = %+v", p.shells[0])
	}
	reloaded, err := LoadShellManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	def := reloaded.FindShell("sidecar-sh-sidecar-1")
	if def == nil || def.WorkDir != "/work/sidecar" {
		t.Fatalf("persisted definition = %+v", def)
	}
}

func TestShellToDefinitionKeepsWorkDir(t *testing.T) {
	got := shellToDefinition(&ShellSession{
		Name: "review", TmuxName: "sidecar-sh-feature-1", WorkDir: "/wt/feature",
	})
	if got.WorkDir != "/wt/feature" {
		t.Fatalf("WorkDir = %q", got.WorkDir)
	}
}

func TestEmptyWorktreeHasNoNest(t *testing.T) {
	p := nestedSidebarPlugin(t)
	p.worktrees = append(p.worktrees, &Worktree{Name: "empty", Path: filepath.Join(t.TempDir(), "empty"), Key: "empty"})
	items := p.visibleSidebarItems()
	var emptyChildren int
	for _, item := range items {
		if item.kind == navKindWorktree && p.worktrees[item.worktreeIdx].Name == "empty" {
			continue
		}
		if item.kind == navKindNestedShell && p.worktrees[item.worktreeIdx].Name == "empty" {
			emptyChildren++
		}
	}
	if emptyChildren != 0 {
		t.Fatalf("empty worktree grew %d nested rows", emptyChildren)
	}
}
