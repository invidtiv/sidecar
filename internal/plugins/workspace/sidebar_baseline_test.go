package workspace

import (
	"fmt"
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
	if !p.shellSelected && p.selectedNestedTmux != "" {
		if _, shell := p.findNestedShell(p.selectedNestedTmux); shell != nil {
			return "nested:" + shell.Name
		}
		return "nested:?"
	}
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
	p.diff.Cursor = 3
	p.scrollOffset = 5

	pressList(p, "j")
	if selectionLabel(p) != "shell:two" {
		t.Fatalf("selection did not move: %q", selectionLabel(p))
	}
	if p.previewOffset != 0 || p.previewScroll != 0 || p.diff.Cursor != 0 {
		t.Fatalf("selection change left stale preview state: offset=%d scroll=%d diffCursor=%d",
			p.previewOffset, p.previewScroll, p.diff.Cursor)
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
	if !strings.Contains(view, workspacelist.SectionTitle("Worktrees", 3)) {
		t.Fatalf("the section heading is gone:\n%s", view)
	}
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == regionWorkspacesPlusButton {
			t.Fatalf("a second create button is still offered:\n%s", view)
		}
	}
}

// The heading wording and the blank lines around sections are what a user
// compares the two Workspaces surfaces by, so the project sidebar pins them
// here: title, one blank line, the first heading with no further separator
// above it, then one blank line before the next section.
func TestSidebarHeadingsAndSeparatorPlacement(t *testing.T) {
	p := sidebarBaselinePlugin(t)
	lines := strings.Split(ansi.Strip(p.renderSidebarContent(30, 24)), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	if len(lines) < 7 {
		t.Fatalf("sidebar rendered %d lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}

	if lines[1] != "" {
		t.Fatalf("no blank line under the panel header: %q\n%s", lines[1], strings.Join(lines, "\n"))
	}
	if got, want := strings.TrimSpace(lines[2]), workspacelist.SectionTitle("Shells", 2); !strings.HasPrefix(got, want) {
		t.Fatalf("first heading = %q, want %q one blank line under the title", got, want)
	}
	if lines[3] == "" {
		t.Fatalf("a separator was drawn above the first section:\n%s", strings.Join(lines, "\n"))
	}
	if lines[5] != "" {
		t.Fatalf("sections are not separated by a blank line: %q\n%s", lines[5], strings.Join(lines, "\n"))
	}
	if got, want := strings.TrimSpace(lines[6]), workspacelist.SectionTitle("Worktrees", 3); !strings.HasPrefix(got, want) {
		t.Fatalf("second heading = %q, want %q", got, want)
	}
}

func longSidebarPlugin(t *testing.T, extraWorktrees int) *Plugin {
	t.Helper()
	p := sidebarBaselinePlugin(t)
	for i := 0; i < extraWorktrees; i++ {
		p.worktrees = append(p.worktrees, &Worktree{
			Name: fmt.Sprintf("wt-%02d", i), Path: p.ctx.ProjectRoot, Branch: fmt.Sprintf("b-%02d", i),
		})
	}
	return p
}

func sidebarPlainLines(p *Plugin, width, height int) []string {
	tLines := strings.Split(ansi.Strip(p.renderSidebarContent(width, height)), "\n")
	for i, line := range tLines {
		tLines[i] = strings.TrimRight(line, " ")
	}
	return tLines
}

func TestSidebarFirstPaintStaysAtTopWhenSelectionFits(t *testing.T) {
	p := sidebarBaselinePlugin(t)
	p.scrollOffset, p.visibleCount = 0, 0
	lines := sidebarPlainLines(p, 40, 24)
	if p.scrollOffset != 0 {
		t.Fatalf("scrollOffset = %d, want 0 on first paint of a short list", p.scrollOffset)
	}
	if got, want := strings.TrimSpace(lines[2]), workspacelist.SectionTitle("Shells", 2); !strings.HasPrefix(got, want) {
		t.Fatalf("first body row = %q, want the Shells heading", got)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "one") {
		t.Fatalf("first shell is not on screen:\n%s", strings.Join(lines, "\n"))
	}
}

func TestSidebarLongListDoesNotScrollWhenSelectionFitsFromTop(t *testing.T) {
	p := longSidebarPlugin(t, 20)
	// A short first paint leaves a stale, underestimated visibleCount. Selecting
	// an early worktree and calling ensureVisible used to page by that count
	// and hide the first shells even though they fit in the real pane.
	p.shellSelected, p.selectedShellIdx = true, 0
	_ = p.renderSidebarContent(40, 10)
	if p.visibleCount <= 0 {
		t.Fatal("short paint did not record a visibleCount")
	}
	p.shellSelected, p.selectedIdx = false, 2
	p.ensureVisible()
	view := ansi.Strip(p.renderSidebarContent(40, 30))
	if p.scrollOffset != 0 {
		t.Fatalf("scrollOffset = %d, want 0: selected row %d fits from the top", p.scrollOffset, p.sharedSidebarSelectionIndex())
	}
	if !strings.Contains(view, p.shells[0].Name) {
		t.Fatalf("first shell is above the fold:\n%s", view)
	}
	if !strings.Contains(view, p.worktrees[2].Name) {
		t.Fatalf("selected worktree is not on screen:\n%s", view)
	}
}

func TestSidebarScrollsTheMinimumToRevealABelowFoldSelection(t *testing.T) {
	p := longSidebarPlugin(t, 20)
	last := len(p.worktrees) - 1
	p.shellSelected, p.selectedIdx = false, last
	p.scrollOffset, p.visibleCount = 0, 0
	view := ansi.Strip(p.renderSidebarContent(40, 16))
	selected := p.worktrees[last].Name
	if !strings.Contains(view, selected) {
		t.Fatalf("selected %q is not on screen:\n%s", selected, view)
	}
	if strings.Contains(view, p.shells[0].Name) {
		t.Fatalf("first shell should sit above the fold when the last worktree is selected:\n%s", view)
	}
	index := p.sharedSidebarSelectionIndex()
	if p.scrollOffset <= 0 {
		t.Fatalf("scrollOffset = %d, want a below-fold scroll", p.scrollOffset)
	}
	if p.visibleCount != 1 && p.scrollOffset >= index {
		t.Fatalf("scrollOffset = %d equals/exceeds selected index %d; want the minimum that reveals it", p.scrollOffset, index)
	}

	// Starting from the selected index itself must clamp back to that minimum
	// rather than park the last row at the top of an empty pane.
	p.scrollOffset = index
	_ = p.renderSidebarContent(40, 16)
	if p.scrollOffset >= index && p.visibleCount != 1 {
		t.Fatalf("stale last-row offset stayed at %d, want the filled last page", p.scrollOffset)
	}
	if !strings.Contains(ansi.Strip(p.renderSidebarContent(40, 16)), selected) {
		t.Fatal("minimum clamp lost the selected row")
	}
}

func TestSidebarGrowingThePaneClampsEmptyScrollSpace(t *testing.T) {
	p := longSidebarPlugin(t, 20)
	last := len(p.worktrees) - 1
	p.shellSelected, p.selectedIdx = false, last
	p.scrollOffset, p.visibleCount = 0, 0
	_ = p.renderSidebarContent(40, 12)
	small := p.scrollOffset
	if small == 0 {
		t.Fatal("short pane did not need to scroll; the grow case is untested")
	}
	view := ansi.Strip(p.renderSidebarContent(40, 40))
	grown := p.scrollOffset
	if grown >= small {
		t.Fatalf("after growing, scroll stayed %d (was %d); want a clamp that fills the pane", grown, small)
	}
	if !strings.Contains(view, p.worktrees[last].Name) {
		t.Fatalf("taller pane lost the selected worktree:\n%s", view)
	}
	p.scrollOffset = grown + 8
	_ = p.renderSidebarContent(40, 40)
	if p.scrollOffset != grown {
		t.Fatalf("taller pane accepted over-scroll %d, want last-page %d", p.scrollOffset, grown)
	}
}
