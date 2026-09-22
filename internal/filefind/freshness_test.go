package filefind

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// fakeClock pins the cache's clock so a test can age a list deliberately.
func fakeClock(t *testing.T, start time.Time) func(time.Duration) {
	t.Helper()
	now := start
	prev := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = prev })
	return func(d time.Duration) { now = now.Add(d) }
}

// settleTree backdates every directory under root so its stamps are old
// enough to be trusted (see stampSettle); fixtures are written moments before
// they are walked.
func settleTree(t *testing.T, root string) {
	t.Helper()
	settled := time.Now().Add(-time.Minute)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() == ".gitignore" {
			return os.Chtimes(path, settled, settled)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func landScan(t *testing.T, c *Cache, cmd tea.Cmd) ScannedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no scan command")
	}
	msg, ok := cmd().(ScannedMsg)
	if !ok {
		t.Fatalf("scan produced %T, want ScannedMsg", msg)
	}
	c.Apply(msg)
	return msg
}

// The bug this guards against: a file written into a directory the watcher was
// not watching, found by nobody until the process restarted.
func TestCacheAgedListIsProbedThenWalkedOnlyIfTheTreeMoved(t *testing.T) {
	advance := fakeClock(t, time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC))
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main")
	writeFile(t, filepath.Join(root, "briefs", "old", "BRIEF.md"), "old")
	settleTree(t, root)

	var c Cache
	first := landScan(t, &c, c.Ensure(root, 1))
	if first.Unchanged || len(first.Stamps) == 0 {
		t.Fatalf("first walk: unchanged=%v stamps=%d", first.Unchanged, len(first.Stamps))
	}
	if len(c.Files) != 2 {
		t.Fatalf("files = %v", c.Files)
	}

	// Within the age: nothing to do.
	advance(DefaultMaxAge / 2)
	if cmd := c.Ensure(root, 1); cmd != nil {
		t.Fatal("a fresh cache was rescanned")
	}

	// Past the age with nothing moved: a probe, not a walk, and the list
	// stays as it was.
	advance(DefaultMaxAge)
	probed := landScan(t, &c, c.Ensure(root, 1))
	if !probed.Unchanged {
		t.Fatal("an unchanged tree was walked in full")
	}
	if len(c.Files) != 2 || len(c.Stamps) != len(first.Stamps) {
		t.Errorf("probe disturbed the list: files=%v stamps=%d", c.Files, len(c.Stamps))
	}
	if c.Ensure(root, 1) != nil {
		t.Error("the probe did not move the clock")
	}

	// A file lands in a directory nobody was watching. Directory mtimes are
	// coarse on some filesystems, so push the new entry's parent into the
	// future rather than trusting the write to move it.
	advance(DefaultMaxAge + time.Second)
	newDir := filepath.Join(root, "briefs", "new")
	writeFile(t, filepath.Join(newDir, "USE-CASES.md"), "new")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "briefs"), future, future); err != nil {
		t.Fatal(err)
	}
	walked := landScan(t, &c, c.Ensure(root, 1))
	if walked.Unchanged {
		t.Fatal("a tree with a new file probed as unchanged")
	}
	if len(c.Files) != 3 {
		t.Errorf("files after the walk = %v, want the new file too", c.Files)
	}
}

func TestCacheDirtyWalksWithoutProbing(t *testing.T) {
	advance := fakeClock(t, time.Now())
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main")

	var c Cache
	landScan(t, &c, c.Ensure(root, 1))
	advance(DefaultMaxAge * 2)
	c.MarkDirty()
	msg := landScan(t, &c, c.Ensure(root, 1))
	if msg.Unchanged {
		t.Error("a cache told the disk moved was probed rather than walked")
	}
}

func TestCacheWithoutStampsWalksWhenAged(t *testing.T) {
	advance := fakeClock(t, time.Now())
	walks := 0
	c := Cache{Scan: func(root string, dirs bool) ([]string, string) {
		walks++
		return []string{"remote.go"}, ""
	}}
	landScan(t, &c, c.Ensure("/remote", 1))
	if c.Ensure("/remote", 1) != nil {
		t.Fatal("fresh remote list rescanned")
	}
	advance(DefaultMaxAge * 2)
	msg := landScan(t, &c, c.Ensure("/remote", 1))
	if msg.Unchanged || walks != 2 {
		t.Errorf("aged stamp-less cache: unchanged=%v walks=%d", msg.Unchanged, walks)
	}
}

func TestCacheMaxAgeOverrides(t *testing.T) {
	advance := fakeClock(t, time.Now())
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main")

	never := Cache{MaxAge: -1}
	landScan(t, &never, never.Ensure(root, 1))
	advance(time.Hour)
	if never.Ensure(root, 1) != nil {
		t.Error("MaxAge < 0 still aged out")
	}

	longer := Cache{MaxAge: time.Minute}
	landScan(t, &longer, longer.Ensure(root, 1))
	advance(DefaultMaxAge * 2)
	if longer.Ensure(root, 1) != nil {
		t.Error("a longer MaxAge aged out at the default")
	}
	advance(time.Minute)
	if longer.Ensure(root, 1) == nil {
		t.Error("a longer MaxAge never aged out")
	}
}

func TestScanTreeStampsEveryDirectoryItDescends(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "one.go"), "")
	writeFile(t, filepath.Join(root, "a", "b", "two.go"), "")
	writeFile(t, filepath.Join(root, "node_modules", "dep", "index.js"), "")
	writeFile(t, filepath.Join(root, "ignored", "x.go"), "")
	writeFile(t, filepath.Join(root, ".gitignore"), "ignored/\n")
	settleTree(t, root)

	_, stamps, errText := scanTree(root, false)
	if errText != "" {
		t.Fatal(errText)
	}
	got := map[string]bool{}
	for _, s := range stamps {
		got[s.Path] = true
		if s.ModTime.IsZero() {
			t.Errorf("%q has no mtime", s.Path)
		}
	}
	for _, want := range []string{".", "a", "a/b"} {
		if !got[want] {
			t.Errorf("no stamp for %q: %v", want, stamps)
		}
	}
	for _, skipped := range []string{"node_modules", "node_modules/dep", "ignored"} {
		if got[skipped] {
			t.Errorf("a skipped directory was stamped: %q", skipped)
		}
	}
	if !treeUnchanged(root, stamps) {
		t.Error("an untouched tree reads as changed")
	}
	if err := os.Remove(filepath.Join(root, "a", "b", "two.go")); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "a", "b"), future, future); err != nil {
		t.Fatal(err)
	}
	if treeUnchanged(root, stamps) {
		t.Error("a removal was not noticed")
	}
	if treeUnchanged(root, nil) {
		t.Error("no stamps must mean no answer, not 'unchanged'")
	}
}

func TestFinderKeepsCursorOnItsFileAcrossARescan(t *testing.T) {
	f := NewFinder(nil, "/root", 1)
	f.Open()
	f.Update(ScannedMsg{Files: []string{"a/app.go", "b/app.go", "c/app.go"}, Epoch: 1})
	f.SetQuery("app")
	f.SetCursor(2)
	selected := f.Matches()[2].Path

	// A rescan lands with a new file that sorts ahead of the selection.
	f.Update(ScannedMsg{Files: []string{"a/app.go", "app.go", "b/app.go", "c/app.go"}, Epoch: 1})
	if got := f.Matches()[f.Cursor()].Path; got != selected {
		t.Errorf("cursor moved from %q to %q when the list changed under it", selected, got)
	}

	// A probe that found nothing moved leaves the matches alone entirely.
	before := f.Matches()
	f.Update(ScannedMsg{Epoch: 1, Unchanged: true})
	if len(f.Matches()) != len(before) || f.Cache.Files == nil {
		t.Error("an Unchanged result disturbed the finder")
	}
}

// A host that routes a landed scan to the finder that issued it drops one that
// lands after the finder closed. Close must not leave the shared cache waiting
// for that result forever.
func TestFinderCloseReleasesTheScanInFlight(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main")

	var c Cache
	f := NewFinder(&c, root, 1)
	if f.Open() == nil {
		t.Fatal("first open issued no scan")
	}
	f.Close() // the scan's result is never delivered

	g := NewFinder(&c, root, 1)
	if g.Open() == nil {
		t.Fatal("after a dropped scan the cache never scans again")
	}
}

func TestScanTreeStampsTheRootGitignore(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "one.go"), "")
	writeFile(t, filepath.Join(root, ".gitignore"), "nothing\n")
	settleTree(t, root)

	_, stamps, _ := scanTree(root, false)
	if !treeUnchanged(root, stamps) {
		t.Fatal("an untouched tree reads as changed")
	}
	// Editing the ignore rules moves no directory, but changes the list.
	writeFile(t, filepath.Join(root, ".gitignore"), "a/\n")
	if treeUnchanged(root, stamps) {
		t.Error("an edited .gitignore was not noticed")
	}
}

func TestScanTreeWithholdsStampsItCannotVouchFor(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "one.go"), "")

	// Just written: the stamps have not settled, so none are returned and an
	// aged cache walks rather than trusting a probe.
	if _, stamps, _ := scanTree(root, false); stamps != nil {
		t.Errorf("stamps taken within %s of the walk were returned: %v", stampSettle, stamps)
	}

	settleTree(t, root)
	if _, stamps, _ := scanTree(root, false); len(stamps) == 0 {
		t.Error("settled stamps were withheld")
	}
}

func TestFilterWhitespaceOnlyQueryMatchesNothing(t *testing.T) {
	files := []string{"a.go", "b/c.go"}
	if got := FuzzyFilter(files, "   ", 10); len(got) != 0 {
		t.Errorf("a whitespace query returned %v", got)
	}
	if got := FuzzyFilter(files, "", 10); len(got) != 2 {
		t.Errorf("an empty query returned %v", got)
	}
}
