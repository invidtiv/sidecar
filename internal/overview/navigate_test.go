package overview

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// Double-click in the global browser opens the exact owning workspace through
// the same validated navigation the Agents board uses. Enter starts typing
// in place and never navigates.
//
// What is proved here is the request the browser makes: which identity travels,
// when no request is made at all, and that activating never captures, attaches,
// or types. The app side of the journey — validation, the project switch, the
// pending selection, and the stale-item toast — is proved in
// internal/app/global_navigation_test.go, and the plugin's side in
// internal/plugins/workspace/navigated_selection_test.go.

// navigation runs an activation command and returns the request it produced.
func navigation(t *testing.T, cmd tea.Cmd) (NavigateMsg, bool) {
	t.Helper()
	if cmd == nil {
		return NavigateMsg{}, false
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if got, ok := navigation(t, sub); ok {
				return got, true
			}
		}
		return NavigateMsg{}, false
	}
	got, ok := msg.(NavigateMsg)
	return got, ok
}

func activate(t *testing.T, m *Model) (NavigateMsg, bool) {
	t.Helper()
	return navigation(t, m.activateWorkspace())
}

func TestEnterStartsTypingOnALiveRowAndDoesNotNavigate(t *testing.T) {
	m, recorder := previewModel(t)
	original := newPreviewTerminal
	terminal := newFakeTerminal("live pane body")
	newPreviewTerminal = func(config tty.Config, hooks tty.Hooks) previewTerminal {
		terminal.config = config
		terminal.hooks = hooks
		return terminal
	}
	t.Cleanup(func() { newPreviewTerminal = original })
	run(t, m, m.SetWorkspacesVisible(true))
	captures := len(recorder.panes())

	handled, cmd := m.WorkspacesKey(key("enter"))
	if !handled {
		t.Fatal("enter was not handled by the global Workspaces tab")
	}
	if request, ok := navigation(t, cmd); ok {
		t.Fatalf("enter navigated to %#v", request.Workspace)
	}
	run(t, m, cmd)
	if !m.PreviewInteractive() {
		t.Fatal("enter on a live row did not start typing")
	}
	if terminal.target != (tty.Target{Session: "sc-alpha", Pane: "%1"}) {
		t.Fatalf("the terminal opened %+v, want the selected row", terminal.target)
	}
	if got := len(recorder.panes()); got != captures {
		t.Fatalf("enter captured extra panes: %v", recorder.panes())
	}
}

func TestDoubleClickOpensTheSelectedWorkspaceByStableIdentity(t *testing.T) {
	m, recorder := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	captures := len(recorder.panes())

	cases := []struct {
		name string
		id   string
		kind workspaceinventory.Kind
	}{
		{"agent worktree", "a", workspaceinventory.KindWorktree},
		{"ambiguous shell", "c", workspaceinventory.KindShell},
		{"plain worktree with no session", "d", workspaceinventory.KindWorktree},
		{"agent whose session ended", "e", workspaceinventory.KindWorktree},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !m.workspaces.SelectID(tc.id) {
				t.Fatalf("could not select %q", tc.id)
			}
			m.WorkspacesView(previewWide, previewTall)
			request, ok := activate(t, m)
			if !ok {
				t.Fatal("activation produced no navigation request")
			}
			want, _ := m.SelectedWorkspace()
			if request.Workspace.ID != tc.id || request.Workspace.Kind != tc.kind {
				t.Fatalf("request = %#v, want the %s catalog record", request.Workspace, tc.id)
			}
			// The identity travels whole — ProjectKey + Kind + Key — not a row
			// number and not a display name.
			if request.Workspace.ProjectKey != want.ProjectKey || request.Workspace.Key != want.Key || request.Workspace.Path != want.Path {
				t.Fatalf("request identity = %#v, want %#v", request.Workspace, want)
			}
			if request.Generation != m.generation {
				t.Fatalf("request generation = %d, want the live cycle %d", request.Generation, m.generation)
			}
		})
	}

	// Opening is a request to validate an identity. It captures nothing, and it
	// cannot reach a terminal from here at all.
	if got := len(recorder.panes()); got != captures {
		t.Fatalf("activation captured panes: %v", recorder.panes())
	}
}

func TestActivationFollowsTheCursorThroughSortAndFilter(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))

	// The row under the cursor keeps its identity when sorting reorders the
	// list beneath it, so activation opens the item the user is looking at.
	m.workspaces.SelectID("d")
	m.WorkspacesView(previewWide, previewTall)
	m.workspaces.SetSort(workspacelist.SortRecent)
	request, ok := activate(t, m)
	if !ok || request.Workspace.ID != "d" {
		t.Fatalf("after a sort change activation opened %#v, want d", request.Workspace)
	}

	// Filtering to one row and activating opens that row, not the one that
	// used to occupy its position. Enter in the filter only accepts the query.
	press(t, m, "/")
	for _, r := range "echo" {
		m.WorkspacesKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.WorkspacesFilterFocused() {
		t.Fatal("enter in the filter did not hand navigation back to the list")
	}
	m.WorkspacesView(previewWide, previewTall)
	request, ok = activate(t, m)
	if !ok || request.Workspace.ID != "e" {
		t.Fatalf("filtered activation opened %#v, want e", request.Workspace)
	}
}

// A query that matches nothing selects nothing, and enter on nothing must not
// fall back to a neighbour.
func TestEnterWithNoSelectionRequestsNothing(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	press(t, m, "/")
	for _, r := range "zzzz" {
		m.WorkspacesKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if matched, _ := m.workspaces.Counts(); matched != 0 {
		t.Fatalf("no-match query matched %d rows", matched)
	}
	m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.WorkspacesView(previewWide, previewTall)
	if request, ok := activate(t, m); ok {
		t.Fatalf("activation on an empty list requested %#v", request.Workspace)
	}
	handled, cmd := m.WorkspacesKey(key("enter"))
	if !handled {
		t.Fatal("enter on an empty list was not answered")
	}
	if request, ok := navigation(t, cmd); ok {
		t.Fatalf("enter on an empty list navigated to %#v", request.Workspace)
	}
}

// While the query has focus, enter belongs to the filter: it accepts the
// current match and returns to list navigation rather than opening a project
// or starting to type.
func TestEnterInsideTheFilterAcceptsInsteadOfNavigating(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	press(t, m, "/")
	for _, r := range "bravo" {
		m.WorkspacesKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled {
		t.Fatal("the focused filter did not handle enter")
	}
	if request, ok := navigation(t, cmd); ok {
		t.Fatalf("enter inside the filter navigated to %#v", request.Workspace)
	}
	if m.WorkspacesFilterFocused() || m.workspaces.SelectedID() != "b" {
		t.Fatalf("filter enter = focused:%v selected:%q", m.WorkspacesFilterFocused(), m.workspaces.SelectedID())
	}
}

func TestDoubleClickOpensTheRowItSelectsAndSingleClickDoesNot(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.WorkspacesView(previewWide, previewTall)

	x, y, ok := rowPoint(m, "d")
	if !ok {
		t.Fatal("the plain worktree row was not rendered")
	}
	first := m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if request, opened := navigation(t, first); opened {
		t.Fatalf("a single click opened %#v", request.Workspace)
	}
	if m.workspaces.SelectedID() != "d" {
		t.Fatalf("the click selected %q, want d", m.workspaces.SelectedID())
	}
	second := m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	request, opened := navigation(t, second)
	if !opened {
		t.Fatal("double click did not open the row")
	}
	if request.Workspace.ID != "d" {
		t.Fatalf("double click opened %#v, want the row it clicked", request.Workspace)
	}
}

// Two projects can name a shell or a worktree the same thing. Only the stable
// identity distinguishes them, so both rows must open their own project.
func TestDuplicateDisplayNamesOpenTheirOwnProject(t *testing.T) {
	m := catalogModel(t)
	now := time.Now()
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar", Workspaces: []workspaceinventory.Workspace{
		{ID: "sidecar:worktree:/repos/sidecar/feature", ProjectKey: "sidecar", ProjectName: "sidecar", ProjectRoot: "/repos/sidecar",
			Kind: workspaceinventory.KindWorktree, Name: "feature", Key: "/repos/sidecar/feature", Path: "/repos/sidecar/feature",
			Branch: "feature", Plain: true, ObservedAt: now},
	}}
	m.results["braid"] = workspaceinventory.ProjectResult{ProjectKey: "braid", Workspaces: []workspaceinventory.Workspace{
		{ID: "braid:worktree:/repos/braid/feature", ProjectKey: "braid", ProjectName: "braid", ProjectRoot: "/repos/braid",
			Kind: workspaceinventory.KindWorktree, Name: "feature", Key: "/repos/braid/feature", Path: "/repos/braid/feature",
			Branch: "feature", Plain: true, ObservedAt: now},
	}}
	m.showIdleWorktrees = true
	m.syncBoard()
	m.workspaces.SetSort(workspacelist.SortName)
	m.WorkspacesView(previewWide, previewTall)

	for _, want := range []struct{ id, root string }{
		{"sidecar:worktree:/repos/sidecar/feature", "/repos/sidecar"},
		{"braid:worktree:/repos/braid/feature", "/repos/braid"},
	} {
		if !m.workspaces.SelectID(want.id) {
			t.Fatalf("could not select %q", want.id)
		}
		m.WorkspacesView(previewWide, previewTall)
		request, ok := activate(t, m)
		if !ok || request.Workspace.ID != want.id || request.Workspace.ProjectRoot != want.root {
			t.Fatalf("duplicate-name activation = %#v, want %s in %s", request.Workspace, want.id, want.root)
		}
	}
}

// A newer activation supersedes an older one, and leaving the catalog's
// Workspaces projection supersedes whatever was still being validated.
func TestActivationsSupersedeEachOtherAndTheTabSwitch(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))

	first, ok := activate(t, m)
	if !ok {
		t.Fatal("first activation produced no request")
	}
	press(t, m, "j")
	second, ok := activate(t, m)
	if !ok {
		t.Fatal("second activation produced no request")
	}
	if m.IsCurrentNavigation(first.Generation, first.RequestID) {
		t.Fatal("the superseded first activation is still current")
	}
	if !m.IsCurrentNavigation(second.Generation, second.RequestID) {
		t.Fatal("the newest activation is not current")
	}

	// Switching to the other projection of the same catalog ends the request:
	// the user has left the view they pressed enter on.
	m.SetWorkspacesVisible(false)
	if m.IsCurrentNavigation(second.Generation, second.RequestID) {
		t.Fatal("leaving the Workspaces tab left an activation in flight")
	}
}

// rowPoint returns a point inside the rendered row for a stable ID, taken from
// the same layout the view registered its hit regions from.
func rowPoint(m *Model, id string) (int, int, bool) {
	split := m.previewSplit(previewWide)
	rendered := m.workspaces.Render(workspacelist.RenderOptions{
		Width: split.SidebarWidth, Height: previewTall, Title: "Workspaces", Focused: true, Now: m.now(),
	})
	for _, region := range rendered.Regions {
		if region.Kind == workspacelist.RegionRow && region.ID == id {
			return globalContentInset + region.X + 1, 1 + region.Y, true
		}
	}
	return 0, 0, false
}

// The global browser's list is a reader. Creation, deletion, attach and the Git
// and Task lifecycle stay in the owning project's Workspaces plugin, where their
// validation and refusal rules live — so the keys that mean those things here
// mean nothing. Typing into a pane that already exists is on the other side of
// that line: it creates nothing, so Enter / E start typing from the list.
func TestGlobalBrowserListOffersNoMutatingPath(t *testing.T) {
	m, recorder := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	captures := len(recorder.panes())

	// The discoverable command set — what help and the palette offer for this
	// tab — carries the same boundary as the keys below. rename-shell is a
	// display-name write (same as `sidecar shell rename`), not create/destroy.
	allowed := map[string]bool{"rename-shell": true}
	var registered int
	for _, binding := range keymap.DefaultBindings() {
		if binding.Context != "global-workspaces" && binding.Context != "global-workspaces-filter" {
			continue
		}
		registered++
		if allowed[binding.Command] {
			continue
		}
		for _, forbidden := range []string{"new", "delete", "rename", "attach", "merge", "diff", "task", "commit", "start", "stop", "kill", "push", "approve", "create"} {
			if strings.Contains(binding.Command, forbidden) {
				t.Fatalf("the list's command set offers %q", binding.Command)
			}
		}
	}
	if registered == 0 {
		t.Fatal("the global Workspaces tab registers no bindings, so help and the palette document nothing")
	}

	// The project plugin's mutating keys are not the browser's to answer.
	// R on a worktree is ignored; R on a shell is rename-shell (tested separately).
	before := m.workspaces.SelectedID()
	for _, k := range []string{"n", "D", "a", "c", "x", "N", "R"} {
		if handled, cmd := m.WorkspacesKey(key(k)); handled {
			t.Fatalf("%q was answered by the global browser (cmd=%v)", k, cmd != nil)
		}
	}
	if m.workspaces.SelectedID() != before {
		t.Fatalf("a refused key moved the selection to %q", m.workspaces.SelectedID())
	}
	if len(recorder.panes()) != captures {
		t.Fatalf("a refused key captured a pane: %v", recorder.panes())
	}
}
