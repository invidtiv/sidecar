package adapter

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// mkdir creates a directory whose name contains a space, matching the real
// Warp/Kiro database locations on macOS.
func spacedPath(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "Application Support", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A missing database must not be created. The bare "path?mode=ro" form did
// create it, because the driver drops the query string and passes
// SQLITE_OPEN_CREATE.
func TestReadOnlyDSN_DoesNotCreateMissingDB(t *testing.T) {
	path := spacedPath(t, "missing.sqlite")

	db, err := sql.Open("sqlite3", ReadOnlyDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Ping(); err == nil {
		t.Error("Ping on a nonexistent read-only database should fail")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("database file was created at %s", path)
	}
}

func TestReadOnlyDSN_ReadsExistingDBAndRejectsWrites(t *testing.T) {
	path := spacedPath(t, "real.sqlite")

	w, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec("CREATE TABLE t(x); INSERT INTO t VALUES (42)"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	db, err := sql.Open("sqlite3", ReadOnlyDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var x int
	if err := db.QueryRow("SELECT x FROM t").Scan(&x); err != nil {
		t.Fatalf("read existing database: %v", err)
	}
	if x != 42 {
		t.Errorf("x = %d, want 42", x)
	}

	if _, err := db.Exec("INSERT INTO t VALUES (1)"); err == nil {
		t.Error("write to a read-only connection should fail")
	}
}
