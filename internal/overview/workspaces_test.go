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
	m.sidebarWidth = 60
	view := ansi.Strip(m.WorkspacesView(120, 24))
	for _, want := range []string{"◆ modal", "▶ codex", "sidecar", "needs input", "modal-look", "1m", "● pipeline", "claude", "braid"} {
		if !strings.Contains(view, want) {
			t.Fatalf("global row lost %q:\n%s", want, view)
		}
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
		"Needs Attention (1)", "Working (1)", "Live (1)",
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
	// j moves the highlight; enter applies that mode without cycling.
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 'j', Text: "j"}); !handled {
		t.Fatal("j was not handled in the fly-out")
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter}); !handled {
		t.Fatal("enter was not handled in the fly-out")
	}
	if m.workspaces.Sort() != workspacelist.SortProject {
		t.Fatalf("enter applied %s, want Project", m.workspaces.Sort().Label())
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEsc}); !handled || m.ViewFlyoutOpen() {
		t.Fatal("esc did not close the fly-out")
	}
	if view := ansi.Strip(m.WorkspacesView(60, 24)); !strings.Contains(view, "Project") {
		t.Fatalf("list header did not keep the chosen sort:\n%s", view)
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
	if !strings.Contains(view, "No Session (1)") || !strings.Contains(view, "dormant") {
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
	if !strings.Contains(view, "No shells or worktrees found in the configured projects") {
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

func visibleByID(m *Model) map[string]workspacelist.Item {
	byID := map[string]workspacelist.Item{}
	for _, item := range m.workspaces.Visible() {
		byID[item.ID] = item
	}
	return byID
}
