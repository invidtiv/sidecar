package rootfile

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenNoFollowRejectsPostPolicySymlinkReplacement(t *testing.T) {
	t.Parallel()
	for _, component := range []string{"file", "directory"} {
		t.Run(component, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"ordinary", ".git"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, dir := range []string{"ordinary", ".git"} {
				if err := os.WriteFile(filepath.Join(root, dir, "config"), []byte(dir+" content"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			pinned, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = pinned.Close() }()
			rel := "ordinary/config"
			// Deterministically replace a component after the caller's policy check.
			if component == "file" {
				if err := os.Remove(filepath.Join(root, rel)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../.git/config", filepath.Join(root, rel)); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(filepath.Join(root, "ordinary"), filepath.Join(root, "previous")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(".git", filepath.Join(root, "ordinary")); err != nil {
					t.Fatal(err)
				}
			}
			// os.Root alone permits this in-root redirect: reproduction of the race.
			vulnerable, err := pinned.Open(rel)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(vulnerable)
			_ = vulnerable.Close()
			if err != nil || string(data) != ".git content" {
				t.Fatalf("race not reproduced: %q %v", data, err)
			}
			safe, err := OpenNoFollow(pinned, rel)
			if err == nil {
				_ = safe.Close()
				t.Fatal("read followed a component replaced after policy")
			}
		})
	}
}

func TestOpenNoFollowReadsPinnedContent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "note.md"), []byte("ordinary content"), 0600); err != nil {
		t.Fatal(err)
	}
	pinned, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pinned.Close() }()
	file, err := OpenNoFollow(pinned, "docs/note.md")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "ordinary content" {
		t.Fatalf("content=%q err=%v", data, err)
	}
	for _, path := range []string{"../note.md", "docs/../note.md", filepath.Join(root, "docs", "note.md")} {
		if file, err := OpenNoFollow(pinned, path); err == nil {
			_ = file.Close()
			t.Errorf("invalid path accepted: %s", path)
		}
	}
}
