package filefind

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCacheEnsureScansAndApplies(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main")
	writeFile(t, filepath.Join(root, "src", "app.go"), "package src")

	var c Cache
	cmd := c.Ensure(root, 7)
	if cmd == nil {
		t.Fatal("Ensure returned no command for an empty cache")
	}
	if !c.Scanning {
		t.Error("Scanning should be set while the scan is in flight")
	}

	msg, ok := cmd().(ScannedMsg)
	if !ok {
		t.Fatalf("scan produced %T, want ScannedMsg", msg)
	}
	if msg.Epoch != 7 {
		t.Errorf("Epoch = %d, want 7", msg.Epoch)
	}
	if msg.Dirs {
		t.Error("file cache produced a directory scan")
	}
	if len(msg.Files) != 2 {
		t.Fatalf("Files = %v, want main.go and src/app.go", msg.Files)
	}

	c.Apply(msg)
	if c.Scanning || !c.OK {
		t.Errorf("after Apply: Scanning=%v OK=%v, want false/true", c.Scanning, c.OK)
	}
	if len(c.Files) != 2 {
		t.Errorf("Files = %v, want 2 entries", c.Files)
	}

	if cmd := c.Ensure(root, 7); cmd != nil {
		t.Error("Ensure rescanned a current cache")
	}
}

func TestCacheEnsureSkipsWhileScanning(t *testing.T) {
	root := t.TempDir()
	c := Cache{Scanning: true}
	if cmd := c.Ensure(root, 0); cmd != nil {
		t.Error("Ensure started a second scan while one was in flight")
	}
}

func TestCacheEnsureKeepsExistingContentsDuringRescan(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main")

	c := Cache{Files: []string{"stale.go"}, OK: true, Dirty: true}
	cmd := c.Ensure(root, 0)
	if cmd == nil {
		t.Fatal("dirty cache did not rescan")
	}
	if len(c.Files) != 1 || c.Files[0] != "stale.go" {
		t.Errorf("Files = %v, want the old contents to stay visible", c.Files)
	}
	if c.Dirty {
		t.Error("Dirty should be cleared at the start of the scan")
	}

	// A change arriving mid-scan re-marks the cache, so the result about to
	// land is not mistaken for fresh.
	c.MarkDirty()
	c.Apply(cmd().(ScannedMsg))
	if !c.Dirty {
		t.Error("a change during the scan should leave the cache dirty")
	}
	if cmd := c.Ensure(root, 0); cmd == nil {
		t.Error("a cache dirtied during a scan should rescan")
	}
}

func TestCacheDirsScansDirectories(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "app.go"), "package src")

	var c Cache
	cmd := c.EnsureDirs(root, 0)
	if cmd == nil {
		t.Fatal("Ensure returned no command")
	}
	msg := cmd().(ScannedMsg)
	if !msg.Dirs {
		t.Error("directory cache produced a file scan")
	}
	if len(msg.Files) != 1 || msg.Files[0] != "src" {
		t.Errorf("Files = %v, want [src]", msg.Files)
	}
}

func TestCacheReset(t *testing.T) {
	c := Cache{Files: []string{"src"}, OK: true, Scanning: true, Dirty: true, ErrText: "boom"}
	c.Reset()
	if c.Files != nil || c.OK || c.Scanning || c.Dirty || c.ErrText != "" {
		t.Errorf("Reset left state behind: %+v", c)
	}
}

func TestScanPathsRespectsGitignore(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".gitignore"), "ignored.go\n")
	writeFile(t, filepath.Join(root, "ignored.go"), "x")
	writeFile(t, filepath.Join(root, "kept.go"), "x")

	files, errText := ScanPaths(root, false)
	if errText != "" {
		t.Fatalf("scan error: %s", errText)
	}
	if len(files) != 1 || files[0] != "kept.go" {
		t.Errorf("files = %v, want [kept.go]", files)
	}
}
