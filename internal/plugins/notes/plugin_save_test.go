package notes

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestLeaveBeforeDebounceStillSaves(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.activePane = PaneEditor
	p.previewMode = false
	p.editorNote = a
	p.editorTextarea.SetValue("UNSAVED-leave")
	p.editorDirty = true
	p.autoSaveID = 4

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.activePane != PaneList {
		t.Fatal("tab did not leave the editor pane")
	}
	if cmd != nil {
		t.Fatal("successful persist on tab should be silent")
	}
	if p.editorDirty {
		t.Fatal("tab left the buffer dirty")
	}
	got, err := p.store.Get(a.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != "UNSAVED-leave" {
		t.Fatalf("store = %q, want persisted on tab", got.Content)
	}

	// A matching tick after leaving the pane must still save if something
	// left the buffer dirty (the generation is the owner, not the pane).
	p.editorTextarea.SetValue("UNSAVED-tick")
	p.editorDirty = true
	p.autoSaveID = 5
	p.activePane = PaneList
	_, cmd = p.Update(AutoSaveTickMsg{ID: 5})
	if cmd == nil {
		t.Fatal("tick ignored because the list pane was focused")
	}
	saved, ok := cmd().(NoteContentSavedMsg)
	if !ok {
		t.Fatalf("tick produced %T, want NoteContentSavedMsg", cmd())
	}
	if saved.Err != nil || saved.Generation != 5 || saved.Content != "UNSAVED-tick" {
		t.Fatalf("tick save = %+v", saved)
	}
}

func TestNavigateBeforeDebouncePersistsDirtyNote(t *testing.T) {
	p, a, b := newTwoNoteSavePlugin(t)
	p.activePane = PaneList
	p.editorNote = a
	p.cursor = 0
	p.editorTextarea.SetValue("from-A")
	p.editorDirty = true

	p.cursor = 1
	cmd := p.loadNoteIntoEditor()
	if cmd != nil {
		t.Fatal("persist on navigate returned an error toast")
	}
	if p.editorDirty {
		t.Fatal("navigate left the previous note dirty")
	}
	if p.editorNote == nil || p.editorNote.ID != b.ID {
		t.Fatalf("editor note = %+v, want B", p.editorNote)
	}
	got, err := p.store.Get(a.ID)
	if err != nil || got == nil {
		t.Fatalf("get A: %v", err)
	}
	if got.Content != "from-A" {
		t.Fatalf("A content = %q, want persisted before loading B", got.Content)
	}
}

func TestSaveOverlapWithNewTypingKeepsDirty(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.editorTextarea.SetValue("first")
	p.editorDirty = true
	p.autoSaveID = 7

	pending := p.saveEditorContent()
	if pending == nil {
		t.Fatal("saveEditorContent returned nil")
	}

	p.editorTextarea.SetValue("second")
	p.editorDirty = true
	p.autoSaveID = 8

	_, cmd := p.Update(pending())
	if cmd != nil {
		t.Fatal("older save completion scheduled work")
	}
	if !p.editorDirty {
		t.Fatal("older save completion cleared newer dirty state")
	}
	if got := p.editorTextarea.Value(); got != "second" {
		t.Fatalf("buffer = %q, want second", got)
	}

	_, cmd = p.Update(AutoSaveTickMsg{ID: 8})
	if cmd == nil {
		t.Fatal("newer generation did not save")
	}
	saved := cmd().(NoteContentSavedMsg)
	if saved.Err != nil {
		t.Fatal(saved.Err)
	}
	_, _ = p.Update(saved)
	if p.editorDirty {
		t.Fatal("matching save left dirty set")
	}
	got, err := p.store.Get(a.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != "second" {
		t.Fatalf("store = %q, want second", got.Content)
	}
}

func TestStaleSaveResultDoesNotClearDirty(t *testing.T) {
	p, a, b := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.editorTextarea.SetValue("mine")
	p.editorDirty = true
	p.autoSaveID = 3

	stale := []NoteContentSavedMsg{
		{ID: a.ID, Epoch: 99, Generation: 3, Content: "mine"},
		{ID: b.ID, Epoch: 1, Generation: 3, Content: "mine"},
		{ID: a.ID, Epoch: 1, Generation: 2, Content: "mine"},
		{ID: a.ID, Epoch: 1, Generation: 3, Content: "other"},
	}
	for _, m := range stale {
		_, cmd := p.Update(m)
		if cmd != nil {
			t.Fatalf("stale %+v scheduled work", m)
		}
		if !p.editorDirty {
			t.Fatalf("stale %+v cleared dirty", m)
		}
	}
}

func TestSaveFailureIsVisibleAndKeepsDirty(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.editorTextarea.SetValue("keep-me")
	p.editorDirty = true
	p.autoSaveID = 1

	_, cmd := p.Update(NoteContentSavedMsg{
		ID: a.ID, Epoch: 1, Generation: 1, Content: "keep-me",
		Err: errors.New("disk full"),
	})
	if !p.editorDirty {
		t.Fatal("save error cleared dirty")
	}
	if got := p.editorTextarea.Value(); got != "keep-me" {
		t.Fatalf("buffer = %q", got)
	}
	toast, ok := cmd().(msg.ToastMsg)
	if !ok || !toast.IsError || !strings.Contains(toast.Message, "Save failed") {
		t.Fatalf("toast = %T %+v", cmd(), cmd)
	}

	_ = p.store.Close()
	p.editorDirty = true
	p.cursor = 1
	if cmd = p.loadNoteIntoEditor(); cmd == nil {
		t.Fatal("failed persist on navigate produced no toast")
	}
	if !p.editorDirty {
		t.Fatal("failed persist dropped the dirty buffer")
	}
	if p.editorNote == nil || p.editorNote.ID != a.ID {
		t.Fatal("failed persist still switched notes")
	}
	if p.cursor != 0 {
		t.Fatalf("cursor = %d, want reverted to dirty note", p.cursor)
	}
}

func TestFilterChangePersistsDirtyNote(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.editorTextarea.SetValue("before-archive-view")
	p.editorDirty = true

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if p.editorDirty {
		t.Fatal("filter change left dirty set")
	}
	if p.editorNote != nil {
		t.Fatal("filter change did not abandon the editor")
	}
	if cmd == nil {
		t.Fatal("filter change should reload notes")
	}
	got, err := p.store.Get(a.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != "before-archive-view" {
		t.Fatalf("store = %q, want persisted on filter change", got.Content)
	}
}

func TestInlineExitSaveDoesNotClearBuiltInDirty(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.editorTextarea.SetValue("typed after vim :wq")
	p.editorDirty = true
	p.autoSaveID = 11
	p.inlineEditActivation = 8

	_, cmd := p.Update(NoteContentSavedMsg{
		ID: a.ID, Epoch: 1, EditorActivation: 8,
	})
	if !p.editorDirty {
		t.Fatal("inline-exit save cleared newer built-in dirty state")
	}
	if got := p.editorTextarea.Value(); got != "typed after vim :wq" {
		t.Fatalf("buffer = %q", got)
	}
	if cmd == nil {
		t.Fatal("inline save should still reload notes")
	}
}

func TestExternalReadBackStillReloads(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.editorTextarea.SetValue("body-a")
	p.editorDirty = false

	_, cmd := p.Update(NoteContentSavedMsg{
		ID: a.ID, Epoch: 1, External: true,
	})
	if cmd == nil {
		t.Fatal("$EDITOR read-back produced no reload")
	}
	if p.editorDirty {
		t.Fatal("clean $EDITOR read-back marked the buffer dirty")
	}
}

func TestInFlightSaveDoesNotClobberPersist(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.editorTextarea.SetValue("first")
	p.editorDirty = true
	p.autoSaveID = 7

	pending := p.saveEditorContent()
	if pending == nil {
		t.Fatal("saveEditorContent returned nil")
	}

	p.editorTextarea.SetValue("second")
	p.editorDirty = true
	p.autoSaveID = 8
	if cmd := p.persistDirtyEditor(); cmd != nil {
		t.Fatal("persist failed")
	}

	result := pending()
	saved, ok := result.(NoteContentSavedMsg)
	if !ok {
		t.Fatalf("got %T", result)
	}
	if !saved.Skipped {
		t.Fatal("in-flight save wrote after persist")
	}
	got, err := p.store.Get(a.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != "second" {
		t.Fatalf("store = %q, want persist winner second", got.Content)
	}
}

func TestStopPersistFailureKeepsDirtyBuffer(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.editorTextarea.SetValue("keep-me")
	p.editorDirty = true
	_ = p.store.Close()

	p.Stop()
	if !p.editorDirty {
		t.Fatal("failed Stop persist cleared dirty")
	}
	if p.editorNote == nil || p.editorNote.ID != a.ID {
		t.Fatal("failed Stop persist dropped the editor note")
	}
	if got := p.editorTextarea.Value(); got != "keep-me" {
		t.Fatalf("buffer = %q", got)
	}
	if p.store == nil {
		t.Fatal("failed Stop persist closed away the only store")
	}

	err := p.Init(&plugin.Context{
		Epoch: 2, ProjectRoot: t.TempDir(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil {
		t.Fatal("Init wiped a dirty buffer after persist failure")
	}
	if !p.editorDirty {
		t.Fatal("failed Init persist cleared dirty")
	}
	if got := p.editorTextarea.Value(); got != "keep-me" {
		t.Fatalf("Init dropped buffer: %q", got)
	}
}

func TestStopPersistsDirtyNote(t *testing.T) {
	dir := t.TempDir()
	store, err := NewTestStore(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	a, err := store.Create("A", "body-a")
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.ctx = &plugin.Context{Epoch: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.store = store
	p.editorNote = a
	p.editorTextarea = textarea.New()
	p.editorTextarea.SetValue("across-stop")
	p.editorDirty = true

	p.Stop()

	peer, err := newInProcessStore(dir, "peer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	got, err := peer.Get(a.ID)
	if err != nil || got == nil {
		t.Fatalf("get after stop: %v %+v", err, got)
	}
	if got.Content != "across-stop" {
		t.Fatalf("store = %q, want persisted on Stop", got.Content)
	}
}

func newTwoNoteSavePlugin(t *testing.T) (*Plugin, *Note, *Note) {
	t.Helper()
	store := openTestStore(t)
	a, err := store.Create("A", "body-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Create("B", "body-b")
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.ctx = &plugin.Context{Epoch: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.store = store
	p.notes = []Note{*a, *b}
	p.cursor = 0
	p.viewFilter = FilterActive
	p.width = 100
	p.height = 24
	p.listWidth = 30
	p.editorTextarea = textarea.New()
	p.editorTextarea.SetValue(a.Content)
	p.previewLines = []string{a.Content}
	return p, a, b
}
