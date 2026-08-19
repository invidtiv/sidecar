package filebrowser

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
)

func TestInlineEditStartedRejectsStaleProjectActivation(t *testing.T) {
	logPath := installFilebrowserFakeTmux(t)
	p := New()
	p.ctx = &plugin.Context{WorkDir: t.TempDir(), Epoch: 9, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.edit.Activation = 4

	_, cmd := p.Update(InlineEditStartedMsg{
		SessionName: "stale-editor", FilePath: "old.md", Editor: "nvim",
		Activation: 3, Epoch: 8,
	})
	if cmd == nil {
		t.Fatal("stale editor start did not schedule orphan cleanup")
	}
	_ = cmd()
	if p.edit.Active || p.edit.Model.IsActive() {
		t.Fatal("stale editor start activated the current file browser")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "kill-session -t stale-editor") {
		t.Fatalf("stale editor session was not cleaned up; log:\n%s", data)
	}
}

func TestAttachToInlineEditSessionGatedByFullAttachFlag(t *testing.T) {
	installFilebrowserFakeTmux(t)
	p := New()
	p.edit.Name = "sidecar-edit-test"
	if p.edit.Model.Config.AttachKey != "" {
		t.Fatalf("inline editor AttachKey = %q, want empty by default", p.edit.Model.Config.AttachKey)
	}
	if cmd := p.attachToInlineEditSession(); cmd != nil {
		t.Fatal("attachToInlineEditSession ran with tmux_full_attach off")
	}
	if p.edit.Name != "sidecar-edit-test" {
		t.Fatal("gated attach exited the editor")
	}

	cfg := config.Default()
	cfg.Features.Flags[features.TmuxFullAttach.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	p.applyInlineEditorAttachKey()
	if p.edit.Model.Config.AttachKey == "" {
		t.Fatal("inline editor AttachKey stayed empty with tmux_full_attach on")
	}
	if cmd := p.attachToInlineEditSession(); cmd == nil {
		t.Fatal("attachToInlineEditSession did nothing with tmux_full_attach on")
	}
}

func TestStaleInlineEditStartNeverKillsCurrentSameNamedSession(t *testing.T) {
	logPath := installFilebrowserFakeTmux(t)
	p := New()
	p.ctx = &plugin.Context{WorkDir: t.TempDir(), Epoch: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.edit.Activation = 9
	p.edit.Active = true
	p.edit.Name = "current-editor"
	p.edit.Path = "current.md"
	p.edit.EditorCmd = "nvim"
	p.edit.Model.Open(tty.Target{Session: "current-editor"})

	_, cmd := p.Update(InlineEditStartedMsg{
		SessionName: "current-editor", FilePath: "old.md", Editor: "nvim",
		Activation: 8, Epoch: 1,
	})
	if cmd != nil {
		_ = cmd()
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "kill-session -t current-editor") {
		t.Fatalf("stale start killed the current active editor; log:\n%s", data)
	}
	if !p.edit.Model.IsActive() || p.edit.Name != "current-editor" {
		t.Fatal("stale start disturbed current editor state")
	}
}

func TestInlineEditorTabReentryUsesFreshModelScope(t *testing.T) {
	installFilebrowserFakeTmux(t)
	p := New()
	p.ctx = &plugin.Context{WorkDir: t.TempDir(), Epoch: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.tabs = []FileTab{{Path: "note.md", EditSession: "editor", EditEditor: "nvim"}}
	p.activeTab = 0
	p.edit.Active = true
	p.edit.Name = "editor"
	p.edit.Path = "note.md"
	p.edit.EditorCmd = "nvim"
	p.edit.Model.Enter("editor", "")
	oldScope := p.edit.Model.Scope()

	p.saveEditStateToTab()
	p.clearPluginEditState()
	if p.edit.Model.IsActive() {
		t.Fatal("inactive tab retained terminal authority")
	}
	if !p.restoreEditStateFromTab() {
		t.Fatal("live editor session was not restored")
	}
	_ = p.reattachInlineEditSession()
	newScope := p.edit.Model.Scope()
	if newScope.Generation == oldScope.Generation {
		t.Fatalf("reattach reused model generation %d", newScope.Generation)
	}

	// A capture from the inactive activation must not seed the reattached view.
	_, _ = p.Update(tty.CaptureResultMsg{
		Scope: oldScope, Target: "editor", Output: "STALE FRAME",
		PollGeneration: 1, PaneWidth: 80, PaneHeight: 20,
	})
	if got := p.edit.Model.View(); strings.Contains(got, "STALE FRAME") {
		t.Fatalf("stale inactive-tab frame reached reattached editor: %q", got)
	}
}

func installFilebrowserFakeTmux(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_TEST_LOG"
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TEST_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

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
	p.edit.Model = tty.New(nil)
	p.edit.Model.Width = p.calculateInlineEditorWidth()
	p.edit.Model.Height = p.calculateInlineEditorHeight()
	p.edit.Model.Enter("sidecar-edit", "")
	// Another instance resized the shared session: the pane is wider and taller
	// than this viewport, with the cursor near its bottom-right.
	p.edit.Model.State.PaneWidth = p.edit.Model.Width + 40
	p.edit.Model.State.PaneHeight = p.edit.Model.Height + 10
	p.edit.Model.State.CursorCol = p.edit.Model.State.PaneWidth - 1
	p.edit.Model.State.CursorRow = p.edit.Model.State.PaneHeight - 1
	p.edit.Model.State.CursorVisible = true

	col, row, ok := p.calculateInlineEditorMouseCoords(2, 2)
	if !ok {
		t.Fatal("top-left content cell reported no hit")
	}
	if col != 41 || row != 11 {
		t.Fatalf("coords = (%d,%d), want (41,11) — the pane cell actually drawn there", col, row)
	}
}

// A pane smaller than the viewport is letterboxed, so a click in the padding is
// not a pane cell at all — it must be dropped rather than falling back to the
// raw mapping and forwarding a coordinate outside the pane (td-73fa86).
func TestCalculateInlineEditorMouseCoordsRejectsLetterboxPadding(t *testing.T) {
	p := &Plugin{width: 100, height: 30}
	p.edit.Model = tty.New(nil)
	p.edit.Model.Width = p.calculateInlineEditorWidth()
	p.edit.Model.Height = p.calculateInlineEditorHeight()
	p.edit.Model.Enter("sidecar-edit", "")
	// Another instance on a smaller terminal drives the shared session.
	p.edit.Model.State.PaneWidth = p.edit.Model.Width - 10
	p.edit.Model.State.PaneHeight = p.edit.Model.Height - 5

	// Inside the pane: mapped normally.
	if col, row, ok := p.calculateInlineEditorMouseCoords(2, 2); !ok || col != 1 || row != 1 {
		t.Fatalf("in-pane coords = (%d,%d,%v), want (1,1,true)", col, row, ok)
	}
	// Past the pane's right edge but inside the editor viewport.
	if col, row, ok := p.calculateInlineEditorMouseCoords(2+p.edit.Model.State.PaneWidth, 2); ok {
		t.Fatalf("click in horizontal letterbox padding = (%d,%d,true), want no hit", col, row)
	}
	// Past the pane's bottom edge but inside the editor viewport.
	if col, row, ok := p.calculateInlineEditorMouseCoords(2, 2+p.edit.Model.State.PaneHeight); ok {
		t.Fatalf("click in vertical letterbox padding = (%d,%d,true), want no hit", col, row)
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
	p.edit.Active = true
	p.edit.Model.Enter("editor", "")
	p.edit.Model.Width = p.calculateInlineEditorWidth()
	p.edit.Model.Height = p.calculateInlineEditorHeight()
	p.edit.Model.State.OutputBuf.Write("one\ntwo")
	p.edit.Model.State.CursorVisible = true
	p.edit.Model.State.CursorRow = 1
	p.edit.Model.State.CursorCol = 3
	p.edit.Model.State.PaneHeight = p.edit.Model.Height

	cursor := p.Cursor()
	if cursor == nil || cursor.X != 36 || cursor.Y != 3 {
		t.Fatalf("Cursor() = %#v, want plugin-local (36,3)", cursor)
	}
	if mode := p.PreferredMouseMode(); mode != tea.MouseModeCellMotion {
		t.Fatalf("PreferredMouseMode() = %v, want cell motion", mode)
	}

	p.edit.ShowExitConfirm = true
	if cursor := p.Cursor(); cursor != nil {
		t.Fatalf("confirmation-covered Cursor() = %#v, want nil", cursor)
	}
	if mode := p.PreferredMouseMode(); mode != tea.MouseModeAllMotion {
		t.Fatalf("confirmation mouse mode = %v, want all motion", mode)
	}
}

// Rejecting an out-of-pane click is only half the job: the press handler used
// to fall through to inlineEditor.Update on a miss, which forwards absolute
// screen coordinates to tmux — further outside the pane than the raw mapping it
// replaced. A padding click must be dropped outright, as hover and release do.
func TestInlineEditorPressInLetterboxPaddingIsDropped(t *testing.T) {
	p := New()
	p.width, p.height = 100, 30
	p.treeWidth, p.previewWidth = 30, 60
	p.edit.Model = tty.New(nil)
	p.edit.Model.Width = p.calculateInlineEditorWidth()
	p.edit.Model.Height = p.calculateInlineEditorHeight()
	p.edit.Model.Enter("sidecar-edit", "")
	// Another instance on a smaller terminal drives the shared session.
	p.edit.Model.State.PaneWidth = p.edit.Model.Width - 10
	p.edit.Model.State.PaneHeight = p.edit.Model.Height - 5
	p.edit.Model.State.MouseReportingEnabled = true
	p.edit.Active = true

	// Find the pane's left edge, then step one column past its right edge.
	padY := 10
	originX := -1
	for x := 0; x < p.width; x++ {
		if _, _, ok := p.calculateInlineEditorMouseCoords(x, padY); ok {
			originX = x
			break
		}
	}
	if originX < 0 {
		t.Fatal("no in-pane column found on the test row")
	}
	padX := originX + p.edit.Model.State.PaneWidth
	if _, _, ok := p.calculateInlineEditorMouseCoords(padX, padY); ok {
		t.Fatalf("(%d,%d) is inside the pane; pick a padding cell", padX, padY)
	}

	p.mouseHandler.Clear()
	p.mouseHandler.HitMap.AddRect(regionPreviewPane, 30, 0, 70, 30, nil)

	_, cmd := p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: padX, Y: padY, Button: tea.MouseLeft}))
	if cmd != nil {
		t.Fatal("press on letterbox padding produced a command; it must be dropped, not forwarded to tmux")
	}
	if p.edit.Dragging {
		t.Fatal("press on letterbox padding started a drag")
	}
}
