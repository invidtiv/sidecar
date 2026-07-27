package warp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/adapter"
)

func TestSearchMessages_InterfaceCompliance(t *testing.T) {
	a := New()
	// Verify interface compliance at compile time
	var _ adapter.MessageSearcher = a
}

func TestSearchMessages_NonExistentSession(t *testing.T) {
	// Point at an empty temp dir rather than New()'s discovered path: reading the
	// user's live Warp database makes this test depend on whether Warp is running.
	a := New()
	a.dbPath = filepath.Join(t.TempDir(), "warp.sqlite")
	t.Cleanup(func() { _ = a.Close() })

	matches, err := a.SearchMessages("nonexistent-session-xyz", "test", adapter.DefaultSearchOptions())
	if err == nil {
		t.Error("expected an error when the database is absent")
	}
	if matches != nil {
		t.Errorf("matches = %v, want nil", matches)
	}
	if _, statErr := os.Stat(a.dbPath); !os.IsNotExist(statErr) {
		t.Error("searching created the database file")
	}
}
