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
