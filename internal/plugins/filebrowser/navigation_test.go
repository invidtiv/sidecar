package filebrowser

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
)

// newDeepTreePlugin builds a plugin over a tree shaped like
//
//	a/b/c/target.go
//	a/sibling-*/decoy.go        (one set per level)
//	root-sibling-*/decoy.go
//
// so a navigation to the deep path can be checked against everything it should
// not have read from disk.
func newDeepTreePlugin(t *testing.T) (*Plugin, string) {
	t.Helper()
	tmpDir := t.TempDir()

	deep := filepath.Join(tmpDir, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir deep path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deep, "target.go"), []byte("package c"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	// Siblings at every level of the descent, each holding a file so an
	// accidental full walk would have to read them.
	for _, parent := range []string{"", "a", filepath.Join("a", "b"), filepath.Join("a", "b", "c")} {
		for _, name := range []string{"sibling-one", "sibling-two"} {
			dir := filepath.Join(tmpDir, parent, name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir sibling %s: %v", dir, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "decoy.go"), []byte("package decoy"), 0o644); err != nil {
				t.Fatalf("write decoy in %s: %v", dir, err)
			}
		}
	}

	p := &Plugin{
		ctx: &plugin.Context{
			WorkDir: tmpDir,
			Logger:  slog.New(slog.NewTextHandler(os.Stderr, nil)),
		},
		width:  80,
		height: 24,
	}
	p.tree = NewFileTree(tmpDir)
	if err := p.tree.Build(); err != nil {
		t.Fatalf("build tree: %v", err)
	}
	return p, tmpDir
}

// assertSiblingsUnloaded fails when any directory outside the descent path had
// its children read from disk. This is the regression guard for the full-tree
// walks that navigation used to perform.
func assertSiblingsUnloaded(t *testing.T, p *Plugin) {
	t.Helper()

	unrelated := []string{
		"sibling-one",
		"sibling-two",
		filepath.Join("a", "sibling-one"),
		filepath.Join("a", "sibling-two"),
		filepath.Join("a", "b", "sibling-one"),
		filepath.Join("a", "b", "sibling-two"),
		filepath.Join("a", "b", "c", "sibling-one"),
		filepath.Join("a", "b", "c", "sibling-two"),
	}

	byPath := map[string]*FileNode{}
	var collect func(*FileNode)
	collect = func(node *FileNode) {
		for _, child := range node.Children {
			byPath[child.Path] = child
			collect(child)
		}
	}
	collect(p.tree.Root)

	for _, rel := range unrelated {
		node, ok := byPath[rel]
		if !ok {
			continue // never even reached: better than loaded
		}
		if len(node.Children) != 0 {
			t.Errorf("navigation loaded children of unrelated directory %s (%d entries)",
				rel, len(node.Children))
		}
	}
}

func TestNavigateToFile_DoesNotLoadUnrelatedDirs(t *testing.T) {
	p, _ := newDeepTreePlugin(t)
	target := filepath.Join("a", "b", "c", "target.go")

	updated, _ := p.navigateToFile(target)
	got := updated.(*Plugin)

	node := got.tree.GetNode(got.treeCursor)
	if node == nil || node.Path != target {
		t.Fatalf("cursor did not land on %s (got %+v)", target, node)
	}
	assertSiblingsUnloaded(t, got)
}

func TestSyncTreeSelection_DoesNotLoadUnrelatedDirs(t *testing.T) {
	p, _ := newDeepTreePlugin(t)
	target := filepath.Join("a", "b", "c", "target.go")

	// The target sits under collapsed directories, so this takes the fallback
	// branch rather than the FlatList lookup.
	for _, node := range p.tree.FlatList {
		if node.Path == target {
			t.Fatalf("target %s is already visible; test would not exercise the fallback", target)
		}
	}

	p.syncTreeSelection(target)

	node := p.tree.GetNode(p.treeCursor)
	if node == nil || node.Path != target {
		t.Fatalf("cursor did not land on %s (got %+v)", target, node)
	}
	assertSiblingsUnloaded(t, p)
}

func TestNavigateToFile_MissingPathLeavesTreeAlone(t *testing.T) {
	p, _ := newDeepTreePlugin(t)
	before := len(p.tree.FlatList)

	updated, cmd := p.navigateToFile(filepath.Join("a", "b", "c", "nope.go"))
	if cmd != nil {
		t.Error("navigating to a missing path returned a command")
	}
	got := updated.(*Plugin)
	if n := len(got.tree.FlatList); n != before {
		t.Errorf("flat list changed for a missing path: %d -> %d", before, n)
	}

	// The miss path returns before flattening, so the flat list alone proves
	// nothing: check the ancestors were not left marked expanded, which the next
	// Flatten() (any j/k/l keypress) would make visible.
	for _, rel := range []string{"a", filepath.Join("a", "b"), filepath.Join("a", "b", "c")} {
		if node := got.tree.FindByPath(rel); node != nil && node.IsExpanded {
			t.Errorf("failed navigation expanded %s", rel)
		}
	}
	got.tree.Flatten()
	if n := len(got.tree.FlatList); n != before {
		t.Errorf("failed navigation opened the tree on the next flatten: %d -> %d", before, n)
	}
}

func TestNavigateToFile_SyncsWatcherDirs(t *testing.T) {
	p, tmpDir := newDeepTreePlugin(t)

	w, err := NewTreeWatcher()
	if err != nil {
		t.Fatalf("NewTreeWatcher() failed: %v", err)
	}
	defer w.Stop()
	p.watcher = w

	p.navigateToFile(filepath.Join("a", "b", "c", "target.go"))

	w.mu.Lock()
	watched := make(map[string]bool, len(w.watched))
	for dir := range w.watched {
		watched[dir] = true
	}
	w.mu.Unlock()

	// Every directory the navigation opened has to be watched, or files an agent
	// creates in them never show up.
	for _, rel := range []string{".", "a", filepath.Join("a", "b"), filepath.Join("a", "b", "c")} {
		abs, err := filepath.Abs(filepath.Join(tmpDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !watched[abs] {
			t.Errorf("expanded directory %s is not watched (watched: %v)", rel, watched)
		}
	}
}
