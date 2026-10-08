package filefind

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestIndexCacheInvalidationAndCancellation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "first.go"), "x")
	var idx Index
	files, _, err := idx.Paths(t.Context(), root, nil)
	if err != nil || !reflect.DeepEqual(files, []string{"first.go"}) {
		t.Fatalf("%v %v", files, err)
	}
	writeFile(t, filepath.Join(root, "second.go"), "x")
	cached, _, err := idx.Paths(t.Context(), root, nil)
	if err != nil || !reflect.DeepEqual(cached, files) {
		t.Fatal("typing rescanned the cache")
	}
	idx.entries[root].cache.Scanned = time.Now().Add(-DefaultMaxAge - time.Second)
	refreshed, _, err := idx.Paths(t.Context(), root, nil)
	if err != nil || len(refreshed) != 2 {
		t.Fatalf("expired cache did not invalidate: %v %v", refreshed, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := idx.Paths(ctx, root, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := FilterContext(ctx, refreshed, "go", 10, FilterOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("matcher cancellation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("second.go\n"), 0600); err != nil {
		t.Fatal(err)
	}
	idx.entries[root].cache.Scanned = time.Now().Add(-DefaultMaxAge - time.Second)
	refreshed, _, err = idx.Paths(t.Context(), root, nil)
	if err != nil || !reflect.DeepEqual(refreshed, []string{".gitignore", "first.go"}) {
		t.Fatalf("ignore invalidation: %v %v", refreshed, err)
	}
}

func TestIndexWaitCancellationAndRootReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	writeFile(t, filepath.Join(root, "old.go"), "x")
	var idx Index
	if _, _, err := idx.Paths(t.Context(), root, nil); err != nil {
		t.Fatal(err)
	}
	entry := idx.entries[root]
	entry.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, _, err := idx.Paths(ctx, root, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting ignored cancellation: %v", err)
	}
	<-entry.gate
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "new.go"), "x")
	files, _, err := idx.Paths(t.Context(), root, nil)
	if err != nil || !reflect.DeepEqual(files, []string{"new.go"}) {
		t.Fatalf("root replacement retained old index: %v %v", files, err)
	}
}
