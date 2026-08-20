package notes

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestLeaveBeforeDebounceStartsNonblockingSave(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.activePane = PaneEditor
	p.previewMode = false
	p.editorNote = a
	p.editorTextarea.SetValue("UNSAVED-leave")
	p.editorDirty = true
	p.autoSaveID = 4

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.activePane != PaneList || cmd == nil || !p.saveInFlight {
		t.Fatalf("tab state: pane=%v cmd=%v saving=%v", p.activePane, cmd != nil, p.saveInFlight)
	}
	if !p.editorDirty {
		t.Fatal("tab declared the buffer clean before td completed")
	}
	drainNotesCmd(t, p, cmd)
	if p.editorDirty || p.saveInFlight {
		t.Fatalf("completed save: dirty=%v saving=%v", p.editorDirty, p.saveInFlight)
	}
	assertStoredContent(t, p.store, a.ID, "UNSAVED-leave")
}

func TestNavigateWaitsForSaveWithoutBlockingUpdate(t *testing.T) {
	p, a, b := newTwoNoteSavePlugin(t)
	blocked := newBlockingStore(p.store)
	p.store = blocked
	p.editorNote = a
	p.editorTextarea.SetValue("from-A")
	p.editorDirty = true
	p.cursor = 1

	cmd := p.loadNoteIntoEditor()
	if cmd == nil || p.editorNote.ID != a.ID {
		t.Fatal("navigation should wait with A visible while its save is pending")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-blocked.started

	done := make(chan struct{})
	go func() {
		_, _ = p.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Bubble Tea Update blocked behind slow note save")
	}

	close(blocked.release)
	drainNotesMsg(t, p, <-result)
	if p.editorNote == nil || p.editorNote.ID != b.ID {
		t.Fatalf("editor note = %+v, want B after save", p.editorNote)
	}
	assertStoredContent(t, p.store, a.ID, "from-A")
}

func TestAutosaveOverlapKeepsNewTypingDirty(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.editorTextarea.SetValue("first")
	p.editorDirty = true
	p.autoSaveID = 7
	pending := p.saveEditorContent()

	p.editorTextarea.SetValue("second")
	p.editorDirty = true
	p.autoSaveID = 8
	drainNotesCmd(t, p, pending)
	if !p.editorDirty || p.lastSavedContent != "first" {
		t.Fatalf("older completion: dirty=%v baseline=%q", p.editorDirty, p.lastSavedContent)
	}

	_, cmd := p.Update(AutoSaveTickMsg{ID: 8})
	if cmd == nil {
		t.Fatal("new debounce generation did not save")
	}
	drainNotesCmd(t, p, cmd)
	if p.editorDirty {
		t.Fatal("matching completion left dirty set")
	}
	assertStoredContent(t, p.store, a.ID, "second")
}

func TestNavigationQueuesLatestBufferBehindInflightSave(t *testing.T) {
	p, a, b := newTwoNoteSavePlugin(t)
	blocked := newBlockingStore(p.store)
	p.store = blocked
	p.editorNote = a
	p.editorTextarea.SetValue("first")
	p.editorDirty = true
	p.autoSaveID = 1
	first := p.saveEditorContent()
	firstResult := make(chan tea.Msg, 1)
	go func() { firstResult <- first() }()
	<-blocked.started

	p.editorTextarea.SetValue("latest")
	p.editorDirty = true
	p.autoSaveID = 2
	p.cursor = 1
	if cmd := p.loadNoteIntoEditor(); cmd != nil {
		t.Fatal("navigation should join the in-flight save, not launch another")
	}
	if !p.saveQueued {
		t.Fatal("latest buffer was not queued behind in-flight save")
	}

	close(blocked.release)
	msg := <-firstResult
	_, next := p.Update(msg)
	if next == nil || !p.saveInFlight {
		t.Fatal("first completion did not start the queued latest save")
	}
	drainNotesCmd(t, p, next)
	if p.editorNote == nil || p.editorNote.ID != b.ID {
		t.Fatalf("editor note = %+v, want B", p.editorNote)
	}
	assertStoredContent(t, p.store, a.ID, "latest")
}

func TestPendingNavigationCanReturnToCurrentNote(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	blocked := newBlockingStore(p.store)
	p.store = blocked
	p.editorNote = a
	p.editorTextarea.SetValue("dirty")
	p.editorDirty = true
	p.cursor = 1
	cmd := p.loadNoteIntoEditor()
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-blocked.started
	p.cursor = 0
	if cmd := p.loadNoteIntoEditor(); cmd != nil {
		t.Fatal("returning to the current note launched another save")
	}
	close(blocked.release)
	drainNotesMsg(t, p, <-result)
	if p.editorNote == nil || p.editorNote.ID != a.ID {
		t.Fatalf("stale pending navigation won: editor=%+v", p.editorNote)
	}
}

func TestSaveFailureStaysVisibleRetryableAndKeepsTransition(t *testing.T) {
	p, a, b := newTwoNoteSavePlugin(t)
	failing := &failOnceStore{noteStore: p.store, err: errors.New("database busy")}
	p.store = failing
	p.editorNote = a
	p.editorTextarea.SetValue("keep-me")
	p.editorDirty = true
	p.autoSaveID = 1
	p.cursor = 1

	cmd := p.loadNoteIntoEditor()
	failed := cmd().(NoteContentSavedMsg)
	_, toastCmd := p.Update(failed)
	if !p.editorDirty || p.editorNote.ID != a.ID || p.cursor != 0 {
		t.Fatalf("failed save lost ownership: dirty=%v note=%v cursor=%d", p.editorDirty, p.editorNote.ID, p.cursor)
	}
	status, isErr := p.FooterStatus()
	if !isErr || !strings.Contains(status, "Ctrl-S") {
		t.Fatalf("footer status = %q error=%v", status, isErr)
	}
	toast, ok := toastCmd().(msg.ToastMsg)
	if !ok || !toast.IsError {
		t.Fatalf("failure toast = %T %+v", toastCmd(), toast)
	}

	_, retry := p.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if retry == nil {
		t.Fatal("Ctrl-S from the list did not retry the failed save")
	}
	drainNotesCmd(t, p, retry)
	if p.editorDirty || p.editorNote == nil || p.editorNote.ID != b.ID {
		t.Fatalf("retry did not resume transition: dirty=%v note=%+v", p.editorDirty, p.editorNote)
	}
	assertStoredContent(t, p.store, a.ID, "keep-me")
}

func TestFilterChangeRunsAfterDirtySave(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.editorTextarea.SetValue("before-archive-view")
	p.editorDirty = true

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd == nil || p.viewFilter != FilterActive || p.editorNote == nil {
		t.Fatal("filter changed before dirty content was durable")
	}
	drainNotesCmd(t, p, cmd)
	if p.viewFilter != FilterArchived || p.editorNote != nil {
		t.Fatalf("filter transition did not finish: filter=%v note=%+v", p.viewFilter, p.editorNote)
	}
	assertStoredContent(t, p.store, a.ID, "before-archive-view")
}

func TestContentSaveUpdatesCacheWithoutListReload(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	counting := &countingStore{noteStore: p.store}
	p.store = counting
	p.editorNote = &p.notes[0]
	p.editorTextarea.SetValue("new body")
	p.editorDirty = true
	p.autoSaveID = 3

	drainNotesCmd(t, p, p.saveEditorContent())
	if counting.listCalls != 0 {
		t.Fatalf("content save made %d avoidable list calls", counting.listCalls)
	}
	if p.notes[0].Content != "new body" || p.editorNote.Content != "new body" {
		t.Fatalf("cache not updated: note=%q editor=%q", p.notes[0].Content, p.editorNote.Content)
	}
	assertStoredContent(t, p.store, a.ID, "new body")
}

func TestSlowInitialLoadDoesNotBlockUpdate(t *testing.T) {
	p, _, _ := newTwoNoteSavePlugin(t)
	blocked := newBlockingStore(p.store)
	blocked.blockList = true
	p.store = blocked
	p.notes = nil
	cmd := p.loadNotes()
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-blocked.started

	done := make(chan struct{})
	go func() {
		_, _ = p.Update(tea.WindowSizeMsg{Width: 90, Height: 22})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Bubble Tea Update blocked behind slow note load")
	}
	close(blocked.release)
	drainNotesMsg(t, p, <-result)
}

func TestRecoveryFailureKeepsNotesVisibleAndRetryable(t *testing.T) {
	p, _, _ := newTwoNoteSavePlugin(t)
	if _, err := writeNoteDraft(p.ctx.ProjectRoot, "nt-missing", "recover me"); err != nil {
		t.Fatal(err)
	}
	p.notes = nil
	loaded := p.loadNotes()().(NotesLoadedMsg)
	if loaded.Err != nil || loaded.RecoveryErr == nil || len(loaded.Notes) != 2 {
		t.Fatalf("load = notes:%d err:%v recovery:%v", len(loaded.Notes), loaded.Err, loaded.RecoveryErr)
	}
	_, _ = p.Update(loaded)
	if len(p.notes) != 2 {
		t.Fatal("recovery failure hid the ordinary notes list")
	}
	status, isErr := p.FooterStatus()
	if !isErr || !strings.Contains(status, "r to retry") {
		t.Fatalf("footer status = %q error=%v", status, isErr)
	}
}

func TestInlineAndExternalCompletionsDoNotClearBuiltInDirty(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.editorTextarea.SetValue("typed after editor")
	p.editorDirty = true
	p.inlineEditActivation = 8

	_, cmd := p.Update(NoteContentSavedMsg{ID: a.ID, Epoch: 1, EditorActivation: 8})
	if !p.editorDirty || cmd == nil {
		t.Fatal("inline completion cleared newer built-in content or skipped refresh")
	}
	_, cmd = p.Update(NoteContentSavedMsg{ID: a.ID, Epoch: 1, External: true})
	if !p.editorDirty || cmd == nil {
		t.Fatal("external completion cleared newer built-in content or skipped refresh")
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
	p.ctx = &plugin.Context{Epoch: 1, Logger: discardLogger()}
	p.store = store
	p.editorNote = a
	p.editorTextarea = textarea.New()
	p.editorTextarea.SetValue("across-stop")
	p.editorDirty = true
	p.Stop()
	waitFor(t, time.Second, func() bool {
		_, err := os.Stat(draftPath(dir, a.ID))
		return os.IsNotExist(err)
	})

	peer, err := newInProcessStore(dir, "peer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	assertStoredContent(t, peer, a.ID, "across-stop")
}

func TestStopReusesMatchingInflightWrite(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	blocked := newBlockingStore(p.store)
	p.store = blocked
	p.editorNote = a
	p.editorTextarea.SetValue("already-in-flight")
	p.editorDirty = true
	cmd := p.saveEditorContent()
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-blocked.started
	startedStop := time.Now()
	p.Stop()
	if elapsed := time.Since(startedStop); elapsed > 100*time.Millisecond {
		t.Fatalf("Stop waited %s for td instead of checkpointing", elapsed)
	}
	close(blocked.release)
	saved := (<-result).(NoteContentSavedMsg)
	<-blocked.closed
	if blocked.saveCalls != 1 {
		t.Fatalf("Stop wrote the same content %d times, want one", blocked.saveCalls)
	}
	if p.editorDirty {
		t.Fatal("matching in-flight write left the stopped buffer dirty")
	}
	if saved.Err != nil || saved.Note == nil || saved.Note.Content != "already-in-flight" {
		t.Fatalf("in-flight result = %+v", saved)
	}
}

func TestStopFailureRetainsRecoverableDraft(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	failing := &alwaysFailStore{noteStore: p.store, err: errors.New("disk full"), closed: make(chan struct{})}
	p.store = failing
	p.editorNote = a
	p.editorTextarea.SetValue("keep-me")
	p.editorDirty = true
	p.Stop()
	<-failing.closed
	path := draftPath(p.ctx.ProjectRoot, a.ID)
	waitFor(t, time.Second, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
	if p.editorDirty || p.store != nil {
		t.Fatal("Stop did not hand ownership to the durable recovery draft")
	}
	peer, err := newInProcessStore(p.ctx.ProjectRoot, "recovery")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	if err := recoverNoteDrafts(p.ctx.ProjectRoot, peer); err != nil {
		t.Fatal(err)
	}
	assertStoredContent(t, peer, a.ID, "keep-me")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("successful recovery retained its draft")
	}
}

func TestStopPreservesUndoAgainstOlderInflightSave(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.lastSavedContent = "body-a"
	p.editorTextarea.SetValue("intermediate")
	p.editorDirty = true
	blocked := newBlockingStore(p.store)
	p.store = blocked
	cmd := p.saveEditorContent()
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-blocked.started

	p.editorTextarea.SetValue("body-a") // user undoes while the older write owns td
	_ = p.afterContentChange()
	if !p.editorDirty {
		t.Fatal("undo matched the old durable value but ignored the conflicting in-flight write")
	}
	started := time.Now()
	p.Stop()
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("Stop blocked on the in-flight save")
	}
	close(blocked.release)
	<-result
	<-blocked.closed

	peer, err := newInProcessStore(p.ctx.ProjectRoot, "undo-peer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	assertStoredContent(t, peer, a.ID, "body-a")
}

func TestOlderInflightCompletionCannotRetireNewerCheckpoint(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.lastSavedContent = "body-a"
	p.editorTextarea.SetValue("intermediate")
	p.editorDirty = true
	blocked := newBlockingStore(p.store)
	p.store = blocked
	cmd := p.saveEditorContent()
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-blocked.started

	draft := noteDraft{ID: a.ID, Content: "final-intent", BaseContent: "body-a", InFlightContent: "intermediate"}
	path, err := writeNoteDraftState(p.ctx.ProjectRoot, draft)
	if err != nil {
		t.Fatal(err)
	}
	close(blocked.release)
	<-result
	if _, err := os.Stat(path); err != nil {
		t.Fatal("older successful save retired a checkpoint written after it began")
	}
}

func TestSuccessfulSaveRetiresOlderRecoveryDraft(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	draft := noteDraft{ID: a.ID, Content: "old-recovery", BaseContent: "body-a"}
	path, err := writeNoteDraftState(p.ctx.ProjectRoot, draft)
	if err != nil {
		t.Fatal(err)
	}
	failing := &alwaysFailStore{noteStore: p.store, err: errors.New("offline"), closed: make(chan struct{})}
	if err := recoverNoteDrafts(p.ctx.ProjectRoot, failing); err == nil {
		t.Fatal("recovery unexpectedly succeeded")
	}
	p.editorNote = a
	p.lastSavedContent = "body-a"
	p.editorTextarea.SetValue("newer-save")
	p.editorDirty = true
	drainNotesCmd(t, p, p.saveEditorContent())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("successful canonical save retained a superseded recovery draft")
	}
	if err := recoverNoteDrafts(p.ctx.ProjectRoot, p.store); err != nil {
		t.Fatal(err)
	}
	assertStoredContent(t, p.store, a.ID, "newer-save")
}

func TestRecoveryRefusesToOverwriteExternalChange(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	draft := noteDraft{ID: a.ID, Content: "recovered", BaseContent: "body-a"}
	path, err := writeNoteDraftState(p.ctx.ProjectRoot, draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.SaveContent(a.ID, "external-newer"); err != nil {
		t.Fatal(err)
	}
	if err := recoverNoteDrafts(p.ctx.ProjectRoot, p.store); err == nil {
		t.Fatal("recovery overwrote a note that no longer matched its safe base")
	}
	assertStoredContent(t, p.store, a.ID, "external-newer")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("conflicting recovery draft was not retained for manual resolution")
	}
}

func TestOlderListSnapshotCannotRegressSuccessfulSave(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	snapshot := &snapshotBlockingStore{noteStore: p.store, captured: make(chan struct{}), release: make(chan struct{})}
	p.store = snapshot
	load := p.loadNotes()
	loaded := make(chan tea.Msg, 1)
	go func() { loaded <- load() }()
	<-snapshot.captured

	p.editorNote = a
	p.lastSavedContent = "body-a"
	p.editorTextarea.SetValue("canonical-new")
	p.editorDirty = true
	drainNotesCmd(t, p, p.saveEditorContent())
	close(snapshot.release)
	_, _ = p.Update(<-loaded)
	for _, n := range p.notes {
		if n.ID == a.ID && n.Content != "canonical-new" {
			t.Fatalf("stale list snapshot regressed saved cache to %q", n.Content)
		}
	}
}

func TestFailedExportSaveRetainsFileAndCtrlSRetry(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	failing := &failOnceSaveStore{noteStore: p.store, err: errors.New("busy")}
	p.store = failing
	path := filepath.Join(t.TempDir(), "edited.md")
	if err := os.WriteFile(path, []byte("external-final"), 0o600); err != nil {
		t.Fatal(err)
	}
	drainNotesCmd(t, p, p.saveRetainedExport(a.ID, path, 0))
	if _, err := os.Stat(path); err != nil || p.pendingInlineEditPath != path {
		t.Fatalf("failed export was not retained: stat=%v pending=%q", err, p.pendingInlineEditPath)
	}
	intentSequence := p.activeExport.Sequence
	requestID := p.activeExport.RequestID
	_, retry := p.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if retry == nil {
		t.Fatal("Ctrl-S did not retry the retained export")
	}
	if p.activeExport.Sequence != intentSequence {
		t.Fatalf("retry promoted content intent sequence from %d to %d", intentSequence, p.activeExport.Sequence)
	}
	if p.activeExport.RequestID == requestID {
		t.Fatal("retry did not allocate a new attempt request ID")
	}
	drainNotesCmd(t, p, retry)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("successful retry did not remove the acknowledged export")
	}
	assertStoredContent(t, p.store, a.ID, "external-final")
}

func TestStaleExternalPreparationCannotOpenOlderNote(t *testing.T) {
	p, a, b := newTwoNoteSavePlugin(t)
	oldPath := filepath.Join(t.TempDir(), "old.md")
	newPath := filepath.Join(t.TempDir(), "new.md")
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.externalPrepareID = 2
	_, cmd := p.Update(ExternalEditorPreparedMsg{ID: a.ID, Path: oldPath, Epoch: 1, RequestID: 1})
	if cmd != nil {
		t.Fatal("stale external preparation opened an editor")
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("stale external export was not cleaned up")
	}
	_, cmd = p.Update(ExternalEditorPreparedMsg{ID: b.ID, Path: newPath, Epoch: 1, RequestID: 2})
	if cmd == nil {
		t.Fatal("latest external preparation did not open")
	}
}

func TestQueuedSaveDoesNotToastUntilLatestContentIsDurable(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.editorNote = a
	p.lastSavedContent = "body-a"
	p.editorTextarea.SetValue("first")
	p.editorDirty = true
	blocked := newBlockingStore(p.store)
	p.store = blocked
	first := p.saveEditorContent()
	result := make(chan tea.Msg, 1)
	go func() { result <- first() }()
	<-blocked.started
	p.editorTextarea.SetValue("second")
	p.editorDirty = true
	if cmd := p.saveEditorContent(); cmd != nil {
		t.Fatal("overlap started a second concurrent write")
	}
	close(blocked.release)
	_, next := p.Update(<-result)
	if next == nil {
		t.Fatal("latest content was not queued after the first write")
	}
	if _, ok := next().(NoteContentSavedMsg); !ok {
		t.Fatal("intermediate completion emitted a toast/batch before latest content was durable")
	}
}

func TestStopUsesFallbackDraftWithoutBlockingTd(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	badRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badRoot, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.ctx.ProjectRoot = badRoot
	p.editorNote = a
	p.lastSavedContent = "body-a"
	p.editorTextarea.SetValue("fallback-final")
	p.editorDirty = true
	failing := &alwaysFailStore{noteStore: p.store, err: errors.New("td blocked"), closed: make(chan struct{})}
	p.store = failing
	started := time.Now()
	p.Stop()
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("Stop synchronously called td after the primary checkpoint failed")
	}
	path, err := fallbackDraftPath(badRoot, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fallback recovery draft missing: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	closeStoreEventually := failing.closed
	<-closeStoreEventually
}

func TestOlderInlineAutosaveCannotOverwriteFinalExitSave(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	path := filepath.Join(t.TempDir(), "inline.md")
	if err := os.WriteFile(path, []byte("autosave-A1"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocked := newBlockingStore(p.store)
	p.store = blocked
	p.inlineEditMode = true
	p.inlineEditNoteID = a.ID
	p.inlineEditPath = path
	p.inlineEditActivation = 4
	p.inlineAutoSaveGen = 9
	p.inlineLastSavedContent = "body-a"
	autosave := p.performInlineAutoSave()
	autoResult := make(chan tea.Msg, 1)
	go func() { autoResult <- autosave() }()
	<-blocked.started

	if err := os.WriteFile(path, []byte("final-exit-A2"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.exitInlineEditMode()
	finalSave := p.saveRetainedExport(a.ID, path, 0)
	if finalSave == nil {
		t.Fatal("final exit export was not adopted while periodic save was slow")
	}
	finalResult := make(chan tea.Msg, 1)
	go func() { finalResult <- finalSave() }()
	close(blocked.release)
	<-autoResult
	drainNotesMsg(t, p, <-finalResult)
	assertStoredContent(t, p.store, a.ID, "final-exit-A2")
}

func TestSecondRetainedExportQueuesBehindFirst(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	firstPath := filepath.Join(t.TempDir(), "first.md")
	secondPath := filepath.Join(t.TempDir(), "second.md")
	if err := os.WriteFile(firstPath, []byte("export-A1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("export-A2"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocked := newBlockingStore(p.store)
	p.store = blocked
	first := p.saveRetainedExport(a.ID, firstPath, 0)
	firstResult := make(chan tea.Msg, 1)
	go func() { firstResult <- first() }()
	<-blocked.started

	if cmd := p.saveRetainedExport(a.ID, secondPath, 0); cmd != nil {
		t.Fatal("second export started concurrently instead of joining the ordered queue")
	}
	if len(p.exportQueue) != 1 || p.exportQueue[0].Path != secondPath {
		t.Fatalf("second export is unowned: queue=%+v", p.exportQueue)
	}
	close(blocked.release)
	_, next := p.Update(<-firstResult)
	if next == nil {
		t.Fatal("first completion did not start the queued final export")
	}
	drainNotesCmd(t, p, next)
	assertStoredContent(t, p.store, a.ID, "export-A2")
	for _, path := range []string{firstPath, secondPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("acknowledged export was not removed: %s", path)
		}
	}
}

func TestStopCheckpointsEveryQueuedExportWithLatestIntentLast(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	firstPath := filepath.Join(t.TempDir(), "first-stop.md")
	secondPath := filepath.Join(t.TempDir(), "second-stop.md")
	if err := os.WriteFile(firstPath, []byte("queued-A1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("queued-A2-final"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cmd := p.saveRetainedExport(a.ID, firstPath, 0); cmd == nil {
		t.Fatal("first export was not adopted")
	}
	if cmd := p.saveRetainedExport(a.ID, secondPath, 0); cmd != nil {
		t.Fatal("second export did not queue")
	}
	p.Stop()
	draft, err := readNoteDraft(draftPath(p.ctx.ProjectRoot, a.ID))
	if err != nil {
		t.Fatal(err)
	}
	if draft.Content != "queued-A2-final" || draft.InFlightContent != "queued-A1" {
		t.Fatalf("queued stop draft = %+v", draft)
	}
	for _, path := range []string{firstPath, secondPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("checkpointed export was not removed: %s", path)
		}
	}
}

func TestNewerQueuedExportSupersedesFailedOlderExit(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.store = &failOnceSaveStore{noteStore: p.store, err: errors.New("first exit busy")}
	firstPath := filepath.Join(t.TempDir(), "failed-first.md")
	secondPath := filepath.Join(t.TempDir(), "final-second.md")
	if err := os.WriteFile(firstPath, []byte("failed-A1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("final-A2"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstResult := p.saveRetainedExport(a.ID, firstPath, 0)()
	if cmd := p.saveRetainedExport(a.ID, secondPath, 0); cmd != nil {
		t.Fatal("newer export did not queue behind the first attempt")
	}
	_, next := p.Update(firstResult)
	if next == nil {
		t.Fatal("failed older exit did not advance to the newer queued export")
	}
	drainNotesCmd(t, p, next)
	assertStoredContent(t, p.store, a.ID, "final-A2")
	for _, path := range []string{firstPath, secondPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("superseded export was not cleaned after final success: %s", path)
		}
	}
}

func TestQueuedExportAfterObservedFailureSupersedesOlderRetry(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.store = &failOnceSaveStore{noteStore: p.store, err: errors.New("first exit busy")}
	firstPath := filepath.Join(t.TempDir(), "failed-first.md")
	secondPath := filepath.Join(t.TempDir(), "final-second.md")
	if err := os.WriteFile(firstPath, []byte("failed-old-A1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("new-final-A2"), 0o600); err != nil {
		t.Fatal(err)
	}
	drainNotesCmd(t, p, p.saveRetainedExport(a.ID, firstPath, 0))
	if p.activeExport.Path != firstPath || p.exportSaveInFlight {
		t.Fatal("failed older export did not remain available for retry")
	}

	next := p.saveRetainedExport(a.ID, secondPath, 0)
	if next == nil {
		t.Fatal("newer export did not supersede the observed failed export")
	}
	drainNotesCmd(t, p, next)
	assertStoredContent(t, p.store, a.ID, "new-final-A2")
	for _, path := range []string{firstPath, secondPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("acknowledged or superseded export was not removed: %s", path)
		}
	}
}

func TestBuiltInAcknowledgmentRefusesStaleFailedExportRetry(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.store = &failOnceSaveStore{noteStore: p.store, err: errors.New("first exit busy")}
	exportPath := filepath.Join(t.TempDir(), "failed-old.md")
	if err := os.WriteFile(exportPath, []byte("failed-old-A1"), 0o600); err != nil {
		t.Fatal(err)
	}
	drainNotesCmd(t, p, p.saveRetainedExport(a.ID, exportPath, 0))
	if p.activeExport.Path != exportPath {
		t.Fatal("failed export was not retained for the retry check")
	}

	p.editorNote = a
	p.lastSavedContent = "body-a"
	p.editorTextarea.SetValue("newer-built-in-A2")
	p.editorDirty = true
	drainNotesCmd(t, p, p.saveEditorContent())
	assertStoredContent(t, p.store, a.ID, "newer-built-in-A2")

	_, retry := p.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if retry != nil {
		t.Fatal("durably superseded failed export was retried")
	}
	assertStoredContent(t, p.store, a.ID, "newer-built-in-A2")
	if p.activeExport.Path != "" {
		t.Fatal("durably superseded export remained active")
	}
	if _, err := os.Stat(exportPath); !os.IsNotExist(err) {
		t.Fatal("durably superseded failed export was not retired")
	}
}

func TestNewerBuiltInAcknowledgmentRetiresSkippedExportBeforeStopRecovery(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	exportPath := filepath.Join(t.TempDir(), "older-external.md")
	if err := os.WriteFile(exportPath, []byte("older-external-A1"), 0o600); err != nil {
		t.Fatal(err)
	}
	olderExport := p.saveRetainedExport(a.ID, exportPath, 0)
	if olderExport == nil {
		t.Fatal("older export was not adopted")
	}

	p.editorNote = a
	p.lastSavedContent = "body-a"
	p.editorTextarea.SetValue("newer-built-in-A2")
	p.editorDirty = true
	drainNotesCmd(t, p, p.saveEditorContent())
	result := olderExport().(NoteContentSavedMsg)
	if !result.Skipped {
		t.Fatal("older delayed export was not skipped after newer canonical save")
	}
	_, _ = p.Update(result)
	if len(p.supersededExports) != 0 {
		t.Fatal("durably obsolete export remained eligible for Stop checkpointing")
	}
	if _, err := os.Stat(exportPath); !os.IsNotExist(err) {
		t.Fatal("durably obsolete export file was not retired")
	}
	root := p.ctx.ProjectRoot
	p.Stop()
	peer, err := newInProcessStore(root, "post-stop-recovery")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	if err := recoverNoteDrafts(root, peer); err != nil {
		t.Fatal(err)
	}
	assertStoredContent(t, peer, a.ID, "newer-built-in-A2")
}

func TestNewExternalPreparationPreservesFailedActiveExport(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	p.store = &alwaysFailStore{noteStore: p.store, err: errors.New("busy"), closed: make(chan struct{})}
	failedPath := filepath.Join(t.TempDir(), "failed.md")
	newPath := filepath.Join(t.TempDir(), "new.md")
	if err := os.WriteFile(failedPath, []byte("only-recoverable-copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new editor"), 0o600); err != nil {
		t.Fatal(err)
	}
	drainNotesCmd(t, p, p.saveRetainedExport(a.ID, failedPath, 0))
	if p.activeExport.Path != failedPath || p.pendingInlineEditPath != failedPath {
		t.Fatal("failed export did not remain active and retryable")
	}
	p.externalPrepareID = 7
	_, open := p.Update(ExternalEditorPreparedMsg{ID: a.ID, Path: newPath, Epoch: p.ctx.Epoch, RequestID: 7})
	if open == nil {
		t.Fatal("new external editor preparation did not open")
	}
	if _, err := os.Stat(failedPath); err != nil {
		t.Fatalf("new preparation deleted the failed export's only copy: %v", err)
	}
	if p.activeExport.Path != failedPath || p.pendingInlineEditPath != newPath {
		t.Fatalf("ownership after preparation: active=%q pending=%q", p.activeExport.Path, p.pendingInlineEditPath)
	}
}

func TestSlowRetainedExportShowsNoSavingFooter(t *testing.T) {
	p, a, _ := newTwoNoteSavePlugin(t)
	blocked := newBlockingStore(p.store)
	p.store = blocked
	path := filepath.Join(t.TempDir(), "slow-export.md")
	if err := os.WriteFile(path, []byte("slow-final"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := p.saveRetainedExport(a.ID, path, 0)
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-blocked.started
	if !p.exportSaveInFlight {
		t.Fatal("slow export did not register as in flight")
	}
	// An in-flight save is routine: it must not claim the footer.
	if status, isErr := p.FooterStatus(); status != "" || isErr {
		t.Fatalf("slow export footer = %q error=%v, want empty", status, isErr)
	}
	close(blocked.release)
	drainNotesMsg(t, p, <-result)
}

type blockingStore struct {
	noteStore
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
	blockList bool
	saveCalls int
	closed    chan struct{}
	closeOnce sync.Once
}

type snapshotBlockingStore struct {
	noteStore
	captured chan struct{}
	release  chan struct{}
}

func (s *snapshotBlockingStore) List(includeArchived bool) ([]Note, error) {
	notes, err := s.noteStore.List(includeArchived)
	close(s.captured)
	<-s.release
	return notes, err
}

type failOnceSaveStore struct {
	noteStore
	err  error
	once sync.Once
}

func (s *failOnceSaveStore) SaveContent(id, content string) (*Note, error) {
	failed := false
	s.once.Do(func() { failed = true })
	if failed {
		return nil, s.err
	}
	return s.noteStore.SaveContent(id, content)
}

func newBlockingStore(store noteStore) *blockingStore {
	return &blockingStore{noteStore: store, started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
}

func (s *blockingStore) wait() {
	s.startOnce.Do(func() { close(s.started) })
	<-s.release
}

func (s *blockingStore) SaveContent(id, content string) (*Note, error) {
	s.wait()
	s.saveCalls++
	return s.noteStore.SaveContent(id, content)
}

func (s *blockingStore) List(includeArchived bool) ([]Note, error) {
	if s.blockList {
		s.wait()
	}
	return s.noteStore.List(includeArchived)
}

func (s *blockingStore) Close() error {
	err := s.noteStore.Close()
	s.closeOnce.Do(func() { close(s.closed) })
	return err
}

type failOnceStore struct {
	noteStore
	err error
}

func (s *failOnceStore) SaveContent(id, content string) (*Note, error) {
	if s.err != nil {
		err := s.err
		s.err = nil
		return nil, err
	}
	return s.noteStore.SaveContent(id, content)
}

type alwaysFailStore struct {
	noteStore
	err       error
	closed    chan struct{}
	closeOnce sync.Once
}

func (s *alwaysFailStore) SaveContent(string, string) (*Note, error) { return nil, s.err }
func (s *alwaysFailStore) UpdateContent(string, string) error        { return s.err }
func (s *alwaysFailStore) Close() error {
	err := s.noteStore.Close()
	s.closeOnce.Do(func() { close(s.closed) })
	return err
}

type countingStore struct {
	noteStore
	listCalls int
}

func (s *countingStore) List(includeArchived bool) ([]Note, error) {
	s.listCalls++
	return s.noteStore.List(includeArchived)
}

func drainNotesCmd(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	drainNotesMsg(t, p, cmd())
}

func prepareExternalEditor(t *testing.T, p *Plugin, cmd tea.Cmd) plugin.OpenFileMsg {
	t.Helper()
	result := cmd()
	prepared, ok := result.(ExternalEditorPreparedMsg)
	if !ok {
		t.Fatalf("external prepare produced %T", result)
	}
	_, openCmd := p.Update(prepared)
	if openCmd == nil {
		t.Fatal("external prepare produced no open command")
	}
	result = openCmd()
	open, ok := result.(plugin.OpenFileMsg)
	if !ok {
		t.Fatalf("external open produced %T", result)
	}
	return open
}

func drainNotesMsg(t *testing.T, p *Plugin, m tea.Msg) {
	t.Helper()
	if batch, ok := m.(tea.BatchMsg); ok {
		for _, cmd := range batch {
			drainNotesCmd(t, p, cmd)
		}
		return
	}
	_, cmd := p.Update(m)
	if cmd != nil {
		drainNotesCmd(t, p, cmd)
	}
}

func assertStoredContent(t *testing.T, store noteStore, id, want string) {
	t.Helper()
	got, err := store.Get(id)
	if err != nil || got == nil {
		t.Fatalf("Get(%s): note=%+v err=%v", id, got, err)
	}
	if got.Content != want {
		t.Fatalf("stored content = %q, want %q", got.Content, want)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTwoNoteSavePlugin(t *testing.T) (*Plugin, *Note, *Note) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewTestStore(dir, "test-session")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	a, err := store.Create("A", "body-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Create("B", "body-b")
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.ctx = &plugin.Context{Epoch: 1, ProjectRoot: dir, Logger: discardLogger()}
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

func waitFor(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition did not become ready")
}
