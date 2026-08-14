package workspace

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

func numberedTerminalLines(start, count int) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%04d", start+i)
	}
	return strings.Join(lines, "\n")
}

// watchedReachPlugin is a list-mode plugin whose preview shows a pane holding
// more history than the capture that seeded it: 620 lines loaded from absolute
// 600, with 1200 above the grid in tmux. It is the global browser's fixture,
// stated in this surface's terms.
func watchedReachPlugin(t *testing.T) *Plugin {
	t.Helper()
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.UpdateSnapshot(numberedTerminalLines(600, 620), 600)

	p := New()
	p.width, p.height = 120, 40
	p.sidebarWidth = 40
	p.viewMode = ViewModeList
	p.previewTab = PreviewTabOutput
	p.shellSelected = true
	p.shells = []*ShellSession{{
		TmuxName: "shell-1",
		Agent:    &Agent{TmuxSession: "shell-1", TmuxPane: "%1", OutputBuf: buffer},
	}}
	p.terminalHistory[terminalHistoryKey("shell", "shell-1")] = tty.HistoryReach{HistorySize: 1200}
	// The wheel is coalesced by the shared burst, so the fixture spaces its
	// notches: each reads a clock a debounce window later than the last.
	at := time.Now()
	p.clock = func() time.Time {
		at = at.Add(2 * tty.WheelDebounceInterval)
		return at
	}
	return p
}

// How far back a surface reads is a property of the layer, not of the surface.
// A watched preview at the bound asks for the chunk behind it, and once that
// lands the window reaches tmux's oldest line — the same row the global browser
// lands on in internal/overview's
// TestAWatchedGlobalPreviewReachesTmuxsOldestLine.
func TestAWatchedPreviewReachesTmuxsOldestLine(t *testing.T) {
	p := watchedReachPlugin(t)
	source, ok := p.terminalHistoryFor(false)
	if !ok {
		t.Fatal("the fixture's preview has no history source")
	}

	p.jumpPreviewWindow(p.previewMaxScroll())
	cmd := p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -5, Region: &mouse.Region{ID: regionPreviewPane}})
	if cmd == nil {
		t.Fatal("a watched window at the bound never reached for the history behind it")
	}
	state := p.terminalHistory[source.Key]
	if !state.Loading {
		t.Fatal("the read was never recorded against the preview")
	}

	p.applyTerminalHistory(terminalHistoryLoadedMsg{
		Source: source,
		Capture: tty.CaptureRange{
			Output:      numberedTerminalLines(0, 600),
			HistorySize: 1200,
			StartLine:   0,
			EndLine:     600,
		},
		RequestGen: state.RequestGen,
	})

	base, _, absolute := source.Buffer.AbsoluteRange()
	if !absolute || base != 0 {
		t.Fatalf("buffer base = %d absolute=%v, want the pane's oldest line", base, absolute)
	}
	p.jumpPreviewWindow(p.previewMaxScroll())
	layout := p.terminalViewportLayoutFor(false)
	if layout.AbsoluteStart != 0 {
		t.Fatalf("the oldest window starts at absolute %d, want line 0", layout.AbsoluteStart)
	}
	if first := source.Buffer.Lines()[layout.Start]; first != "line-0000" {
		t.Fatalf("the oldest window's first row is %q, want line-0000", first)
	}
}

// At tmux's oldest line there is nothing left to read, and the reader is told so
// in the words the global browser uses, once.
func TestTheEndOfTmuxsHistoryIsSaidOnce(t *testing.T) {
	p := watchedReachPlugin(t)
	source, _ := p.terminalHistoryFor(false)
	// The buffer already holds everything tmux has.
	source.Buffer.UpdateSnapshot(numberedTerminalLines(0, 1220), 0)
	notch := func() tea.Cmd {
		t.Helper()
		p.jumpPreviewWindow(p.previewMaxScroll())
		return p.handleMouseScroll(mouse.MouseAction{
			Type: mouse.ActionScrollUp, Delta: -5, Region: &mouse.Region{ID: regionPreviewPane}})
	}

	cmd := notch()
	if cmd == nil {
		t.Fatal("the end of this pane's history was never mentioned")
	}
	toast, ok := cmd().(appmsg.ToastMsg)
	if !ok || toast.Message != tty.HistoryExhaustedNotice {
		t.Fatalf("message at the end of history = %#v, want %q", cmd(), tty.HistoryExhaustedNotice)
	}
	if again := notch(); again != nil {
		t.Fatalf("the end of history was announced twice: %#v", again())
	}
}

func TestParseCapturedCursorIncludesAbsoluteHistoryMetadata(t *testing.T) {
	cursor := parseCapturedCursor("12,4,1,30,100,1250")
	if !cursor.Valid || !cursor.capturedPaneMetadata.Valid {
		t.Fatalf("cursor metadata was not valid: %#v", cursor)
	}
	if cursor.HistorySize != 1250 || cursor.CaptureBase != 650 {
		t.Fatalf("history metadata = size %d base %d, want 1250/650",
			cursor.HistorySize, cursor.CaptureBase)
	}
}

func TestApplyTerminalHistoryPrependsAndReplaysPendingScroll(t *testing.T) {
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.UpdateSnapshot(numberedTerminalLines(600, 620), 600)
	p := New()
	p.shellSelected = true
	p.shells = []*ShellSession{{
		TmuxName: "shell-1",
		Agent: &Agent{
			TmuxSession: "shell-1",
			OutputBuf:   buffer,
		},
	}}
	p.terminalHistory[terminalHistoryKey("shell", "shell-1")] = tty.HistoryReach{
		HistorySize:   1200,
		Loading:       true,
		PendingScroll: 20,
	}

	p.applyTerminalHistory(terminalHistoryLoadedMsg{
		Source: terminalHistorySource{
			Key:    terminalHistoryKey("shell", "shell-1"),
			Target: "shell-1",
			Buffer: buffer,
		},
		Capture: tty.CaptureRange{
			Output:      numberedTerminalLines(0, 600),
			HistorySize: 1200,
			StartLine:   0,
			EndLine:     600,
		},
	})

	start, end, ok := buffer.AbsoluteRange()
	if !ok || start != 0 || end != 1220 {
		t.Fatalf("absolute range = [%d,%d) ok=%v, want [0,1220)", start, end, ok)
	}
	if p.previewScroll != 20 {
		t.Fatalf("preview scroll = %d, want the 20-line scroll replayed, unmoved by the prepend", p.previewScroll)
	}
	state := p.terminalHistory[terminalHistoryKey("shell", "shell-1")]
	if state.Loading || !state.Exhausted {
		t.Fatalf("history state = %#v, want idle and exhausted", state)
	}
}

func TestTerminalViewportScrollbarUsesAbsolutePosition(t *testing.T) {
	buffer := tty.NewOutputBuffer(100)
	buffer.UpdateSnapshot(numberedTerminalLines(80, 20), 80)
	result := renderTerminalViewport(terminalViewportInput{
		Buffer:       buffer,
		Width:        20,
		Height:       5,
		Offset:       0,
		AbsoluteBase: 80,
		TotalItems:   100,
	}, ui.NewTruncateCache(32))

	if !result.Layout.ShowScrollbar {
		t.Fatal("expected scrollbar for partially loaded terminal history")
	}
	if result.Layout.AbsoluteStart != 80 {
		t.Fatalf("absolute start = %d, want 80", result.Layout.AbsoluteStart)
	}
	if result.Layout.DisplayWidth != 19 {
		t.Fatalf("display width = %d, want 19 with reserved scrollbar column", result.Layout.DisplayWidth)
	}
}

func TestTerminalHistorySummaryReportsBufferBaseWithoutTrackedHistory(t *testing.T) {
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.UpdateSnapshot(numberedTerminalLines(600, 620), 600)
	// No shells, no worktrees: terminalHistoryFor finds no source for this
	// buffer. The buffer's own absolute range is still the coordinate space
	// selection and search matches were recorded in.
	p := New()

	base, total, loading := p.terminalHistorySummary(false, buffer)
	if base != 600 {
		t.Fatalf("absolute base = %d, want 600 (buffer's own base, not 0)", base)
	}
	if total != 1220 {
		t.Fatalf("total items = %d, want 1220 (absolute end, not loaded line count)", total)
	}
	if loading {
		t.Fatal("loading = true without tracked history state")
	}
}

func TestTerminalHistorySummaryMatchesSearchCoordinates(t *testing.T) {
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.UpdateSnapshot(numberedTerminalLines(600, 620), 600)
	p := New()
	p.interactiveState = &InteractiveState{}

	// recomputeTerminalSearch and revealTerminalSearchMatch derive match lines
	// straight from the buffer. The render path maps them back through
	// terminalHistorySummary, so the two must agree whether or not the buffer
	// has tracked history state.
	wantBase, _, _ := buffer.AbsoluteRange()
	for _, tracked := range []bool{false, true} {
		if tracked {
			p.shellSelected = true
			p.shells = []*ShellSession{{
				TmuxName: "shell-1",
				Agent:    &Agent{TmuxSession: "shell-1", OutputBuf: buffer},
			}}
			p.terminalHistory[terminalHistoryKey("shell", "shell-1")] = tty.HistoryReach{HistorySize: 1200}
		}
		base, _, _ := p.terminalHistorySummary(false, buffer)
		if base != wantBase {
			t.Fatalf("tracked=%v: summary base = %d, want %d", tracked, base, wantBase)
		}
	}
}

func TestTerminalHistoryAccumulatesScrollIntentWhileLoading(t *testing.T) {
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.UpdateSnapshot(numberedTerminalLines(600, 620), 600)
	p := New()
	p.shellSelected = true
	p.shells = []*ShellSession{{
		TmuxName: "shell-1",
		Agent:    &Agent{TmuxSession: "shell-1", OutputBuf: buffer},
	}}
	key := terminalHistoryKey("shell", "shell-1")
	p.terminalHistory[key] = tty.HistoryReach{HistorySize: 1200}

	if cmd := p.loadOlderTerminalHistory(false, 1); cmd == nil {
		t.Fatal("first history intent did not start a request")
	}
	if cmd := p.loadOlderTerminalHistory(false, 20); cmd != nil {
		t.Fatal("second history intent started a duplicate in-flight request")
	}
	state := p.terminalHistory[key]
	if !state.Loading || state.PendingScroll != 21 {
		t.Fatalf("in-flight state = %#v, want loading with 21 pending lines", state)
	}

	p.applyTerminalHistory(terminalHistoryLoadedMsg{
		Source: terminalHistorySource{Key: key, Target: "shell-1", Buffer: buffer},
		Capture: tty.CaptureRange{
			Output:      numberedTerminalLines(0, 600),
			HistorySize: 1200,
			StartLine:   0,
			EndLine:     600,
		},
		RequestGen: state.RequestGen,
	})
	if p.previewScroll != 21 {
		t.Fatalf("preview scroll = %d, want the accumulated 21-line intent replayed", p.previewScroll)
	}
}

func TestTerminalHistoryLateResponseCannotLeaveLiveView(t *testing.T) {
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.UpdateSnapshot(numberedTerminalLines(600, 620), 600)
	p := New()
	p.shellSelected = true
	p.shells = []*ShellSession{{
		TmuxName: "shell-1",
		Agent:    &Agent{TmuxSession: "shell-1", OutputBuf: buffer},
	}}
	key := terminalHistoryKey("shell", "shell-1")
	p.terminalHistory[key] = tty.HistoryReach{HistorySize: 1200}
	if p.loadOlderTerminalHistory(false, 20) == nil {
		t.Fatal("history request did not start")
	}
	oldGen := p.terminalHistory[key].RequestGen
	p.cancelTerminalHistoryIntent(false)
	p.jumpPreviewWindow(0)

	p.applyTerminalHistory(terminalHistoryLoadedMsg{
		Source: terminalHistorySource{Key: key, Target: "shell-1", Buffer: buffer},
		Capture: tty.CaptureRange{
			Output:      numberedTerminalLines(0, 600),
			HistorySize: 1200,
			StartLine:   0,
			EndLine:     600,
		},
		RequestGen: oldGen,
	})
	start, _, _ := buffer.AbsoluteRange()
	if start != 600 || p.previewScroll != 0 {
		t.Fatalf("late response changed live view: base=%d scroll=%d", start, p.previewScroll)
	}
}

func TestTrimCapturedOutputAdvancesAbsoluteBaseByRemovedRows(t *testing.T) {
	output := "row-100\nrow-101\nrow-102\n"
	trimmed, removed := trimCapturedOutputRows(output, 12)
	if trimmed != "row-102\n" || removed != 2 {
		t.Fatalf("trim = %q removed=%d, want row-102 and 2", trimmed, removed)
	}
	buffer := tty.NewOutputBuffer(10)
	captureBase := 100 + removed
	buffer.UpdateSnapshot(trimmed, captureBase)
	start, end, ok := buffer.AbsoluteRange()
	if !ok || start != 102 || end != 103 {
		t.Fatalf("trimmed absolute range = [%d,%d) ok=%v, want [102,103)", start, end, ok)
	}
}

func TestTrimCapturedOutputPreservesSingleOversizedRow(t *testing.T) {
	output := strings.Repeat("x", 100)
	trimmed, removed := trimCapturedOutputRows(output, 10)
	if trimmed != output || removed != 0 {
		t.Fatalf("oversized row became partial: len=%d removed=%d", len(trimmed), removed)
	}
}

func TestTrimCapturedOutputHonorsExactLineBoundary(t *testing.T) {
	output := "row1\nrow2\n"
	trimmed, removed := trimCapturedOutputRows(output, len("row2\n"))
	if trimmed != "row2\n" || removed != 1 {
		t.Fatalf("exact-boundary trim = %q removed=%d, want row2 and 1", trimmed, removed)
	}
}

func TestTrimCapturedOutputDropsPrefixesBeforeOversizedFinalRow(t *testing.T) {
	tail := strings.Repeat("x", 100)
	output := "prefix1\nprefix2\n" + tail
	trimmed, removed := trimCapturedOutputRows(output, 10)
	if trimmed != tail || removed != 2 {
		t.Fatalf("oversized-tail trim len=%d removed=%d, want tail-only and 2", len(trimmed), removed)
	}
}
