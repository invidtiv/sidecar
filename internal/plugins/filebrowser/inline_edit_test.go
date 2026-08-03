package filebrowser

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
)

func TestNormalizeEditorName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Direct names
		{"vim", "vim"},
		{"nano", "nano"},
		{"emacs", "emacs"},
		{"helix", "helix"},
		{"micro", "micro"},
		{"kakoune", "kakoune"},
		{"joe", "joe"},
		{"ne", "ne"},
		{"amp", "amp"},

		// Aliases map to canonical names
		{"nvim", "vim"},
		{"neovim", "vim"},
		{"vi", "vim"},
		{"hx", "helix"},
		{"kak", "kakoune"},
		{"emacsclient", "emacs"},

		// Full paths
		{"/usr/bin/vim", "vim"},
		{"/usr/local/bin/nvim", "vim"},
		{"/opt/homebrew/bin/hx", "helix"},
		{"/usr/bin/nano", "nano"},

		// Windows .exe suffix
		{"vim.exe", "vim"},
		{"nvim.exe", "vim"},

		// Unknown editors pass through
		{"code", "code"},
		{"subl", "subl"},
		{"atom", "atom"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := tty.NormalizeEditorName(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeEditorName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCalculateInlineEditorWidth(t *testing.T) {
	tests := []struct {
		name        string
		width       int
		treeVisible bool
		treeWidth   int
		wantMin     int // minimum expected width (exact depends on calculatePaneWidths)
	}{
		{
			name:        "tree hidden, full width",
			width:       100,
			treeVisible: false,
			wantMin:     96, // 100 - 4 (borders + padding)
		},
		{
			name:        "tree visible, default split",
			width:       100,
			treeVisible: true,
			treeWidth:   30,
			wantMin:     60, // previewWidth(69) - 4 = 65 approx
		},
		{
			name:        "narrow window, tree hidden",
			width:       40,
			treeVisible: false,
			wantMin:     36, // 40 - 4
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				width:       tt.width,
				treeVisible: tt.treeVisible,
				treeWidth:   tt.treeWidth,
			}
			got := p.calculateInlineEditorWidth()
			if got < tt.wantMin {
				t.Errorf("calculateInlineEditorWidth() = %d, want >= %d", got, tt.wantMin)
			}
			if got <= 0 {
				t.Errorf("calculateInlineEditorWidth() = %d, want > 0", got)
			}
		})
	}
}

func TestCalculateInlineEditorHeight(t *testing.T) {
	tests := []struct {
		name     string
		height   int
		tabCount int
		want     int
	}{
		{
			name:     "standard height, no tabs",
			height:   24,
			tabCount: 0,
			want:     20, // 24 - 2 (borders) - 2 (header + empty line)
		},
		{
			name:     "standard height, with tabs",
			height:   24,
			tabCount: 2,
			want:     19, // 24 - 2 (borders) - 2 (header + empty line) - 1 (tab line)
		},
		{
			name:     "minimum height clamp",
			height:   4,
			tabCount: 0,
			want:     5, // clamped to minimum
		},
		{
			name:     "very small height",
			height:   2,
			tabCount: 0,
			want:     5, // clamped to minimum (height < 4 becomes 4)
		},
		{
			name:     "tall terminal",
			height:   50,
			tabCount: 0,
			want:     46, // 50 - 2 - 2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				height: tt.height,
			}
			// Set up tabs to match tabCount
			for i := 0; i < tt.tabCount; i++ {
				p.tabs = append(p.tabs, FileTab{Path: "test"})
			}
			got := p.calculateInlineEditorHeight()
			if got != tt.want {
				t.Errorf("calculateInlineEditorHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCalculateInlineEditorMouseCoords(t *testing.T) {
	tests := []struct {
		name        string
		width       int
		height      int
		treeVisible bool
		treeWidth   int
		tabCount    int
		clickX      int
		clickY      int
		wantCol     int
		wantRow     int
		wantOK      bool
	}{
		{
			name:        "valid click in editor area, tree visible",
			width:       100,
			height:      24,
			treeVisible: true,
			treeWidth:   30,
			tabCount:    0,
			clickX:      33, // previewX(31) + border(1) + padding(1) + 0
			clickY:      2,  // border(1) + header(1) = content start
			wantCol:     1,  // 1-indexed
			wantRow:     1,  // 1-indexed
			wantOK:      true,
		},
		{
			name:        "click at origin with tree hidden",
			width:       80,
			height:      24,
			treeVisible: false,
			treeWidth:   0,
			tabCount:    0,
			clickX:      2, // border(1) + padding(1) = content start
			clickY:      2, // border(1) + header(1) = content start
			wantCol:     1,
			wantRow:     1,
			wantOK:      true,
		},
		{
			name:        "click outside bounds (too far left)",
			width:       100,
			height:      24,
			treeVisible: true,
			treeWidth:   30,
			tabCount:    0,
			clickX:      0, // in tree pane
			clickY:      5,
			wantCol:     0,
			wantRow:     0,
			wantOK:      false,
		},
		{
			name:        "zero dimensions",
			width:       0,
			height:      0,
			treeVisible: false,
			clickX:      5,
			clickY:      5,
			wantCol:     0,
			wantRow:     0,
			wantOK:      false,
		},
		{
			name:        "with tabs shifts Y offset down",
			width:       100,
			height:      24,
			treeVisible: true,
			treeWidth:   30,
			tabCount:    3,
			clickX:      33, // same content X as first test
			clickY:      3,  // border(1) + tab(1) + header(1) = content start
			wantCol:     1,
			wantRow:     1,
			wantOK:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				width:       tt.width,
				height:      tt.height,
				treeVisible: tt.treeVisible,
				treeWidth:   tt.treeWidth,
			}
			for i := 0; i < tt.tabCount; i++ {
				p.tabs = append(p.tabs, FileTab{Path: "test"})
			}

			col, row, ok := p.calculateInlineEditorMouseCoords(tt.clickX, tt.clickY)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK {
				if col != tt.wantCol {
					t.Errorf("col = %d, want %d", col, tt.wantCol)
				}
				if row != tt.wantRow {
					t.Errorf("row = %d, want %d", row, tt.wantRow)
				}
			}
		})
	}
}

// The editor draws a pane larger than this viewport clipped and scrolled, so a
// forwarded click has to be mapped through the same fit or it lands on the
// wrong character (td-73fa86).
func TestCalculateInlineEditorMouseCoordsFollowsClippedPane(t *testing.T) {
	p := &Plugin{width: 80, height: 24}
	p.inlineEditor = tty.New(nil)
	p.inlineEditor.Width = p.calculateInlineEditorWidth()
	p.inlineEditor.Height = p.calculateInlineEditorHeight()
	p.inlineEditor.Enter("sidecar-edit", "")
	// Another instance resized the shared session: the pane is wider and taller
	// than this viewport, with the cursor near its bottom-right.
	p.inlineEditor.State.PaneWidth = p.inlineEditor.Width + 40
	p.inlineEditor.State.PaneHeight = p.inlineEditor.Height + 10
	p.inlineEditor.State.CursorCol = p.inlineEditor.State.PaneWidth - 1
	p.inlineEditor.State.CursorRow = p.inlineEditor.State.PaneHeight - 1
	p.inlineEditor.State.CursorVisible = true

	col, row, ok := p.calculateInlineEditorMouseCoords(2, 2)
	if !ok {
		t.Fatal("top-left content cell reported no hit")
	}
	if col != 41 || row != 11 {
		t.Fatalf("coords = (%d,%d), want (41,11) — the pane cell actually drawn there", col, row)
	}
}

func TestSendEditorSaveAndQuit_KnownEditors(t *testing.T) {
	// Test that known editors return true (sequence is sent)
	// We can't test the actual tmux commands without a session,
	// but we can verify the function recognizes known editors.
	known := []string{
		"vim", "nvim", "vi", "nano", "emacs", "emacsclient",
		"helix", "hx", "micro", "kakoune", "kak", "joe", "ne", "amp",
	}

	for _, editor := range known {
		t.Run(editor, func(t *testing.T) {
			// sendEditorSaveAndQuit will fail (no tmux session) but should still
			// return true for recognized editors
			got := (tty.EditorSession{Name: "nonexistent-session", Editor: editor}).SaveAndQuit()
			if !got {
				t.Errorf("sendEditorSaveAndQuit(_, %q) = false, want true (known editor)", editor)
			}
		})
	}
}

func TestSendEditorSaveAndQuit_UnknownEditors(t *testing.T) {
	unknown := []string{"code", "subl", "atom", "gedit", "notepad"}

	for _, editor := range unknown {
		t.Run(editor, func(t *testing.T) {
			got := (tty.EditorSession{Name: "nonexistent-session", Editor: editor}).SaveAndQuit()
			if got {
				t.Errorf("sendEditorSaveAndQuit(_, %q) = true, want false (unknown editor)", editor)
			}
		})
	}
}

func TestInlineEditorUsesAnsiPreservation(t *testing.T) {
	// Verify that the tty.Model's default config includes scrollback lines,
	// which is used with CapturePaneOutput (which includes -e flag).
	m := tty.New(nil)
	if m.Config.ScrollbackLines <= 0 {
		t.Errorf("default ScrollbackLines = %d, want > 0 for capture-pane history", m.Config.ScrollbackLines)
	}
}

func TestInlineEditorNativeCursorAndMouseMode(t *testing.T) {
	p := New()
	p.width = 100
	p.height = 24
	p.focused = true
	p.activePane = PanePreview
	p.treeVisible = true
	p.treeWidth = 30
	p.inlineEditMode = true
	p.inlineEditor.Enter("editor", "")
	p.inlineEditor.Width = p.calculateInlineEditorWidth()
	p.inlineEditor.Height = p.calculateInlineEditorHeight()
	p.inlineEditor.State.OutputBuf.Write("one\ntwo")
	p.inlineEditor.State.CursorVisible = true
	p.inlineEditor.State.CursorRow = 1
	p.inlineEditor.State.CursorCol = 3
	p.inlineEditor.State.PaneHeight = p.inlineEditor.Height

	cursor := p.Cursor()
	if cursor == nil || cursor.X != 36 || cursor.Y != 3 {
		t.Fatalf("Cursor() = %#v, want plugin-local (36,3)", cursor)
	}
	if mode := p.PreferredMouseMode(); mode != tea.MouseModeCellMotion {
		t.Fatalf("PreferredMouseMode() = %v, want cell motion", mode)
	}

	p.showExitConfirmation = true
	if cursor := p.Cursor(); cursor != nil {
		t.Fatalf("confirmation-covered Cursor() = %#v, want nil", cursor)
	}
	if mode := p.PreferredMouseMode(); mode != tea.MouseModeAllMotion {
		t.Fatalf("confirmation mouse mode = %v, want all motion", mode)
	}
}
