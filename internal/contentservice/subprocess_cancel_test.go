package contentservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/noteview"
)

// td-e4be7d: every API content read holds its per-client slot until the
// backend returns. A git helper that inherits stdout (an fsmonitor daemon, a
// hook) must not keep a cancelled read, and so its slot, alive.
func TestDefaultGitDoesNotWaitForInheritedStdoutAfterCancel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\nsleep 30 &\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	started := time.Now()
	if _, err := defaultGit(ctx, t.TempDir(), "rev-parse", "--git-dir"); err == nil {
		t.Fatal("cancelled git call succeeded")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancelled git call waited %v for an inherited stdout", elapsed)
	}
}

func TestDefaultLookupNoteStopsWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	started := time.Now()
	if _, err := defaultLookupNote(ctx, t.TempDir(), "nt-abc123"); err == nil {
		t.Fatal("cancelled note lookup succeeded")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancelled note lookup took %v", elapsed)
	}
}

// A cancelled note read surfaces as cancellation, not as a refusal of the note.
func TestReadNoteKeepsCancellationDistinctFromRefusal(t *testing.T) {
	s := &Service{LookupNote: func(context.Context, string, string) (*noteview.Data, error) { return nil, context.DeadlineExceeded }}
	if _, err := s.readNoteAt(context.Background(), t.TempDir(), "nt-abc123", ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}
