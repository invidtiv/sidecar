package workspace

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// The viewport renders the pane at the size tmux reports, not the size sidecar
// last requested (td-73fa86).
func TestTerminalViewportActualPaneGeometry(t *testing.T) {
	buffer := testTerminalBuffer("0000000000\n1111111111\n2222222222\n3333333333")

	tests := []struct {
		name          string
		paneWidth     int
		paneHeight    int
		wantLines     []string
		wantWidth     int
		wantHeight    int
		wantIndicator string
	}{
		{
			name:       "equal size",
			paneWidth:  10,
			paneHeight: 4,
			wantLines:  []string{"0000000000", "1111111111", "2222222222", "3333333333"},
			wantWidth:  10,
			wantHeight: 4,
		},
		{
			name:       "pane smaller letterboxes instead of stretching",
			paneWidth:  6,
			paneHeight: 2,
			wantLines:  []string{"222222", "333333"},
			wantWidth:  6,
			wantHeight: 2,
		},
		{
			name:          "pane larger clips to the viewport",
			paneWidth:     20,
			paneHeight:    8,
			wantLines:     []string{"0000000000", "1111111111", "2222222222", "3333333333"},
			wantWidth:     10,
			wantHeight:    4,
			wantIndicator: "20x8, showing 10x4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := terminalViewportInput{
				Buffer: buffer, Width: 10, Height: 4, Follow: true,
				Interactive: true, PaneWidth: tt.paneWidth, PaneHeight: tt.paneHeight,
			}
			result := renderTerminalViewport(in, ui.NewTruncateCache(20))
			if result.Layout.DisplayWidth != tt.wantWidth || result.Layout.DisplayHeight != tt.wantHeight {
				t.Fatalf("display = %dx%d, want %dx%d",
					result.Layout.DisplayWidth, result.Layout.DisplayHeight, tt.wantWidth, tt.wantHeight)
			}
			got := strings.Split(result.Content, "\n")
			if len(got) != len(tt.wantLines) {
				t.Fatalf("rendered %d lines %q, want %d", len(got), got, len(tt.wantLines))
			}
			for i, want := range tt.wantLines {
				if got[i] != want {
					t.Fatalf("line %d = %q, want %q", i, got[i], want)
				}
			}
			indicator := tty.PaneSizeIndicator(tt.paneWidth, tt.paneHeight,
				result.Layout.DisplayWidth, result.Layout.DisplayHeight)
			if indicator != tt.wantIndicator {
				t.Fatalf("indicator = %q, want %q", indicator, tt.wantIndicator)
			}
		})
	}
}

// A pane wider than the viewport scrolls horizontally so the cursor stays
// visible, and the overlay cursor moves with it.
func TestTerminalViewportClipsWidePaneAnchoredOnCursor(t *testing.T) {
	buffer := testTerminalBuffer("0123456789abcdef\n0123456789abcdef")
	in := terminalViewportInput{
		Buffer: buffer, Width: 8, Height: 2, Follow: true,
		Interactive: true, CursorVisible: true, CursorRow: 1, CursorCol: 13,
		PaneWidth: 16, PaneHeight: 2,
	}
	layout := calculateTerminalViewportLayout(in)
	if layout.Fit.ColOffset != 6 {
		t.Fatalf("ColOffset = %d, want 6", layout.Fit.ColOffset)
	}
	result := renderTerminalViewport(in, ui.NewTruncateCache(20))
	for i, line := range strings.Split(result.Content, "\n") {
		if plain := ansi.Strip(line); plain != "6789abcd" {
			t.Fatalf("line %d = %q, want %q", i, plain, "6789abcd")
		}
	}
	x, y, ok := terminalViewportCursorPosition(in)
	if !ok || x != 7 || y != 1 {
		t.Fatalf("cursor = (%d,%d,%v), want (7,1,true)", x, y, ok)
	}
}

// A pane taller than the viewport pins the window to the cursor rather than to
// the pane's trailing blank rows.
func TestTerminalViewportClipsTallPaneAnchoredOnCursor(t *testing.T) {
	buffer := tty.NewOutputBuffer(100)
	lines := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		lines = append(lines, strings.Repeat("x", 4))
	}
	buffer.UpdateSnapshot(strings.Join(lines, "\n"), 0)

	in := terminalViewportInput{
		Buffer: buffer, Width: 8, Height: 4, Follow: true,
		Interactive: true, CursorVisible: true, CursorRow: 2, CursorCol: 0,
		PaneWidth: 8, PaneHeight: 8,
		CursorHistorySize: 4, BufferBase: 0, HasCursorHistory: true,
	}
	layout := calculateTerminalViewportLayout(in)
	// Cursor sits on buffer line 6; without anchoring the window would start at
	// line 8 and drop it.
	if layout.Start != 3 || layout.End != 7 {
		t.Fatalf("window = [%d,%d), want [3,7)", layout.Start, layout.End)
	}
	if _, y, ok := terminalViewportCursorPosition(in); !ok || y != 3 {
		t.Fatalf("cursor y = %d (ok=%v), want 3", y, ok)
	}
}

// List view has no interactive state, so it depends on the cached geometry to
// know how big the pane really is.
func TestRenderCapturedTerminalUsesCachedGeometryInListView(t *testing.T) {
	p := &Plugin{truncateCache: ui.NewTruncateCache(64)}
	p.shellSelected = true
	long := strings.Repeat("0123456789", 4)
	buffer := testTerminalBuffer(long + "\n" + long)
	p.shells = []*ShellSession{{
		TmuxName: "sidecar-shell",
		Agent:    &Agent{OutputBuf: buffer, TmuxSession: "sidecar-shell"},
	}}
	p.selectedShellIdx = 0
	p.autoScrollOutput = true

	// Without geometry the viewport is used as-is.
	plain := ansi.Strip(p.renderCapturedTerminal("hint", buffer, 30, 3, false, "empty"))
	if strings.Contains(plain, "showing") {
		t.Fatalf("unknown geometry surfaced an indicator: %q", plain)
	}

	p.recordPaneGeometry("shell", "sidecar-shell", 40, 6)
	plain = ansi.Strip(p.renderCapturedTerminal("hint", buffer, 30, 3, false, "empty"))
	if !strings.Contains(plain, "40x6, showing 30x2") {
		t.Fatalf("clipped pane did not surface its true size: %q", plain)
	}
	for _, line := range strings.Split(plain, "\n")[1:] {
		if len(line) > 30 {
			t.Fatalf("line %q exceeds the viewport width", line)
		}
	}
}

func TestPaneGeometryCache(t *testing.T) {
	p := &Plugin{}
	p.recordPaneGeometry("agent", "sidecar-main", 200, 50)
	if got := p.paneGeometry[terminalHistoryKey("agent", "sidecar-main")]; got.Width != 200 || got.Height != 50 {
		t.Fatalf("geometry = %+v, want 200x50", got)
	}
	// Unknown geometry never overwrites a good reading.
	p.recordPaneGeometry("agent", "sidecar-main", 0, 0)
	if got := p.paneGeometry[terminalHistoryKey("agent", "sidecar-main")]; got.Width != 200 || got.Height != 50 {
		t.Fatalf("geometry = %+v after zero update, want 200x50", got)
	}
	if (paneGeometry{}).known() {
		t.Fatal("zero geometry reported as known")
	}
}
