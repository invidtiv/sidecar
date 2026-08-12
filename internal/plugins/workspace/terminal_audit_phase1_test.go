package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
)

func TestInteractivePollContinuesDuringScrollBurst(t *testing.T) {
	p := &Plugin{
		interactiveState: &InteractiveState{Active: true},
		worktrees:        []*Worktree{{Name: "sidecar-test"}},
	}
	p.wheel.Add(-1, time.Now())

	if cmd := p.pollInteractivePane(); cmd == nil {
		t.Fatal("adaptive poll returned nil during scroll; poll chain would terminate")
	}
	if cmd := p.pollInteractivePaneImmediate(); cmd == nil {
		t.Fatal("immediate poll returned nil during scroll; poll chain would terminate")
	}
}

func TestInteractiveLiteralKeysSurviveRecentScroll(t *testing.T) {
	for _, literal := range []string{"m", "M", ";", "<"} {
		t.Run(literal, func(t *testing.T) {
			p := newInteractiveInputTestPlugin()
			for range 4 {
				p.wheel.Add(-1, time.Now())
			}

			msg := tea.KeyPressMsg{Code: []rune(literal)[0], Text: literal}
			if cmd := p.handleInteractiveKeys(msg); cmd == nil {
				t.Fatalf("literal %q was filtered after scrolling", literal)
			}
		})
	}
}

func TestInteractiveInputDropsSplitSGRMouseReportsAtEveryBoundary(t *testing.T) {
	const report = "[<65;33;12M"

	for split := 1; split < len(report); split++ {
		t.Run("without-escape-"+string(rune('a'+split)), func(t *testing.T) {
			p := newInteractiveInputTestPlugin()
			// The component keeps the mouse clock the gate reads, so a test that
			// wants a mouse event just gone says so where the surface would.
			attachLiveTerminal(p, true).NoteMouseActivity()

			if cmd := p.handleInteractiveKeys(keyPressForText(report[:split])); cmd != nil {
				t.Fatalf("prefix %q reached command path", report[:split])
			}
			if cmd := p.handleInteractiveKeys(keyPressForText(report[split:])); cmd != nil {
				t.Fatalf("suffix %q reached command path", report[split:])
			}
		})
	}

	// Model the leading ESC as Bubble Tea delivers it: a pending Escape key,
	// followed by the remaining CSI bytes split at every possible boundary.
	for split := 1; split < len(report); split++ {
		t.Run("with-escape-"+string(rune('a'+split)), func(t *testing.T) {
			p := newInteractiveInputTestPlugin()
			_ = p.handleInteractiveKeys(tea.KeyPressMsg{Code: tea.KeyEscape})

			if cmd := p.handleInteractiveKeys(keyPressForText(report[:split])); cmd != nil {
				t.Fatalf("prefix %q after ESC reached command path", report[:split])
			}
			if cmd := p.handleInteractiveKeys(keyPressForText(report[split:])); cmd != nil {
				t.Fatalf("suffix %q after ESC reached command path", report[split:])
			}
		})
	}
}

func TestHandleMouseScrollHonorsFullWheelDelta(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.previewOffset = 20
	p.autoScrollOutput = true

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -3})
	if p.previewOffset != 17 {
		t.Fatalf("previewOffset = %d, want 17 after Delta -3", p.previewOffset)
	}
}

func TestHandleMouseScrollHonorsFullWheelDeltaInTerminalPanel(t *testing.T) {
	p := &Plugin{viewMode: ViewModeList, termPanelScroll: 10}
	region := &mouse.Region{ID: regionTermPanelContent}

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -3, Region: region})
	if p.termPanelScroll != 13 {
		t.Fatalf("termPanelScroll = %d, want 13 after Delta -3", p.termPanelScroll)
	}
	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollDown, Delta: 3, Region: region})
	if p.termPanelScroll != 10 {
		t.Fatalf("termPanelScroll = %d, want 10 after Delta +3", p.termPanelScroll)
	}
}

func TestScrollBurstAccumulatesDebouncedDeltas(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.previewOffset = 20
	p.autoScrollOutput = true
	// The burst takes the time from its caller, so the whole flick is driven here
	// rather than waited out.
	at := time.Now()
	p.clock = func() time.Time { return at }
	// Open the burst, so the notch under test arrives inside the debounce window.
	p.wheel.Add(0, at)

	p.forwardScrollToTmux(mouse.MouseAction{}, -3)
	if p.previewOffset != 20 {
		t.Fatalf("previewOffset = %d, want the debounced notch held back", p.previewOffset)
	}
	if got := p.wheel.Pending(); got != -3 {
		t.Fatalf("held-back delta = %d, want the whole notch retained (-3)", got)
	}

	at = at.Add(2 * tty.WheelDebounceInterval)
	p.forwardScrollToTmux(mouse.MouseAction{}, -3)
	if p.previewOffset != 14 {
		t.Fatalf("previewOffset = %d, want the held-back notch to arrive with the next one (14)",
			p.previewOffset)
	}
	if got := p.wheel.Pending(); got != 0 {
		t.Fatalf("delta left pending after a flush = %d", got)
	}
}

// A forwarded click is delivered by the terminal component, so it holds no
// reference to interactive state that the user may have left in the meantime.
func TestForwardedClickSurvivesLeavingInteractiveMode(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width = 100
	p.height = 30
	p.shellSelected = true
	p.interactiveState.TargetPane = "%7"
	attachLiveTerminal(p, true)

	cmd := p.forwardClickToTmux(10, 5)
	if cmd == nil {
		t.Fatal("expected click forwarding command")
	}
	p.exitInteractiveMode()

	runCommandTree(cmd) // The command must not reach for p.interactiveState here.
	tty.WaitForPendingSends()

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "-t %7") {
		t.Fatalf("forwarded click did not reach the pane: %s", logged)
	}
}

// An app that has not asked for mouse reports never receives a click: the
// component is the one authority on who owns the pointer.
func TestClickIsNotForwardedWithoutMouseReporting(t *testing.T) {
	installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width = 100
	p.height = 30
	p.shellSelected = true
	attachLiveTerminal(p, false)

	if cmd := p.forwardClickToTmux(10, 5); cmd != nil {
		t.Fatal("a click was forwarded to an app that never asked for the mouse")
	}
}

func TestCursorOverlayIsSuppressedAwayFromLiveEdge(t *testing.T) {
	if shouldOverlayCursor(true, true, false) {
		t.Fatal("cursor must not be fabricated over historical scrollback")
	}
	if !shouldOverlayCursor(true, true, true) {
		t.Fatal("cursor should render at the live edge")
	}
}

func TestMaybeResizeInteractivePaneUsesCapturedSizeWithoutQuery(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width = 100
	p.height = 30
	p.interactiveState.TargetPane = "%42"

	cmd := p.maybeResizeInteractivePane(1, 1)
	if cmd == nil {
		t.Fatal("expected resize command for mismatched captured size")
	}
	if _, ok := cmd().(paneResizedMsg); !ok {
		t.Fatal("resize command did not return paneResizedMsg")
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logged), "pane_width") {
		t.Fatalf("resize re-queried pane size: %s", logged)
	}
	// The ownership lease reads @sidecar-owner once per local tick (td-ee222a);
	// that read resolves the session in the same invocation, so the resize still
	// costs one tmux process beyond the resize itself and none of it re-derives
	// geometry the capture already reported.
	if lines := strings.Count(strings.TrimSpace(string(logged)), "\n") + 1; lines != 2 {
		t.Fatalf("resize spawned %d tmux commands, want 2: %s", lines, logged)
	}
	if count := strings.Count(string(logged), "resize-"); count != 1 {
		t.Fatalf("resize spawned %d resize commands, want 1: %s", count, logged)
	}
}

// The option the geometry lease lives under, spelled out here so the test fails
// loudly if internal/tty renames it.
const leaseOptionForTest = "@sidecar-owner"

// A pane that already matches needs no resize, but the geometry lease still has
// to be ticked: it is kept alive by the poll, not by the resize. An owner that
// stopped ticking once it settled looked abandoned to the other machine, which
// claimed and resized, which un-settled this one — ownership ping-ponging on the
// staleness period (td-ee222a).
func TestMaybeResizeInteractivePaneTicksLeaseWhenSizeAlreadyMatches(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width = 100
	p.height = 30
	p.interactiveState.TargetPane = "%42"
	// A settled pane is sized to the content width — the scrollbar owns the
	// viewport's last column (td-0818ef).
	w, h := p.calculatePreviewDimensions()
	w = p.terminalContentWidth(w)

	cmd := p.maybeResizeInteractivePane(w, h)
	if cmd == nil {
		t.Fatal("matched pane produced no command, so the lease is never refreshed")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("lease touch reported a resize: %#v", msg)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), leaseOptionForTest) {
		t.Fatalf("matched pane did not tick the ownership lease: %q", logged)
	}
	if strings.Contains(string(logged), "resize-") {
		t.Fatalf("matched pane resized anyway: %q", logged)
	}
}

func TestCaptureWithCursorUsesSingleTmuxCommandChain(t *testing.T) {
	args := capturePaneWithCursorArgs("session name;$()", "%7", false)
	if countStrings(args, ";") != 1 {
		t.Fatalf("command separators = %d, want 1: %#v", countStrings(args, ";"), args)
	}
	if !slices.Contains(args, "display-message") || !slices.Contains(args, "capture-pane") {
		t.Fatalf("missing merged cursor/capture commands: %#v", args)
	}
	if countStrings(args, "session name;$()") != 1 {
		t.Fatalf("session target was not preserved as one argv element: %#v", args)
	}

	cursor := parseCapturedCursor("12,4,1,30,100")
	if !cursor.Valid || cursor.Col != 12 || cursor.Row != 4 ||
		cursor.PaneHeight != 30 || cursor.PaneWidth != 100 || !cursor.Visible {
		t.Fatalf("parsed cursor = %#v", cursor)
	}
}

func TestBatchCaptureUsesArgvOnlyNonceDelimitedCommands(t *testing.T) {
	sessions := []string{"sidecar-$(touch /tmp/pwn)", "sidecar-`uname`-${USER}"}
	const nonce = "0123456789abcdef"
	args := buildBatchCaptureArgs(sessions, nonce, true)

	if slices.Contains(args, "bash") || slices.Contains(args, "-c") {
		t.Fatalf("batch capture contains shell mediation: %#v", args)
	}
	for _, session := range sessions {
		if countStrings(args, session) != 2 {
			t.Fatalf("hostile session %q was not preserved as argv targets: %#v", session, args)
		}
	}
	if countStrings(args, "-J") != len(sessions) {
		t.Fatalf("joined capture flag count = %d, want %d", countStrings(args, "-J"), len(sessions))
	}

	output := batchCaptureMarker(nonce, 0) + "\n" +
		"alpha\n===SIDECAR_SESSION:guessable===\n" +
		batchCaptureMarker(nonce, 1) + "\nbeta\n"
	parsed := parseBatchCaptureOutput(output, sessions, nonce)
	if !strings.Contains(parsed[sessions[0]], "===SIDECAR_SESSION:guessable===") {
		t.Fatalf("pane content collided with delimiter: %#v", parsed)
	}
	if parsed[sessions[1]] != "beta\n" {
		t.Fatalf("second pane output = %q, want beta newline", parsed[sessions[1]])
	}
}

func TestBatchCaptureIncludesActivityMetadataInSameTmuxInvocation(t *testing.T) {
	args := buildBatchCaptureArgs([]string{"sidecar-ws-one"}, "nonce", true)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "#{pane_current_command}") || !strings.Contains(joined, "#{pane_title}") {
		t.Fatalf("activity metadata missing from batch argv: %q", joined)
	}
	if got := strings.Count(joined, "capture-pane"); got != 1 {
		t.Fatalf("capture command count = %d, want 1", got)
	}
	output := batchCaptureMarker("nonce", 0) + captureMetadataSeparator + "node" + captureMetadataSeparator + "⠼ repo\nworking\n"
	parsed := parseBatchCaptureOutput(output, []string{"sidecar-ws-one"}, "nonce")
	screen, metadata := splitCaptureEnvelope(parsed["sidecar-ws-one"])
	if screen != "working\n" || metadata.CurrentCommand != "node" || metadata.PaneTitle != "⠼ repo" {
		t.Fatalf("screen=%q metadata=%+v", screen, metadata)
	}
}

func newInteractiveInputTestPlugin() *Plugin {
	return &Plugin{
		viewMode:         ViewModeInteractive,
		previewTab:       PreviewTabOutput,
		autoScrollOutput: true,
		interactiveState: &InteractiveState{
			Active:        true,
			TargetSession: "sidecar-test",
			LastKeyTime:   time.Now(),
		},
	}
}

// attachLiveTerminal gives the plugin the terminal component interactive mode
// routes input through, without a tmux server behind it. Whether a click or a
// notch belongs to the application is the component's one answer, so a test that
// wants an app tracking the mouse says so here rather than on a mirror of it.
func attachLiveTerminal(p *Plugin, mouseReporting bool) *tty.Model {
	// Built the way the plugin builds its own, so the host hooks the component
	// calls — its chords, its snap-back, its way out — are the real ones.
	model := p.newWorkspaceTerminal()
	model.State = &tty.State{
		Active:                true,
		TargetSession:         p.interactiveState.TargetSession,
		TargetPane:            p.interactiveState.TargetPane,
		MouseReportingEnabled: mouseReporting,
		CursorVisible:         true,
		OutputBuf:             tty.NewOutputBuffer(outputBufferCap),
	}
	if p.interactiveState.TermPanel {
		p.panelTerminal = model
	} else {
		p.primaryTerminal = model
	}
	return model
}

func keyPressForText(text string) tea.KeyPressMsg {
	runes := []rune(text)
	return tea.KeyPressMsg{Code: runes[0], Text: text}
}

func countStrings(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

func installSuccessfulFakeTmux(t *testing.T) string {
	t.Helper()
	// Keystroke sends are enqueued where the key is handled and run on a
	// background queue, so an earlier test's send can still be in flight. Flush
	// before claiming the fake tmux so its log holds only this test's commands,
	// and again on the way out so this test's sends cannot leak into the next
	// one's log while PATH still points here.
	tty.WaitForPendingSends()
	t.Cleanup(tty.WaitForPendingSends)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux.log")
	script := filepath.Join(dir, "tmux")
	body := "#!/bin/sh\n/bin/echo \"$@\" >> \"$TMUX_TEST_LOG\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TMUX_TEST_LOG", logPath)
	return logPath
}

// Interactive mode is advertised under three keys — enter (primary), and the
// "E"/"i" alternates named by the preview hint and the command palette. "E" was
// listed in the keymap but never handled, so it silently did nothing and the
// keys typed after it were read as workspace bindings (td-10c761).
func TestInteractiveModeEntryKeys(t *testing.T) {
	for _, key := range []string{"enter", "E", "i"} {
		t.Run(key, func(t *testing.T) {
			installSuccessfulFakeTmux(t)
			p := New()
			p.width, p.height = 100, 30
			p.shellSelected = true
			p.selectedShellIdx = 0
			p.shells = []*ShellSession{{
				TmuxName: "sidecar-test",
				Agent:    &Agent{TmuxSession: "sidecar-test", TmuxPane: "%1"},
			}}

			p.handleListKeys(keyPressFor(key))

			if p.viewMode != ViewModeInteractive {
				t.Fatalf("%q did not enter interactive mode: viewMode = %v", key, p.viewMode)
			}
			if p.interactiveState == nil || !p.interactiveState.Active {
				t.Fatalf("%q left interactive state inactive: %#v", key, p.interactiveState)
			}
		})
	}
}

func keyPressFor(key string) tea.KeyPressMsg {
	if key == "enter" {
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	runes := []rune(key)
	msg := tea.KeyPressMsg{Code: runes[0], Text: key}
	if len(runes) == 1 && runes[0] >= 'A' && runes[0] <= 'Z' {
		msg.Code = runes[0] + ('a' - 'A')
		msg.ShiftedCode = runes[0]
		msg.Mod = tea.ModShift
	}
	return msg
}

// A mode-entry message can precede the wrapper's reconciliation by one update.
// Ordinary keys reach the pane through the plugin's own provisional path in that
// window, and modified ones — shift+enter, ctrl+enter into Claude Code — have to
// take it too rather than being silently dropped.
func TestModifiedKeysReachThePaneBeforeTheTerminalIsReconciled(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.interactiveState.TargetPane = "%9"
	// No terminal component yet: exactly the window the fallback exists for.
	if p.activeInteractiveTerminal() != nil {
		t.Fatal("fixture already has a reconciled terminal")
	}

	shiftEnter := uv.UnknownCsiEvent("\x1b[13;2u")
	cmd := p.handleUnknownSequence(shiftEnter)
	if cmd == nil {
		t.Fatal("shift+enter was dropped while an ordinary key would have been sent")
	}
	runCommandTree(cmd)
	tty.WaitForPendingSends()

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "-t %9") {
		t.Fatalf("the modified key did not reach the pane: %s", logged)
	}
}

// While interactive mode is live the pane keeps the wheel wherever the pointer
// is. Routing a notch to the region under it hands one that drifted off the pane
// to the sidebar, where moving the cursor changes the selected workspace and
// drops the user out of the pane they are typing in.
func TestAWheelOverTheSidebarStaysWithTheLiveTerminal(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.Update(strings.Repeat("scrollback\n", 100))
	p.shells = []*ShellSession{
		{Name: "one", TmuxName: "sc-one", Agent: &Agent{OutputBuf: buffer}},
		{Name: "two", TmuxName: "sc-two"},
	}
	p.selectedShellIdx = 0
	p.previewOffset = 12
	p.autoScrollOutput = false
	attachLiveTerminal(p, false)

	p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -3, X: 2, Y: 5,
		Region: &mouse.Region{ID: regionSidebar},
	})

	if p.previewOffset != 9 {
		t.Fatalf("previewOffset = %d, want the notch to have scrolled the terminal to 9", p.previewOffset)
	}
	if p.viewMode != ViewModeInteractive || p.interactiveState == nil || !p.interactiveState.Active {
		t.Fatal("a notch over the sidebar dropped the user out of interactive mode")
	}
	if p.selectedShellIdx != 0 {
		t.Fatalf("selected shell = %d, want the notch to have left the selection alone", p.selectedShellIdx)
	}
}
