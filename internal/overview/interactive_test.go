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
	hooks      tty.Hooks
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
	released   int
	exits      int
	closes     int
	onOpen     func(tty.Target, *fakeTerminal)
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
	if f.onOpen != nil {
		f.onOpen(target, f)
	}
	return nil
}

// Update stands in for the component's own key pipeline, in its order: the
// host's chords first, then the ways out, then what is left reaches the pane.
func (f *fakeTerminal) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if f.hooks.OnKey != nil {
			if cmd, handled := f.hooks.OnKey(msg); handled {
				return cmd
			}
		}
		key := msg.String()
		if key == f.config.ExitKey {
			if f.hooks.ExitAction == tty.ExitClosesTerminal {
				f.active = false
			}
			if f.hooks.OnExit != nil {
				return f.hooks.OnExit()
			}
			return nil
		}
		if f.hooks.BeforeSend != nil {
			f.hooks.BeforeSend(msg)
		}
		f.keys = append(f.keys, key)
	case tea.PasteMsg:
		f.pastes = append(f.pastes, msg.Content)
	case tty.CaptureResultMsg:
		f.buffer.ApplySnapshot(tty.PaneSnapshot{Output: msg.Output})
	case tty.SessionDeadMsg:
		// The component ends itself on a pane that died under a send, and says so
		// through the hook rather than leaving the host to notice.
		f.active = false
		if f.hooks.OnSessionEnded != nil {
			return f.hooks.OnSessionEnded()
		}
	}
	return nil
}

// Buffer is nil once the terminal is no longer live, exactly as the component's
// is: a fake that kept answering after the mode ended would let a host that
// reads the buffer too late pass here and draw nothing in the real program.
func (f *fakeTerminal) Buffer() *tty.OutputBuffer {
	if !f.active {
		return nil
	}
	return f.buffer
}

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
	f.paneW, f.paneH = width, height
	return nil
}

func (f *fakeTerminal) SendUnknownSequence(msg tea.Msg) tea.Cmd {
	f.sequences = append(f.sequences, string(tty.ExtractUnknownCSIBytes(msg)))
	return nil
}

func (f *fakeTerminal) IsActive() bool { return f.active }

func (f *fakeTerminal) Exit() { f.exits++; f.active = false }

func (f *fakeTerminal) Close() { f.closes++; f.active = false }

// ReleaseInput records that the host left through the seam that also drops a
// half-read mouse report, which is the only way out this surface may take.
func (f *fakeTerminal) ReleaseInput() {
	f.released++
	if f.hooks.ExitAction == tty.ExitClosesTerminal {
		f.active = false
	}
}

// interactiveModel is the preview fixture with the terminal seam replaced, so
// entering interactive mode never reaches a tmux server.
func interactiveModel(t *testing.T) (*Model, *captureRecorder, *fakeTerminal) {
	t.Helper()
	m, recorder := previewModel(t)
	terminal := newFakeTerminal("live pane body")
	terminal.onOpen = func(target tty.Target, terminal *fakeTerminal) {
		if target.Pane != "%1" {
			terminal.buffer.ApplySnapshot(tty.PaneSnapshot{Output: "pane " + target.Pane + " output"})
		}
	}
	original := newPreviewTerminal
	newPreviewTerminal = func(config tty.Config, hooks tty.Hooks) previewTerminal {
		terminal.config = config
		terminal.hooks = hooks
		return terminal
	}
	t.Cleanup(func() { newPreviewTerminal = original })
	run(t, m, m.SetWorkspacesVisible(true))
	return m, recorder, terminal
}

// enterInteractive starts typing from the list. Enter is the primary way in.
func enterInteractive(t *testing.T, m *Model) {
	t.Helper()
	press(t, m, "enter")
}

func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

func TestPreviewHandsTheKeyboardToTheSelectedLivePane(t *testing.T) {
	m, recorder, terminal := interactiveModel(t)
	captures := len(recorder.panes())

	enterInteractive(t, m)

	if !m.PreviewInteractive() {
		t.Fatal("enter did not hand the keyboard to the selected pane")
	}
	if terminal.target != (tty.Target{Session: "sc-alpha", Pane: "%1"}) {
		t.Fatalf("the terminal opened %+v, want the selected row's own session and pane", terminal.target)
	}
	if len(recorder.panes()) != captures {
		t.Fatalf("entering captured a pane as well: %v", recorder.panes())
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

// The primary keyboard entry point is the list: Enter (and E) with the list
// focused reaches the pane without a focus move first.
func TestTheListHandsTheKeyboardToThePaneInOnePress(t *testing.T) {
	for _, key := range []string{"enter", interactiveEnterKeyAlt} {
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
			opens := terminal.opens
			m.workspaces.SelectID(tc.id)
			run(t, m, m.previewSync())
			enterInteractive(t, m)

			if m.PreviewInteractive() {
				t.Fatalf("%q handed the keyboard to an item with no live pane", tc.id)
			}
			if terminal.opens != opens {
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

	for _, k := range []string{"/", "q", "i", "j", "s", "\\"} {
		handled, cmd := m.WorkspacesKey(key(k))
		if !handled {
			t.Fatalf("%q was not forwarded to the live pane", k)
		}
		run(t, m, cmd)
	}
	if got := strings.Join(terminal.keys, ""); got != "/qijs\\" {
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
	m.closePreviewTerminal()
	m.preview.terminal = nil
	run(t, m, m.syncPreviewTerminal())
	terminal = m.preview.terminal.(*fakeTerminal)
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

// The terminal component owns the ways out, and reports them through the OnExit
// hook this surface registered. The browser gives the keyboard back while the
// same producer continues watching the selection.
func TestTheExitKeyGivesTheKeyboardBackAndKeepsTheProducer(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	opens := terminal.opens

	handled, cmd := m.WorkspacesKey(ctrlKey('\\'))
	if !handled {
		t.Fatal("the exit key was not answered while the pane was live")
	}
	run(t, m, cmd)

	if m.PreviewInteractive() {
		t.Fatal("the exit key left the keyboard with the pane")
	}
	if m.PreviewFocused() {
		t.Fatal("leaving the pane left focus on a watched preview")
	}
	if m.WorkspaceFocusContext() != "global-workspaces" {
		t.Fatalf("exit landed on %q, want the list", m.WorkspaceFocusContext())
	}
	if m.workspaces.SelectedID() == "" {
		t.Fatal("exit lost the session the list was showing")
	}
	if !terminal.IsActive() || terminal.opens != opens {
		t.Fatalf("leaving replaced the watched producer: active=%v opens=%d want=%d", terminal.IsActive(), terminal.opens, opens)
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
		if m.PreviewInteractive() || !terminal.IsActive() || terminal.target.Pane != "%2" {
			t.Fatal("selecting another row did not rebind a watched producer without keeping its keyboard")
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
	if strings.Contains(view, "i to type") || strings.Contains(view, "enter to type") {
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

	run(t, m, m.WorkspacesTerminalMsg(tty.CaptureResultMsg{Output: "watched frame"}))
	if terminal.buffer.String() != "watched frame" {
		t.Fatal("a terminal frame did not reach the watched producer")
	}

	enterInteractive(t, m)
	run(t, m, m.WorkspacesTerminalMsg(tty.CaptureResultMsg{Output: "fresh frame"}))
	if got := terminal.buffer.String(); got != "fresh frame" {
		t.Fatalf("the live terminal did not receive its own message: %q", got)
	}
	run(t, m, m.WorkspacesTerminalMsg(struct{}{}))
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

// The way in is discoverable: Enter on the global list, E on both surfaces.
// i is find-TD-task, not a way into the pane.
func TestInteractiveEnterKeysAreDiscoverableOnBothSurfaces(t *testing.T) {
	contexts := map[string]map[string]bool{
		"global-workspaces": {},
		"workspace-list":    {},
		"workspace-preview": {},
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
		if keys["i"] {
			t.Fatalf("%s still binds i to interactive", context)
		}
		if !keys[interactiveEnterKeyAlt] {
			t.Fatalf("%s binds interactive to %v, want %q", context, keys, interactiveEnterKeyAlt)
		}
	}
	if !contexts["global-workspaces"]["enter"] {
		t.Fatal("global Workspaces does not bind enter to interactive")
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

func TestEnterStillTypesAfterHidingTheSidebar(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	press(t, m, "\\")
	if m.WorkspaceSidebarVisible() || m.PreviewFocused() {
		t.Fatal("test premise: backslash hid the sidebar and left the list")
	}

	press(t, m, "enter")
	if !m.PreviewInteractive() {
		t.Fatal("enter after hiding the sidebar did not start typing")
	}
	if terminal.target != (tty.Target{Session: "sc-alpha", Pane: "%1"}) {
		t.Fatalf("the terminal opened %+v, want the selected row", terminal.target)
	}
}

func TestEnterOnADeadRowStaysOnTheList(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	opens := terminal.opens
	m.workspaces.SelectID("d")
	run(t, m, m.previewSync())

	handled, cmd := m.WorkspacesKey(key("enter"))
	if !handled {
		t.Fatal("enter on a dead row was not answered")
	}
	run(t, m, cmd)

	if m.PreviewInteractive() || terminal.opens != opens {
		t.Fatal("enter on a dead row started typing")
	}
	if request, ok := navigation(t, cmd); ok {
		t.Fatalf("enter on a dead row navigated to %#v", request.Workspace)
	}
	if m.PreviewFocused() {
		t.Fatal("enter on a dead row moved focus off the list")
	}
	if m.workspaces.SelectedID() != "d" {
		t.Fatalf("enter moved the selection to %q", m.workspaces.SelectedID())
	}
}

func TestRightAndLDoNotFocusAWatchedPreview(t *testing.T) {
	m, _, _ := interactiveModel(t)
	for _, k := range []string{"right", "l"} {
		handled, _ := m.WorkspacesKey(key(k))
		if handled {
			t.Fatalf("%q was handled as a watched-preview focus", k)
		}
		if m.PreviewFocused() || m.PreviewInteractive() {
			t.Fatalf("%q moved focus off the list", k)
		}
	}
}

func TestClickingAListRowDoesNotStartTyping(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	opens := terminal.opens
	m.WorkspacesView(previewWide, previewTall)
	x, y, ok := rowPoint(m, "b")
	if !ok {
		t.Fatal("row b was not rendered")
	}

	click(t, m, x, y)

	if m.PreviewInteractive() || terminal.opens != opens+1 {
		t.Fatal("a single click on a list row started typing")
	}
	if m.workspaces.SelectedID() != "b" {
		t.Fatalf("the click selected %q, want b", m.workspaces.SelectedID())
	}
	if m.PreviewFocused() {
		t.Fatal("a list-row click left focus on the preview")
	}
}

func TestWheelOverTheTerminalDoesNotStartTyping(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	opens := terminal.opens
	m.WorkspacesView(previewWide, previewTall)
	surface, ok := m.previewSurface()
	if !ok {
		t.Fatal("the rendered preview has no terminal surface")
	}

	settleWheel()
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{X: surface.X + 2, Y: surface.Y + 3, Button: tea.MouseWheelUp}))

	if m.PreviewInteractive() || terminal.opens != opens {
		t.Fatal("a wheel notch over the terminal started typing")
	}
	if m.PreviewFocused() {
		t.Fatal("a wheel notch over the terminal focused a watched preview")
	}
}

// A click on the left pane is how a user gets the arrow keys back, exactly as
// it is on the project surface. Selecting another live row moves the producer
// to it and watches it; it does not keep typing into the pane the user left.
func TestClickingAnotherLiveRowWhileTypingReturnsToTheList(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)
	x, y, ok := rowPoint(m, "b")
	if !ok {
		t.Fatal("row b was not rendered")
	}

	click(t, m, x, y)

	if m.workspaces.SelectedID() != "b" {
		t.Fatalf("the click selected %q, want b", m.workspaces.SelectedID())
	}
	if m.PreviewInteractive() {
		t.Fatal("clicking a row left the keyboard in the pane, so j/k would type into the shell")
	}
	if m.PreviewFocused() {
		t.Fatal("clicking a row left focus on the preview")
	}
	if terminal.target != (tty.Target{Session: "sc-bravo", Pane: "%2"}) {
		t.Fatalf("the terminal opened %+v, want bravo's pane", terminal.target)
	}
	if !terminal.IsActive() {
		t.Fatal("the clicked row's pane stopped being watched")
	}
}

func TestClickingADeadRowWhileTypingLandsOnTheList(t *testing.T) {
	m, _, _ := interactiveModel(t)
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)
	x, y, ok := rowPoint(m, "d")
	if !ok {
		t.Fatal("row d was not rendered")
	}

	click(t, m, x, y)

	if m.workspaces.SelectedID() != "d" {
		t.Fatalf("the click selected %q, want d", m.workspaces.SelectedID())
	}
	if m.PreviewInteractive() || m.PreviewFocused() {
		t.Fatal("clicking a dead row while typing did not land on the list")
	}
}
