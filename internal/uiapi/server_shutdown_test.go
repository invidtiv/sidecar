package uiapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Shutdown must close bound listeners even when their Serve goroutines have
// not registered with net/http yet. A late close must never unlink a successor.
func TestShutdownBeforeServePreservesSuccessorSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "uiapi-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	lock, err := acquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, localSocketName)
	old, err := listenPrivateUnix(path)
	if err != nil {
		releaseLock(lock)
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	previous := &Server{dir: dir, instance: "previous", lock: lock, ctx: ctx, cancel: cancel}
	previous.addListener(ListenerLocal, old, ListenerInfo{Name: ListenerLocal, Network: "unix", Address: path})
	if err := previous.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	nextLock, err := acquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(nextLock)
	next, err := listenPrivateUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = next.Close() }()
	// Reproduce a goroutine scheduled only after the successor has bound.
	if err := previous.servers[0].Serve(previous.bound[0]); !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("late predecessor Serve removed successor socket: %v", err)
	}
}
