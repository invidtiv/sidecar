package contentservice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveFileRelativeStaysInsideRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveFile(root, "note.md")
	if err != nil {
		t.Fatal(err)
	}
	if got.Display != "note.md" {
		t.Fatalf("display = %q, want note.md", got.Display)
	}
	if !strings.HasSuffix(got.Absolute, "note.md") {
		t.Fatalf("absolute = %q", got.Absolute)
	}
}

func TestResolveFileRefusesRelativeTraversal(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	root := filepath.Join(parent, "proj")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(outside, []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveFile(root, "../secret.txt"); !IsRejected(err) {
		t.Fatalf("traversal: err = %v, want rejected", err)
	}
}

func TestResolveFileRefusesSymlinkEscape(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	root := filepath.Join(parent, "proj")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(outside, []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveFile(root, "escape"); !IsRejected(err) {
		t.Fatalf("symlink escape: err = %v, want rejected", err)
	}
}

func TestResolveFileAllowsAbsoluteRegularFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "abs.txt")
	if err := os.WriteFile(outside, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveFile(root, outside)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	if got.Display != resolved && got.Absolute != resolved {
		t.Fatalf("absolute resolve = %+v, want %s", got, resolved)
	}
}

func TestResolveFileRefusesControlCharactersAndOversize(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := ResolveFile(root, "bad\nname"); !IsRejected(err) {
		t.Fatalf("control: err = %v, want rejected", err)
	}
	long := strings.Repeat("a", MaxLocatorBytes+1)
	if _, err := ResolveFile(root, long); !IsRejected(err) {
		t.Fatalf("oversize locator: err = %v, want rejected", err)
	}
}

func TestReadFileConditionalRevision(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "note.md")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := ReadFile(context.Background(), root, "note.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.NotModified || first.Content != "hello\n" || first.Revision == "" {
		t.Fatalf("first read = %+v", first)
	}
	second, err := ReadFile(context.Background(), root, "note.md", first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !second.NotModified || second.Revision != first.Revision || second.Content != "" {
		t.Fatalf("conditional read = %+v", second)
	}
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := ReadFile(context.Background(), root, "note.md", first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if third.NotModified || third.Content != "changed\n" || third.Revision == first.Revision {
		t.Fatalf("changed read = %+v", third)
	}
}

func TestReadFileHonoursContextCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadFile(ctx, t.TempDir(), "note.md", ""); err == nil {
		t.Fatal("cancelled context succeeded")
	}
}
