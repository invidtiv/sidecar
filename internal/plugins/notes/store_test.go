package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tdnotes "github.com/marcus/td/pkg/notes"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewTestStore(t.TempDir(), "test-session")
	if err != nil {
		t.Fatalf("NewTestStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestStoreCreateGetListRoundTrip(t *testing.T) {
	store := openTestStore(t)

	note, err := store.Create("# Hello", "# Hello\n\nbody")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if note.ID == "" || note.Title != "# Hello" {
		t.Fatalf("created note: %+v", note)
	}

	got, err := store.Get(note.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.Content != "# Hello\n\nbody" {
		t.Fatalf("Get = %+v", got)
	}

	list, err := store.List(false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != note.ID {
		t.Fatalf("List = %+v", list)
	}

	if err := store.Delete(note.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := store.Get(note.ID)
	if err != nil {
		t.Fatalf("Get deleted: %v", err)
	}
	if gone == nil || gone.DeletedAt == nil {
		t.Fatalf("soft-deleted note should still Get with DeletedAt set, got %+v", gone)
	}

	if err := store.Restore(note.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	live, err := store.Get(note.ID)
	if err != nil || live == nil || live.DeletedAt != nil {
		t.Fatalf("Get after restore: %+v %v", live, err)
	}
}

func TestStoreNoteVisibleViaTDPackage(t *testing.T) {
	dir := t.TempDir()
	store, err := NewTestStore(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	created, err := store.Create("from sidecar", "body")
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	peer, err := tdnotes.Open(dir)
	if err != nil {
		t.Fatalf("tdnotes.Open: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	list, err := peer.List(tdnotes.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range list {
		if n.ID == created.ID && n.Title == "from sidecar" {
			found = true
		}
	}
	if !found {
		t.Fatalf("note %s not visible via pkg/notes List: %+v", created.ID, list)
	}
}

func TestStoreUpdateContentPreservesTitle(t *testing.T) {
	store := openTestStore(t)

	note, err := store.Create("td-set title", "original body")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	note.Title = "renamed by td"
	if err := store.Update(note); err != nil {
		t.Fatalf("Update title: %v", err)
	}

	if err := store.UpdateContent(note.ID, "new first line\nrest of body"); err != nil {
		t.Fatalf("UpdateContent: %v", err)
	}

	got, err := store.Get(note.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Title != "renamed by td" {
		t.Fatalf("title = %q, want td-set title to survive UpdateContent", got.Title)
	}
	if got.Content != "new first line\nrest of body" {
		t.Fatalf("content = %q, want updated body", got.Content)
	}
}

func TestStoreNotePathCreatesUniqueSecureTemp(t *testing.T) {
	store := openTestStore(t)
	note, err := store.Create("title", "exported body")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	path1 := store.NotePath(note.ID)
	if path1 == "" {
		t.Fatal("NotePath returned empty")
	}
	t.Cleanup(func() { removeNoteExport(path1) })
	path2 := store.NotePath(note.ID)
	if path2 == "" {
		t.Fatal("second NotePath returned empty")
	}
	t.Cleanup(func() { removeNoteExport(path2) })

	if path1 == path2 {
		t.Fatalf("NotePath reused %q", path1)
	}
	if path1 == filepath.Join(os.TempDir(), "sidecar-note-"+note.ID+".md") {
		t.Fatal("NotePath used the predictable sidecar-note-<id>.md name")
	}

	for _, path := range []string{path1, path2} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat %s: %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 0600", path, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "exported body" {
			t.Fatalf("%s content = %q", path, data)
		}
	}
}

func TestNewStoreDefersValidationUntilFirstCommand(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, "test")
	if err != nil {
		t.Fatalf("NewStore should not perform startup I/O: %v", err)
	}
	if _, err := store.List(false); err == nil {
		t.Fatal("first command on an uninitialized project should report the td error")
	}
	_ = store.Close()
}

func TestProductionSaveContentUsesOneEditCommand(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	tdPath := filepath.Join(dir, "td")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TD_TEST_LOG"
printf '%s\n' '{"note":{"id":"nt-one","title":"kept","content":"new body","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:01Z","pinned":false,"archived":false}}'
`
	if err := os.WriteFile(tdPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TD_TEST_LOG", logPath)
	store, err := NewStore(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.SaveContent("nt-one", "new body")
	if err != nil || note == nil || note.Content != "new body" {
		t.Fatalf("SaveContent = %+v, %v", note, err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.FieldsFunc(strings.TrimSpace(string(data)), func(r rune) bool { return r == '\n' })
	if len(lines) != 1 || !strings.Contains(lines[0], "note edit nt-one") || strings.Contains(lines[0], "show") {
		t.Fatalf("td calls = %q, want one edit and no show", string(data))
	}
}

func TestProductionTDCommandTimesOut(t *testing.T) {
	dir := t.TempDir()
	tdPath := filepath.Join(dir, "td")
	if err := os.WriteFile(tdPath, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	store, err := NewStore(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	store.timeout = 40 * time.Millisecond
	started := time.Now()
	_, err = store.List(false)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("List error = %v, want visible timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}
