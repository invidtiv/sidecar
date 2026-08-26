package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// The pointer over the global browser's terminal box behaves as it does over the
// project plugin's, because both drive the same gesture machine in internal/tty.
// What is proved here is the wiring: which gesture reaches it, what each one
// resolves to, and that none of it needs a tmux server.

// The pointer helpers press, drag and release at tab-local coordinates.
func pointerDown(t *testing.T, m *Model, x, y int) {
	t.Helper()
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}))
}

func dragTo(t *testing.T, m *Model, x, y int) {
	t.Helper()
	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft}))
}

func release(t *testing.T, m *Model, x, y int) {
	t.Helper()
	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}))
}

func click(t *testing.T, m *Model, x, y int) {
	t.Helper()
	pointerDown(t, m, x, y)
	release(t, m, x, y)
}

// previewAt renders the tab and reports where its terminal content begins.
func previewAt(t *testing.T, m *Model) (int, int) {
	t.Helper()
	m.WorkspacesView(previewWide, previewTall)
	surface, ok := m.previewSurface()
	if !ok {
		t.Fatal("the rendered preview has no terminal surface")
	}
	return surface.X, surface.Y
}

// A click is the primary way in: no "i" required, and the release decides —
// nothing is committed while the button is still down. One click does it from
// anywhere, including from the list, exactly as the project surface answers one.
func TestClickingThePreviewStartsTyping(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	x, y := previewAt(t, m)

	click(t, m, x+3, y+1)
	if !m.PreviewFocused() {
		t.Fatal("a click on the preview did not move focus to it")
	}
	if !m.PreviewInteractive() {
		t.Fatal("one click from the list did not hand the pane its keyboard")
	}
	if terminal.opens != 1 {
		t.Fatalf("the click opened %d terminals, want exactly one", terminal.opens)
	}
}

// Motion turns the same press into a selection, so a user dragging across output
// is never dropped into a live pane mid-gesture.
func TestDraggingOverThePreviewSelectsInsteadOfActivating(t *testing.T) {
	m, _, _ := interactiveModel(t)
	x, y := previewAt(t, m)

	pointerDown(t, m, x, y)
	dragTo(t, m, x+6, y)
	release(t, m, x+6, y)

	if m.PreviewInteractive() {
		t.Fatal("a drag activated the pane, so the selection it made is unreachable")
	}
	if m.PreviewFocused() {
		t.Fatal("a drag-select from the list left the keyboard on a watched preview")
	}
	if !m.previewTerminalLeaf().Selection.HasSelection() {
		t.Fatal("dragging across the preview selected nothing")
	}
	if got := strings.Join(m.previewSelectionLines(), "\n"); !strings.HasPrefix(got, "live pa") {
		t.Fatalf("selected %q, want the run of text under the drag", got)
	}
}

// A double click takes the word under the pointer and a triple click the line,
// as every terminal does.
func TestDoubleAndTripleClickTakeTheWordAndTheLine(t *testing.T) {
	m, _, _ := interactiveModel(t)
	x, y := previewAt(t, m)

	pointerDown(t, m, x+11, y)
	pointerDown(t, m, x+11, y)
	if got := strings.Join(m.previewSelectionLines(), "\n"); got != "body" {
		t.Fatalf("double click selected %q, want the word under the pointer", got)
	}
	if m.PreviewInteractive() {
		t.Fatal("a double click also activated the pane under the selection")
	}

	pointerDown(t, m, x+11, y)
	if got := strings.Join(m.previewSelectionLines(), "\n"); got != "live pane body" {
		t.Fatalf("triple click selected %q, want the whole line", got)
	}
}

// A release the app never saw commits nothing: neither the activation the press
// armed nor a click on its way to the application.
func TestALostReleaseCancelsTheClick(t *testing.T) {
	m, _, _ := interactiveModel(t)
	x, y := previewAt(t, m)

	pointerDown(t, m, x+2, y)
	// Button-less motion is how the shared handler notices a release it never
	// received.
	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: x + 2, Y: y}))
	release(t, m, x+2, y)

	if m.PreviewInteractive() {
		t.Fatal("a gesture whose release was lost still handed the pane its keyboard")
	}
}

// Clicking away gives the keyboard back, the same as moving focus with the
// keyboard does.
func TestClickingAwayFromALivePaneGivesTheKeyboardBack(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	previewAt(t, m)

	// Chrome, not a row: a row click while typing is a tab switch.
	m.WorkspacesView(previewWide, previewTall)
	dividerX := m.previewSplit(previewWide).SidebarWidth
	click(t, m, dividerX, 5)
	if m.PreviewInteractive() || !terminal.IsActive() {
		t.Fatal("clicking away did not release the keyboard while keeping the watched producer")
	}
	if m.PreviewFocused() {
		t.Fatal("clicking away left focus on a watched preview")
	}
}

// A click on a pane whose application asked for mouse events is that
// application's, at the cell the user aimed at — and is reported from where the
// button went down, because a click that resolves here never moved.
func TestClickingALiveMouseAwarePaneReachesTheApplication(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	terminal.reporting = true
	x, y := previewAt(t, m)

	click(t, m, x+4, y+1)
	if len(terminal.clicks) != 1 || terminal.clicks[0] != [2]int{5, 2} {
		t.Fatalf("the pane received clicks %v, want one at its own 5,2", terminal.clicks)
	}
	if !m.PreviewInteractive() {
		t.Fatal("forwarding a click also ended the mode")
	}

	// A pane that has not asked for mouse events is not sent clicks it cannot
	// interpret, and the click does not silently exit either.
	terminal.reporting = false
	click(t, m, x+6, y+2)
	if len(terminal.clicks) != 1 {
		t.Fatalf("a click reached a pane that never asked for mouse events: %v", terminal.clicks)
	}
	if !m.PreviewInteractive() {
		t.Fatal("a click inside a live pane ended the mode")
	}
}

// The chords that act on the terminal are the configured ones, and they are
// answered in both of the preview's states — a selection made while watching is
// still there, and still copyable, once the user starts typing.
func TestTerminalChordsFollowTheConfiguration(t *testing.T) {
	m, _, _ := interactiveModel(t)
	m.SetTerminalConfig(tty.Config{CopyKey: "alt+y", PasteKey: "alt+p"})
	enterInteractive(t, m)
	previewAt(t, m)

	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if !handled {
		t.Fatal("interactive mode dropped the unconfigured copy chord")
	}
	if cmd != nil {
		t.Fatal("the unconfigured default copy chord copied")
	}

	handled, _ = m.WorkspacesKey(ctrlKey('a'))
	if !handled {
		t.Fatal("ctrl+a did not select the output")
	}
	if got := strings.Join(m.previewSelectionLines(), "\n"); got != "live pane body" {
		t.Fatalf("ctrl+a selected %q, want the live pane buffer", got)
	}

	handled, cmd = m.WorkspacesKey(tea.KeyPressMsg{Code: 'y', Mod: tea.ModAlt})
	if !handled || cmd == nil {
		t.Fatal("the configured copy chord did not copy the selection")
	}

	// The platform chord copies alongside it, because the terminal owns the
	// selection and the emulator's own copy has nothing to act on.
	handled, cmd = m.WorkspacesKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModSuper})
	if !handled || cmd == nil {
		t.Fatalf("%s did not copy the selection", tty.SuperCopyKey)
	}
}

// One resolution of the user's terminal settings reaches this surface, so the
// keys it answers cannot drift from the project plugin's.
func TestTheBrowserAnswersTheResolvedConfiguration(t *testing.T) {
	m := &Model{}
	m.SetTerminalConfig(tty.Config{ExitKey: "ctrl+q", CopyKey: "alt+y", PasteKey: "alt+p", AttachKey: "ctrl+]"})

	config := m.TerminalConfig()
	if config.ExitKey != "ctrl+q" || config.CopyKey != "alt+y" || config.PasteKey != "alt+p" {
		t.Fatalf("resolved config = %+v, want the configured chords", config)
	}
	// The one field this surface overrides, and the reason it does: there is no
	// attach path here, so the chord belongs to the pane like any other key.
	if config.AttachKey != "" {
		t.Fatalf("the browser answers an attach chord %q it has no attach path for", config.AttachKey)
	}

	// An unconfigured surface still answers the shared defaults rather than
	// nothing at all.
	defaults := (&Model{}).TerminalConfig()
	if defaults.ExitKey != tty.DefaultConfig().ExitKey ||
		defaults.CopyKey != tty.DefaultConfig().CopyKey ||
		defaults.PasteKey != tty.DefaultConfig().PasteKey {
		t.Fatalf("unconfigured surface = %+v, want the shared defaults", defaults)
	}
}

// rowAt reports where the rendered row for an item begins.
func rowAt(t *testing.T, m *Model, id string) (int, int) {
	t.Helper()
	m.WorkspacesView(previewWide, previewTall)
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		row, ok := region.Data.(workspacelist.Region)
		if ok && row.Kind == workspacelist.RegionRow && row.ID == id {
			return region.Rect.X, region.Rect.Y
		}
	}
	t.Fatalf("no rendered row for %q", id)
	return 0, 0
}

// Clicking a different row while typing captures the row that was clicked. The
// keyboard is handed back as part of the same event, and that hand-back rebinds
// the terminal producer — so it has to see the new selection, not the old one.
func TestClickingAnotherRowWhileTypingRebindsThatRow(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	previewAt(t, m)

	x, y := rowAt(t, m, "b")
	click(t, m, x, y)

	if m.workspaces.SelectedID() != "b" {
		t.Fatalf("selection = %q, want the clicked row", m.workspaces.SelectedID())
	}
	if terminal.target != (tty.Target{Session: "sc-bravo", Pane: "%2"}) {
		t.Fatalf("producer target after click = %+v, want the clicked row", terminal.target)
	}
}

// Typing is owed a view of itself. The project surface snaps its viewport back
// to the live edge before forwarding a keystroke, and so does this one — without
// it, what the user types lands invisibly under a window parked in history.
func TestTypingSnapsAScrolledBackPreviewToTheLiveEdge(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	terminal.buffer.ApplySnapshot(tty.PaneSnapshot{Output: paneBody(60)})
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)

	m.previewTerminalLeaf().Scroll = 6
	press(t, m, "a")
	if m.previewTerminalLeaf().Scroll != 0 {
		t.Fatalf("the window stayed at %d while the user typed into the pane", m.previewTerminalLeaf().Scroll)
	}
	if len(terminal.keys) == 0 || terminal.keys[len(terminal.keys)-1] != "a" {
		t.Fatalf("the keystroke did not reach the pane: %v", terminal.keys)
	}

	// A key that is not typing leaves the window where the reader put it.
	m.previewTerminalLeaf().Scroll = 6
	press(t, m, "esc")
	if m.previewTerminalLeaf().Scroll != 6 {
		t.Fatalf("escape moved the window to %d", m.previewTerminalLeaf().Scroll)
	}
}

// A pane whose lines are not tmux's coordinates cannot say where its history
// ends, so the window stops at the top of what was captured and claims nothing.
// Reaching, and the notice at the real end of history, are
// preview_history_test.go's subject.
func TestReachingTheTopOfAnUnnumberedCaptureClaimsNothing(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	terminal.buffer.ApplySnapshot(tty.PaneSnapshot{Output: paneBody(60)})
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)

	x, y := previewAt(t, m)
	m.previewTerminalLeaf().Scroll = m.previewMaxOffset()
	settleWheel()
	if cmd := m.WorkspacesMouse(tea.MouseWheelMsg{X: x + 2, Y: y + 2, Button: tea.MouseWheelUp}); cmd != nil {
		t.Fatalf("a buffer with no absolute coordinates answered %#v at its top", cmd())
	}
	if m.previewTerminalLeaf().Scroll != m.previewMaxOffset() {
		t.Fatalf("the window moved past the top of the capture to %d", m.previewTerminalLeaf().Scroll)
	}
}

// Clicking away from the terminal is not a release of the gesture it armed: the
// sidebar, a row and the divider all abandon it identically.
func TestClickingAwayAbandonsAnArmedTerminalGesture(t *testing.T) {
	for _, away := range []struct {
		name string
		x, y int
	}{
		{"the sidebar", 2, 6},
		{"the divider", 0, 4},
	} {
		t.Run(away.name, func(t *testing.T) {
			m, _, _ := interactiveModel(t)
			x, y := previewAt(t, m)
			if away.name == "the divider" {
				away.x = m.previewSplit(previewWide).SidebarWidth
			}

			pointerDown(t, m, x+3, y+1)
			if m.previewTerminalLeaf().Pointer.Resolution != tty.ClickActivate {
				t.Fatalf("the press armed %v, want activation", m.previewTerminalLeaf().Pointer.Resolution)
			}
			run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: away.x, Y: away.y, Button: tea.MouseLeft}))
			if m.previewTerminalLeaf().Pointer.Resolution != tty.ClickNone {
				t.Fatalf("clicking %s left the terminal's click armed", away.name)
			}
		})
	}
}
