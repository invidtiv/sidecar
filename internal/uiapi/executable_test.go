package uiapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchExecutableFollowsReplacementAndRetargetedLink(t *testing.T) {
	for _, link := range []bool{false, true} {
		t.Run(map[bool]string{false: "replace", true: "retarget"}[link], func(t *testing.T) {
			root := t.TempDir()
			old := filepath.Join(root, "old")
			next := filepath.Join(root, "next")
			path := old
			for _, file := range []string{old, next} {
				if err := os.WriteFile(file, []byte("same size"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if link {
				path = filepath.Join(root, "sidecar")
				if err := os.Symlink(old, path); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			changed, err := WatchExecutable(ctx, path, 5*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-changed:
				t.Fatal("unchanged executable triggered shutdown")
			case <-time.After(15 * time.Millisecond):
			}
			if link {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(next, path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(next, path); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-changed:
			case <-time.After(time.Second):
				t.Fatal("replacement did not trigger shutdown")
			}
		})
	}
}
func TestWatchExecutableIgnoresTemporaryAbsenceAndCancels(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sidecar")
	parked := path + ".old"
	if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed, err := WatchExecutable(ctx, path, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, parked); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
		t.Fatal("transient disappearance triggered exit")
	case <-time.After(15 * time.Millisecond):
	}
	cancel()
	if err := os.WriteFile(path, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
		t.Fatal("watch fired after cancellation")
	case <-time.After(15 * time.Millisecond):
	}
}
