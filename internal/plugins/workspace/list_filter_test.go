package workspace

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Slice 2 item 3 of docs/plans/active/global-overview-workspaces.md: `/`
// filtering in the project Workspaces list, sharing internal/workspacelist with
// the global browser.
//
// The unfiltered journey is characterized in sidebar_baseline_test.go and is
// deliberately not restated here: these cases only cover what filtering adds.

func filterPlugin(t *testing.T) *Plugin {
	t.Helper()
	p := sidebarBaselinePlugin(t)
	p.shells[0].Name = "codex shell"
	p.shells[1].Name = "plain shell"
	p.worktrees[0].Branch = "main"
	p.worktrees[1].Branch = "modal-look-and-feel"
	p.worktrees[1].TaskID = "td-71de3d"
	p.worktrees[2].Branch = "spike-kanban"
	return p
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func typeQuery(p *Plugin, text string) {
	for _, r := range text {
		p.handleKeyPress(key(string(r)))
	}
}

func TestSlashOpensADedicatedTextInputContextThatKeepsProjectCommandsSafe(t *testing.T) {
	p := filterPlugin(t)
	if p.FocusContext() != "workspace-list" || p.ConsumesTextInput() {
		t.Fatalf("unfiltered context = %q consumes=%v", p.FocusContext(), p.ConsumesTextInput())
	}

	p.handleKeyPress(key("/"))
	if !p.filterFocused() {
		t.Fatal("`/` did not focus the filter")
	}
	if p.FocusContext() != "workspace-filter" || !p.ConsumesTextInput() {
		t.Fatalf("filter context = %q consumes=%v", p.FocusContext(), p.ConsumesTextInput())
	}

	// While the filter has focus, printable keys are query text — not the
	// project's own n/D/p commands, and not a view-mode change.
	before := p.viewMode
	typeQuery(p, "np")
	if p.listFilter.Query() != "np" {
		t.Fatalf("query = %q, want the typed characters", p.listFilter.Query())
	}
	if p.viewMode != before {
		t.Fatalf("a printable key changed the view mode to %v", p.viewMode)
	}

	// Releasing focus restores the list context and its commands.
	p.handleKeyPress(key("esc")) // clears
	p.handleKeyPress(key("esc")) // exits
	if p.filterFocused() || p.FocusContext() != "workspace-filter" && p.FocusContext() != "workspace-list" {
		t.Fatalf("escape left focus=%v context=%q", p.filterFocused(), p.FocusContext())
	}
	if p.FocusContext() != "workspace-list" || p.ConsumesTextInput() {
		t.Fatalf("after exit context = %q consumes=%v", p.FocusContext(), p.ConsumesTextInput())
	}
}

func TestProjectFilterKeepsBackslashLiteral(t *testing.T) {
	p := filterPlugin(t)
	p.handleKeyPress(key("/"))
	p.handleKeyPress(tea.KeyPressMsg{Code: '\\', Text: "\\"})
	if got := p.listFilter.Query(); got != "\\" {
		t.Fatalf("filter query = %q, want literal backslash", got)
	}
	if !p.sidebarVisible {
		t.Fatal("literal filter input toggled the sidebar")
	}
}

func TestFilterMatchesNameBranchTaskAgentAndKeepsNavigationLive(t *testing.T) {
	p := filterPlugin(t)
	p.handleKeyPress(key("/"))
	typeQuery(p, "modal")
	if got := len(p.visibleWorktreeIndices()); got != 1 {
		t.Fatalf("branch/name query matched %d worktrees, want 1", got)
	}
	if len(p.visibleShellIndices()) != 0 {
		t.Fatal("query matched a shell it should not")
	}
	if matched, total := p.filterCounts(); matched != 1 || total != 5 {
		t.Fatalf("counts = %d of %d, want 1 of 5", matched, total)
	}
	// Selection follows the filter: the removed selection moves to the only match.
	if selectionLabel(p) != "worktree:topic" {
		t.Fatalf("selection = %s, want the matching row", selectionLabel(p))
	}

	// A task id matches the same row.
	p.handleKeyPress(key("ctrl+u"))
	typeQuery(p, "td-71")
	if len(p.visibleWorktreeIndices()) != 1 {
		t.Fatal("task id did not match")
	}

	// Arrow navigation stays live while typing.
	p.handleKeyPress(key("ctrl+u"))
	typeQuery(p, "shell")
	if len(p.visibleShellIndices()) != 2 {
		t.Fatalf("shell query matched %d shells", len(p.visibleShellIndices()))
	}
	first := selectionLabel(p)
	p.handleKeyPress(key("down"))
	if selectionLabel(p) == first {
		t.Fatal("arrow navigation is dead while filtering")
	}
	// And it clamps inside the filtered set rather than escaping into hidden rows.
	for i := 0; i < 5; i++ {
		p.handleKeyPress(key("down"))
	}
	if !strings.HasPrefix(selectionLabel(p), "shell:") {
		t.Fatalf("navigation left the filtered rows: %s", selectionLabel(p))
	}
}

func TestEnterKeepsTheSelectionAndEscapeClearsThenExits(t *testing.T) {
	p := filterPlugin(t)
	p.handleKeyPress(key("/"))
	typeQuery(p, "spike")
	selected := selectionLabel(p)

	p.handleKeyPress(key("enter"))
	if p.filterFocused() {
		t.Fatal("enter did not return focus to the list")
	}
	if !p.filterActive() || selectionLabel(p) != selected {
		t.Fatalf("enter lost the query or the selection: active=%v selection=%s", p.filterActive(), selectionLabel(p))
	}
	// With focus released, project commands work again inside the filtered list.
	p.handleKeyPress(key("/"))
	p.handleKeyPress(key("esc"))
	if p.listFilter.Query() != "" || !p.filterFocused() {
		t.Fatalf("first escape = %q focused=%v", p.listFilter.Query(), p.filterFocused())
	}
	p.handleKeyPress(key("esc"))
	if p.filterFocused() || p.filterActive() {
		t.Fatal("second escape did not exit the filter")
	}
	if len(p.visibleWorktreeIndices()) != len(p.worktrees) {
		t.Fatal("exiting the filter did not restore the full list")
	}
}

func TestFilterAcceptsPastesAndRendersCountsAndNoMatch(t *testing.T) {
	p := filterPlugin(t)
	p.handleKeyPress(key("/"))
	if _, cmd := p.Update(tea.PasteMsg{Content: "kanban"}); cmd != nil {
		cmd()
	}
	if p.listFilter.Query() != "kanban" {
		t.Fatalf("paste did not reach the query: %q", p.listFilter.Query())
	}

	view := ansi.Strip(p.renderSidebarContent(40, 24))
	if !strings.Contains(view, "/ kanban") || !strings.Contains(view, "1 of 5") {
		t.Fatalf("filter row is missing its query or counts:\n%s", view)
	}

	if _, cmd := p.Update(tea.PasteMsg{Content: " nothing"}); cmd != nil {
		cmd()
	}
	view = ansi.Strip(p.renderSidebarContent(40, 24))
	if !strings.Contains(view, "0 of 5") || !strings.Contains(view, "No workspaces match") {
		t.Fatalf("no-match state is not honest:\n%s", view)
	}
}

func TestUnfilteredSidebarRendersExactlyAsBefore(t *testing.T) {
	p := filterPlugin(t)
	before := p.renderSidebarContent(40, 24)

	// Focusing and clearing the filter must leave the list byte-identical: the
	// non-filtered journey is not allowed to change.
	p.handleKeyPress(key("/"))
	p.handleKeyPress(key("esc"))
	p.handleKeyPress(key("esc"))
	if after := p.renderSidebarContent(40, 24); after != before {
		t.Fatalf("an empty filter changed the sidebar:\nbefore=%q\nafter=%q", before, after)
	}
	if strings.Contains(ansi.Strip(before), "/ filter") {
		t.Fatal("the filter row is drawn when no filter is in play")
	}
}

func TestFilterRowIsClickableAndShiftsRowsByExactlyOneLine(t *testing.T) {
	p := filterPlugin(t)
	p.mouseHandler.Clear()
	unfiltered := p.renderSidebarContent(40, 24)
	unfilteredRegions := len(p.mouseHandler.HitMap.Regions())

	// An empty focused filter shows every row, so the only difference is the
	// filter row itself: it takes row 1, and every content row below it shifts
	// down by exactly one line.
	p.handleKeyPress(key("/"))
	p.mouseHandler.Clear()
	filtered := p.renderSidebarContent(40, 24)
	before := strings.Split(ansi.Strip(unfiltered), "\n")
	after := strings.Split(ansi.Strip(filtered), "\n")
	if len(after) < 2 || !strings.HasPrefix(after[1], "/ ") {
		t.Fatalf("filter row is not the row under the header:\n%s", filtered)
	}
	for i := 1; i < len(before) && i+1 < len(after); i++ {
		if before[i] != after[i+1] {
			t.Fatalf("content row %d moved by more than the filter row:\n%q\n%q", i, before[i], after[i+1])
		}
	}
	if len(p.mouseHandler.HitMap.Regions()) != unfilteredRegions+1 {
		t.Fatal("no click target was registered for the filter row")
	}

	// The registered filter region is on the row the filter was drawn on.
	var found bool
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == regionListFilter {
			found = true
			if region.Rect.Y != 2 {
				t.Fatalf("filter region Y = %d, want the row under the header", region.Rect.Y)
			}
		}
	}
	if !found {
		t.Fatal("the filter row registered no region")
	}
}

func TestSharedSidebarRegionsFollowWarningsAndPanelInsets(t *testing.T) {
	p := filterPlugin(t)
	p.handleKeyPress(key("/"))
	p.deleteWarnings = []string{"cleanup is unavailable"}
	p.mouseHandler.Clear()
	view := strings.Split(ansi.Strip(p.renderSidebarContent(40, 24)), "\n")

	var filterY, firstRowY = -1, -1
	for _, region := range p.mouseHandler.HitMap.Regions() {
		switch {
		case region.ID == regionListFilter:
			filterY = region.Rect.Y
		case region.ID == regionWorktreeItem && firstRowY < 0:
			firstRowY = region.Rect.Y
			if region.Rect.X != 2 {
				t.Fatalf("row region X = %d, want first panel content column 2", region.Rect.X)
			}
		}
	}
	if filterY != 3 { // border row + header + warning
		t.Fatalf("filter region Y = %d, want 3 after warning", filterY)
	}
	if firstRowY < 0 || !strings.Contains(view[firstRowY-1], "codex shell") {
		t.Fatalf("first row region Y=%d does not match painted row:\n%s", firstRowY, strings.Join(view, "\n"))
	}
}

// scrollOffset is a position into the *filtered* worktree projection, so a
// query typed while the sidebar is scrolled has to bring the offset back inside
// the rows that survived it — including when the selection itself survived and
// the cursor never moves.
func TestFilteringAScrolledSidebarStillDrawsTheMatchingRows(t *testing.T) {
	p := filterPlugin(t)
	for i := 0; i < 30; i++ {
		p.worktrees = append(p.worktrees, &Worktree{Name: fmt.Sprintf("bulk-%02d", i), Path: p.ctx.ProjectRoot})
	}
	p.worktrees[len(p.worktrees)-1].Name = "needle"

	p.renderSidebarContent(40, 24) // establishes visibleCount
	p.handleKeyPress(key("G"))     // select the last worktree
	// Line-aware scroll is applied on paint, not by paging visibleCount.
	_ = p.renderSidebarContent(40, 24)
	if p.scrollOffset == 0 {
		t.Fatal("G did not scroll the sidebar; the case no longer exercises the bug")
	}
	if selectionLabel(p) != "worktree:needle" {
		t.Fatalf("G selected %s, want the last worktree", selectionLabel(p))
	}

	p.handleKeyPress(key("/"))
	typeQuery(p, "needle")
	if len(p.visibleWorktreeIndices()) != 1 || !p.selectionVisible() {
		t.Fatalf("query left %d visible worktrees, selectionVisible=%v",
			len(p.visibleWorktreeIndices()), p.selectionVisible())
	}
	if p.scrollOffset != 0 {
		t.Fatalf("scroll offset = %d, want the filtered list clamped to its only row", p.scrollOffset)
	}
	view := ansi.Strip(p.renderSidebarContent(40, 24))
	if !strings.Contains(view, "needle") {
		t.Fatalf("the matching row was scrolled off the filtered list:\n%s", view)
	}
}

// Shells and worktrees share one viewport, so a long shell section cannot push
// every worktree below the pane and the selected row is counted in the same
// shell-first projection used for navigation.
func TestScrollOffsetUsesSharedShellFirstViewport(t *testing.T) {
	p := filterPlugin(t)
	for i := 0; i < 20; i++ {
		p.worktrees = append(p.worktrees, &Worktree{Name: fmt.Sprintf("bulk-%02d", i), Path: p.ctx.ProjectRoot})
	}
	p.renderSidebarContent(40, 24)
	if p.visibleCount <= 0 || p.visibleCount >= len(p.worktrees) {
		t.Fatalf("visibleCount = %d does not exercise scrolling over %d worktrees", p.visibleCount, len(p.worktrees))
	}

	p.shellSelected, p.selectedIdx = false, 0
	p.ensureVisible()
	_ = p.renderSidebarContent(40, 24)
	if p.scrollOffset != 0 {
		t.Fatalf("first worktree left the list scrolled to %d", p.scrollOffset)
	}

	last := len(p.worktrees) - 1
	p.selectedIdx = last
	p.ensureVisible()
	view := ansi.Strip(p.renderSidebarContent(40, 24))
	index := p.sharedSidebarSelectionIndex()
	if p.scrollOffset <= 0 {
		t.Fatalf("last worktree left the list unscrolled")
	}
	if p.visibleCount != 1 && p.scrollOffset >= index {
		t.Fatalf("scroll offset = %d, want the minimum that reveals selected index %d", p.scrollOffset, index)
	}
	if !strings.Contains(view, p.worktrees[last].Name) {
		t.Fatalf("selected last worktree is not on screen:\n%s", view)
	}
	if strings.Contains(view, p.shells[0].Name) {
		t.Fatalf("first shell should share the same viewport and sit above the fold:\n%s", view)
	}
}
