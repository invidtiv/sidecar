package workspace

import (
	"fmt"
	"strings"
	"testing"

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
	p.terminalHistory[terminalHistoryKey("shell", "shell-1")] = terminalHistoryState{
		HistorySize: 1200,
		Loading:     true,
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
		ScrollLines: 20,
	})

	start, end, ok := buffer.AbsoluteRange()
	if !ok || start != 0 || end != 1220 {
		t.Fatalf("absolute range = [%d,%d) ok=%v, want [0,1220)", start, end, ok)
	}
	if p.previewOffset != 580 {
		t.Fatalf("preview offset = %d, want 580 after preserving old content and replaying 20-line scroll", p.previewOffset)
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
			p.terminalHistory[terminalHistoryKey("shell", "shell-1")] = terminalHistoryState{HistorySize: 1200}
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
	p.terminalHistory[key] = terminalHistoryState{HistorySize: 1200}

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
	if p.previewOffset != 579 {
		t.Fatalf("preview offset = %d, want 579 after replaying accumulated 21-line intent", p.previewOffset)
	}
}

func TestTerminalHistoryLateResponseCannotLeaveLiveView(t *testing.T) {
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.UpdateSnapshot(numberedTerminalLines(600, 620), 600)
	p := New()
	p.shellSelected = true
	p.autoScrollOutput = true
	p.shells = []*ShellSession{{
		TmuxName: "shell-1",
		Agent:    &Agent{TmuxSession: "shell-1", OutputBuf: buffer},
	}}
	key := terminalHistoryKey("shell", "shell-1")
	p.terminalHistory[key] = terminalHistoryState{HistorySize: 1200}
	if p.loadOlderTerminalHistory(false, 20) == nil {
		t.Fatal("history request did not start")
	}
	oldGen := p.terminalHistory[key].RequestGen
	p.cancelTerminalHistoryIntent(false)
	p.previewOffset = p.getMaxScrollOffset()
	p.autoScrollOutput = true

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
	if start != 600 || !p.autoScrollOutput {
		t.Fatalf("late response changed live view: base=%d follow=%v", start, p.autoScrollOutput)
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
