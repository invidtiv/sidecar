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
			// The scrollbar reserves the viewport's final column unconditionally
			// (td-0818ef), so a pane sized to the viewport shows one column less
			// — chrome, not a mismatch, hence no indicator.
			name:       "equal size",
			paneWidth:  10,
			paneHeight: 4,
			wantLines:  []string{"000000000", "111111111", "222222222", "333333333"},
			wantWidth:  9,
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
			wantLines:     []string{"000000000", "111111111", "222222222", "333333333"},
			wantWidth:     9,
			wantHeight:    4,
			wantIndicator: "20x8, showing 9x4",
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
			got := terminalTextLines(result)
			if len(got) != len(tt.wantLines) {
				t.Fatalf("rendered %d lines %q, want %d", len(got), got, len(tt.wantLines))
			}
			for i, want := range tt.wantLines {
				if got[i] != want {
					t.Fatalf("line %d = %q, want %q", i, got[i], want)
				}
			}
			// Mirror the view: the banner is gated on the pane's own fit so the
			// scrollbar column never reads as a mismatch.
			var indicator string
			if result.Layout.PaneClipped {
				indicator = tty.PaneSizeIndicator(tt.paneWidth, tt.paneHeight,
					result.Layout.DisplayWidth, result.Layout.DisplayHeight)
			}
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
	// Seven content columns: the scrollbar owns the eighth (td-0818ef).
	layout := calculateTerminalViewportLayout(in)
	if layout.Fit.ColOffset != 7 {
		t.Fatalf("ColOffset = %d, want 7", layout.Fit.ColOffset)
	}
	result := renderTerminalViewport(in, ui.NewTruncateCache(20))
	for i, line := range terminalTextLines(result) {
		if line != "789abcd" {
			t.Fatalf("line %d = %q, want %q", i, line, "789abcd")
		}
	}
	x, y, ok := terminalViewportCursorPosition(in)
	if !ok || x != 6 || y != 1 {
		t.Fatalf("cursor = (%d,%d,%v), want (6,1,true)", x, y, ok)
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
	plain := ansi.Strip(p.renderCapturedTerminal(nil, "hint", buffer, 30, 3, false, "empty"))
	if strings.Contains(plain, "showing") {
		t.Fatalf("unknown geometry surfaced an indicator: %q", plain)
	}

	p.recordPaneGeometry("shell", "sidecar-shell", 40, 6, false)
	plain = ansi.Strip(p.renderCapturedTerminal(nil, "hint", buffer, 30, 3, false, "empty"))
	if !strings.Contains(plain, "40x6, showing 29x2") {
		t.Fatalf("clipped pane did not surface its true size: %q", plain)
	}
	for _, line := range strings.Split(plain, "\n")[1:] {
		if len(line) > 30 {
			t.Fatalf("line %q exceeds the viewport width", line)
		}
	}
}

// The scrollbar takes a column from the content, which is chrome rather than a
// geometry mismatch. Reporting it as one made the "NxM, showing AxB" banner
// permanent for every single-machine user with scrollback (td-73fa86).
func TestRenderCapturedTerminalScrollbarIsNotAMismatch(t *testing.T) {
	p := &Plugin{truncateCache: ui.NewTruncateCache(64)}
	p.shellSelected = true
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = strings.Repeat("x", 10)
	}
	buffer := testTerminalBuffer(strings.Join(lines, "\n"))
	p.shells = []*ShellSession{{
		TmuxName: "sidecar-shell",
		Agent:    &Agent{OutputBuf: buffer, TmuxSession: "sidecar-shell"},
	}}
	p.selectedShellIdx = 0
	p.autoScrollOutput = true

	// Viewport 30x3 renders 30x2 after the hint line, and the pane is exactly
	// that — but 12 lines of scrollback put a scrollbar on screen.
	p.recordPaneGeometry("shell", "sidecar-shell", 30, 2, false)
	plain := ansi.Strip(p.renderCapturedTerminal(nil, "hint", buffer, 30, 3, false, "empty"))
	if strings.Contains(plain, "showing") {
		t.Fatalf("scrollbar column reported as a pane mismatch: %q", plain)
	}

	// A pane that genuinely overflows still says so.
	p.recordPaneGeometry("shell", "sidecar-shell", 40, 6, false)
	plain = ansi.Strip(p.renderCapturedTerminal(nil, "hint", buffer, 30, 3, false, "empty"))
	if !strings.Contains(plain, "showing") {
		t.Fatalf("clipped pane did not surface its true size: %q", plain)
	}
}

// Forwarded tmux mouse coordinates have to move with the pixels on both axes:
// a taller pane starts partway down and a wider one starts partway across, and
// the scrollbar shifts the horizontal window by one more column (td-73fa86).
func TestInteractiveMouseCoordsFollowClippedPane(t *testing.T) {
	p := &Plugin{truncateCache: ui.NewTruncateCache(64)}
	p.width, p.height = 100, 30
	p.viewMode = ViewModeInteractive
	p.previewTab = PreviewTabOutput
	p.shellSelected = true
	p.autoScrollOutput = true

	// 4 lines of scrollback followed by the pane's 35 rows.
	buffer := tty.NewOutputBuffer(200)
	lines := make([]string, 39)
	for i := range lines {
		lines[i] = strings.Repeat("x", 200)
	}
	buffer.UpdateSnapshot(strings.Join(lines, "\n"), 0)
	p.shells = []*ShellSession{{
		TmuxName: "sidecar-shell",
		Agent:    &Agent{OutputBuf: buffer, TmuxSession: "sidecar-shell"},
	}}
	p.selectedShellIdx = 0
	p.interactiveState = &InteractiveState{
		Active:        true,
		TargetSession: "sidecar-shell",
		PaneWidth:     200,
		PaneHeight:    35,
		CursorRow:     34,
		CursorCol:     190,
		CursorVisible: true,
	}

	// Content origin: border+padding across, border+hint down.
	col, row, ok := p.interactiveMouseCoords(panelOverhead/2, 2)
	if !ok {
		t.Fatal("top-left content cell reported no hit")
	}
	if layout := p.terminalSelectionViewportLayout(); !layout.ShowScrollbar {
		t.Fatalf("expected a scrollbar for %d lines in %d rows", layout.EffectiveCount, layout.DisplayHeight)
	}
	// 96 content columns lose one to the scrollbar, so the window is anchored at
	// pane column 96 (190-95+1) and the first visible cell is column 97. The
	// window starts on buffer line 12, which is pane row 8 — not row 1.
	if col != 97 {
		t.Fatalf("col = %d, want 97 (ColOffset with the scrollbar's column accounted for)", col)
	}
	if row != 9 {
		t.Fatalf("row = %d, want 9 (the pane row actually drawn on the top line)", row)
	}
}

func TestPaneGeometryCache(t *testing.T) {
	p := &Plugin{}
	p.recordPaneGeometry("agent", "sidecar-main", 200, 50, false)
	if got := p.paneGeometry[terminalHistoryKey("agent", "sidecar-main")]; got.Width != 200 || got.Height != 50 {
		t.Fatalf("geometry = %+v, want 200x50", got)
	}
	// Unknown geometry never overwrites a good reading.
	p.recordPaneGeometry("agent", "sidecar-main", 0, 0, false)
	if got := p.paneGeometry[terminalHistoryKey("agent", "sidecar-main")]; got.Width != 200 || got.Height != 50 {
		t.Fatalf("geometry = %+v after zero update, want 200x50", got)
	}
	if (paneGeometry{}).known() {
		t.Fatal("zero geometry reported as known")
	}
}
