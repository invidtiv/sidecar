package overview

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// Slice 2 item 4 of docs/plans/active/global-overview-workspaces.md: the global
// Workspaces tab renders the shared catalog with the shared list component.
// Item 5 — one cache for both projections — is proved here at the model level
// and at the app level in internal/app/scope_test.go.

func catalogModel(t *testing.T) *Model {
	t.Helper()
	original := ActivityStorePath
	ActivityStorePath = func() string { return "" }
	t.Cleanup(func() { ActivityStorePath = original })
	now := time.Now()
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{
		{Name: "sidecar", Path: "/tmp/sidecar", Key: "sidecar"},
		{Name: "braid", Path: "/tmp/braid", Key: "braid"},
	}
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar", Workspaces: []workspaceinventory.Workspace{
		{ID: "s1", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindWorktree, Name: "modal", Branch: "modal-look", Provider: "codex", Live: true,
			Presentation: agentstatus.Presentation{Lane: agentstatus.LaneBlocked, Icon: "◆", Label: "needs input", ChangedAt: now.Add(-time.Minute)}},
		{ID: "s2", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindShell, Name: "Shell 1", TmuxName: "sidecar-sh-1", Live: true},
		{ID: "s3", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindWorktree, Name: "dormant", Branch: "old", Plain: true},
	}}
	m.results["braid"] = workspaceinventory.ProjectResult{ProjectKey: "braid", Workspaces: []workspaceinventory.Workspace{
		{ID: "b1", ProjectKey: "braid", ProjectName: "braid", Kind: workspaceinventory.KindWorktree, Name: "pipeline", Branch: "pipeline", Provider: "claude", Live: true,
			Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking, Icon: "●", Label: "working", ChangedAt: now.Add(-2 * time.Minute)}},
	}}
	m.syncBoard()
	return m
}

func TestGlobalRowsProjectStatusProviderProjectDetailAndAge(t *testing.T) {
	m := catalogModel(t)
	m.collector.Now = func() time.Time { return time.Now() }
	list := ansi.Strip(m.renderWorkspaceList(0, 0, 60, 22))
	for _, want := range []string{"◆", "sidecar modal", "1m", "▶", "codex", "modal-look", "●", "braid pipeline", "claude", "⑂", "❯"} {
		if !strings.Contains(list, want) {
			t.Fatalf("global row lost %q:\n%s", want, list)
		}
	}
	for _, status := range []string{"needs input", "working", "live", "idle", "ambiguous panes", "no session"} {
		if strings.Contains(list, status) {
			t.Fatalf("global list repeated status text %q:\n%s", status, list)
		}
	}
}

func TestListItemCarriesPresentationKind(t *testing.T) {
	wt := listItem(workspaceinventory.Item{Kind: workspaceinventory.KindWorktree, Name: "topic"}, "sidecar", 0, false)
	if wt.Kind != workspacelist.KindWorktree {
		t.Fatalf("worktree kind = %q", wt.Kind)
	}
	sh := listItem(workspaceinventory.Item{Kind: workspaceinventory.KindShell, Name: "Shell 1"}, "sidecar", 0, false)
	if sh.Kind != workspacelist.KindShell {
		t.Fatalf("shell kind = %q", sh.Kind)
	}
	if workspacelist.KindGlyph(wt.Kind) != kindGlyph(workspaceinventory.KindWorktree) {
		t.Fatal("list worktree glyph drifted from the Agents board")
	}
	if workspacelist.KindGlyph(sh.Kind) != kindGlyph(workspaceinventory.KindShell) {
		t.Fatal("list shell glyph drifted from the Agents board")
	}
}

func TestGlobalRowMarkerProjectionCoversHealthPlainAndMain(t *testing.T) {
	cases := []struct {
		name string
		item workspaceinventory.Item
		icon string
		tone workspacelist.MarkerTone
	}{
		{"missing health", workspaceinventory.Item{Agent: &agentstatus.Presentation{Health: true, Icon: "✗", Label: "folder missing"}}, "✗", workspacelist.MarkerError},
		{"orphan health", workspaceinventory.Item{Agent: &agentstatus.Presentation{Health: true, Icon: "⚠", Label: "session ended"}}, "⚠", workspacelist.MarkerWarning},
		{"ambiguous", workspaceinventory.Item{Ambiguous: true}, "?", workspacelist.MarkerWarning},
		{"plain live", workspaceinventory.Item{Live: true}, "◎", workspacelist.MarkerLive},
		{"main fallback", workspaceinventory.Item{IsMain: true}, "◉", workspacelist.MarkerMain},
		{"plain stopped", workspaceinventory.Item{}, "○", workspacelist.MarkerMuted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := listItem(tc.item, "sidecar", 0, false).Marker
			if got.Icon != tc.icon || got.Tone != tc.tone {
				t.Fatalf("marker = %#v, want icon %q tone %q", got, tc.icon, tc.tone)
			}
		})
	}
}

func TestGlobalWorkspacesListsEveryProjectsShellsAndWorktrees(t *testing.T) {
	m := catalogModel(t)
	view := ansi.Strip(m.WorkspacesView(60, 24))

	for _, want := range []string{
		"Workspaces", "Activity",
		"◆ NEEDS ATTENTION (1)", "● WORKING (1)", "● LIVE (1)",
		"modal", "Shell 1", "pipeline",
		"sidecar", "braid",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("global list is missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "No Session") || strings.Contains(view, "dormant") {
		t.Fatalf("idle worktrees should be hidden by default:\n%s", view)
	}

	// The Agents board keeps the agent-only projection of the same results.
	if len(m.cards) != 2 {
		t.Fatalf("board cards = %d, want only the two agent-backed workspaces", len(m.cards))
	}
	if _, plain := m.cards["s3"]; plain {
		t.Fatal("a plain worktree reached the Kanban board")
	}
	if _, shell := m.cards["s2"]; shell {
		t.Fatal("an unidentified shell reached the Kanban board")
	}
}

func TestPlainRowsGetPresentationBucketsNotFabricatedAgentState(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	byID := map[string]workspacelist.Item{}
	for _, item := range m.workspaces.Visible() {
		byID[item.ID] = item
	}
	if got := byID["s2"]; got.Group != workspacelist.GroupLive || got.Status != "live" || got.Provider != "" || got.Marker.Icon != "◎" {
		t.Fatalf("live plain shell = %#v", got)
	}
	if _, ok := byID["s3"]; ok {
		t.Fatal("a no-session worktree stayed visible while idle rows are hidden")
	}
	if got := byID["s1"]; got.Group != workspacelist.GroupNeedsAttention || got.Status != "needs input" {
		t.Fatalf("agent worktree lost its shared semantics: %#v", got)
	}
}

func TestSortAndFilterKeysDriveTheGlobalList(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)

	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 's', Text: "s"}); !handled || !m.ViewFlyoutOpen() {
		t.Fatal("`s` did not open the view fly-out")
	}
	view := ansi.Strip(m.WorkspacesView(60, 24))
	for _, want := range []string{"Current sort: Activity", "Activity", "Project", "Recent", "Name", "show idle worktrees"} {
		if !strings.Contains(view, want) {
			t.Fatalf("fly-out is missing %q:\n%s", want, view)
		}
	}
	// j moves the highlight; enter applies that mode and closes the fly-out.
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 'j', Text: "j"}); !handled {
		t.Fatal("j was not handled in the fly-out")
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter}); !handled {
		t.Fatal("enter was not handled in the fly-out")
	}
	if m.workspaces.Sort() != workspacelist.SortProject {
		t.Fatalf("enter applied %s, want Project", m.workspaces.Sort().Label())
	}
	if m.ViewFlyoutOpen() {
		t.Fatal("enter on a sort left the fly-out open; the user should not need Done")
	}
	if view := ansi.Strip(m.WorkspacesView(60, 24)); !strings.Contains(view, "Project") {
		t.Fatalf("list header did not keep the chosen sort:\n%s", view)
	}

	// Esc still dismisses without requiring Done.
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 's', Text: "s"}); !handled || !m.ViewFlyoutOpen() {
		t.Fatal("`s` did not reopen the view fly-out")
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEsc}); !handled || m.ViewFlyoutOpen() {
		t.Fatal("esc did not close the fly-out")
	}

	// Enter on the idle checkbox toggles and leaves the fly-out open.
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 's', Text: "s"}); !handled || !m.ViewFlyoutOpen() {
		t.Fatal("`s` did not reopen the view fly-out for the idle checkbox")
	}
	beforeIdle := m.showIdleWorktrees
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyTab}); !handled {
		t.Fatal("tab was not handled in the fly-out")
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter}); !handled {
		t.Fatal("enter on the idle checkbox was not handled")
	}
	if m.showIdleWorktrees == beforeIdle {
		t.Fatal("enter on the idle checkbox did not toggle show idle worktrees")
	}
	if !m.ViewFlyoutOpen() {
		t.Fatal("enter on the idle checkbox closed the fly-out")
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEsc}); !handled || m.ViewFlyoutOpen() {
		t.Fatal("esc did not close the fly-out after the idle toggle")
	}

	// `/` focuses the filter; typed characters are query text, and even keys
	// that mean something to the app elsewhere stay in the query.
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: '/', Text: "/"}); !handled || !m.WorkspacesFilterFocused() {
		t.Fatal("`/` did not focus the global filter")
	}
	for _, r := range "braid1q" {
		if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: r, Text: string(r)}); !handled {
			t.Fatalf("filter did not consume %q", r)
		}
	}
	if m.workspaces.Filter().Query() != "braid1q" {
		t.Fatalf("query = %q", m.workspaces.Filter().Query())
	}
	if matched, _ := m.workspaces.Counts(); matched != 0 {
		t.Fatalf("no-match query matched %d rows", matched)
	}
	view = ansi.Strip(m.WorkspacesView(60, 24))
	if !strings.Contains(view, "No workspaces match") {
		t.Fatalf("no-match state missing:\n%s", view)
	}

	// Escape clears, then releases focus; navigation then works again.
	m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.workspaces.Filter().Query() != "" || !m.WorkspacesFilterFocused() {
		t.Fatal("first escape did not clear the query")
	}
	m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.WorkspacesFilterFocused() {
		t.Fatal("second escape did not release focus")
	}
	first := m.workspaces.SelectedID()
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 'j', Text: "j"}); !handled || m.workspaces.SelectedID() == first {
		t.Fatal("j did not move the selection after leaving the filter")
	}
}

func TestProjectAndRecentSortsRenderSectionHeadings(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	m.workspaces.SetSort(workspacelist.SortProject)
	list := ansi.Strip(m.renderWorkspaceList(0, 0, 50, 22))
	if !strings.Contains(list, "○ SIDECAR (2)") || !strings.Contains(list, "○ BRAID (1)") {
		t.Fatalf("project sort lost per-project headings:\n%s", list)
	}

	now := time.Now()
	m.collector.Now = func() time.Time { return now }
	m.workspaces.SetSort(workspacelist.SortRecent)
	list = ansi.Strip(m.renderWorkspaceList(0, 0, 50, 22))
	if !strings.Contains(list, "○ NEW (") && !strings.Contains(list, "○ TODAY (") {
		t.Fatalf("recent sort lost time-bucket headings:\n%s", list)
	}
}

func TestPerProjectFailureIsAVisibleRowNotASilentGap(t *testing.T) {
	m := catalogModel(t)
	m.projectErrors["braid"] = errStub{"not a Git repository"}
	m.syncBoard()
	view := ansi.Strip(m.WorkspacesView(70, 24))
	if !strings.Contains(view, "braid unavailable") {
		t.Fatalf("project failure is not surfaced:\n%s", view)
	}
	// And in the ordinary case, where the catalog fills the pane and there are
	// no rows left over for the failure to occupy.
	short := ansi.Strip(m.WorkspacesView(70, 8))
	if !strings.Contains(short, "braid unavailable") {
		t.Fatalf("project failure vanished in a full viewport:\n%s", short)
	}
}

type errStub struct{ text string }

func (e errStub) Error() string { return e.text }

func TestBothProjectionsComeFromOneSyncOfOneResultsMap(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	if got := len(m.workspaces.Items()); got != 3 {
		t.Fatalf("list items = %d, want live catalog rows (idle hidden)", got)
	}
	if len(m.catalog) != 4 {
		t.Fatalf("catalog = %d, want every collected workspace including idle", len(m.catalog))
	}
	// Removing a project from the shared results map removes it from both
	// projections at once: there is no second cache to fall out of step.
	delete(m.results, "braid")
	m.projects = m.projects[:1]
	m.syncBoard()
	m.WorkspacesView(60, 24)
	if len(m.workspaces.Items()) != 2 {
		t.Fatalf("list did not follow the shared cache: %#v", m.workspaces.Items())
	}
	if _, ok := m.cards["b1"]; ok {
		t.Fatal("board did not follow the shared cache")
	}
}

func TestSelectedWorkspaceResolvesBackToTheCatalogRecord(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	if !m.workspaces.SelectID("s2") {
		t.Fatal("could not select the shell row")
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.ID != "s2" || workspace.Kind != workspaceinventory.KindShell {
		t.Fatalf("selected workspace = %#v ok=%v", workspace, ok)
	}
}

// ctrl+c is host-reserved: it is one of sidecar's two ways out, and a focused
// filter must not be the one text input that swallows it.
func TestFocusedFilterLeavesCtrlCToTheHost(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	m.WorkspacesKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	for _, r := range "brai" {
		m.WorkspacesKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if !m.WorkspacesFilterFocused() {
		t.Fatal("filter lost focus while typing")
	}

	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); handled {
		t.Fatal("focused filter swallowed ctrl+c; the quit confirmation is unreachable")
	}
	// It is left to the host untouched: the query and its focus survive.
	if !m.WorkspacesFilterFocused() || m.workspaces.Filter().Query() != "brai" {
		t.Fatalf("ctrl+c disturbed the filter: focused=%v query=%q",
			m.WorkspacesFilterFocused(), m.workspaces.Filter().Query())
	}
}

// A height too small to draw a box is not a narrow layout: the split still
// places both panels, so the tab does not silently become a full-width list at
// degenerate sizes.
func TestDegenerateHeightStillDrawsBothPanels(t *testing.T) {
	m, _ := previewModel(t)
	m.WorkspacesView(previewWide, 2)
	if layout := m.workspacesLayout(); layout.listOnly || layout.previewOnly || layout.previewDrawn {
		t.Fatalf("layout at height 2 = %#v, want the split with nothing drawable", layout)
	}
	var preview, sidebar bool
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		switch region.Data {
		case previewRegionKind:
			preview = true
		case workspacesSidebarRegion:
			sidebar = true
		}
	}
	if !preview || !sidebar {
		t.Fatalf("height 2 registered sidebar=%v preview=%v, want both panels", sidebar, preview)
	}
}

func TestHideIdleWorktreesIsTheDefaultAndTheFlyOutRestoresThem(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	if _, ok := visibleByID(m)["s3"]; ok {
		t.Fatal("idle worktree is visible on a fresh list")
	}
	if view := ansi.Strip(m.WorkspacesView(60, 24)); strings.Contains(view, "No Session") {
		t.Fatalf("No Session section shown while idle rows are hidden:\n%s", view)
	}

	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 's', Text: "s"}); !handled {
		t.Fatal("`s` did not open the fly-out")
	}
	// Tab past the sort list onto the checkbox, then enter to toggle.
	m.WorkspacesView(60, 24)
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyTab}); !handled {
		t.Fatal("tab was not handled")
	}
	before := m.showIdleWorktrees
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter}); !handled {
		t.Fatal("enter did not toggle the idle checkbox")
	}
	if m.showIdleWorktrees == before || !m.showIdleWorktrees {
		t.Fatal("idle checkbox did not turn on")
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEsc}); !handled || m.ViewFlyoutOpen() {
		t.Fatal("esc did not return focus to the list")
	}

	byID := visibleByID(m)
	if got := byID["s3"]; got.Group != workspacelist.GroupNoSession || got.Status != "no session" {
		t.Fatalf("turning idle on did not restore the no-session row: %#v", got)
	}
	view := ansi.Strip(m.WorkspacesView(60, 24))
	if !strings.Contains(view, "○ NO SESSION (1)") || !strings.Contains(view, "dormant") {
		t.Fatalf("idle rows did not return:\n%s", view)
	}

	m.showIdleWorktrees = false
	m.syncBoard()
	if _, ok := visibleByID(m)["s3"]; ok {
		t.Fatal("turning idle off left the no-session row on screen")
	}
}

func TestHideIdleKeepsLiveAndAgentRows(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	byID := visibleByID(m)
	for _, id := range []string{"s1", "s2", "b1"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("live/agent row %s was hidden with idle rows", id)
		}
	}
}

func TestFirstRunEmptyCatalogRegistersCreateControl(t *testing.T) {
	m := catalogModel(t)
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar"}
	m.results["braid"] = workspaceinventory.ProjectResult{ProjectKey: "braid"}
	m.showIdleWorktrees = true
	m.syncBoard()
	_ = m.WorkspacesView(60, 24)
	if !firstRunCreateHit(m) {
		t.Fatal("global first-run empty state has no create hit region")
	}
}

func TestFirstRunEmptyCatalogShowsAtDefaultHideIdle(t *testing.T) {
	m := catalogModel(t)
	m.showIdleWorktrees = false
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar"}
	m.results["braid"] = workspaceinventory.ProjectResult{ProjectKey: "braid"}
	m.syncBoard()
	if m.showIdleWorktrees {
		t.Fatal("test premise: hide-idle is the default")
	}
	if len(m.catalog) != 0 {
		t.Fatalf("catalog still has %d workspaces", len(m.catalog))
	}
	view := ansi.Strip(m.WorkspacesView(60, 24))
	if strings.Contains(view, "no sessions") {
		t.Fatalf("hide-idle replaced first-run copy on an empty catalog:\n%s", view)
	}
	if !strings.Contains(view, "No workspaces yet") {
		t.Fatalf("default-empty catalog lost first-run copy:\n%s", view)
	}
	if !strings.Contains(view, "Press n") || !strings.Contains(view, "ctrl+n") {
		t.Fatalf("first-run empty state missing create keys:\n%s", view)
	}
	if !strings.Contains(view, "agent") {
		t.Fatalf("first-run empty state did not say how to launch an agent:\n%s", view)
	}
	if !firstRunCreateHit(m) {
		t.Fatal("default-empty catalog has no create hit region")
	}
}

func firstRunCreateHit(m *Model) bool {
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if region.ID != string(workspacelist.RegionEmptyAction) {
			continue
		}
		hit, ok := region.Data.(workspacelist.Region)
		if ok && hit.ID == globalCreateActionID {
			return true
		}
	}
	return false
}

func TestHideIdleEmptyStateSaysNoSessions(t *testing.T) {
	m := catalogModel(t)
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar", Workspaces: []workspaceinventory.Workspace{
		{ID: "s3", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindWorktree, Name: "dormant", Branch: "old", Plain: true},
	}}
	m.results["braid"] = workspaceinventory.ProjectResult{ProjectKey: "braid"}
	m.syncBoard()
	view := ansi.Strip(m.WorkspacesView(60, 24))
	if !strings.Contains(view, "no sessions") {
		t.Fatalf("empty live list did not say no sessions:\n%s", view)
	}
	if strings.Contains(view, "No shells or worktrees found") {
		t.Fatalf("hid-idle empty state used the catalog-empty copy:\n%s", view)
	}

	m.showIdleWorktrees = true
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar"}
	m.syncBoard()
	view = ansi.Strip(m.WorkspacesView(60, 24))
	if !strings.Contains(view, "No workspaces yet") {
		t.Fatalf("truly empty catalog lost its empty copy:\n%s", view)
	}
}

func TestSortChipOpensTheFlyOutNotASilentCycle(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	var x, y int
	var ok bool
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		r, isRegion := region.Data.(workspacelist.Region)
		if isRegion && r.Kind == workspacelist.RegionSort {
			x, y = region.Rect.X, region.Rect.Y
			ok = true
			break
		}
	}
	if !ok {
		t.Fatal("sort chip was not registered")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}))
	if !m.ViewFlyoutOpen() {
		t.Fatal("clicking the sort chip did not open the fly-out")
	}
	if m.workspaces.Sort() != workspacelist.SortActivity {
		t.Fatal("clicking the sort chip cycled the sort")
	}
}

func TestSlashFromTheFlyOutFocusesTheFilter(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	m.WorkspacesKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if !m.ViewFlyoutOpen() {
		t.Fatal("test premise: fly-out is closed")
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: '/', Text: "/"}); !handled || m.ViewFlyoutOpen() || !m.WorkspacesFilterFocused() {
		t.Fatal("`/` from the fly-out did not focus the inline filter")
	}
}

func TestEnterTypesWhenTheFlyOutIsClosed(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	if m.ViewFlyoutOpen() {
		t.Fatal("test premise: fly-out opened itself")
	}
	press(t, m, "enter")
	if !m.PreviewInteractive() || terminal.opens != 1 {
		t.Fatal("enter on the list did not start typing")
	}
}

func TestDoubleClickOnAnIdleRowStillOpensItsProject(t *testing.T) {
	m := catalogModel(t)
	m.showIdleWorktrees = true
	m.syncBoard()
	m.WorkspacesView(60, 24)
	if !m.workspaces.SelectID("s3") {
		t.Fatal("could not select the restored idle row")
	}
	nav, ok := activate(t, m)
	if !ok || nav.Workspace.ID != "s3" {
		t.Fatalf("idle activation = %#v ok=%v, want s3", nav, ok)
	}
}

func TestIdleFlagPersistsThroughTheFlyOut(t *testing.T) {
	var saved bool
	origLoad, origSave := loadShowIdleWorktrees, saveShowIdleWorktrees
	loadShowIdleWorktrees = func() bool { return saved }
	saveShowIdleWorktrees = func(show bool) error {
		saved = show
		return nil
	}
	t.Cleanup(func() {
		loadShowIdleWorktrees = origLoad
		saveShowIdleWorktrees = origSave
	})

	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	m.WorkspacesKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	m.WorkspacesView(60, 24)
	m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !saved {
		t.Fatal("toggle did not persist showIdleWorktrees")
	}

	m2 := New(workspaceinventory.Collector{})
	if !m2.showIdleWorktrees {
		t.Fatal("a new model did not restore the persisted idle flag")
	}
}

func TestPinKeyTogglesAPinnedSectionAboveTheSort(t *testing.T) {
	var saved []string
	origLoad, origSave := loadPinnedWorkspaceIDs, savePinnedWorkspaceIDs
	loadPinnedWorkspaceIDs = func() []string { return append([]string(nil), saved...) }
	savePinnedWorkspaceIDs = func(ids []string) error {
		saved = append([]string(nil), ids...)
		return nil
	}
	t.Cleanup(func() {
		loadPinnedWorkspaceIDs = origLoad
		savePinnedWorkspaceIDs = origSave
	})

	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	if !m.workspaces.SelectID("s2") {
		t.Fatal("could not select the shell")
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 'p', Text: "p"}); !handled {
		t.Fatal("p on the list was not handled")
	}
	if !m.workspaces.IsPinned("s2") {
		t.Fatal("p did not pin the selected row")
	}
	if strings.Join(saved, ",") != "s2" {
		t.Fatalf("pin did not persist: %v", saved)
	}

	m.workspaces.SelectID("s1")
	m.WorkspacesKey(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if strings.Join(m.workspaces.PinnedIDs(), ",") != "s2,s1" {
		t.Fatalf("pin order = %v, want first-pinned first", m.workspaces.PinnedIDs())
	}
	if got := idsOfVisible(m); strings.Join(got, ",") != "s2,s1,b1" {
		t.Fatalf("visible = %v, want pinned first then activity", got)
	}
	list := ansi.Strip(m.renderWorkspaceList(0, 0, 50, 22))
	if !strings.Contains(list, "📌 PINNED (2)") {
		t.Fatalf("missing Pinned heading:\n%s", list)
	}
	if strings.Count(list, "sidecar Shell 1") != 1 || strings.Count(list, "sidecar modal") != 1 {
		t.Fatalf("pinned rows were duplicated:\n%s", list)
	}

	m.WorkspacesKey(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if m.workspaces.IsPinned("s1") || !m.workspaces.IsPinned("s2") {
		t.Fatal("p did not unpin the selected row")
	}

	m2 := New(workspaceinventory.Collector{})
	if strings.Join(m2.workspaces.PinnedIDs(), ",") != "s2" {
		t.Fatalf("a new model did not restore pins: %v", m2.workspaces.PinnedIDs())
	}
}

func TestGoneCatalogPinsAreDroppedQuietly(t *testing.T) {
	var saved []string
	origLoad, origSave := loadPinnedWorkspaceIDs, savePinnedWorkspaceIDs
	loadPinnedWorkspaceIDs = func() []string { return append([]string(nil), saved...) }
	savePinnedWorkspaceIDs = func(ids []string) error {
		saved = append([]string(nil), ids...)
		return nil
	}
	t.Cleanup(func() {
		loadPinnedWorkspaceIDs = origLoad
		savePinnedWorkspaceIDs = origSave
	})

	m := catalogModel(t)
	m.workspaces.SetPinned([]string{"gone", "s1"})
	m.loading = false
	m.syncBoard()
	if got := strings.Join(m.workspaces.PinnedIDs(), ","); got != "s1" {
		t.Fatalf("gone pin survived sync: %s", got)
	}
	if strings.Join(saved, ",") != "s1" {
		t.Fatalf("gone pin was not dropped from persist: %v", saved)
	}
}

func TestPinKeyWhileFilterFocusedStaysQueryText(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)
	m.WorkspacesKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 'p', Text: "p"}); !handled {
		t.Fatal("filter did not consume p")
	}
	if m.workspaces.Filter().Query() != "p" {
		t.Fatalf("query = %q, want p", m.workspaces.Filter().Query())
	}
	if len(m.workspaces.PinnedIDs()) != 0 {
		t.Fatal("p mid-query pinned a row")
	}
}

func TestPinKeyWhileTypingGoesToThePane(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	if !m.PreviewInteractive() {
		t.Fatal("test premise: not typing")
	}
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if !handled {
		t.Fatal("interactive p was not handled")
	}
	run(t, m, cmd)
	if len(terminal.keys) == 0 || terminal.keys[len(terminal.keys)-1] != "p" {
		t.Fatalf("pane keys = %v, want p", terminal.keys)
	}
	if len(m.workspaces.PinnedIDs()) != 0 {
		t.Fatal("typing p pinned a row")
	}
}

func TestListFocusedCommandsAdvertisePin(t *testing.T) {
	m := catalogModel(t)
	var found bool
	for _, cmd := range m.Commands() {
		if cmd.ID == "pin" && cmd.Name == "Pin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("list-focused Commands() omitted Pin: %#v", m.Commands())
	}
}

func idsOfVisible(m *Model) []string {
	var ids []string
	for _, item := range m.workspaces.Visible() {
		ids = append(ids, item.ID)
	}
	return ids
}

func visibleByID(m *Model) map[string]workspacelist.Item {
	byID := map[string]workspacelist.Item{}
	for _, item := range m.workspaces.Visible() {
		byID[item.ID] = item
	}
	return byID
}

// The working/blocked markers breathe on the global list too, and keep
// breathing while their row is selected — a selected agent is still working.
func TestGlobalWorkspaceMarkersPulseIncludingWhenSelected(t *testing.T) {
	m := catalogModel(t)
	m.preview.visible = true
	m.collector.Now = func() time.Time { return time.Now() }
	m.workspaces.SelectID("b1")

	cmd := m.Update(struct{}{})
	if cmd == nil {
		t.Fatal("no pulse tick armed for a board with a working row")
	}

	frames := map[string]bool{}
	for i := 0; i < len(workspacelist.WorkingPulse); i++ {
		row := workingRow(t, m.renderWorkspaceList(0, 0, 60, 22))
		frames[row] = true
		m.Update(workspacePulseTickMsg{generation: m.pulseGeneration})
	}
	if len(frames) < 3 {
		t.Fatalf("selected working row barely animates: %d distinct frames", len(frames))
	}
}

func TestOverviewStopInvalidatesPulseAndKeepsItStopped(t *testing.T) {
	m := catalogModel(t)
	m.preview.visible = true
	cmd := m.Update(struct{}{})
	if cmd == nil || !m.pulseScheduled {
		t.Fatal("test premise: visible working catalog did not arm pulse")
	}
	staleGeneration := m.pulseGeneration
	frame := m.pulseFrame

	m.Stop()
	if m.preview.visible || m.pulseScheduled {
		t.Fatal("Stop retained visible terminal/pulse ownership")
	}
	m.Update(workspacePulseTickMsg{generation: staleGeneration})
	if m.pulseFrame != frame || m.pulseScheduled {
		t.Fatal("stale pulse advanced or re-armed after Stop")
	}
}

func workingRow(t *testing.T, list string) string {
	t.Helper()
	for _, line := range strings.Split(list, "\n") {
		if strings.Contains(ansi.Strip(line), "braid pipeline") {
			return line
		}
	}
	t.Fatalf("working row missing from list:\n%s", list)
	return ""
}

// The chosen order is as much a part of "where I left off" as the pins and the
// sidebar width beside it, so it survives a relaunch.
func TestWorkspaceListSortPersists(t *testing.T) {
	origLoad, origSave := loadWorkspaceListSort, saveWorkspaceListSort
	saved := ""
	loadWorkspaceListSort = func() string { return saved }
	saveWorkspaceListSort = func(label string) error { saved = label; return nil }
	t.Cleanup(func() { loadWorkspaceListSort, saveWorkspaceListSort = origLoad, origSave })

	m := New(workspaceinventory.Collector{})
	if got := m.workspaces.Sort(); got != workspacelist.SortActivity {
		t.Fatalf("fresh sort = %s, want the Activity default", got.Label())
	}
	m.openViewFlyout()
	if cmd := m.applyViewFlyoutAction(workspacelist.SortActionID(workspacelist.SortProject), m.showIdleWorktrees); cmd != nil {
		_ = cmd()
	}
	if saved != "Project" {
		t.Fatalf("chosen sort saved as %q, want %q", saved, "Project")
	}

	// A fresh model over the same saved state comes back sorted the same way.
	if got := New(workspaceinventory.Collector{}).workspaces.Sort(); got != workspacelist.SortProject {
		t.Fatalf("restored sort = %s, want Project", got.Label())
	}

	// A label this list does not offer falls back to the default rather than
	// landing on an arbitrary mode.
	saved = "Manual" // the project sidebar offers it; global does not
	if got := New(workspaceinventory.Collector{}).workspaces.Sort(); got != workspacelist.SortActivity {
		t.Fatalf("unoffered saved sort restored as %s, want the Activity default", got.Label())
	}
}

// The header control says the list is sorted, not just what word describes it.
func TestSortPillNamesItselfAsASort(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	view := ansi.Strip(m.WorkspacesView(60, 24))
	if !strings.Contains(view, workspacelist.SortGlyph+" Activity") {
		t.Fatalf("header does not mark its sort control:\n%s", view)
	}
}

// The global browser is the second projection of the shared list, so the two
// polish changes that live in workspacelist have to be visible from here as
// well: a blank line between the panel's chrome and its first heading
// (td-a453b5), and no per-row project name under Project sort (td-ccd6cd).
func TestGlobalListSeparatesChromeAndDropsRepeatedProjectUnderProjectSort(t *testing.T) {
	m := catalogModel(t)
	m.WorkspacesView(60, 24)

	lines := strings.Split(ansi.Strip(m.renderWorkspaceList(0, 0, 50, 22)), "\n")
	if !strings.Contains(lines[0], "Workspaces") {
		t.Fatalf("row 0 is not the panel header: %q", lines[0])
	}
	if !strings.Contains(lines[1], "◆ NEEDS ATTENTION") {
		t.Fatalf("first section is not flush under the global panel header: %q", lines[1])
	}

	m.workspaces.SetSort(workspacelist.SortProject)
	for _, line := range strings.Split(ansi.Strip(m.renderWorkspaceList(0, 0, 50, 22)), "\n") {
		if strings.Contains(line, "sidecar") && !strings.Contains(line, "sidecar (") {
			t.Fatalf("a row repeats the project its heading already names: %q", line)
		}
	}
}
