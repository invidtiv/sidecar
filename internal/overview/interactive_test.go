package overview

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/tty"
)

// The global Workspaces preview drives a live pane as well as reading one. What
// is proved here is the interaction path: what hands the keyboard over, what
// refuses to, where the keys go while it is held, what gives it back, and that
// none of it opens a second route to tmux — every send goes through the same
// embedded terminal component the project plugin drives, which is the seam the
// fake below stands in for.

type fakeTerminal struct {
	config     tty.Config
	target     tty.Target
	active     bool
	opens      int
	keys       []string
	pastes     []string
	clicks     [][2]int
	wheel      []fakeWheel
	sequences  []string
	dims       [][2]int
	buffer     *tty.OutputBuffer
	reporting  bool
	paneW      int
	paneH      int
	cursorRow  int
	cursorCol  int
	mouseNoted int
}

type fakeWheel struct {
	up       bool
	col, row int
	notches  int
}

// paneBody is a buffer with enough rows to scroll through.
func paneBody(rows int) string {
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d alpha bravo", i)
	}
	return strings.Join(lines, "\n")
}

func newFakeTerminal(body string) *fakeTerminal {
	f := &fakeTerminal{buffer: tty.NewOutputBuffer(200), paneW: 40, paneH: 10}
	f.buffer.ApplySnapshot(tty.PaneSnapshot{Output: body})
	return f
}

func (f *fakeTerminal) Open(target tty.Target) tea.Cmd {
	f.target = target
	f.active = true
	f.opens++
	return nil
}

func (f *fakeTerminal) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		f.keys = append(f.keys, key)
		// The real component owns both ways out; the fake answers the exit key so
		// the browser's own "the terminal ended the mode" path is exercised.
		if key == f.config.ExitKey {
			f.active = false
		}
	case tea.PasteMsg:
		f.pastes = append(f.pastes, msg.Content)
	case tty.CaptureResultMsg:
		f.buffer.ApplySnapshot(tty.PaneSnapshot{Output: msg.Output})
	case tty.SessionDeadMsg:
		// The component ends itself on a pane that died under a send, which is
		// how the browser learns the mode is over.
		f.active = false
	}
	return nil
}

func (f *fakeTerminal) Buffer() *tty.OutputBuffer { return f.buffer }

func (f *fakeTerminal) PaneSize() (int, int) { return f.paneW, f.paneH }

func (f *fakeTerminal) CursorState() (int, int, bool) { return f.cursorRow, f.cursorCol, true }

func (f *fakeTerminal) PaneMouseReporting() bool { return f.reporting }

func (f *fakeTerminal) SendClick(col, row int) tea.Cmd {
	f.clicks = append(f.clicks, [2]int{col, row})
	return nil
}

func (f *fakeTerminal) SendWheelNotches(up bool, col, row, notches int) tea.Cmd {
	f.wheel = append(f.wheel, fakeWheel{up: up, col: col, row: row, notches: notches})
	return nil
}

func (f *fakeTerminal) NoteMouseActivity() { f.mouseNoted++ }

func (f *fakeTerminal) SetDimensions(width, height int) tea.Cmd {
	f.dims = append(f.dims, [2]int{width, height})
	return nil
}

func (f *fakeTerminal) SendUnknownSequence(msg tea.Msg) tea.Cmd {
	f.sequences = append(f.sequences, string(tty.ExtractUnknownCSIBytes(msg)))
	return nil
}

func (f *fakeTerminal) IsActive() bool { return f.active }

func (f *fakeTerminal) Exit() { f.active = false }

// interactiveModel is the preview fixture with the terminal seam replaced, so
// entering interactive mode never reaches a tmux server.
func interactiveModel(t *testing.T) (*Model, *captureRecorder, *fakeTerminal) {
	t.Helper()
	m, recorder := previewModel(t)
	terminal := newFakeTerminal("live pane body")
	original := newPreviewTerminal
	newPreviewTerminal = func(config tty.Config) previewTerminal {
		terminal.config = config
		return terminal
	}
	t.Cleanup(func() { newPreviewTerminal = original })
	run(t, m, m.SetWorkspacesVisible(true))
	return m, recorder, terminal
}

// enterInteractive focuses the preview and asks for the pane's keyboard.
func enterInteractive(t *testing.T, m *Model) {
	t.Helper()
	press(t, m, "right")
	press(t, m, interactiveEnterKey)
}

func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

func TestPreviewHandsTheKeyboardToTheSelectedLivePane(t *testing.T) {
	m, recorder, terminal := interactiveModel(t)
	captures := len(recorder.panes())

	enterInteractive(t, m)

	if !m.PreviewInteractive() {
		t.Fatal("i did not hand the keyboard to the selected pane")
	}
	if terminal.target != (tty.Target{Session: "sc-alpha", Pane: "%1"}) {
		t.Fatalf("the terminal opened %+v, want the selected row's own session and pane", terminal.target)
	}
	if len(recorder.panes()) != captures {
		t.Fatalf("entering captured a pane as well: %v", recorder.panes())
	}
	if interval := m.previewInterval(); interval != 0 {
		t.Fatalf("the capture cadence is still armed at %s, so one pane has two readers", interval)
	}
	width, height, ok := m.previewTerminalSize()
	if !ok {
		t.Fatal("the rendered preview has no terminal surface")
	}
	if len(terminal.dims) == 0 || terminal.dims[len(terminal.dims)-1] != [2]int{width, height} {
		t.Fatalf("the pane was sized %v, want the columns it is drawn in (%dx%d)", terminal.dims, width, height)
	}
	if m.WorkspaceFocusContext() != "global-workspaces-terminal" {
		t.Fatalf("focus context is %q, so the footer and help describe the wrong surface", m.WorkspaceFocusContext())
	}
}

// The primary keyboard entry point is the list, as it is on the project
// surface: "i" with the list focused reaches the pane without a focus move
// first, so the two surfaces answer the same key from the same place.
func TestTheListHandsTheKeyboardToThePaneInOnePress(t *testing.T) {
	for _, key := range []string{interactiveEnterKey, interactiveEnterKeyAlt} {
		t.Run(key, func(t *testing.T) {
			m, _, terminal := interactiveModel(t)
			if m.PreviewFocused() {
				t.Fatal("test premise: the list does not have focus")
			}

			press(t, m, key)

			if !m.PreviewInteractive() {
				t.Fatalf("%q from the list did not hand the keyboard to the pane", key)
			}
			if terminal.target != (tty.Target{Session: "sc-alpha", Pane: "%1"}) {
				t.Fatalf("the terminal opened %+v, want the selected row's own session and pane", terminal.target)
			}
		})
	}
}

// Nothing here creates a session. An item with no live pane behind it is
// refused with the same reason the preview already shows for it.
func TestPreviewRefusesToTypeIntoAnItemWithNoLivePane(t *testing.T) {
	for _, tc := range []struct{ id, reason string }{
		{"d", "No live session for this workspace"},
		{"e", "The session for this workspace has ended"},
		{"c", "Several panes match this workspace"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			m, _, terminal := interactiveModel(t)
			m.workspaces.SelectID(tc.id)
			run(t, m, m.previewSync())
			enterInteractive(t, m)

			if m.PreviewInteractive() {
				t.Fatalf("%q handed the keyboard to an item with no live pane", tc.id)
			}
			if terminal.opens != 0 {
				t.Fatalf("%q opened a terminal anyway", tc.id)
			}
			if !strings.Contains(m.preview.reason, tc.reason) {
				t.Fatalf("%q was refused with %q, want %q", tc.id, m.preview.reason, tc.reason)
			}
		})
	}
}

// While the pane is live it owns the keyboard outright: keys that mean
// something to the browser are the pane's instead, and ctrl+c interrupts what
// is running there — the same rule the project plugin's interactive mode
// follows, so the two surfaces cannot disagree about what SIGINT means.
func TestLivePaneTakesEveryKeyIncludingCtrlC(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	selected := m.workspaces.SelectedID()

	for _, k := range []string{"/", "q", "j", "s", "\\"} {
		handled, cmd := m.WorkspacesKey(key(k))
		if !handled {
			t.Fatalf("%q was not forwarded to the live pane", k)
		}
		run(t, m, cmd)
	}
	if got := strings.Join(terminal.keys, ""); got != "/qjs\\" {
		t.Fatalf("the pane received %q, want every key", got)
	}
	if m.WorkspacesFilterFocused() || m.workspaces.SelectedID() != selected || !m.WorkspaceSidebarVisible() {
		t.Fatal("a key meant for the pane also moved the browser")
	}

	handled, cmd := m.WorkspacesKey(ctrlKey('c'))
	if !handled {
		t.Fatal("ctrl+c was handed back to the host, so the pane cannot be interrupted")
	}
	run(t, m, cmd)
	if last := terminal.keys[len(terminal.keys)-1]; last != "ctrl+c" {
		t.Fatalf("the pane's last key was %q, want ctrl+c", last)
	}
	if !m.PreviewInteractive() {
		t.Fatal("ctrl+c ended interactive mode instead of reaching the pane")
	}
}

// The exit key is the user's, not this file's: whatever the project plugin's
// interactive mode answers, the browser's live pane answers too.
func TestTheConfiguredExitKeyIsTheOneTheSurfaceAnswers(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	m.SetTerminalConfig(tty.Config{ExitKey: "ctrl+q"})
	enterInteractive(t, m)

	if terminal.config.ExitKey != "ctrl+q" {
		t.Fatalf("the terminal was built with exit key %q, want the configured one", terminal.config.ExitKey)
	}
	if !strings.Contains(m.interactiveHints(), "ctrl+q") {
		t.Fatalf("the header hint says %q, want the configured exit key", m.interactiveHints())
	}
	// The unconfigured default chord is the pane's input like any other key.
	handled, cmd := m.WorkspacesKey(ctrlKey('\\'))
	if !handled {
		t.Fatal("a key was not forwarded to the live pane")
	}
	run(t, m, cmd)
	if !m.PreviewInteractive() {
		t.Fatal("the unconfigured default chord still ended the mode")
	}

	handled, cmd = m.WorkspacesKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("the configured exit key was not answered while the pane was live")
	}
	run(t, m, cmd)
	if m.PreviewInteractive() {
		t.Fatal("the configured exit key left the keyboard with the pane")
	}
}

// The terminal component owns the ways out. The browser learns the mode ended
// from the component's own state and goes back to capturing the selection.
func TestTheExitKeyGivesTheKeyboardBackAndResumesCapture(t *testing.T) {
	m, recorder, _ := interactiveModel(t)
	enterInteractive(t, m)
	captures := len(recorder.panes())

	handled, cmd := m.WorkspacesKey(ctrlKey('\\'))
	if !handled {
		t.Fatal("the exit key was not answered while the pane was live")
	}
	run(t, m, cmd)

	if m.PreviewInteractive() {
		t.Fatal("the exit key left the keyboard with the pane")
	}
	if !m.PreviewFocused() {
		t.Fatal("leaving the pane also left the preview")
	}
	if len(recorder.panes()) <= captures {
		t.Fatalf("leaving did not resume the capture cadence: %v", recorder.panes())
	}
	if interval := m.previewInterval(); interval != previewFocusedPoll {
		t.Fatalf("cadence after leaving is %s, want the focused cadence", interval)
	}
}

// Focus, selection, and visibility all end the mode: the keyboard cannot be in
// two places, and a pane nobody is looking at keeps neither a capture nor a
// live subscription.
func TestLeavingTheSelectionOrTheTabEndsInteractiveMode(t *testing.T) {
	t.Run("focus", func(t *testing.T) {
		m, _, _ := interactiveModel(t)
		enterInteractive(t, m)
		run(t, m, m.focusList())
		if m.PreviewInteractive() {
			t.Fatal("moving focus to the list kept the pane's keyboard")
		}
	})
	t.Run("selection", func(t *testing.T) {
		m, _, terminal := interactiveModel(t)
		enterInteractive(t, m)
		m.workspaces.SelectID("b")
		run(t, m, m.previewSync())
		if m.PreviewInteractive() || terminal.IsActive() {
			t.Fatal("selecting another row kept the previous pane's keyboard")
		}
	})
	t.Run("visibility", func(t *testing.T) {
		m, _, terminal := interactiveModel(t)
		enterInteractive(t, m)
		run(t, m, m.SetWorkspacesVisible(false))
		if m.PreviewInteractive() || terminal.IsActive() {
			t.Fatal("hiding the tab kept the pane's keyboard")
		}
	})
}

// The live body is drawn in the same box a capture is, and the cursor is placed
// against that box — the pane's own cursor, in the window this surface drew.
func TestInteractivePreviewDrawsTheLiveBodyAndPlacesTheCursor(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	terminal.cursorCol, terminal.cursorRow = 3, 2

	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "live pane body") {
		t.Fatalf("the live pane's output is not on screen:\n%s", view)
	}
	if strings.Contains(view, "i to type") {
		t.Fatalf("a pane already being typed into still advertises the way in:\n%s", view)
	}
	if !strings.Contains(view, m.InteractiveExitKey()) {
		t.Fatalf("the header does not say how to stop typing:\n%s", view)
	}

	surface, ok := m.previewSurface()
	if !ok {
		t.Fatal("the rendered preview has no terminal surface")
	}
	cursor := m.WorkspacesCursor()
	if cursor == nil {
		t.Fatal("a live pane draws no cursor, so there is nothing to type at")
	}
	if cursor.X != surface.X+3 || cursor.Y != surface.Y+2 {
		t.Fatalf("cursor at %d,%d, want the pane's own position inside the box at %d,%d",
			cursor.X, cursor.Y, surface.X+3, surface.Y+2)
	}

	run(t, m, m.focusList())
	if m.WorkspacesCursor() != nil {
		t.Fatal("a watched preview still draws a cursor")
	}
}

// Terminal messages are scope-tagged, so the browser is offered every one and
// keeps only what belongs to a live pane of its own.
func TestOnlyTerminalMessagesReachTheBrowsersTerminal(t *testing.T) {
	m, _, terminal := interactiveModel(t)

	if cmd := m.WorkspacesTerminalMsg(tty.CaptureResultMsg{Output: "ignored"}); cmd != nil {
		t.Fatal("a terminal message was accepted with no live pane")
	}
	if terminal.buffer.String() != "live pane body" {
		t.Fatal("a terminal message reached an inactive terminal")
	}

	enterInteractive(t, m)
	run(t, m, m.WorkspacesTerminalMsg(tty.CaptureResultMsg{Output: "fresh frame"}))
	if got := terminal.buffer.String(); got != "fresh frame" {
		t.Fatalf("the live terminal did not receive its own message: %q", got)
	}
	run(t, m, m.WorkspacesTerminalMsg(previewPollMsg{}))
	if got := terminal.buffer.String(); got != "fresh frame" {
		t.Fatal("a message that is not the terminal's was forwarded to it")
	}
}

func TestPasteReachesTheLivePane(t *testing.T) {
	m, _, terminal := interactiveModel(t)

	if handled, _ := m.WorkspacesTerminalPaste("text"); handled {
		t.Fatal("a paste was taken with no live pane")
	}
	enterInteractive(t, m)
	handled, cmd := m.WorkspacesTerminalPaste("echo hi")
	if !handled {
		t.Fatal("the live pane refused a paste")
	}
	run(t, m, cmd)
	if len(terminal.pastes) != 1 || terminal.pastes[0] != "echo hi" {
		t.Fatalf("the pane received pastes %v", terminal.pastes)
	}

	// A modified key never becomes a KeyPressMsg. It is still the pane's input.
	shiftEnter := uv.UnknownCsiEvent("\x1b[13;2u")
	handled, cmd = m.WorkspacesTerminalKeySequence(shiftEnter)
	if !handled {
		t.Fatal("a modified key was dropped on its way to the live pane")
	}
	run(t, m, cmd)
	if len(terminal.sequences) != 1 || terminal.sequences[0] != "\x1b[13;2u" {
		t.Fatalf("the pane received sequences %q", terminal.sequences)
	}
}

// The way in is discoverable, and it is the same pair of keys on both
// surfaces: help and the palette read the bindings the browser and the project
// plugin both answer. The way out is registered by the app from config, which
// is proved where that registration happens.
func TestInteractiveEnterKeysAreDiscoverableOnBothSurfaces(t *testing.T) {
	contexts := map[string]map[string]bool{
		"global-workspaces":         {},
		"global-workspaces-preview": {},
		"workspace-list":            {},
		"workspace-preview":         {},
	}
	for _, binding := range keymap.DefaultBindings() {
		if binding.Command != "interactive" {
			continue
		}
		if keys, ok := contexts[binding.Context]; ok {
			keys[binding.Key] = true
		}
	}
	for context, keys := range contexts {
		if !keys[interactiveEnterKey] || !keys[interactiveEnterKeyAlt] {
			t.Fatalf("%s binds interactive to %v, want both %q and %q", context, keys, interactiveEnterKey, interactiveEnterKeyAlt)
		}
	}
}

// A wheel notch belongs to the application running in the pane only while it has
// asked for mouse reports. Every other notch scrolls the window this surface is
// drawing — which is what makes the wheel work over a plain shell, where it used
// to do nothing at all.
func TestTheWheelOverALivePaneRoutesToTheAppOrTheWindow(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	terminal.buffer.ApplySnapshot(tty.PaneSnapshot{Output: paneBody(60)})
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)
	selected := m.workspaces.SelectedID()

	surface, ok := m.previewSurface()
	if !ok {
		t.Fatal("the rendered preview has no terminal surface")
	}

	// A plain shell has not asked for mouse reports, so the notch is the
	// window's.
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{X: surface.X + 2, Y: surface.Y + 3, Button: tea.MouseWheelUp}))
	if len(terminal.wheel) != 0 {
		t.Fatalf("a notch reached a pane that never asked for mouse events: %+v", terminal.wheel)
	}
	if m.preview.offset == 0 {
		t.Fatal("the wheel did nothing over a live pane that has no mouse reporting")
	}
	if m.workspaces.SelectedID() != selected {
		t.Fatal("a notch over the terminal moved the list underneath it")
	}

	// The same notch downwards walks back towards the live edge.
	back := m.preview.offset
	settleWheel()
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{X: surface.X + 2, Y: surface.Y + 3, Button: tea.MouseWheelDown}))
	if m.preview.offset >= back {
		t.Fatalf("scrolling down left the window at %d, want closer to the live edge than %d", m.preview.offset, back)
	}

	// Once the application asks for mouse events the notch is its own, in its
	// own coordinates.
	terminal.reporting = true
	m.preview.offset = 0
	settleWheel()
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{X: surface.X + 2, Y: surface.Y + 3, Button: tea.MouseWheelUp}))
	if len(terminal.wheel) != 1 {
		t.Fatalf("the pane received wheel notches %+v, want one", terminal.wheel)
	}
	if got := terminal.wheel[0]; !got.up || got.col != 3 || got.row != 4 || got.notches < 1 {
		t.Fatalf("wheel notch = %+v, want an upward notch at the pane's own 3,4", got)
	}

	// While the app owns the wheel it owns what the pane shows, so a window left
	// scrolled back is pinned to the live frame rather than left over stale rows.
	m.preview.offset = 4
	settleWheel()
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{X: surface.X + 2, Y: surface.Y + 3, Button: tea.MouseWheelUp}))
	if m.preview.offset != 0 {
		t.Fatalf("the window stayed at %d while the app owned the wheel", m.preview.offset)
	}
	terminal.wheel = terminal.wheel[:1]

	// Alt is the "give me the surface, not the app" modifier, on both surfaces.
	settleWheel()
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{
		X: surface.X + 2, Y: surface.Y + 3, Button: tea.MouseWheelUp, Mod: tea.ModAlt}))
	if len(terminal.wheel) != 1 {
		t.Fatalf("alt+wheel was forwarded to the app: %+v", terminal.wheel)
	}
	if m.preview.offset == 0 {
		t.Fatal("alt+wheel did not scroll the window")
	}

	settleWheel()
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{X: surface.X - 1, Y: surface.Y, Button: tea.MouseWheelDown}))
	if len(terminal.wheel) != 1 {
		t.Fatal("a notch outside the terminal box was forwarded into the pane")
	}
}

// A live pane is sized by the paths that move its box, not by the next key that
// happens to arrive: an idle pane under control mode sends nothing to ride
// along with, so a resize or a drag has to say so itself.
func TestGeometryChangesResizeAnIdleLivePane(t *testing.T) {
	originalSave := saveWorkspaceSidebarWidth
	saveWorkspaceSidebarWidth = func(int) error { return nil }
	t.Cleanup(func() { saveWorkspaceSidebarWidth = originalSave })

	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)

	sized := func(t *testing.T, what string) [2]int {
		t.Helper()
		if len(terminal.dims) == 0 {
			t.Fatalf("%s never sized the live pane", what)
		}
		return terminal.dims[len(terminal.dims)-1]
	}
	surfaceDims := func(t *testing.T) [2]int {
		t.Helper()
		width, height, ok := m.previewTerminalSize()
		if !ok {
			t.Fatal("the rendered preview has no terminal surface")
		}
		return [2]int{width, height}
	}

	terminal.dims = nil
	run(t, m, m.WorkspacesResize(previewWide-20, previewTall-4))
	if got, want := sized(t, "a window resize"), surfaceDims(t); got != want {
		t.Fatalf("after a window resize the pane is %v, want the new box %v", got, want)
	}

	// A press on the divider is a press away from the terminal, so it takes the
	// keyboard back before the drag starts. The box it settles on is still the
	// box the pane is sized to the next time anyone types in it.
	m.WorkspacesView(previewWide, previewTall)
	dividerX := m.previewSplit(previewWide).SidebarWidth
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: dividerX, Y: 5, Button: tea.MouseLeft}))
	if m.PreviewInteractive() {
		t.Fatal("a press on the divider left the pane holding the keyboard")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: dividerX + 12, Y: 5, Button: tea.MouseLeft}))
	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: dividerX + 12, Y: 5, Button: tea.MouseLeft}))

	m.WorkspacesView(previewWide, previewTall)
	terminal.dims = nil
	enterInteractive(t, m)
	if got, want := sized(t, "typing after a divider drag"), surfaceDims(t); got != want {
		t.Fatalf("after a divider drag the pane is %v, want the settled box %v", got, want)
	}
}

// Shrinking the window while typing must not leave the keyboard pointed at a
// pane the narrow layout no longer draws.
func TestShrinkingTheWindowWhileTypingKeepsThePaneOnScreen(t *testing.T) {
	m, _, _ := interactiveModel(t)
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)

	narrow := globalListMinWidth + globalDividerWidth + globalPreviewMinWidth - 1
	run(t, m, m.WorkspacesResize(narrow, previewTall))
	if !m.PreviewInteractive() {
		t.Fatal("the resize dropped interactive mode without the user asking")
	}
	if layout := m.workspacesLayout(); !layout.previewOnly || !layout.previewDrawn {
		t.Fatalf("narrow interactive layout = %#v, want the pane filling the tab", layout)
	}
}
