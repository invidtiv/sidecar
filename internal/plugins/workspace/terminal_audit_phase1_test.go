package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
)

func TestInteractivePollContinuesDuringScrollBurst(t *testing.T) {
	p := &Plugin{
		interactiveState: &InteractiveState{Active: true, TermPanel: true},
		termPanelSession: "sidecar-test",
		lastScrollTime:   time.Now(),
		scrollBurstCount: 1,
	}

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
			p.lastScrollTime = time.Now()
			p.scrollBurstCount = 4

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
			p.lastMouseEventTime = time.Now()

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

func TestScrollBurstAccumulatesDebouncedDeltas(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.previewOffset = 20
	p.autoScrollOutput = true
	p.lastScrollTime = time.Now()

	p.forwardScrollToTmux(-3)
	if p.previewOffset != 20 || p.pendingScrollDelta != -3 {
		t.Fatalf("debounced delta not retained: offset=%d pending=%d", p.previewOffset, p.pendingScrollDelta)
	}
	p.lastScrollTime = time.Now().Add(-2 * scrollDebounceInterval)
	p.forwardScrollToTmux(-3)
	if p.previewOffset != 14 {
		t.Fatalf("previewOffset = %d, want accumulated movement to 14", p.previewOffset)
	}
	if p.pendingScrollDelta != 0 {
		t.Fatalf("pendingScrollDelta = %d, want 0 after application", p.pendingScrollDelta)
	}
}

func TestForwardClickCommandDoesNotMutateExitedInteractiveState(t *testing.T) {
	installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width = 100
	p.height = 30
	p.shellSelected = true
	p.interactiveState.MouseReportingEnabled = true

	cmd := p.forwardClickToTmux(10, 5)
	if cmd == nil {
		t.Fatal("expected click forwarding command")
	}
	p.exitInteractiveMode()

	msg := cmd() // Regression: this used to dereference p.interactiveState here.
	result, ok := msg.(interactiveClickSentMsg)
	if !ok || result.Err != nil {
		t.Fatalf("click command result = %#v, want successful interactiveClickSentMsg", msg)
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
	if strings.Contains(string(logged), "display-message") {
		t.Fatalf("resize re-queried pane size: %s", logged)
	}
	if lines := strings.Count(strings.TrimSpace(string(logged)), "\n") + 1; lines != 1 {
		t.Fatalf("resize spawned %d tmux commands, want 1: %s", lines, logged)
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
