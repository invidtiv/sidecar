package notes

import (
	"testing"

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

func TestNewStoreOpensProjectRoot(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewStore(dir, "test"); err == nil {
		t.Fatal("NewStore on empty dir should fail without td init")
	}
	if _, err := NewTestStore(dir, "test"); err != nil {
		t.Fatalf("NewTestStore: %v", err)
	}
	store, err := NewStore(dir, "test")
	if err != nil {
		t.Fatalf("NewStore after init: %v", err)
	}
	_ = store.Close()
}
