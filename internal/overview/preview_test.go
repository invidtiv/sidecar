package overview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// Slice 3 of docs/plans/active/global-overview-workspaces.md: the global
// Workspaces tab's right side is one terminal box, fed while it is watched by a
// selected-pane source with generation cancellation, immediate selected
// capture, and adaptive visible-only polling. Handing that box its keyboard is
// interactive_test.go's subject; everything here is the watching state.
//
// The shared geometry and header/body presentation are termpreview's, and the
// agreement between that layer and the pane tree is proved in
// internal/plugins/workspace/shared_preview_geometry_test.go. What is proved
// here is the source's behaviour: what it captures, what it refuses to capture,
// what it throws away, and what it keeps.

type captureRecorder struct {
	mu     sync.Mutex
	calls  []string
	output map[string]string
	// state is the geometry tmux reports beside a pane's capture, per pane. The
	// zero value is a capture taken with no geometry observed.
	state map[string]tty.PaneState
	err   error
}

func (c *captureRecorder) capture(pane string, lines int) (string, tty.PaneState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, pane)
	if c.err != nil {
		return "", tty.PaneState{}, c.err
	}
	state := c.state[pane]
	if out, ok := c.output[pane]; ok {
		return out, state, nil
	}
	return "pane " + pane + " output\nsecond line", state, nil
}

func (c *captureRecorder) panes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

const previewWide, previewTall = 120, 24

// previewModel is the catalog fixture with panes attached: a live agent
// worktree, a live plain shell, an ambiguous item, an item whose session has
// ended, and one with no session at all.
func previewModel(t *testing.T) (*Model, *captureRecorder) {
	t.Helper()
	original := ActivityStorePath
	ActivityStorePath = func() string { return "" }
	t.Cleanup(func() { ActivityStorePath = original })

	now := time.Now()
	m := New(workspaceinventory.Collector{})
	recorder := &captureRecorder{output: map[string]string{}, state: map[string]tty.PaneState{}}
	m.collector.Capture = recorder.capture
	m.projects = []Project{{Name: "sidecar", Path: "/tmp/sidecar", Key: "sidecar"}}
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar", Workspaces: []workspaceinventory.Workspace{
		{ID: "a", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindWorktree, Name: "alpha", Branch: "alpha-branch",
			Provider: "codex", PaneID: "%1", TmuxName: "sc-alpha", Live: true, Path: "/tmp/sidecar-alpha",
			Presentation: agentstatus.Presentation{Lane: agentstatus.LaneBlocked, Label: "needs input", ChangedAt: now.Add(-time.Minute)}},
		{ID: "b", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindWorktree, Name: "bravo", Branch: "bravo-branch",
			Provider: "claude", PaneID: "%2", TmuxName: "sc-bravo", Live: true, Path: "/tmp/sidecar-bravo",
			Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking, Label: "working", ChangedAt: now.Add(-2 * time.Minute)}},
		{ID: "c", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindShell, Name: "charlie", TmuxName: "sc-sh", Ambiguous: true},
		{ID: "d", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindWorktree, Name: "delta", Branch: "delta-branch", Plain: true, Path: "/tmp/sidecar-delta"},
		{ID: "e", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindWorktree, Name: "echo", Branch: "echo-branch",
			Provider: "codex", PaneID: "%9", TmuxName: "sc-echo", Live: false, Path: "/tmp/sidecar-echo",
			Presentation: agentstatus.Presentation{Lane: agentstatus.LanePaused, Label: "paused", ChangedAt: now.Add(-time.Hour)}},
	}}
	m.showIdleWorktrees = true
	m.syncBoard()
	m.workspaces.SetSort(workspacelist.SortName)
	m.workspaces.SelectID("a")
	m.WorkspacesView(previewWide, previewTall)
	return m, recorder
}

// run executes a command and delivers whatever it produced back to the model,
// which is what the Bubble Tea loop does with it.
func run(t *testing.T, m *Model, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var next []tea.Cmd
		for _, sub := range batch {
			next = append(next, run(t, m, sub))
		}
		return tea.Batch(next...)
	}
	if _, poll := msg.(previewPollMsg); poll {
		// Poll ticks are delivered explicitly by the tests that mean to; running
		// them here would sleep out the real cadence.
		return nil
	}
	return m.Update(msg)
}

func key(k string) tea.KeyPressMsg {
	if len(k) == 1 {
		return tea.KeyPressMsg{Code: rune(k[0]), Text: k}
	}
	switch k {
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case ",", ".":
		return tea.KeyPressMsg{Code: rune(k[0]), Text: k}
	}
	return tea.KeyPressMsg{}
}

func press(t *testing.T, m *Model, k string) {
	t.Helper()
	handled, cmd := m.WorkspacesKey(key(k))
	if !handled {
		t.Fatalf("%q was not handled by the global Workspaces tab", k)
	}
	run(t, m, cmd)
	m.WorkspacesView(previewWide, previewTall)
}

// hasCaptureFreshness is the header text this surface used to tick every poll
// and must not show again: "captured now" or "captured Ns ago".
func hasCaptureFreshness(view string) bool {
	return strings.Contains(view, "captured now") ||
		(strings.Contains(view, "captured ") && strings.Contains(view, "s ago"))
}

func TestPreviewHintsKeepStatusAndDropFreshness(t *testing.T) {
	agent := workspaceinventory.Workspace{
		Kind: workspaceinventory.KindWorktree, Live: true, PaneID: "%1",
		Presentation: agentstatus.Presentation{Label: "needs input"},
	}
	if got := previewHints(agent, false); got != "needs input" {
		t.Fatalf("unfocused agent hints = %q, want the presentation label only", got)
	}
	if got := previewHints(agent, true); got != "needs input · enter to type" {
		t.Fatalf("focused agent hints = %q, want status and the type hint", got)
	}

	live := workspaceinventory.Workspace{
		Kind: workspaceinventory.KindShell, Live: true, PaneID: "%2",
	}
	if got := previewHints(live, false); got != "live" {
		t.Fatalf("live shell hints = %q, want the live label only", got)
	}
	if got := previewHints(live, true); got != "live · enter to type" {
		t.Fatalf("focused live shell hints = %q, want live and the type hint", got)
	}

	ended := workspaceinventory.Workspace{
		Kind: workspaceinventory.KindWorktree, Live: false, PaneID: "%9",
		Presentation: agentstatus.Presentation{Label: "paused"},
	}
	if got := previewHints(ended, true); got != "paused · no live pane" {
		t.Fatalf("ended session hints = %q, want status and the no-pane hint", got)
	}

	for _, got := range []string{
		previewHints(agent, false),
		previewHints(agent, true),
		previewHints(live, true),
		previewHints(ended, false),
	} {
		if hasCaptureFreshness(got) {
			t.Fatalf("previewHints still reports capture freshness: %q", got)
		}
	}
}

func TestSelectionCapturesExactlyTheSelectedPaneImmediately(t *testing.T) {
	m, recorder := previewModel(t)

	run(t, m, m.SetWorkspacesVisible(true))
	if got := recorder.panes(); len(got) != 1 || got[0] != "%1" {
		t.Fatalf("becoming visible captured %v, want exactly the selected pane %%1", got)
	}
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "pane %1 output") {
		t.Fatalf("preview does not show the selected pane's capture:\n%s", view)
	}
	if !strings.Contains(view, "alpha") {
		t.Fatalf("preview header lost its identity:\n%s", view)
	}
	if !strings.Contains(view, "needs input") {
		t.Fatalf("preview header lost the agent status:\n%s", view)
	}
	if hasCaptureFreshness(view) {
		t.Fatalf("preview header still reports capture freshness:\n%s", view)
	}
	// The list is the browse surface. Hiding the sidebar does not move the
	// keyboard onto the preview, so the type hint stays off.
	if strings.Contains(view, "i to type") {
		t.Fatalf("the preview still advertises i as a way in:\n%s", view)
	}
	if strings.Contains(view, "enter to type") {
		t.Fatalf("the list-focused preview advertises a type hint:\n%s", view)
	}
	press(t, m, "\\")
	hidden := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if m.PreviewFocused() || m.WorkspaceFocusContext() != "global-workspaces" {
		t.Fatalf("hiding the sidebar moved the keyboard: focused=%v context=%q", m.PreviewFocused(), m.WorkspaceFocusContext())
	}
	if strings.Contains(hidden, "enter to type") {
		t.Fatalf("a hidden-sidebar preview advertises a watched-preview type hint:\n%s", hidden)
	}
	if !strings.Contains(hidden, "needs input") || hasCaptureFreshness(hidden) {
		t.Fatalf("hiding the sidebar lost status or reintroduced freshness:\n%s", hidden)
	}
	press(t, m, "\\")

	// Moving the cursor captures the newly selected pane straight away, and only
	// that one: a selection change is a thing the user feels.
	press(t, m, "j")
	if got := recorder.panes(); len(got) != 2 || got[1] != "%2" {
		t.Fatalf("selection change captured %v, want %%1 then %%2", got)
	}
	if view := ansi.Strip(m.WorkspacesView(previewWide, previewTall)); !strings.Contains(view, "pane %2 output") {
		t.Fatalf("preview did not follow the selection:\n%s", view)
	}
	if strings.Contains(ansi.Strip(m.WorkspacesView(previewWide, previewTall)), "pane %1 output") {
		t.Fatal("the previous item's capture is still on screen")
	}

	// Re-rendering is not work: no frame captures anything.
	for range 5 {
		m.WorkspacesView(previewWide, previewTall)
	}
	if got := recorder.panes(); len(got) != 2 {
		t.Fatalf("rendering captured panes: %v", got)
	}
	if m.PreviewMetrics().Captures != 2 {
		t.Fatalf("capture metric = %d, want 2", m.PreviewMetrics().Captures)
	}
}

func TestLateCaptureForASupersededSelectionIsDropped(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))

	stale := previewMsg{Generation: m.preview.generation, WorkspaceID: "a", PaneID: "%1", Output: "STALE OUTPUT", At: time.Now()}
	press(t, m, "j") // now on bravo, generation bumped

	m.Update(stale)
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if strings.Contains(view, "STALE OUTPUT") {
		t.Fatalf("a capture for a superseded selection painted over the current one:\n%s", view)
	}
	if m.PreviewMetrics().Rejected != 1 {
		t.Fatalf("rejected metric = %d, want the stale capture counted", m.PreviewMetrics().Rejected)
	}

	// A capture for the right generation but the wrong item is equally refused:
	// identity, not timing, is what decides.
	m.Update(previewMsg{Generation: m.preview.generation, WorkspaceID: "a", PaneID: "%1", Output: "WRONG ITEM", At: time.Now()})
	if strings.Contains(ansi.Strip(m.WorkspacesView(previewWide, previewTall)), "WRONG ITEM") {
		t.Fatal("a capture for another workspace was accepted")
	}
}

func TestHiddenTabDoesNoWorkAndKeepsNothing(t *testing.T) {
	m, recorder := previewModel(t)

	// Nothing is visible yet: navigating the list captures nothing at all.
	press(t, m, "j")
	press(t, m, "k")
	if got := recorder.panes(); len(got) != 0 {
		t.Fatalf("hidden tab captured %v", got)
	}
	if interval := m.previewInterval(); interval != 0 {
		t.Fatalf("hidden preview scheduled work every %s", interval)
	}

	run(t, m, m.SetWorkspacesVisible(true))
	if len(recorder.panes()) != 1 {
		t.Fatalf("becoming visible did not capture once: %v", recorder.panes())
	}

	// Hiding the tab cancels the in-flight generation, stops the poll, and drops
	// the captured output — terminal contents are memory-only and belong to a
	// surface somebody is looking at.
	generation := m.preview.generation
	run(t, m, m.SetWorkspacesVisible(false))
	if m.preview.generation == generation {
		t.Fatal("hiding did not supersede the in-flight capture")
	}
	if m.preview.buffer != nil || m.preview.workspaceID != "" {
		t.Fatalf("hidden preview retained %+v", m.preview.capture)
	}
	if cmd := m.pollPreview(previewPollMsg{Generation: generation, WorkspaceID: "a"}); cmd != nil {
		t.Fatal("a poll survived the tab being hidden")
	}
	if got := recorder.panes(); len(got) != 1 {
		t.Fatalf("hidden tab kept capturing: %v", got)
	}
}

func TestUnavailableItemsExplainThemselvesAndCaptureNothing(t *testing.T) {
	m, recorder := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	captured := len(recorder.panes())

	cases := []struct {
		id, want string
	}{
		{"c", "Several panes match"},
		{"d", "No live session"},
		{"e", "session for this workspace has ended"},
	}
	for _, tc := range cases {
		m.workspaces.SelectID(tc.id)
		run(t, m, m.previewSync())
		view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
		if !strings.Contains(view, tc.want) {
			t.Fatalf("item %q did not say why it has no preview (%q):\n%s", tc.id, tc.want, view)
		}
		if !strings.Contains(view, "no live pane") {
			t.Fatalf("item %q did not keep the no-pane hint:\n%s", tc.id, view)
		}
		if hasCaptureFreshness(view) {
			t.Fatalf("item %q still reports capture freshness:\n%s", tc.id, view)
		}
		// The reason is not the whole answer: the metadata is what the pane is
		// for when there is no output to show.
		if !strings.Contains(view, "project") || !strings.Contains(view, "sidecar") {
			t.Fatalf("item %q showed no metadata:\n%s", tc.id, view)
		}
		if m.previewInterval() != 0 {
			t.Fatalf("item %q with no live pane is still being polled", tc.id)
		}
	}
	if got := recorder.panes(); len(got) != captured {
		t.Fatalf("unavailable items were captured anyway: %v", got)
	}
}

func TestCaptureFailureIsReportedNotDrawnAsEmptyOutput(t *testing.T) {
	m, recorder := previewModel(t)
	recorder.err = errors.New("no server running")
	run(t, m, m.SetWorkspacesVisible(true))

	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "Could not read this pane") || !strings.Contains(view, "no server running") {
		t.Fatalf("failed capture was not explained:\n%s", view)
	}
	if m.previewInterval() != 0 {
		t.Fatal("a pane that cannot be read is still being polled at the live cadence")
	}
}

func TestListKeysMoveTheListNotThePreview(t *testing.T) {
	m, recorder := previewModel(t)
	lines := make([]string, 0, 80)
	for i := range 80 {
		lines = append(lines, "line-"+string(rune('A'+i%26))+strings.Repeat("x", i%7))
	}
	recorder.output["%1"] = strings.Join(lines, "\n")
	run(t, m, m.SetWorkspacesVisible(true))

	selected := m.workspaces.SelectedID()
	press(t, m, "j")
	if m.preview.offset != 0 {
		t.Fatal("list navigation scrolled the preview")
	}
	if m.workspaces.SelectedID() == selected {
		t.Fatal("j did not move the list")
	}
	if m.PreviewFocused() || m.WorkspaceFocusContext() != "global-workspaces" {
		t.Fatalf("j entered a watched-preview state: focused=%v context=%q", m.PreviewFocused(), m.WorkspaceFocusContext())
	}
}

func TestWheelOverThePreviewScrollsTheCaptureNotTheList(t *testing.T) {
	m, recorder := previewModel(t)
	recorder.output["%1"] = strings.Join(make([]string, 60), "content\n") + "tail"
	run(t, m, m.SetWorkspacesVisible(true))
	m.WorkspacesView(previewWide, previewTall)

	split := m.previewSplit(previewWide)
	x, y := split.PreviewX+2, 6
	scroll := m.workspaces.SelectedID()

	m.WorkspacesMouse(tea.MouseWheelMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseWheelUp}))
	if m.preview.offset != 3 {
		t.Fatalf("wheel over the preview did not use the shared three-row step: offset %d", m.preview.offset)
	}
	settleWheel()
	m.WorkspacesMouse(tea.MouseWheelMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseWheelDown}))
	if m.preview.offset != 0 {
		t.Fatalf("wheel down did not return to live output: offset %d", m.preview.offset)
	}
	if m.workspaces.SelectedID() != scroll {
		t.Fatal("the wheel over the preview moved the list")
	}

	// A press on the preview does not enter watched-preview. Activation is
	// click-release, and that starts typing rather than leaving a third state.
	m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if m.PreviewFocused() {
		t.Fatal("a press on the preview entered watched-preview")
	}
	if m.PreviewInteractive() {
		t.Fatal("a press without release started typing")
	}
}

func TestGlobalWorkspaceChromeProtectsThePreviewRightEdge(t *testing.T) {
	m, recorder := previewModel(t)
	split := m.previewSplit(previewWide)
	// The preview's final inner column is stable scrollbar chrome. Fill every
	// content column before it and put a marker in the last one; this is the
	// column that disappeared when the raw preview was drawn against the screen
	// edge without the project Workspace panel's inset.
	recorder.output["%1"] = strings.Repeat("x", split.ContentWidth-2) + "Z"
	run(t, m, m.SetWorkspacesVisible(true))
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	lines := strings.Split(view, "\n")

	if got := ansi.StringWidth(lines[2]); got != previewWide {
		t.Fatalf("preview body row width = %d, want %d: %q", got, previewWide, lines[2])
	}
	markerColumn := split.ContentX + split.ContentWidth - 2
	if got := []rune(lines[2])[markerColumn]; got != 'Z' {
		t.Fatalf("last preview content column = %q at x=%d, want marker Z\n%s", got, markerColumn, view)
	}
	if got := []rune(lines[2])[previewWide-1]; got != '│' {
		t.Fatalf("right panel border = %q, want visible border at x=%d", got, previewWide-1)
	}
}

func TestWorkspaceListWheelMovesSelectionLikeTheProjectSidebar(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.WorkspacesView(previewWide, previewTall)

	m.WorkspacesMouse(tea.MouseWheelMsg(tea.Mouse{
		X: globalContentInset + 2, Y: 4, Button: tea.MouseWheelDown,
	}))
	if got := m.workspaces.SelectedID(); got != "d" {
		t.Fatalf("one wheel notch selected %q, want d after the shared three-row step", got)
	}
}

func TestWorkspaceSidebarHoverDoesNotStealListFocus(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))

	selected := m.workspaces.SelectedID()
	// The outer border belongs only to the broad sidebar fallback region, so
	// this proves blank-space hover does not inherit row-click focus behavior.
	m.WorkspacesMouse(tea.MouseMotionMsg(tea.Mouse{X: 0, Y: 4}))

	if m.PreviewFocused() || m.WorkspaceFocusContext() != "global-workspaces" {
		t.Fatalf("hovering over sidebar blank space moved the keyboard: focused=%v context=%q", m.PreviewFocused(), m.WorkspaceFocusContext())
	}
	if got := m.workspaces.SelectedID(); got != selected {
		t.Fatalf("hover changed selection to %q, want %q", got, selected)
	}
}

func TestWorkspaceSidebarWheelUpdatesSelectionAndPreviewWithoutStealingFocus(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))

	// Use the outer border to exercise the broad sidebar region rather than a
	// row. The wheel moves the cursor and its preview; the keyboard stays on
	// the list.
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg(tea.Mouse{
		X: 0, Y: 4, Button: tea.MouseWheelDown,
	})))

	if m.PreviewFocused() || m.WorkspaceFocusContext() != "global-workspaces" {
		t.Fatalf("wheel over sidebar blank space moved the keyboard: focused=%v context=%q", m.PreviewFocused(), m.WorkspaceFocusContext())
	}
	if got := m.workspaces.SelectedID(); got != "d" {
		t.Fatalf("one wheel notch selected %q, want d after the shared three-row step", got)
	}
	if got := m.preview.workspaceID; got != "d" {
		t.Fatalf("preview followed workspace %q, want d", got)
	}
}

func TestGlobalWorkspaceDividerUsesTheProjectSidebarResizeGesture(t *testing.T) {
	originalSave := saveWorkspaceSidebarWidth
	var saved int
	saveWorkspaceSidebarWidth = func(width int) error {
		saved = width
		return nil
	}
	t.Cleanup(func() { saveWorkspaceSidebarWidth = originalSave })

	m, _ := previewModel(t)
	m.sidebarWidth = defaultWorkspaceSidebarPercent
	m.WorkspacesView(previewWide, previewTall)
	dividerX := m.previewSplit(previewWide).SidebarWidth

	m.WorkspacesMouse(tea.MouseClickMsg{X: dividerX, Y: 5, Button: tea.MouseLeft})
	m.WorkspacesMouse(tea.MouseMotionMsg{X: dividerX + 12, Y: 5, Button: tea.MouseLeft})
	if got := m.sidebarWidth; got != 50 {
		t.Fatalf("dragged sidebar width = %d%%, want 50%%", got)
	}
	if got := m.previewSplit(previewWide).SidebarWidth; got <= dividerX {
		t.Fatalf("drag did not move divider: x=%d, started at %d", got, dividerX)
	}
	m.WorkspacesMouse(tea.MouseReleaseMsg{X: dividerX + 12, Y: 5, Button: tea.MouseLeft})
	if saved != 50 {
		t.Fatalf("drag release saved %d%%, want 50%%", saved)
	}
}

func TestNarrowTabShowsOneFullWidthPaneAtATime(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))

	const narrow, tall = 60, 20
	m.WorkspacesView(narrow, tall)
	if !m.previewNarrow() {
		t.Fatal("60 columns was not treated as narrow")
	}
	list := ansi.Strip(m.WorkspacesView(narrow, tall))
	if !strings.Contains(list, "alpha") || strings.Contains(list, "enter to type") {
		t.Fatalf("narrow layout is not a full-width list:\n%s", list)
	}
	for _, line := range strings.Split(list, "\n") {
		if width := ansi.StringWidth(line); width > narrow {
			t.Fatalf("narrow row is %d columns wide: %q", width, line)
		}
	}

	// Hiding the sidebar fills the tab with the preview. The keyboard stays
	// on the list: esc is not consumed here (it leaves global at the app).
	handled, cmd := m.WorkspacesKey(key("\\"))
	if !handled {
		t.Fatal("backslash was not handled in the narrow layout")
	}
	run(t, m, cmd)
	if m.PreviewFocused() || m.WorkspaceFocusContext() != "global-workspaces" {
		t.Fatalf("hiding the sidebar stole the keyboard: focused=%v context=%q", m.PreviewFocused(), m.WorkspaceFocusContext())
	}
	preview := ansi.Strip(m.WorkspacesView(narrow, tall))
	if layout := m.workspacesLayout(); !layout.previewOnly {
		t.Fatalf("hiding the sidebar did not open a full-width preview: %#v\n%s", layout, preview)
	}

	handled, _ = m.WorkspacesKey(key("esc"))
	if handled {
		t.Fatal("esc after hiding the sidebar was consumed as return-to-list")
	}
	press(t, m, "\\")
	if !m.WorkspaceSidebarVisible() || m.PreviewFocused() {
		t.Fatal("backslash did not restore the narrow layout to its list")
	}
	if back := ansi.Strip(m.WorkspacesView(narrow, tall)); !strings.Contains(back, "delta") {
		t.Fatalf("narrow layout did not return to the list:\n%s", back)
	}
}

func TestGlobalBackslashHidesAndRestoresSidebarWithoutTakingTheKeyboard(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.WorkspacesView(previewWide, previewTall)
	selected := m.workspaces.SelectedID()

	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: '\\', Text: "\\"})
	if !handled || cmd == nil || m.WorkspaceSidebarVisible() {
		t.Fatalf("hide handled=%v cmd=%v visible=%v", handled, cmd != nil, m.WorkspaceSidebarVisible())
	}
	if m.PreviewFocused() || m.WorkspaceFocusContext() != "global-workspaces" {
		t.Fatalf("hiding the sidebar entered watched-preview: focused=%v context=%q", m.PreviewFocused(), m.WorkspaceFocusContext())
	}
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if strings.Contains(view, "Activity") {
		t.Fatalf("hidden sidebar still rendered list:\n%s", view)
	}

	// j/k still browse the hidden list. They do not scroll a watched preview.
	press(t, m, "j")
	if m.workspaces.SelectedID() == selected || m.PreviewFocused() {
		t.Fatalf("j after hiding the sidebar did not browse: selected=%q focused=%v", m.workspaces.SelectedID(), m.PreviewFocused())
	}

	handled, _ = m.WorkspacesKey(tea.KeyPressMsg{Code: '\\', Text: "\\"})
	if !handled || !m.WorkspaceSidebarVisible() || m.WorkspaceFocusContext() != "global-workspaces" {
		t.Fatalf("restore handled=%v visible=%v context=%q", handled, m.WorkspaceSidebarVisible(), m.WorkspaceFocusContext())
	}
}

// After hiding the sidebar the keyboard is still the list's: esc is not a
// return-to-list chord (the app uses it to leave global), and h/left do not
// resurrect watched-preview chrome.
func TestHiddenSidebarKeepsListKeys(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.WorkspacesView(previewWide, previewTall)
	press(t, m, "\\")
	if m.WorkspaceSidebarVisible() || m.PreviewFocused() {
		t.Fatalf("test premise: visible=%v previewFocused=%v", m.WorkspaceSidebarVisible(), m.PreviewFocused())
	}

	for _, back := range []string{"esc", "h", "left"} {
		handled, _ := m.WorkspacesKey(key(back))
		if handled {
			t.Fatalf("%q was consumed after hiding the sidebar", back)
		}
		if m.PreviewFocused() || m.WorkspaceFocusContext() != "global-workspaces" {
			t.Fatalf("%q moved the keyboard: focused=%v context=%q", back, m.PreviewFocused(), m.WorkspaceFocusContext())
		}
		if m.WorkspaceSidebarVisible() {
			t.Fatalf("%q restored the sidebar", back)
		}
	}
}

// The same disagreement reached through the window size: a preview focused at a
// width that cannot hold two panes takes the whole tab, whether or not anyone is
// typing into it.
func TestShrinkingTheWindowKeepsAnInteractivePreviewOnScreen(t *testing.T) {
	m, _, _ := interactiveModel(t)
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)

	narrow := globalListMinWidth + globalDividerWidth + globalPreviewMinWidth - 1
	run(t, m, m.WorkspacesResize(narrow, previewTall))

	if layout := m.workspacesLayout(); !layout.previewOnly || !layout.previewDrawn {
		t.Fatalf("narrow interactive layout = %#v, want the preview filling the tab", layout)
	}
	if !m.PreviewInteractive() {
		t.Fatal("shrinking the window ended typing")
	}
}

func TestGlobalFilterKeepsBackslashLiteral(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	press(t, m, "/")
	press(t, m, "\\")
	if got := m.workspaces.Filter().Query(); got != "\\" {
		t.Fatalf("filter query = %q, want literal backslash", got)
	}
	if !m.WorkspaceSidebarVisible() {
		t.Fatal("literal filter input toggled the sidebar")
	}
}

func TestAdaptivePollingCapturesOnlyTheSelectedPaneWhileVisible(t *testing.T) {
	m, recorder := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))

	if !m.preview.scheduled {
		t.Fatal("an applied capture did not arm the next refresh")
	}
	if got := m.previewInterval(); got != previewVisiblePoll {
		t.Fatalf("visible cadence = %s, want %s", got, previewVisiblePoll)
	}

	// A tick for the current generation re-captures the same pane, once.
	run(t, m, m.pollPreview(previewPollMsg{Generation: m.preview.generation, WorkspaceID: "a"}))
	if got := recorder.panes(); len(got) != 2 || got[1] != "%1" {
		t.Fatalf("poll captured %v, want a second read of %%1", got)
	}
	if m.PreviewMetrics().Polls != 1 {
		t.Fatalf("poll metric = %d, want 1", m.PreviewMetrics().Polls)
	}

	// A tick left over from a superseded generation does nothing.
	stale := m.preview.generation - 1
	if cmd := m.pollPreview(previewPollMsg{Generation: stale, WorkspaceID: "a"}); cmd != nil {
		t.Fatal("a superseded poll tick started a capture")
	}
	if got := recorder.panes(); len(got) != 2 {
		t.Fatalf("stale poll captured: %v", got)
	}

	// Selection latency is measured, so the cadence can be tuned against
	// something rather than guessed at.
	if m.PreviewMetrics().LastLatency < 0 {
		t.Fatalf("latency metric = %s", m.PreviewMetrics().LastLatency)
	}
}

func TestCapturedOutputIsNeverPersisted(t *testing.T) {
	m, recorder := previewModel(t)
	recorder.output["%1"] = "SUPER SECRET PANE CONTENTS"
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.json")
	original := ActivityStorePath
	ActivityStorePath = func() string { return path }
	t.Cleanup(func() { ActivityStorePath = original })

	run(t, m, m.SetWorkspacesVisible(true))
	if !strings.Contains(ansi.Strip(m.WorkspacesView(previewWide, previewTall)), "SUPER SECRET") {
		t.Fatal("fixture capture never reached the preview")
	}
	m.persistActivity()

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read activity store: %v", err)
	}
	if strings.Contains(string(data), "SUPER SECRET") {
		t.Fatal("captured terminal contents were written to the activity store")
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		body, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
		if strings.Contains(string(body), "SUPER SECRET") {
			t.Fatalf("captured terminal contents were written to %s", entry.Name())
		}
	}
}

// Slice 3 item 4: measure before tuning. These are the numbers the cadence was
// chosen against — what one selection costs, what an idle tab costs, and where
// the capture actually runs.
func TestPreviewWorkIsMeasuredAndBounded(t *testing.T) {
	m, recorder := previewModel(t)

	// The capture runs inside the command, not inside Update: the event loop is
	// never blocked on tmux, however slow the pane is to answer.
	m.preview.visible = true
	cmd := m.previewSelect()
	if cmd == nil {
		t.Fatal("selecting a live item produced no capture command")
	}
	if got := recorder.panes(); len(got) != 0 {
		t.Fatalf("the update loop captured %v itself", got)
	}
	run(t, m, cmd)
	if got := recorder.panes(); len(got) != 1 {
		t.Fatalf("running the command captured %v, want one read", got)
	}

	// One selection costs exactly one capture, and the poll it arms is the
	// unfocused cadence.
	if m.PreviewMetrics().Captures != 1 || m.PreviewMetrics().Polls != 0 {
		t.Fatalf("selection cost = %+v, want one capture and no polls", m.PreviewMetrics())
	}
	if previewFocusedPoll >= previewVisiblePoll {
		t.Fatalf("focused cadence %s is not faster than the visible cadence %s", previewFocusedPoll, previewVisiblePoll)
	}

	// Ten frames of an idle visible tab cost nothing: only ticks capture.
	before := len(recorder.panes())
	for range 10 {
		m.WorkspacesView(previewWide, previewTall)
	}
	if got := len(recorder.panes()); got != before {
		t.Fatalf("idle rendering ran %d extra captures", got-before)
	}

	// Ten ticks of a hidden tab cost nothing either.
	generation := m.preview.generation
	run(t, m, m.SetWorkspacesVisible(false))
	for range 10 {
		if cmd := m.pollPreview(previewPollMsg{Generation: generation, WorkspaceID: "a"}); cmd != nil {
			t.Fatal("a hidden tab answered a poll tick with a capture")
		}
	}
	if got := len(recorder.panes()); got != before {
		t.Fatalf("hidden tab ran %d captures", got-before)
	}
	if m.PreviewMetrics().LastLatency > time.Second {
		t.Fatalf("selection latency = %s, far past the frame budget", m.PreviewMetrics().LastLatency)
	}
}

// A watched full-screen application (Grok, Claude, …) keeps intentional blank
// rows at the bottom of its grid. Without the geometry tmux reported beside the
// capture, the window took those rows for padding, trimmed them, and started
// that many rows higher — painting the pane's previous bottom chrome under the
// header while the live grid's own bottom fell off the end (td-c3644b).
func TestAWatchedFullScreenPaneIsDrawnFromItsLiveGridNotItsHistory(t *testing.T) {
	m, recorder := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.WorkspacesView(previewWide, previewTall)
	paneHeight := m.previewRows()
	if paneHeight < 8 {
		t.Fatalf("preview body height = %d, too short to stage a full-screen pane", paneHeight)
	}

	history := make([]string, 30)
	for i := range history {
		history[i] = fmt.Sprintf("history %d", i)
	}
	grid := make([]string, paneHeight)
	grid[0] = "GROK TOP"
	grid[paneHeight-4] = "INPUT BOX"
	// tmux terminates its capture, so the pane's last row is a real blank row
	// rather than the tail of the row above it.
	recorder.output["%1"] = strings.Join(append(history, grid...), "\n") + "\n"
	recorder.state["%1"] = tty.PaneState{PaneWidth: m.previewSplit(previewWide).ContentWidth - 1, PaneHeight: paneHeight}

	// Recapture the same pane with its geometry attached.
	run(t, m, m.pollPreview(previewPollMsg{Generation: m.preview.generation, WorkspaceID: "a"}))

	window := m.previewWindow()
	if !window.ok {
		t.Fatal("the rendered preview has no window")
	}
	if window.input.PaneHeight != paneHeight {
		t.Fatalf("the watched window was told pane height %d, want the captured %d", window.input.PaneHeight, paneHeight)
	}
	if window.layout.PaneTop != len(history) {
		t.Fatalf("pane row 0 landed at buffer line %d, want %d — the capture's split was not published", window.layout.PaneTop, len(history))
	}
	if window.layout.Start != window.layout.PaneTop {
		t.Fatalf("the live window starts at %d, want the grid's own top %d", window.layout.Start, window.layout.PaneTop)
	}

	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "GROK TOP") || !strings.Contains(view, "INPUT BOX") {
		t.Fatalf("the watched preview did not draw the pane's live grid:\n%s", view)
	}
	if strings.Contains(view, "history 29") {
		t.Fatalf("history bled into the top of the watched preview:\n%s", view)
	}
}
