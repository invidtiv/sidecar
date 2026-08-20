package notes

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
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

		// Aliases
		{"nvim", "vim"},
		{"neovim", "vim"},
		{"vi", "vim"},
		{"hx", "helix"},
		{"kak", "kakoune"},
		{"emacsclient", "emacs"},

		// Full paths
		{"/usr/bin/vim", "vim"},
		{"/usr/local/bin/nvim", "vim"},
		{"/opt/homebrew/bin/nano", "nano"},

		// Windows .exe suffix
		{"vim.exe", "vim"},

		// Unknown editors pass through
		{"code", "code"},
		{"subl", "subl"},
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

func TestInlineEditMessagesRejectPreviousProjectActivation(t *testing.T) {
	logPath := installNotesFakeTmux(t)
	root := t.TempDir()
	p := New()
	p.ctx = &plugin.Context{
		WorkDir: root, ProjectRoot: root, Epoch: 7,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	p.edit.Activation = 12

	stalePath := filepath.Join(t.TempDir(), "old.md")
	if err := os.WriteFile(stalePath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, cmd := p.Update(InlineEditStartedMsg{
		SessionName: "old-note-editor", NoteID: "old", NotePath: stalePath,
		Editor: "nvim", Activation: 11, Epoch: 6,
	})
	if cmd == nil {
		t.Fatal("stale editor start did not schedule orphan cleanup")
	}
	_ = cmd()
	if p.edit.Active || p.edit.Model.IsActive() {
		t.Fatal("stale editor start activated the new project")
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatal("stale editor start left the export file behind")
	}

	p.edit.Active = true
	p.edit.Name = "current-note-editor"
	p.inlineEditNoteID = "current"
	p.edit.Path = "/tmp/current"
	p.edit.EditorCmd = "nvim"
	p.edit.Model.Enter("current-note-editor", "")
	_, cmd = p.Update(InlineEditExitedMsg{
		NoteID: "old", NotePath: "/tmp/old", Activation: 11, Epoch: 6,
	})
	if cmd != nil {
		t.Fatal("stale editor exit scheduled work against the new project")
	}
	if !p.edit.Active || p.inlineEditNoteID != "current" {
		t.Fatal("stale editor exit cleared the current project editor")
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "kill-session -t old-note-editor") {
		t.Fatalf("stale note editor session was not cleaned up; log:\n%s", data)
	}
}

func TestStaleInlineEditStartNeverKillsCurrentSameNamedSession(t *testing.T) {
	logPath := installNotesFakeTmux(t)
	p := New()
	p.ctx = &plugin.Context{WorkDir: t.TempDir(), ProjectRoot: t.TempDir(), Epoch: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.edit.Activation = 9
	p.edit.Active = true
	p.edit.Name = "current-editor"
	p.inlineEditNoteID = "current"
	p.edit.Path = "/tmp/current"
	p.edit.EditorCmd = "nvim"
	p.edit.Model.Open(tty.Target{Session: "current-editor"})

	_, cmd := p.Update(InlineEditStartedMsg{
		SessionName: "current-editor", NoteID: "old", NotePath: "/tmp/old",
		Editor: "nvim", Activation: 8, Epoch: 1,
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
}

func TestInlineAutoSaveSnapshotsOwningStoreAndAppliesStateOnlyInUpdate(t *testing.T) {
	installNotesFakeTmux(t)
	oldStore, newStore, noteID := makeInlineSaveStores(t)
	notePath := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(notePath, []byte("stale project content"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.ctx = &plugin.Context{Epoch: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.store = oldStore
	p.edit.Active = true
	p.edit.Name = "old-editor"
	p.inlineEditNoteID = noteID
	p.edit.Path = notePath
	p.edit.Activation = 4
	p.inlineAutoSaveGen = 6
	p.inlineLastSavedContent = "old project content"
	cmd := p.performInlineAutoSave()
	if cmd == nil {
		t.Fatal("performInlineAutoSave returned nil")
	}

	// Simulate project replacement before the queued command runs.
	p.store = newStore
	p.ctx = &plugin.Context{Epoch: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.edit.Activation = 5
	p.inlineAutoSaveGen = 7
	p.inlineLastSavedContent = "new project tracker"
	result, ok := cmd().(InlineAutoSaveResultMsg)
	if !ok {
		t.Fatalf("autosave result = %T, want InlineAutoSaveResultMsg", result)
	}

	oldNote, err := oldStore.Get(noteID)
	if err != nil {
		t.Fatal(err)
	}
	newNotes, err := newStore.List(false)
	if err != nil {
		t.Fatal(err)
	}
	if oldNote.Content != "stale project content" {
		t.Fatalf("owning store content = %q, want queued edit", oldNote.Content)
	}
	if len(newNotes) != 1 || newNotes[0].Content != "new project content" {
		t.Fatalf("replacement store was overwritten: %+v", newNotes)
	}
	if p.inlineLastSavedContent != "new project tracker" {
		t.Fatalf("tea.Cmd mutated plugin state: %q", p.inlineLastSavedContent)
	}

	p.edit.Active = true
	p.edit.Name = "new-editor"
	p.edit.Model.Open(tty.Target{Session: "new-editor"})
	_, next := p.Update(result)
	if next != nil || p.inlineLastSavedContent != "new project tracker" {
		t.Fatal("stale autosave result reached the replacement project")
	}
}

func TestInlineAutoSaveUpdatesTrackerOnlyAfterScopedResult(t *testing.T) {
	installNotesFakeTmux(t)
	store, _, noteID := makeInlineSaveStores(t)
	notePath := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(notePath, []byte("current edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New()
	p.ctx = &plugin.Context{Epoch: 3, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.store = store
	p.edit.Active = true
	p.edit.Name = "editor"
	p.inlineEditNoteID = noteID
	p.edit.Path = notePath
	p.edit.Activation = 8
	p.inlineAutoSaveGen = 10
	p.inlineLastSavedContent = "previous edit"
	p.edit.Model.Open(tty.Target{Session: "editor"})

	result, ok := p.performInlineAutoSave()().(InlineAutoSaveResultMsg)
	if !ok {
		t.Fatalf("autosave result = %T, want InlineAutoSaveResultMsg", result)
	}
	if p.inlineLastSavedContent != "previous edit" {
		t.Fatalf("tea.Cmd mutated tracker to %q", p.inlineLastSavedContent)
	}
	_, next := p.Update(result)
	if p.inlineLastSavedContent != "current edit" {
		t.Fatalf("Update left tracker at %q", p.inlineLastSavedContent)
	}
	if next == nil {
		t.Fatal("accepted autosave result did not schedule the next tick")
	}
}

func TestInlineExitSaveSnapshotsOwningStore(t *testing.T) {
	oldStore, newStore, noteID := makeInlineSaveStores(t)
	notePath := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(notePath, []byte("old editor exit content"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New()
	p.ctx = &plugin.Context{Epoch: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.store = oldStore
	p.edit.Activation = 4
	cmd := p.saveNoteAfterInlineExit(noteID, notePath)
	p.store = newStore
	p.ctx = &plugin.Context{Epoch: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.edit.Activation = 5
	p.editorDirty = true
	result, ok := cmd().(NoteContentSavedMsg)
	if !ok {
		t.Fatalf("exit save result = %T, want NoteContentSavedMsg", result)
	}

	oldNote, err := oldStore.Get(noteID)
	if err != nil {
		t.Fatal(err)
	}
	newNotes, err := newStore.List(false)
	if err != nil {
		t.Fatal(err)
	}
	if oldNote.Content != "old editor exit content" {
		t.Fatalf("owning store content = %q, want exit edit", oldNote.Content)
	}
	if len(newNotes) != 1 || newNotes[0].Content != "new project content" {
		t.Fatalf("exit save overwrote replacement store: %+v", newNotes)
	}
	_, followup := p.Update(result)
	if followup != nil {
		t.Fatal("old-project exit result scheduled toast or reload in replacement project")
	}
	if !p.editorDirty {
		t.Fatal("old-project exit result cleared replacement editor dirty state")
	}

	// Epoch equality alone is insufficient when another editor activation has
	// started in the same project.
	_, followup = p.Update(NoteContentSavedMsg{
		ID: noteID, Epoch: 2, EditorActivation: 4,
	})
	if followup != nil || !p.editorDirty {
		t.Fatal("old editor activation mutated the replacement editor")
	}
	_, followup = p.Update(NoteSavedMsg{
		Note: &Note{ID: "stale-note"}, Epoch: 2, EditorActivation: 4,
	})
	if followup != nil || p.pendingEditID != "" {
		t.Fatal("stale inline NoteSavedMsg reached replacement UI state")
	}
}

func makeInlineSaveStores(t *testing.T) (*Store, *Store, string) {
	t.Helper()
	oldStore, err := NewTestStore(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	newStore, err := NewTestStore(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = oldStore.Close()
		_ = newStore.Close()
	})
	oldNote, err := oldStore.Create("note", "old project content")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newStore.Create("note", "new project content"); err != nil {
		t.Fatal(err)
	}
	return oldStore, newStore, oldNote.ID
}

func TestCalculateInlineEditorHeight(t *testing.T) {
	tests := []struct {
		name   string
		height int
		want   int
	}{
		{
			name:   "standard height",
			height: 24,
			want:   21, // 24 - 2 (borders) - 1 (header)
		},
		{
			name:   "minimum height clamp",
			height: 4,
			want:   5, // clamped to minimum
		},
		{
			name:   "very small height",
			height: 2,
			want:   5, // height < 4 becomes 4, then 4 - 2 - 1 = 1, clamped to 5
		},
		{
			name:   "tall terminal",
			height: 50,
			want:   47, // 50 - 2 - 1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				height: tt.height,
			}
			got := p.calculateInlineEditorHeight()
			if got != tt.want {
				t.Errorf("calculateInlineEditorHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCalculateInlineEditorWidth(t *testing.T) {
	tests := []struct {
		name      string
		width     int
		listWidth int
		wantMin   int
	}{
		{
			name:      "standard width",
			width:     100,
			listWidth: 30,
			wantMin:   60, // 100 - 30 - 1 (divider) - 4 (borders+padding) = 65
		},
		{
			name:      "narrow window",
			width:     60,
			listWidth: 20,
			wantMin:   30, // 60 - 20 - 1 - 4 = 35
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				width:     tt.width,
				listWidth: tt.listWidth,
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

func TestCalculateInlineEditorMouseCoords(t *testing.T) {
	tests := []struct {
		name      string
		width     int
		height    int
		listWidth int
		clickX    int
		clickY    int
		wantCol   int
		wantRow   int
		wantOK    bool
	}{
		{
			name:      "valid click at content origin",
			width:     100,
			height:    24,
			listWidth: 30,
			clickX:    33, // listWidth(30) + divider(1) + border(1) + padding(1) = 33
			clickY:    2,  // border(1) + header(1) = content start
			wantCol:   1,
			wantRow:   1,
			wantOK:    true,
		},
		{
			name:      "click in list area (out of bounds)",
			width:     100,
			height:    24,
			listWidth: 30,
			clickX:    5, // in list pane
			clickY:    5,
			wantCol:   0,
			wantRow:   0,
			wantOK:    false,
		},
		{
			name:      "zero dimensions",
			width:     0,
			height:    0,
			listWidth: 0,
			clickX:    5,
			clickY:    5,
			wantCol:   0,
			wantRow:   0,
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				width:     tt.width,
				height:    tt.height,
				listWidth: tt.listWidth,
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
	p := &Plugin{width: 100, height: 24, listWidth: 30}
	p.edit.Model = tty.New(nil)
	p.edit.Model.Width = p.calculateInlineEditorWidth()
	p.edit.Model.Height = p.calculateInlineEditorHeight()
	p.edit.Model.Enter("sidecar-note", "")
	// Another instance resized the shared session: the pane is wider and taller
	// than this viewport, with the cursor near its bottom-right.
	p.edit.Model.State.PaneWidth = p.edit.Model.Width + 40
	p.edit.Model.State.PaneHeight = p.edit.Model.Height + 10
	p.edit.Model.State.CursorCol = p.edit.Model.State.PaneWidth - 1
	p.edit.Model.State.CursorRow = p.edit.Model.State.PaneHeight - 1
	p.edit.Model.State.CursorVisible = true

	col, row, ok := p.calculateInlineEditorMouseCoords(33, 2)
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
	p := &Plugin{width: 100, height: 30, listWidth: 30}
	p.edit.Model = tty.New(nil)
	p.edit.Model.Width = p.calculateInlineEditorWidth()
	p.edit.Model.Height = p.calculateInlineEditorHeight()
	p.edit.Model.Enter("sidecar-note", "")
	// Another instance on a smaller terminal drives the shared session.
	p.edit.Model.State.PaneWidth = p.edit.Model.Width - 10
	p.edit.Model.State.PaneHeight = p.edit.Model.Height - 5

	if col, row, ok := p.calculateInlineEditorMouseCoords(33, 2); !ok || col != 1 || row != 1 {
		t.Fatalf("in-pane coords = (%d,%d,%v), want (1,1,true)", col, row, ok)
	}
	if col, row, ok := p.calculateInlineEditorMouseCoords(33+p.edit.Model.State.PaneWidth, 2); ok {
		t.Fatalf("click in horizontal letterbox padding = (%d,%d,true), want no hit", col, row)
	}
	if col, row, ok := p.calculateInlineEditorMouseCoords(33, 2+p.edit.Model.State.PaneHeight); ok {
		t.Fatalf("click in vertical letterbox padding = (%d,%d,true), want no hit", col, row)
	}
}

func TestSendEditorSaveAndQuit_KnownEditors(t *testing.T) {
	known := []string{
		"vim", "nvim", "vi", "nano", "emacs", "emacsclient",
		"helix", "hx", "micro", "kakoune", "kak",
	}

	for _, editor := range known {
		t.Run(editor, func(t *testing.T) {
			got := (tty.EditorSession{Name: "nonexistent-session", Editor: editor}).SaveAndQuit()
			if !got {
				t.Errorf("sendEditorSaveAndQuit(_, %q) = false, want true", editor)
			}
		})
	}
}

func TestSendEditorSaveAndQuit_UnknownEditors(t *testing.T) {
	unknown := []string{"code", "subl", "atom", "gedit"}

	for _, editor := range unknown {
		t.Run(editor, func(t *testing.T) {
			got := (tty.EditorSession{Name: "nonexistent-session", Editor: editor}).SaveAndQuit()
			if got {
				t.Errorf("sendEditorSaveAndQuit(_, %q) = true, want false", editor)
			}
		})
	}
}

func TestInlineEditorTtyConfig(t *testing.T) {
	// Verify the tty model preserves ANSI sequences via CapturePaneOutput.
	// The -e flag in capture-pane is what enables syntax highlighting.
	m := tty.New(nil)
	if m.Config.ScrollbackLines <= 0 {
		t.Errorf("default ScrollbackLines = %d, want > 0", m.Config.ScrollbackLines)
	}
}

func TestInlineEditorNativeCursorAndMouseMode(t *testing.T) {
	p := New()
	p.width = 100
	p.height = 24
	p.focused = true
	p.activePane = PaneEditor
	p.listWidth = 30
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

func TestStopInvalidatesInlineEditorBeforeProjectSwitch(t *testing.T) {
	logPath := installNotesFakeTmux(t)

	p := New()
	p.edit.Active = true
	p.edit.Name = "old-project-editor"
	p.inlineEditNoteID = "old-note"
	p.edit.Path = "/tmp/old-note"
	p.edit.EditorCmd = "nvim"
	p.inlineLastSavedContent = "old"
	p.inlineAutoSaveGen = 7
	oldGeneration := p.inlineAutoSaveGen

	p.Stop()

	if p.edit.Active || p.edit.Name != "" || p.inlineEditNoteID != "" ||
		p.edit.Path != "" || p.edit.EditorCmd != "" || p.inlineLastSavedContent != "" {
		t.Fatalf("Stop retained old-project inline state: %+v", p)
	}
	if p.inlineAutoSaveGen <= oldGeneration {
		t.Fatalf("autosave generation = %d, want > %d", p.inlineAutoSaveGen, oldGeneration)
	}
	if _, cmd := p.Update(InlineAutoSaveTickMsg{Generation: oldGeneration}); cmd != nil {
		t.Fatal("old-project autosave tick produced a command after Stop")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "kill-session -t old-project-editor") {
		t.Fatalf("Stop did not kill editor session; log:\n%s", data)
	}
}

func TestInitRejectsOldAutosaveTickAgainstNewProjectStore(t *testing.T) {
	logPath := installNotesFakeTmux(t)
	root := t.TempDir()
	seed, err := NewTestStore(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = seed.Close()
	p := New()
	p.edit.Active = true
	p.edit.Name = "old-project-editor"
	p.inlineEditNoteID = "old-note"
	p.edit.Path = "/tmp/old-note"
	p.edit.EditorCmd = "vim"
	p.inlineAutoSaveGen = 11
	oldGeneration := p.inlineAutoSaveGen
	ctx := &plugin.Context{
		WorkDir:     root,
		ProjectRoot: root,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Epoch:       2,
	}
	if err := p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	if p.store == nil {
		t.Fatal("new project store was not initialized")
	}
	if p.edit.Active || p.edit.Name != "" || p.inlineEditNoteID != "" || p.edit.Path != "" {
		t.Fatal("Init retained old-project inline editor state")
	}
	if p.inlineAutoSaveGen <= oldGeneration {
		t.Fatalf("Init autosave generation = %d, want > %d", p.inlineAutoSaveGen, oldGeneration)
	}
	if _, cmd := p.Update(InlineAutoSaveTickMsg{Generation: oldGeneration}); cmd != nil {
		t.Fatal("old-project autosave tick reached new project store")
	}
	if data, err := os.ReadFile(logPath); err == nil && strings.Contains(string(data), "kill-session") {
		t.Fatalf("Init synchronously spawned tmux cleanup:\n%s", data)
	}
	startCmd := p.Start()
	if startCmd == nil {
		t.Fatal("Start did not return orphan cleanup command")
	}
	startResult := startCmd()
	batch, ok := startResult.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Start returned %T, want tea.BatchMsg", startResult)
	}
	for _, cmd := range batch {
		if cmd != nil {
			_ = cmd()
		}
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "kill-session -t old-project-editor") {
		t.Fatalf("Start did not asynchronously clean orphan editor:\n%s", data)
	}
}

func installNotesFakeTmux(t *testing.T) string {
	t.Helper()
	// Editor keystrokes go through tty's ordered send queue, which runs them on
	// a background goroutine. Bracket this test so its log holds only its own
	// commands.
	tty.WaitForPendingSends()
	t.Cleanup(tty.WaitForPendingSends)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_TEST_LOG"
case " $* " in
  *" show-options "*) echo 20000 ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TEST_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// Rejecting an out-of-pane click is only half the job: the press handler used
// to fall through to inlineEditor.Update on a miss, which forwards absolute
// screen coordinates to tmux — further outside the pane than the raw mapping it
// replaced. A padding click must be dropped outright, as hover and release do.
func TestInlineEditorPressInLetterboxPaddingIsDropped(t *testing.T) {
	p := New()
	p.width, p.height = 100, 30
	p.listWidth = 30
	p.edit.Model = tty.New(nil)
	p.edit.Model.Width = p.calculateInlineEditorWidth()
	p.edit.Model.Height = p.calculateInlineEditorHeight()
	p.edit.Model.Enter("sidecar-note", "")
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
	p.mouseHandler.HitMap.AddRect(regionEditorPane, 30, 0, 70, 30, nil)

	_, cmd := p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: padX, Y: padY, Button: tea.MouseLeft}))
	if cmd != nil {
		t.Fatal("press on letterbox padding produced a command; it must be dropped, not forwarded to tmux")
	}
	if p.edit.Dragging {
		t.Fatal("press on letterbox padding started a drag")
	}
}

func TestEnterAndClickStayInSidecar(t *testing.T) {
	installNotesFakeTmux(t)
	p, noteID := newNotesEditorHarness(t)

	// Enter and ctrl+t never leave Sidecar. E deliberately does.
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter},
		{Code: 't', Mod: tea.ModCtrl},
	} {
		_, cmd := p.Update(key)
		assertNotOpenFileMsg(t, key.String(), cmd)
	}

	p.activePane = PaneEditor
	p.previewMode = true
	p.editorNote = &Note{ID: noteID, Content: "old project content"}
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assertNotOpenFileMsg(t, "preview-enter", cmd)
}

// TestEnterOpensTheSimpleEditor pins the default gesture. Enter must land in
// the built-in textarea, not vim: inferring vim from a config value is what
// made the simple editor unreachable.
func TestEnterOpensTheSimpleEditor(t *testing.T) {
	installNotesFakeTmux(t)
	p, _ := newNotesEditorHarness(t)

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		if started, ok := cmd().(InlineEditStartedMsg); ok {
			t.Fatalf("enter started vim (%s), want the built-in editor", started.NoteID)
		}
	}
	if p.edit.Active {
		t.Fatal("enter entered inline vim mode")
	}
	if p.previewMode {
		t.Fatal("enter left the editor read-only, want edit mode")
	}
	if p.activePane != PaneEditor {
		t.Fatal("enter did not focus the editor pane")
	}
}

// TestClickOpensTheSimpleEditor is the same contract for the mouse.
func TestClickOpensTheSimpleEditor(t *testing.T) {
	installNotesFakeTmux(t)
	p, noteID := newNotesEditorHarness(t)
	p.editorNote = &Note{ID: noteID, Content: "old project content"}
	p.previewMode = true
	_ = p.View(p.width, p.height)

	p2, _ := p.handleMouseClick(mouse.MouseAction{
		X: 40, Y: 3,
		Region: &mouse.Region{ID: regionEditorPane, Rect: mouse.Rect{X: 30, Y: 1, W: 40, H: 20}},
	})
	if p2.edit.Active {
		t.Fatal("clicking the note started vim, want the built-in editor")
	}
	if p2.previewMode {
		t.Fatal("clicking the note left it read-only")
	}
}

// TestCapitalEOpensExternalEditor pins the one notes path that still leaves
// Sidecar. It is reachable only from the list and preview.
func TestCapitalEOpensExternalEditor(t *testing.T) {
	installNotesFakeTmux(t)
	p, _ := newNotesEditorHarness(t)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'E', Text: "E"})
	if cmd == nil {
		t.Fatal("E returned no command")
	}
	_ = prepareExternalEditor(t, p, cmd)
}

// TestCapitalEIsTypableInTheSimpleEditor is the regression that kept E out of
// the notes-editor context: the old binding made a capital E unwritable.
func TestCapitalEIsTypableInTheSimpleEditor(t *testing.T) {
	installNotesFakeTmux(t)
	p, _ := newNotesEditorHarness(t)
	p.activePane = PaneEditor
	p.previewMode = false
	p.editorNote = &p.notes[0]
	p.editorTextarea.SetValue("")
	p.editorTextarea.Focus()

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'E', Text: "E"})
	if cmd != nil {
		if _, ok := cmd().(plugin.OpenFileMsg); ok {
			t.Fatal("E in the simple editor opened $EDITOR instead of typing")
		}
	}
	if got := p.editorTextarea.Value(); got != "E" {
		t.Fatalf("textarea = %q, want %q", got, "E")
	}
}

func TestEStartsInlineTTY(t *testing.T) {
	logPath := installNotesFakeTmux(t)
	p, noteID := newNotesEditorHarness(t)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if cmd == nil {
		t.Fatal("e returned no command")
	}
	got := cmd()
	started, ok := got.(InlineEditStartedMsg)
	if !ok {
		t.Fatalf("e produced %T, want InlineEditStartedMsg", got)
	}
	if started.NoteID != noteID {
		t.Fatalf("started note = %q, want %q", started.NoteID, noteID)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new-session") {
		t.Fatalf("e did not start an editor session; log:\n%s", data)
	}
}

func TestNotesEditorCommandsAreAdvertised(t *testing.T) {
	installNotesFakeTmux(t)
	p, _ := newNotesEditorHarness(t)
	want := map[string]bool{"edit-note": false, "vim-edit": false, "external-editor": false}
	for _, cmd := range p.Commands() {
		if _, ok := want[cmd.ID]; !ok {
			continue
		}
		want[cmd.ID] = true
		if cmd.Handler != nil {
			t.Fatalf("%s must not carry a Handler; it leaks a keyless palette row into other plugins", cmd.ID)
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("%s command missing", id)
		}
	}
}

// TestUnavailableEditorToastsInsteadOfOpenFile covers e when tmux is missing.
// It toasts rather than silently falling through; the simple editor on Enter
// is the fallback a user actually has, and it needs no tmux at all.
func TestUnavailableEditorToastsInsteadOfOpenFile(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	p, _ := newNotesEditorHarness(t)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if cmd == nil {
		t.Fatal("e returned no command when tmux is missing")
	}
	got := cmd()
	assertNotOpenFileMsg(t, "missing-tmux", func() tea.Msg { return got })
	if _, ok := got.(msg.ToastMsg); !ok {
		t.Fatalf("missing tmux produced %T, want toast", got)
	}
	if p.edit.Active {
		t.Fatal("failed start left notes in inline edit mode")
	}
}

// TestSimpleEditorNeedsNoTmux is the capability this whole change restores:
// with no tmux on PATH at all, a note is still editable.
func TestSimpleEditorNeedsNoTmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	p, _ := newNotesEditorHarness(t)
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if p.previewMode {
		t.Fatal("enter left the note read-only with no tmux available")
	}
	if p.activePane != PaneEditor {
		t.Fatal("enter did not focus the editor pane")
	}
	p.editorNote = &p.notes[0]
	p.editorTextarea.SetValue("")
	p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if got := p.editorTextarea.Value(); got != "x" {
		t.Fatalf("textarea = %q, want %q; notes are not editable without tmux", got, "x")
	}
}

// TestUnsavedBufferSurvivesHandoffToTheOtherEditors is the data-loss guard.
// Leaving the built-in editor does not save, and autosave is debounced a full
// second. Both other editors materialise the note from the store, so without a
// flush they read the pre-edit copy and write it straight back over the buffer.
func TestUnsavedBufferSurvivesHandoffToTheOtherEditors(t *testing.T) {
	installNotesFakeTmux(t)
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"e-in-pane", tea.KeyPressMsg{Code: 'e', Text: "e"}},
		{"E-external", tea.KeyPressMsg{Code: 'E', Text: "E"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, noteID := newNotesEditorHarness(t)

			// Type into the built-in editor, then leave without waiting for the
			// debounced autosave — the exact keystroke-wide window.
			p.activePane = PaneEditor
			p.previewMode = false
			p.editorNote = &p.notes[0]
			p.editorTextarea.SetValue("edited but not yet autosaved")
			p.editorDirty = true
			_, save := p.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			drainNotesCmd(t, p, save)

			p.Update(tc.key)

			got, err := p.store.Get(noteID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Content != "edited but not yet autosaved" {
				t.Fatalf("store content = %q, want the unsaved buffer; the edit was lost", got.Content)
			}
			if p.editorDirty {
				t.Error("buffer still marked dirty after a successful flush")
			}
		})
	}
}

// TestNotesAttachKeyAlwaysEmpty pins that notes has no full-screen tmux
// experience at all: the embedded pane is the whole vim editor, so ctrl+] has
// nothing to hand a suspended Sidecar off to and must stay inert. This is not
// preference-gated — enabling tmux_full_attach elsewhere must not revive it.
func TestNotesAttachKeyAlwaysEmpty(t *testing.T) {
	installNotesFakeTmux(t)

	// Flag on for the whole test: if anything in the notes path consults it,
	// this is the configuration where a full-screen attach would come back.
	cfg := config.Default()
	cfg.Features.Flags[features.TmuxFullAttach.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	p, noteID := newNotesEditorHarness(t)
	if p.edit.Model.Config.AttachKey != "" {
		t.Fatalf("inline editor AttachKey = %q, want empty", p.edit.Model.Config.AttachKey)
	}

	// Drive the one place OnAttach is wired, rather than asserting on a bare
	// New() that never reaches it.
	notePath := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(notePath, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.handleInlineEditStarted(InlineEditStartedMsg{
		SessionName: "sidecar-note-edit-test",
		NoteID:      noteID,
		NotePath:    notePath,
		Editor:      "vim",
		Activation:  p.edit.Activation,
		Epoch:       p.ctx.Epoch,
	})

	if p.edit.Model.Config.AttachKey != "" {
		t.Fatalf("tmux_full_attach revived the notes attach chord: %q", p.edit.Model.Config.AttachKey)
	}
	if p.edit.Model.OnAttach != nil {
		t.Fatal("notes inline editor wired an OnAttach hook; notes has no full-screen path")
	}
	for _, binding := range keymap.DefaultBindings() {
		if !strings.HasPrefix(binding.Context, "notes-") {
			continue
		}
		if binding.Command == "attach" || binding.Key == "ctrl+]" {
			t.Fatalf("notes revived a full-screen attach binding: %+v", binding)
		}
	}
	for _, command := range p.Commands() {
		if command.ID == "attach" {
			t.Fatalf("notes inline editor advertised an attach footer/palette command: %+v", command)
		}
	}
}

// TestSearchEnterOpensTheMatchedNote pins the note identity through the filter
// teardown: cursor indexes the filtered list, so clearing the filter without
// re-anchoring opens whatever sits at the same offset in the full list.
func TestSearchEnterOpensTheMatchedNote(t *testing.T) {
	installNotesFakeTmux(t)
	p, _ := newNotesEditorHarness(t)
	p.notes = []Note{
		{ID: "nt-a", Title: "alpha", Content: "alpha body"},
		{ID: "nt-b", Title: "beta", Content: "beta body"},
		{ID: "nt-c", Title: "gamma", Content: "gamma body"},
	}
	p.cursor = 0

	p.searchMode = true
	for _, r := range "gam" {
		p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(p.filteredNotes) != 1 {
		t.Fatalf("filtered %d notes, want the single gamma match", len(p.filteredNotes))
	}
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if p.editorNote == nil {
		t.Fatal("search enter loaded no note")
	}
	if p.editorNote.ID != "nt-c" {
		t.Fatalf("search enter opened %q, want nt-c (the matched note)", p.editorNote.ID)
	}
}

// TestExternalEditorWriteBack covers the one path in this change that writes to
// the store from a file: $EDITOR saves while Sidecar is suspended, and the
// refresh on resume is what reads it back.
func TestExternalEditorWriteBack(t *testing.T) {
	p, noteID := newNotesEditorHarness(t)

	cmd := p.openInExternalEditor()
	if cmd == nil {
		t.Fatal("openInExternalEditor returned no command")
	}
	open := prepareExternalEditor(t, p, cmd)
	if p.pendingInlineEditID != noteID {
		t.Fatalf("pendingInlineEditID = %q, want %q", p.pendingInlineEditID, noteID)
	}

	// Stand in for the external editor writing the file.
	if err := os.WriteFile(open.Path, []byte("written by $EDITOR"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, back := p.Update(app.RefreshMsg{})
	if back == nil {
		t.Fatal("refresh after $EDITOR scheduled no read-back")
	}
	if got := back(); got == nil {
		t.Fatal("read-back produced no message")
	} else {
		_, _ = p.Update(got)
	}

	note, err := p.store.Get(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if note.Content != "written by $EDITOR" {
		t.Fatalf("store content = %q, want the $EDITOR write", note.Content)
	}
	if p.pendingInlineEditID != "" || p.pendingInlineEditPath != "" {
		t.Error("pending external-edit state outlived the read-back")
	}
	if _, err := os.Stat(open.Path); !os.IsNotExist(err) {
		t.Error("read-back left the temp file behind")
	}
}

func TestSessionDeathLeavesEditorAndSaves(t *testing.T) {
	installNotesFakeTmux(t)
	store, _, noteID := makeInlineSaveStores(t)
	notePath := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(notePath, []byte("saved after wq"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New()
	p.ctx = &plugin.Context{Epoch: 3, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.store = store
	p.edit.Active = true
	p.edit.Name = "dying-editor"
	p.inlineEditNoteID = noteID
	p.edit.Path = notePath
	p.edit.Activation = 8
	p.edit.Model.Close()

	_, cmd := p.Update(tty.PollTickMsg{})
	if p.edit.Active {
		t.Fatal(":wq / session death left inline edit mode active")
	}
	if cmd == nil {
		t.Fatal("session death did not schedule a save")
	}
	result, ok := cmd().(NoteContentSavedMsg)
	if !ok {
		t.Fatalf("session death save = %T, want NoteContentSavedMsg", result)
	}
	if result.ID != noteID {
		t.Fatalf("saved note = %q, want %q", result.ID, noteID)
	}
	note, err := store.Get(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if note.Content != "saved after wq" {
		t.Fatalf("store content = %q, want saved after wq", note.Content)
	}
}

func TestClickAwayLeavesInlineEditor(t *testing.T) {
	installNotesFakeTmux(t)
	p, noteID := newNotesEditorHarness(t)
	p.edit.Active = true
	p.edit.Name = "live-editor"
	p.inlineEditNoteID = noteID
	p.edit.Path = filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(p.edit.Path, []byte("click away save"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.edit.Model.Enter("live-editor", "")
	p.notes = []Note{{ID: noteID, Title: "note", Content: "old project content"}}
	_ = p.View(p.width, p.height)

	_, cmd := p.Update(tea.MouseClickMsg(tea.Mouse{X: 2, Y: 2, Button: tea.MouseLeft}))
	if p.edit.Active {
		t.Fatal("clicking another note left the editor hanging")
	}
	if cmd == nil {
		t.Fatal("click-away did not schedule a save")
	}
	assertNotOpenFileMsg(t, "click-away", cmd)
}

func TestCtrlTIsNoOpInNotes(t *testing.T) {
	p, _ := newNotesEditorHarness(t)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatalf("ctrl+t produced %T, want no-op", cmd())
	}
	if p.edit.Active {
		t.Fatal("ctrl+t started an editor")
	}
}

func TestInlineEditorHeightFillsPaneWithoutExtraRow(t *testing.T) {
	p := &Plugin{height: 24}
	inner := 24 - 2
	got := p.calculateInlineEditorHeight()
	if got != inner-1 {
		t.Fatalf("editor height = %d, want %d (pane minus borders and header, no extra blank row)", got, inner-1)
	}
}

func newNotesEditorHarness(t *testing.T) (*Plugin, string) {
	t.Helper()
	store, _, noteID := makeInlineSaveStores(t)
	p := New()
	p.ctx = &plugin.Context{Epoch: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.editorTextarea = textarea.New()
	p.store = store
	p.notes = []Note{{ID: noteID, Title: "note", Content: "old project content"}}
	p.cursor = 0
	p.viewFilter = FilterActive
	p.activePane = PaneList
	p.width = 100
	p.height = 24
	p.listWidth = 30
	return p, noteID
}

func assertNotOpenFileMsg(t *testing.T, name string, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	got := cmd()
	if _, ok := got.(plugin.OpenFileMsg); ok {
		t.Fatalf("%s produced plugin.OpenFileMsg; only E may suspend into $EDITOR", name)
	}
}
