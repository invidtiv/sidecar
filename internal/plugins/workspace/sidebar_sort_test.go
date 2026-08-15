package workspace

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// sortPlugin builds a list with one of everything the order has to rank: a
// blocked agent shell, an idle agent shell, a shell with no session, a working
// worktree, a quiet worktree, and a shell nested inside a worktree. Structural
// order deliberately disagrees with every computed order, so a sort that did
// nothing would fail these.
func sortPlugin(t *testing.T) *Plugin {
	t.Helper()
	now := time.Now()
	agentAt := func(activityState agentactivity.State, ago time.Duration) *Agent {
		return &Agent{
			Type: AgentClaude, TmuxPane: "%1", OutputBuf: tty.NewOutputBuffer(20),
			Activity:           agentactivity.Tracker{State: activityState},
			ActivityCapturedAt: now, LastOutput: now.Add(-ago),
		}
	}

	p := New()
	root := t.TempDir()
	p.ctx = &plugin.Context{WorkDir: root, ProjectRoot: root, Epoch: 3}
	p.width, p.height = 140, 40
	p.focused = true
	p.viewMode = ViewModeList
	p.sidebarVisible = true
	p.sidebarWidth = 30
	p.activePane = PaneSidebar

	p.shells = []*ShellSession{
		{Name: "zeta quiet", TmuxName: "sh-zeta", CreatedAt: now.Add(-40 * time.Hour)},
		{Name: "alpha blocked", TmuxName: "sh-alpha", Agent: agentAt(agentactivity.StateBlocked, 2*time.Minute)},
	}
	other := t.TempDir()
	p.worktrees = []*Worktree{
		{Name: "mid worktree", Path: other, Key: "mid", Branch: "mid", UpdatedAt: now.Add(-3 * time.Hour)},
		{Name: "busy worktree", Path: t.TempDir(), Key: "busy", Branch: "busy",
			Agent: agentAt(agentactivity.StateWorking, time.Minute), UpdatedAt: now.Add(-time.Minute)},
	}
	p.nestedByWorkDir = map[string][]*ShellSession{
		other: {{Name: "nested shell", TmuxName: "sh-nested", WorkDir: other, Agent: agentAt(agentactivity.StateWorking, 30*time.Second)}},
	}

	saved := state.WorkspaceState{}
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, s state.WorkspaceState) error { saved = s; return nil },
	}
	p.selectTopShellAt(0)
	return p
}

// navNames is the sidebar's order as a reader would say it aloud.
func navNames(p *Plugin) []string {
	var names []string
	for _, item := range p.visibleSidebarItems() {
		switch item.kind {
		case navKindShell:
			names = append(names, p.shells[item.shellIdx].Name)
		case navKindNestedShell:
			names = append(names, item.shell.Name)
		default:
			names = append(names, p.worktrees[item.worktreeIdx].Name)
		}
	}
	return names
}

// The invariant the whole refactor exists for: whatever the sort, the sequence
// j and k walk is the sequence the renderer drew. A sort that reordered only
// one of them would leave the cursor jumping around the screen.
func TestNavigationOrderMatchesRenderedOrderInEverySort(t *testing.T) {
	for _, mode := range projectSortModes {
		p := sortPlugin(t)
		p.listSort = mode

		var rendered []string
		for _, section := range p.sidebarNavSections() {
			for _, item := range section.items {
				rendered = append(rendered, p.rowID(item))
			}
		}
		var walked []string
		for _, item := range p.visibleSidebarItems() {
			walked = append(walked, p.rowID(item))
		}
		if strings.Join(rendered, ",") != strings.Join(walked, ",") {
			t.Fatalf("%s: rendered %v but navigation walks %v", mode.Label(), rendered, walked)
		}
		if len(rendered) != 5 {
			t.Fatalf("%s: %d rows, want all 5 present in every sort: %v", mode.Label(), len(rendered), rendered)
		}
	}
}

func TestManualSortKeepsTheStructuralTree(t *testing.T) {
	p := sortPlugin(t)
	p.listSort = workspacelist.SortManual

	sections := p.sidebarNavSections()
	if len(sections) != 2 || sections[0].title != "Shells" || sections[1].title != "Worktrees" {
		t.Fatalf("manual sections = %v, want Shells then Worktrees", sectionTitles(sections))
	}
	// The nested shell stays a child of its worktree, and stays indented.
	if got := navNames(p); strings.Join(got, ",") != "zeta quiet,alpha blocked,mid worktree,nested shell,busy worktree" {
		t.Fatalf("manual order = %v", got)
	}
	row := ansi.Strip(p.renderNestedShellEntry(p.nestedByWorkDir[p.worktrees[0].Path][0], false, 40))
	if !strings.HasPrefix(row, "  ") {
		t.Fatalf("nested shell lost its indent under manual sort: %q", row)
	}
}

func TestActivitySortFlattensAndLeadsWithWhatNeedsAttention(t *testing.T) {
	p := sortPlugin(t)
	p.listSort = workspacelist.SortActivity

	sections := p.sidebarNavSections()
	titles := sectionTitles(sections)
	if titles[0] != string(workspacelist.GroupNeedsAttention) {
		t.Fatalf("activity sections = %v, want Needs Attention first", titles)
	}
	// The blocked shell was second-to-last structurally; it now leads.
	names := navNames(p)
	if names[0] != "alpha blocked" {
		t.Fatalf("activity order = %v, want the blocked shell first", names)
	}
	// The nested shell is working, so it sits with the other working rows
	// rather than three rows under an idle worktree.
	nestedAt, busyAt := indexOf(names, "nested shell"), indexOf(names, "busy worktree")
	midAt := indexOf(names, "mid worktree")
	if nestedAt < 0 || busyAt < 0 || midAt < 0 {
		t.Fatalf("activity order lost a row: %v", names)
	}
	if nestedAt > midAt {
		t.Fatalf("activity order = %v, want the working nested shell above the quiet worktree", names)
	}
	// Flattened: it is drawn as a peer, with its worktree as context and no indent.
	row := ansi.Strip(p.renderNavItem(p.visibleSidebarItems()[nestedAt], 44, false)[0])
	if strings.HasPrefix(row, "  ") {
		t.Fatalf("nested shell kept its indent under a computed sort: %q", row)
	}
	if !strings.Contains(row, "mid worktree") {
		t.Fatalf("flattened nested shell lost its worktree context: %q", row)
	}
}

func TestRecentSortOrdersByTheSameInstantTheRowDisplays(t *testing.T) {
	p := sortPlugin(t)
	p.listSort = workspacelist.SortRecent

	names := navNames(p)
	want := []string{"busy worktree", "alpha blocked", "nested shell", "mid worktree", "zeta quiet"}
	// busy (1m) and alpha (2m) and nested (30s) are all within the hour, so the
	// exact interleave is by timestamp; assert the extremes and the sink.
	if names[len(names)-1] != "zeta quiet" {
		t.Fatalf("recent order = %v, want the 40-hour-old shell last", names)
	}
	if indexOf(names, "mid worktree") < indexOf(names, "nested shell") {
		t.Fatalf("recent order = %v, want the 30s nested shell above the 3h worktree", names)
	}
	if len(names) != len(want) {
		t.Fatalf("recent order dropped rows: %v", names)
	}
}

func TestNameSortIsOneUnheadedRun(t *testing.T) {
	p := sortPlugin(t)
	p.listSort = workspacelist.SortName

	sections := p.sidebarNavSections()
	if len(sections) != 1 || sections[0].title != "" {
		t.Fatalf("name sections = %v, want one unheaded run", sectionTitles(sections))
	}
	got := navNames(p)
	want := "alpha blocked,busy worktree,mid worktree,nested shell,zeta quiet"
	if strings.Join(got, ",") != want {
		t.Fatalf("name order = %v, want %s", got, want)
	}
}

// Selection is by identity, so changing the sort must not move the cursor to a
// different workspace — the row travels, the cursor stays on it.
func TestChangingSortKeepsTheSelectedWorkspace(t *testing.T) {
	p := sortPlugin(t)
	p.listSort = workspacelist.SortManual
	p.selectTopShellAt(1) // "alpha blocked"
	before := p.selectedRowID()

	for _, mode := range projectSortModes {
		p.listSort = mode
		if got := p.selectedRowID(); got != before {
			t.Fatalf("%s moved the selection from %q to %q", mode.Label(), before, got)
		}
	}
}

func sectionTitles(sections []sidebarNavSection) []string {
	titles := make([]string, 0, len(sections))
	for _, section := range sections {
		titles = append(titles, section.title)
	}
	return titles
}

func indexOf(names []string, want string) int {
	for i, name := range names {
		if name == want {
			return i
		}
	}
	return -1
}

// v opens the View surface and it owns the keyboard while open, so a stray key
// cannot fall through to a project command that creates or deletes something.
func TestViewFlyoutOpensOnVAndOwnsTheKeyboard(t *testing.T) {
	p := sortPlugin(t)
	if p.viewFlyoutActive() {
		t.Fatal("View opened itself")
	}
	pressList(p, "v")
	if !p.viewFlyoutActive() {
		t.Fatal("v did not open View")
	}

	// "n" would otherwise start creating a workspace.
	before := p.viewMode
	pressList(p, "n")
	if p.viewMode != before {
		t.Fatalf("a key fell through the View surface into %v", p.viewMode)
	}
	pressList(p, "v")
	if p.viewFlyoutActive() {
		t.Fatal("v did not close View again")
	}
}

// Picking a mode from the surface applies it and closes.
func TestViewFlyoutAppliesTheChosenSort(t *testing.T) {
	p := sortPlugin(t)
	p.openViewFlyout()
	if cmd := p.applyViewFlyoutAction(workspacelist.SortActionID(workspacelist.SortActivity)); cmd != nil {
		_ = cmd()
	}
	if p.listSort != workspacelist.SortActivity {
		t.Fatalf("sort = %s, want Activity", p.listSort.Label())
	}
	if p.viewFlyoutActive() {
		t.Fatal("View stayed open after a choice")
	}
}

// V still reaches the kanban board now that v is spoken for.
func TestCapitalVTogglesKanban(t *testing.T) {
	p := sortPlugin(t)
	pressList(p, "V")
	if p.viewMode != ViewModeKanban {
		t.Fatalf("V left view mode at %v, want kanban", p.viewMode)
	}
	pressList(p, "V")
	if p.viewMode != ViewModeList {
		t.Fatalf("V did not return to the list: %v", p.viewMode)
	}
}

// The chosen order survives a relaunch, per project.
func TestListSortPersistsPerProject(t *testing.T) {
	p := sortPlugin(t)
	if p.listSort != workspacelist.SortManual {
		t.Fatalf("default sort = %s, want Manual", p.listSort.Label())
	}
	if cmd := p.setListSort(workspacelist.SortActivity); cmd != nil {
		_ = cmd()
	}

	// A fresh plugin over the same saved state comes back sorted the same way.
	next := New()
	next.ctx = p.ctx
	next.shellStartupHooks = p.shellStartupHooks
	next.restoreListSort()
	if next.listSort != workspacelist.SortActivity {
		t.Fatalf("restored sort = %s, want Activity", next.listSort.Label())
	}
}

// A state file naming a mode this surface does not offer must not select an
// arbitrary one — it falls back to the default.
func TestUnknownSavedSortFallsBackToTheDefault(t *testing.T) {
	p := sortPlugin(t)
	hooks := p.shellStartupHooks.withDefaults()
	saved := hooks.getWorkspaceState(p.ctx.ProjectRoot)
	saved.ListSort = "Project" // global offers it; this surface does not
	_ = hooks.setWorkspaceState(p.ctx.ProjectRoot, saved)

	p.listSort = workspacelist.SortName
	p.restoreListSort()
	if p.listSort != workspacelist.SortName {
		t.Fatalf("an unoffered saved sort changed the mode to %s", p.listSort.Label())
	}

	fresh := New()
	fresh.ctx = p.ctx
	fresh.shellStartupHooks = p.shellStartupHooks
	fresh.restoreListSort()
	if fresh.listSort != workspacelist.SortManual {
		t.Fatalf("unoffered saved sort restored as %s, want the Manual default", fresh.listSort.Label())
	}
}

// The sort pill is a control, so it has to be clickable. It carries the
// plugin's own region ID; registering it under the shared component's kind left
// it drawn, hit-tested, and wired to a handler that could never be reached.
func TestSortPillIsClickable(t *testing.T) {
	p := sortPlugin(t)
	p.mouseHandler.Clear()
	_ = p.renderSidebarContent(40, 30)

	var found *mouse.Region
	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID == regionListSortButton {
			copied := r
			found = &copied
			break
		}
	}
	if found == nil {
		t.Fatal("no hit region for the sort pill; clicking it can never reach its handler")
	}
	_ = p.handleMouseClick(mouse.MouseAction{
		Type: mouse.ActionClick, X: found.Rect.X, Y: found.Rect.Y, Region: found,
	})
	if !p.viewFlyoutActive() {
		t.Fatal("clicking the sort pill did not open View")
	}
}

// "N of M" measures against what the list would show with no query. The main
// checkout is offered only when it hosts shells, and asking the filtered nested
// list made that answer depend on the query — so the denominator shrank as the
// user typed.
func TestFilterDenominatorDoesNotMoveWithTheQuery(t *testing.T) {
	p := New()
	root, other := t.TempDir(), t.TempDir()
	p.ctx = &plugin.Context{WorkDir: other, ProjectRoot: root, Epoch: 1}
	p.worktrees = []*Worktree{
		{Name: "repo", Path: root, Key: "main", Branch: "main", IsMain: true},
		{Name: "wt-a", Path: other, Key: "a", Branch: "a"},
	}
	p.nestedByWorkDir = map[string][]*ShellSession{
		filepath.Clean(root): {{Name: "main shell", TmuxName: "sh-main", WorkDir: root}},
	}

	_, before := p.filterCounts()
	p.listFilter.Focus()
	for _, r := range "zzzz" {
		p.listFilter.Insert(string(r))
	}
	matched, after := p.filterCounts()
	if after != before {
		t.Fatalf("denominator moved with the query: %d then %d", before, after)
	}
	if matched != 0 {
		t.Fatalf("a query matching nothing still matched %d rows", matched)
	}
}

// A project whose only worktree is the main checkout has an empty list, and an
// empty list has to say so. Counting raw worktrees left a fresh clone with a
// blank sidebar and no word about what to do next.
func TestFreshCloneExplainsItsEmptyList(t *testing.T) {
	p := New()
	root := t.TempDir()
	p.ctx = &plugin.Context{WorkDir: root, ProjectRoot: root, Epoch: 1}
	p.sidebarVisible = true
	p.activePane = PaneSidebar
	p.worktrees = []*Worktree{{Name: "repo", Path: root, Key: "main", Branch: "main", IsMain: true}}

	view := ansi.Strip(p.renderSidebarContent(40, 20))
	if !strings.Contains(view, "No workspaces") {
		t.Fatalf("a list with nothing in it said nothing:\n%s", view)
	}
}
