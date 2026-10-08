package noteview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// td-e4be7d: an API note read its client abandons must stop at once. The
// per-client content-read slot is held until the lookup returns.
func TestLookupContextStopsTdWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	started := time.Now()
	data, err := LookupContext(ctx, t.TempDir(), "nt-abc123")
	if !errors.Is(err, context.Canceled) || data != nil {
		t.Fatalf("LookupContext = %#v, %v; want context.Canceled", data, err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancelled note read took %v", elapsed)
	}
}

// A helper that inherits td's stdout (a daemon it starts) must not keep the
// read waiting after td itself was killed.
func TestLookupContextDoesNotWaitForInheritedStdout(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte("#!/bin/sh\nsleep 30 &\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	started := time.Now()
	if _, err := LookupContext(ctx, t.TempDir(), "nt-abc123"); err == nil {
		t.Fatal("cancelled note read succeeded")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancelled note read waited %v for an inherited stdout", elapsed)
	}
}
