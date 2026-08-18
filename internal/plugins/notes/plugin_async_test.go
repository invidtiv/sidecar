package notes

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestStaleAsyncNoteResultsAreDropped(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.store = openTestStore(t)
	p.editorDirty = true

	stale := []tea.Msg{
		NoteDeletedMsg{ID: "n1", Epoch: 1},
		NotePinToggledMsg{ID: "n1", Epoch: 1},
		NoteArchiveToggledMsg{ID: "n1", Epoch: 1},
		NoteRestoredMsg{ID: "n1", Title: "x", Epoch: 1},
		TaskCreatedMsg{TaskID: "td-1", NoteID: "n1", Epoch: 1},
	}
	for _, m := range stale {
		_, cmd := p.Update(m)
		if cmd != nil {
			t.Fatalf("stale %T scheduled work", m)
		}
	}
	if !p.editorDirty {
		t.Fatal("stale results mutated editor dirty state")
	}

	_, cmd := p.Update(NoteDeletedMsg{ID: "n1", Epoch: 2})
	if cmd == nil {
		t.Fatal("current-epoch delete should reload")
	}
}

func TestTaskCreatedErrorIncludesEpochAndToasts(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 7, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.store = openTestStore(t)

	_, cmd := p.Update(TaskCreatedMsg{
		NoteID: "n1",
		Err:    errors.New("td create failed"),
		Epoch:  7,
	})
	if cmd == nil {
		t.Fatal("task-create error produced no toast")
	}
	toast, ok := cmd().(msg.ToastMsg)
	if !ok {
		t.Fatalf("got %T, want error toast", cmd())
	}
	if !toast.IsError || !strings.Contains(toast.Message, "Task creation failed") {
		t.Fatalf("toast = %+v", toast)
	}
}

func TestTaskCreatedArchiveFailureIsPartialSuccess(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 3, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.store = openTestStore(t)

	_, cmd := p.Update(TaskCreatedMsg{
		TaskID:     "td-abc",
		NoteID:     "n1",
		ArchiveErr: errors.New("archive boom"),
		Epoch:      3,
	})
	if cmd == nil {
		t.Fatal("partial success produced no command")
	}
	toast, ok := firstToast(cmd)
	if !ok {
		t.Fatal("partial success produced no toast")
	}
	if !toast.IsError {
		t.Fatal("partial success was reported as a clean success")
	}
	if !strings.Contains(toast.Message, "Created td-abc") || !strings.Contains(toast.Message, "archive failed") {
		t.Fatalf("toast = %q", toast.Message)
	}
}

func TestCreateTaskFromNoteErrorPathSetsEpoch(t *testing.T) {
	dir := t.TempDir()
	fakeTD := filepath.Join(dir, "td")
	if err := os.WriteFile(fakeTD, []byte("#!/bin/sh\necho fail >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	p := New()
	p.ctx = &plugin.Context{
		WorkDir: dir, Epoch: 42,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	p.store = openTestStore(t)
	p.taskModalNote = &Note{ID: "n1", Content: "body"}
	p.taskModalTitleInput = textinput.New()
	p.taskModalTitleInput.SetValue("title")

	cmd := p.createTaskFromNote()
	if cmd == nil {
		t.Fatal("createTaskFromNote returned nil")
	}
	result, ok := cmd().(TaskCreatedMsg)
	if !ok {
		t.Fatalf("got %T, want TaskCreatedMsg", cmd())
	}
	if result.Err == nil {
		t.Fatal("expected td create error")
	}
	if result.Epoch != 42 {
		t.Fatalf("error-path Epoch = %d, want 42", result.Epoch)
	}
}

func TestCreateTaskFromNoteCapturesStoreAndReportsArchiveError(t *testing.T) {
	dir := t.TempDir()
	fakeTD := filepath.Join(dir, "td")
	script := "#!/bin/sh\necho 'Created td-abc123'\n"
	if err := os.WriteFile(fakeTD, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	oldStore := openTestStore(t)
	note, err := oldStore.Create("note", "body")
	if err != nil {
		t.Fatal(err)
	}

	p := New()
	p.ctx = &plugin.Context{
		WorkDir: dir, Epoch: 9,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	p.store = oldStore
	p.taskModalNote = note
	p.taskModalTitleInput = textinput.New()
	p.taskModalTitleInput.SetValue("title")
	p.taskModalArchiveNote = true

	cmd := p.createTaskFromNote()
	p.store = openTestStore(t)
	p.ctx = &plugin.Context{
		WorkDir: t.TempDir(), Epoch: 10,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	result, ok := cmd().(TaskCreatedMsg)
	if !ok {
		t.Fatalf("got %T, want TaskCreatedMsg", cmd())
	}
	if result.Err != nil {
		t.Fatalf("create failed: %v", result.Err)
	}
	if result.Epoch != 9 {
		t.Fatalf("Epoch = %d, want captured 9", result.Epoch)
	}
	if result.ArchiveErr != nil {
		t.Fatalf("archive of captured store failed: %v", result.ArchiveErr)
	}
	got, err := oldStore.Get(note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.Archived {
		t.Fatal("archive used replacement store or did not archive")
	}
}

func TestCreateTaskFromNoteMissingNoteReportsArchiveError(t *testing.T) {
	dir := t.TempDir()
	fakeTD := filepath.Join(dir, "td")
	if err := os.WriteFile(fakeTD, []byte("#!/bin/sh\necho 'Created td-xyz'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	p := New()
	p.ctx = &plugin.Context{
		WorkDir: dir, Epoch: 5,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	p.store = openTestStore(t)
	p.taskModalNote = &Note{ID: "missing-note", Content: "body"}
	p.taskModalTitleInput = textinput.New()
	p.taskModalTitleInput.SetValue("title")
	p.taskModalArchiveNote = true

	result, ok := p.createTaskFromNote()().(TaskCreatedMsg)
	if !ok {
		t.Fatal("expected TaskCreatedMsg")
	}
	if result.Err != nil {
		t.Fatalf("create failed: %v", result.Err)
	}
	if result.ArchiveErr == nil {
		t.Fatal("missing-note archive should fail")
	}
	if result.Epoch != 5 {
		t.Fatalf("Epoch = %d, want 5", result.Epoch)
	}
	if result.TaskID != "td-xyz" {
		t.Fatalf("TaskID = %q", result.TaskID)
	}
}

func TestNoteExportCleanupOnErrorMsg(t *testing.T) {
	p, _ := newNotesEditorHarness(t)
	cmd := p.openInExternalEditor()
	if cmd == nil {
		t.Fatal("openInExternalEditor returned nil")
	}
	open, ok := cmd().(plugin.OpenFileMsg)
	if !ok {
		t.Fatalf("got %T", cmd())
	}
	if _, err := os.Stat(open.Path); err != nil {
		t.Fatalf("export missing: %v", err)
	}

	_, _ = p.Update(app.ErrorMsg{Err: fmt.Errorf("editor failed")})
	if _, err := os.Stat(open.Path); !os.IsNotExist(err) {
		t.Fatal("ErrorMsg left the export file behind")
	}
	if p.pendingInlineEditPath != "" {
		t.Fatal("pending export path survived ErrorMsg")
	}
}

func TestNoteExportCleanupOnUnavailableEditor(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	p, noteID := newNotesEditorHarness(t)
	before, _ := filepath.Glob(filepath.Join(os.TempDir(), "sidecar-note-*.md"))
	cmd := p.enterInlineEditMode(noteID)
	if cmd == nil {
		t.Fatal("enterInlineEditMode returned nil")
	}
	_ = cmd()
	after, _ := filepath.Glob(filepath.Join(os.TempDir(), "sidecar-note-*.md"))
	if len(after) > len(before) {
		t.Fatalf("start-fail left export files: before=%d after=%d", len(before), len(after))
	}
}

func firstToast(cmd tea.Cmd) (msg.ToastMsg, bool) {
	if cmd == nil {
		return msg.ToastMsg{}, false
	}
	switch m := cmd().(type) {
	case msg.ToastMsg:
		return m, true
	case tea.BatchMsg:
		for _, nested := range m {
			if toast, ok := firstToast(nested); ok {
				return toast, true
			}
		}
	}
	return msg.ToastMsg{}, false
}
