package viewerlayout

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/state"
)

func TestValidationErrorsAreDistinctFromStorageFailures(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "layouts")
	store := FileStore{Dir: dir}
	_, etag, err := store.Get("viewer", "project")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Put("viewer", "project", etag, Document{Layout: &state.PaneLayoutJSON{Kind: "unknown"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid tree must be identifiable: %v", err)
	}
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = (FileStore{Dir: blocked}).Put("viewer", "project", etag, Document{})
	if err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("disk failure must not blame the supplied tree: %v", err)
	}
}
