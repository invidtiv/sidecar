package notes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const testActionLogSchema = `CREATE TABLE IF NOT EXISTS action_log (
	id TEXT PRIMARY KEY, session_id TEXT, action_type TEXT, entity_type TEXT,
	entity_id TEXT, previous_data TEXT, new_data TEXT, timestamp TEXT, undone INTEGER
)`

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "issues.db")
	store, err := NewStore(path, "test-session")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.db.Exec(testActionLogSchema); err != nil {
		t.Fatalf("action_log schema: %v", err)
	}
	return store
}

func TestNewStoreUsesModerncWAL(t *testing.T) {
	store := openTestStore(t)

	var mode string
	if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode=%q, want wal", mode)
	}

	// mattn registers as sqlite3; modernc's driver type name must not.
	got := fmt.Sprintf("%T", store.db.Driver())
	if strings.Contains(got, "sqlite3") {
		t.Fatalf("driver %s is mattn/go-sqlite3; want modernc", got)
	}
	if !strings.Contains(strings.ToLower(got), "sqlite") {
		t.Fatalf("driver %s does not look like modernc sqlite", got)
	}
}

func TestWriteLockTimeout(t *testing.T) {
	store := openTestStore(t)
	lockPath := filepath.Join(filepath.Dir(store.dbPath), dbLockName)
	holder, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(holder.Fd()), syscall.LOCK_UN) }()

	err = store.withWriteLock(func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "write lock timeout") {
		t.Fatalf("want write lock timeout, got %v", err)
	}
}

func TestWriteLockSerializes(t *testing.T) {
	store := openTestStore(t)
	const n = 8
	var (
		mu      sync.Mutex
		current int
		maxSeen int
		wg      sync.WaitGroup
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			err := store.withWriteLock(func() error {
				mu.Lock()
				current++
				if current > maxSeen {
					maxSeen = current
				}
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				mu.Lock()
				current--
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Errorf("withWriteLock: %v", err)
			}
		}()
	}
	wg.Wait()
	if maxSeen != 1 {
		t.Fatalf("max concurrent lock holders = %d, want 1", maxSeen)
	}
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
}
