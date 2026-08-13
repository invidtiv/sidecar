package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// Baseline characterization for the project Workspaces sidebar, recorded before
// the global Overview/Workspaces work extracts a shared list component and a
// shared preview presentation layer out of this plugin
// (docs/plans/active/global-overview-workspaces.md, slice 0).
//
// These run at the shipped default of the workspace_doc_panes flag (on), and
// one case keeps a document pane open, so an extraction that quietly changes
// sidebar order, selection reset, or the outer sidebar/preview split fails here
// rather than in a later slice.
//
// Deliberately not duplicated here, because the behaviour already has a home:
//   - pane tree geometry and terminalLeafBox agreement — pane_tree_geometry_test.go,
//     panetree_test.go;
//   - doc pane open/focus/close/persistence — doc_panes_test.go;
//   - preview split clamps, header rows, terminal surface origin — terminal_surface_test.go;
//   - preview scrolling and wheel routing — scroll_test.go, pane_geometry_test.go;
//   - pending cross-project selection — pending_overview_selection_test.go.

// sidebarBaselinePlugin builds a list-view plugin with two shells and three
// worktrees, a visible sidebar, and in-memory workspace-state hooks so the
// selection saves this exercises never touch disk.
func sidebarBaselinePlugin(t *testing.T) *Plugin {
	t.Helper()
	p := New()
	p.ctx = &plugin.Context{WorkDir: t.TempDir(), ProjectRoot: t.TempDir(), Epoch: 3}
	p.width, p.height = 140, 40
	p.focused = true
	p.viewMode = ViewModeList
	p.previewTab = PreviewTabOutput
	p.sidebarVisible = true
	p.sidebarWidth = 30
	p.activePane = PaneSidebar
	p.shellSelected = true
	p.shells = []*ShellSession{
		{Name: "one", TmuxName: "shell-one", Agent: &Agent{TmuxPane: "%1", OutputBuf: tty.NewOutputBuffer(20)}},
		{Name: "two", TmuxName: "shell-two", Agent: &Agent{TmuxPane: "%2", OutputBuf: tty.NewOutputBuffer(20)}},
	}
	p.worktrees = []*Worktree{
		{Name: "main", Path: p.ctx.ProjectRoot},
		{Name: "topic", Path: p.ctx.ProjectRoot},
		{Name: "spike", Path: p.ctx.ProjectRoot},
	}
	saved := state.WorkspaceState{}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, s state.WorkspaceState) error { saved = s; return nil },
	}
	return p
}

// selectionLabel names the current sidebar selection in navigation order.
func selectionLabel(p *Plugin) string {
	if p.shellSelected {
		if p.selectedShellIdx < 0 || p.selectedShellIdx >= len(p.shells) {
			return "shell:?"
		}
		return "shell:" + p.shells[p.selectedShellIdx].Name
	}
	if p.selectedIdx < 0 || p.selectedIdx >= len(p.worktrees) {
		return "worktree:?"
	}
	return "worktree:" + p.worktrees[p.selectedIdx].Name
}

func pressList(p *Plugin, key string) {
	msg := tea.KeyPressMsg{Code: rune(key[0]), Text: key}
	if key == "down" || key == "up" {
		if key == "down" {
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		} else {
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		}
	}
	// The returned command is deliberately not executed: this characterizes
	// selection movement, not the content loads it schedules.
	_ = p.handleListKeys(msg)
}

func TestSidebarNavigationIsShellFirstAndClampsAtBothEnds(t *testing.T) {
	p := sidebarBaselinePlugin(t)

	var walk []string
	walk = append(walk, selectionLabel(p))
	for i := 0; i < 6; i++ {
		pressList(p, "j")
		walk = append(walk, selectionLabel(p))
	}
	want := []string{
		"shell:one", "shell:two",
		"worktree:main", "worktree:topic", "worktree:spike",
		"worktree:spike", "worktree:spike",
	}
	if strings.Join(walk, ",") != strings.Join(want, ",") {
		t.Fatalf("downward walk = %v, want shell-first order clamped on the last worktree %v", walk, want)
	}

	walk = walk[:0]
	for i := 0; i < 6; i++ {
		pressList(p, "k")
		walk = append(walk, selectionLabel(p))
	}
	want = []string{
		"worktree:topic", "worktree:main",
		"shell:two", "shell:one",
		"shell:one", "shell:one",
	}
	if strings.Join(walk, ",") != strings.Join(want, ",") {
		t.Fatalf("upward walk = %v, want reverse order clamped on the first shell %v", walk, want)
	}

	// Arrow keys are the same navigation as j/k.
	pressList(p, "down")
	if got := selectionLabel(p); got != "shell:two" {
		t.Fatalf("down arrow = %q, want shell:two", got)
	}
	pressList(p, "up")
	if got := selectionLabel(p); got != "shell:one" {
		t.Fatalf("up arrow = %q, want shell:one", got)
	}

	// g selects the first shell, G the last worktree.
	pressList(p, "G")
	if got := selectionLabel(p); got != "worktree:spike" {
		t.Fatalf("G = %q, want the last worktree", got)
	}
	pressList(p, "g")
	if got := selectionLabel(p); got != "shell:one" {
		t.Fatalf("g = %q, want the first shell", got)
	}
}

func TestSidebarSelectionChangeResetsPreviewScrollAndFollowsLiveOutput(t *testing.T) {
	p := sidebarBaselinePlugin(t)
	p.previewOffset = 12
	p.previewScroll = 9
	p.diffTabCursor = 3
	p.scrollOffset = 5

	pressList(p, "j")
	if selectionLabel(p) != "shell:two" {
		t.Fatalf("selection did not move: %q", selectionLabel(p))
	}
	if p.previewOffset != 0 || p.previewScroll != 0 || p.diffTabCursor != 0 {
		t.Fatalf("selection change left stale preview state: offset=%d scroll=%d diffCursor=%d",
			p.previewOffset, p.previewScroll, p.diffTabCursor)
	}

	// A movement that changes nothing (already clamped) leaves the state alone.
	p.previewOffset = 7
	p.previewScroll = 7
	pressList(p, "k")
	pressList(p, "k")
	if selectionLabel(p) != "shell:one" {
		t.Fatalf("selection = %q, want the clamped first shell", selectionLabel(p))
	}
	if p.previewOffset != 0 || p.previewScroll != 0 {
		t.Fatalf("clamped move should still have reset once: offset=%d scroll=%d",
			p.previewOffset, p.previewScroll)
	}

	// g resets the sidebar scroll offset with the selection.
	p.scrollOffset = 9
	pressList(p, "g")
	if p.scrollOffset != 0 {
		t.Fatalf("g left sidebar scroll at %d, want 0", p.scrollOffset)
	}
}

func TestSidebarSelectionOnlyMovesInsideTheSidebarPane(t *testing.T) {
	p := sidebarBaselinePlugin(t)
	p.activePane = PanePreview
	before := selectionLabel(p)

	p.shells[0].Agent.OutputBuf = tty.NewOutputBuffer(500)
	p.shells[0].Agent.OutputBuf.Write(strings.Repeat("line\n", 200))
	// A window already back in scrollback, so j has somewhere to move it.
	p.previewScroll = 5

	pressList(p, "j")
	pressList(p, "j")
	if got := selectionLabel(p); got != before {
		t.Fatalf("preview-pane j changed selection: %q -> %q", before, got)
	}
	if p.previewScroll != 3 {
		t.Fatalf("preview-pane j did not scroll the preview instead: scroll=%d", p.previewScroll)
	}
}

func TestSidebarNavigationAtShippedDocPaneDefaultWithDocumentOpen(t *testing.T) {
	if !features.IsEnabled(features.WorkspaceDocPanes.Name) {
		t.Fatalf("workspace_doc_panes shipped default = off; baseline recorded against the on default")
	}

	p := sidebarBaselinePlugin(t)
	root := p.ctx.WorkDir
	writeDocPaneFixture(t, root, "docs/guide.md", "# Guide\n\nbody\n")
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.paneFocus = 1
	p.paneNextID = 2
	p.docs = make(map[int]*docPane)
	p.worktrees[0].Agent = &Agent{TmuxPane: "%9", OutputBuf: tty.NewOutputBuffer(20)}

	splitBefore := p.previewSplit()
	terminalBefore, ok := p.terminalLeafBox()
	if !ok {
		t.Fatal("terminal leaf not placed before opening a document")
	}

	open := p.openTerminalPath("docs/guide.md", 1)
	if open == nil {
		t.Fatal("markdown path did not open a document pane")
	}
	for _, child := range open().(tea.BatchMsg) {
		if msg, ok := child().(docview.LoadedMsg); ok {
			p.applyDocLoaded(msg)
		}
	}
	doc, leaf := p.activeDocPane()
	if doc == nil || leaf == nil {
		t.Fatal("document pane did not open at the shipped default")
	}

	// The outer sidebar/preview split is the plan's shared seam: a doc pane
	// splits the preview region, never the sidebar column.
	if got := p.previewSplit(); got != splitBefore {
		t.Fatalf("doc pane changed the outer split: %+v, want %+v", got, splitBefore)
	}
	terminalAfter, ok := p.terminalLeafBox()
	if !ok {
		t.Fatal("terminal leaf not placed with a document open")
	}
	if terminalAfter.X != terminalBefore.X || terminalAfter.Y != terminalBefore.Y ||
		terminalAfter.H != terminalBefore.H || terminalAfter.W >= terminalBefore.W {
		t.Fatalf("terminal leaf with doc open = %+v, want the left share of %+v", terminalAfter, terminalBefore)
	}

	// The full list frame draws the sidebar and the document beside the
	// terminal, every row exactly the plugin's width.
	rendered := p.renderListView(p.width, p.height)
	lines := strings.Split(rendered, "\n")
	if len(lines) != p.height {
		t.Fatalf("frame rows = %d, want %d", len(lines), p.height)
	}
	for row, line := range lines {
		if got := ansi.StringWidth(line); got != p.width {
			t.Fatalf("row %d width = %d, want %d", row, got, p.width)
		}
	}
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "guide.md") || !strings.Contains(stripped, "one") {
		t.Fatalf("frame lost the document or the sidebar:\n%s", stripped)
	}

	// Sidebar navigation is unchanged while a document is open: it still moves
	// shell-first, the sidebar keeps focus, and the plugin's own
	// selection-change rule collapses the document subtree.
	p.activePane = PaneSidebar
	pressList(p, "j")
	if p.activePane != PaneSidebar {
		t.Fatalf("doc pane stole sidebar focus: active pane = %v", p.activePane)
	}
	if got := selectionLabel(p); got != "shell:two" {
		t.Fatalf("selection with doc open = %q, want shell:two", got)
	}
	if p.activeDocPaneOrNil() != nil {
		t.Fatal("selection change left the previous surface's document open")
	}
	if got := p.previewSplit(); got != splitBefore {
		t.Fatalf("closing the document changed the outer split: %+v, want %+v", got, splitBefore)
	}
	if got, ok := p.terminalLeafBox(); !ok || got != terminalBefore {
		t.Fatalf("terminal leaf after close = %+v ok=%v, want %+v", got, ok, terminalBefore)
	}
}

// The panel header's "New" and the Workspaces section's "+" create the same
// thing. With no shells above it the section heading sits directly under the
// header, and the two would read as two different offers.
func TestWorkspacesSectionOffersNoSecondCreateButtonWithoutShells(t *testing.T) {
	p := sidebarBaselinePlugin(t)
	p.shellSelected = false
	p.shells = nil
	view := ansi.Strip(p.renderSidebarContent(30, 24))
	if !strings.Contains(view, workspacelist.SectionTitle("Workspaces", 3)) {
		t.Fatalf("the section heading is gone:\n%s", view)
	}
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == regionWorkspacesPlusButton {
			t.Fatalf("a second create button is still offered:\n%s", view)
		}
	}
}

// The heading wording and the blank line between sections are what a user
// compares the two Workspaces surfaces by, so the project sidebar pins them
// here: title, then the first heading with no separator above it, then one
// blank line before the next section.
func TestSidebarHeadingsAndSeparatorPlacement(t *testing.T) {
	p := sidebarBaselinePlugin(t)
	lines := strings.Split(ansi.Strip(p.renderSidebarContent(30, 24)), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	if len(lines) < 7 {
		t.Fatalf("sidebar rendered %d lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	if got, want := strings.TrimSpace(lines[1]), workspacelist.SectionTitle("Shells", 2); !strings.HasPrefix(got, want) {
		t.Fatalf("first heading = %q, want %q directly under the title", got, want)
	}
	if lines[2] == "" {
		t.Fatalf("a separator was drawn above the first section:\n%s", strings.Join(lines, "\n"))
	}
	if lines[4] != "" {
		t.Fatalf("sections are not separated by a blank line: %q\n%s", lines[4], strings.Join(lines, "\n"))
	}
	if got, want := strings.TrimSpace(lines[5]), workspacelist.SectionTitle("Workspaces", 3); !strings.HasPrefix(got, want) {
		t.Fatalf("second heading = %q, want %q", got, want)
	}
}
