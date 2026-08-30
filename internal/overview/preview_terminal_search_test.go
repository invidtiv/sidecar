package overview

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

type rangeCapturingTerminal struct {
	*fakeTerminal
	captures [][2]int
	capture  func(start, end int) (tty.CaptureRange, error)
}

func (t *rangeCapturingTerminal) CaptureRange(start, end int) (tty.CaptureRange, error) {
	t.captures = append(t.captures, [2]int{start, end})
	if t.capture == nil {
		return tty.CaptureRange{}, fmt.Errorf("capture range not configured")
	}
	return t.capture(start, end)
}

func TestInteractivePreviewSearchOwnsItsQueryInsteadOfSendingItToThePane(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)

	shortcut := tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl | tea.ModShift}
	if !pressWorkspaces(t, m, shortcut) || !m.terminalSearch.InputActive {
		t.Fatal("ctrl+shift+f did not open terminal search while typing")
	}
	for _, r := range "pane" {
		if !pressWorkspaces(t, m, tea.KeyPressMsg{Code: r, Text: string(r)}) {
			t.Fatalf("search query rune %q was not handled", r)
		}
	}
	if got := m.terminalSearch.Query; got != "pane" {
		t.Fatalf("query = %q, want pane", got)
	}
	if got := len(m.terminalSearch.Matches); got != 1 {
		t.Fatalf("matches = %d, want one match in the loaded pane", got)
	}
	if len(terminal.keys) != 0 {
		t.Fatalf("terminal received search input: %v", terminal.keys)
	}

	pressWorkspaces(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.terminalSearch.InputActive {
		t.Fatal("enter did not commit terminal search")
	}
	pressWorkspaces(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	pressWorkspaces(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.terminalSearch.Query != "" {
		t.Fatal("escape did not clear committed terminal search")
	}
	if len(terminal.keys) != 0 {
		t.Fatalf("terminal received committed search navigation: %v", terminal.keys)
	}
}

func TestTerminalSearchCommandIsBoundAndAdvertisedOnBothWorkspaceSurfaces(t *testing.T) {
	registry := keymap.NewRegistry()
	keymap.RegisterDefaults(registry)
	for _, context := range []string{ctxGlobalWorkspacesTerminal, "workspace-interactive"} {
		if got, ok := registry.CommandForContextKey(context, "ctrl+shift+f"); !ok || got != "search-terminal" {
			t.Fatalf("%s ctrl+shift+f -> %q (bound=%v), want search-terminal", context, got, ok)
		}
	}

	m, _, _ := interactiveModel(t)
	enterInteractive(t, m)
	if !commandNamed(m, "search-terminal") {
		t.Fatalf("global terminal Commands() omitted search-terminal: %#v", m.Commands())
	}
}

func TestPreviewTerminalSearchLoadsCompleteHistoryThroughTheTerminal(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	capturer := &rangeCapturingTerminal{fakeTerminal: terminal}
	m.primaryTerminalState().terminal = capturer
	terminal.buffer = tty.NewOutputBuffer(2000)
	terminal.buffer.UpdateSnapshot(numberedPreviewSearchLines(600, 620, -1), 600)
	terminal.history = tty.HistoryInfo{HasHistory: true, HistorySize: 1200}
	capturer.capture = func(start, end int) (tty.CaptureRange, error) {
		return tty.CaptureRange{
			Output: numberedPreviewSearchLines(0, 599, 10), StartLine: 0,
			HistorySize: 1200,
		}, nil
	}
	enterInteractive(t, m)

	pressWorkspaces(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl | tea.ModShift})
	for _, r := range "needle" {
		pressWorkspaces(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(capturer.captures) != 1 || capturer.captures[0] != [2]int{-1200, -601} {
		t.Fatalf("history captures = %v, want the complete range before the loaded base", capturer.captures)
	}
	if base, _, ok := terminal.buffer.AbsoluteRange(); !ok || base != 0 {
		t.Fatalf("history base = %d absolute=%v, want complete buffer from zero", base, ok)
	}
	if got := len(m.terminalSearch.Matches); got != 1 || m.terminalSearch.Matches[0].Line != 10 {
		t.Fatalf("matches = %#v, want the match from unvisited history", m.terminalSearch.Matches)
	}
}

func TestPreviewTerminalSearchRejectsLateHistoryAfterSourceSwitch(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	capturer := &rangeCapturingTerminal{fakeTerminal: terminal}
	m.primaryTerminalState().terminal = capturer
	terminal.buffer.UpdateSnapshot(numberedPreviewSearchLines(600, 620, -1), 600)
	terminal.history = tty.HistoryInfo{HasHistory: true, HistorySize: 1200}
	capturer.capture = func(start, end int) (tty.CaptureRange, error) {
		return tty.CaptureRange{Output: numberedPreviewSearchLines(0, 599, 10), StartLine: 0, HistorySize: 1200}, nil
	}
	enterInteractive(t, m)
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl | tea.ModShift})
	if !handled || cmd == nil {
		t.Fatal("search did not request complete history")
	}
	msg := cmd()
	m.setPrimaryTarget(tty.Target{Session: "other", Pane: "%9"})
	m.Update(msg)
	if base, _, _ := terminal.buffer.AbsoluteRange(); base != 600 {
		t.Fatalf("late result changed the old source buffer base to %d", base)
	}
}

func TestPreviewTerminalSearchHighlightsAndNavigatesLoadedMatches(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	terminal.buffer.UpdateSnapshot("needle first\nplain\nneedle second", 20)
	enterInteractive(t, m)
	pressWorkspaces(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl | tea.ModShift})
	for _, r := range "needle" {
		pressWorkspaces(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := len(m.terminalSearch.Matches); got != 2 {
		t.Fatalf("matches = %d, want 2", got)
	}
	decorated := m.previewTerminalDecorator(m.previewTerminalLeaf())("needle first", 20)
	if !strings.Contains(decorated, ui.GetSelectionBgANSI()) {
		t.Fatalf("search highlight missing from %q", decorated)
	}
	pressWorkspaces(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !pressWorkspaces(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"}) || m.terminalSearch.Current != 1 {
		t.Fatalf("next match current = %d, want 1", m.terminalSearch.Current)
	}
	if !pressWorkspaces(t, m, tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift}) || m.terminalSearch.Current != 0 {
		t.Fatalf("previous match current = %d, want 0", m.terminalSearch.Current)
	}
}

func numberedPreviewSearchLines(start, end, needle int) string {
	lines := make([]string, 0, end-start+1)
	for line := start; line <= end; line++ {
		value := fmt.Sprintf("line-%04d", line)
		if line == needle {
			value += " needle"
		}
		lines = append(lines, value)
	}
	return strings.Join(lines, "\n")
}
