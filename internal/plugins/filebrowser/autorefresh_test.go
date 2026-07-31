package filebrowser

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/plugin"
)

// createRefreshTestPlugin builds a plugin rooted at tmpDir with the given
// relative files already present and the tree built.
func createRefreshTestPlugin(t *testing.T, tmpDir string, files ...string) *Plugin {
	t.Helper()
	for _, f := range files {
		full := filepath.Join(tmpDir, f)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", f, err)
		}
		if err := os.WriteFile(full, []byte("x"), 0644); err != nil {
			t.Fatalf("failed to create %s: %v", f, err)
		}
	}

	p := &Plugin{
		ctx: &plugin.Context{
			WorkDir:     tmpDir,
			ProjectRoot: tmpDir,
			Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		},
		width:  80,
		height: 24,
		// Skip first-build state restoration; these tests exercise the swap only.
		stateRestored: true,
	}
	p.tree = NewFileTree(tmpDir)
	if err := p.tree.Build(); err != nil {
		t.Fatalf("failed to build file tree: %v", err)
	}
	return p
}

// runRefresh runs the refresh command and returns the TreeBuiltMsg it produced.
func runRefresh(t *testing.T, p *Plugin) TreeBuiltMsg {
	t.Helper()
	cmd := p.refresh()
	if cmd == nil {
		t.Fatal("refresh() returned nil command")
	}
	msg, ok := cmd().(TreeBuiltMsg)
	if !ok {
		t.Fatal("refresh() command did not produce a TreeBuiltMsg")
	}
	return msg
}

func TestRefresh_BuildsIntoNewTreeWithoutMutatingLiveTree(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "a.txt")

	liveTree := p.tree
	liveLen := liveTree.Len()

	// External change that a rebuild should pick up
	if err := os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	msg := runRefresh(t, p)

	if msg.Err != nil {
		t.Fatalf("unexpected build error: %v", msg.Err)
	}
	if msg.Tree == nil {
		t.Fatal("expected a built tree in the message")
	}
	if msg.Tree == liveTree {
		t.Error("refresh must build a separate tree, not rebuild the live one")
	}
	if p.tree != liveTree {
		t.Error("refresh must not swap the tree before the message is handled")
	}
	if liveTree.Len() != liveLen {
		t.Errorf("live tree mutated by refresh: len %d -> %d", liveLen, liveTree.Len())
	}
	if msg.Tree.IndexOfPath("b.txt") < 0 {
		t.Error("built tree should contain the newly created file")
	}
}

func TestRefresh_SnapshotsSpecFromLiveTree(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "dir/inner.txt", "a.txt")

	dir := p.tree.FindByPath("dir")
	if dir == nil {
		t.Fatal("expected dir in tree")
	}
	if err := p.tree.Expand(dir); err != nil {
		t.Fatal(err)
	}
	p.tree.SortMode = SortByTime
	p.tree.ShowIgnored = false

	msg := runRefresh(t, p)
	if msg.Err != nil {
		t.Fatalf("unexpected build error: %v", msg.Err)
	}

	if msg.Tree.SortMode != SortByTime {
		t.Errorf("SortMode = %v, want %v", msg.Tree.SortMode, SortByTime)
	}
	if msg.Tree.ShowIgnored {
		t.Error("ShowIgnored should be carried into the rebuilt tree")
	}
	if msg.Tree.IndexOfPath(filepath.Join("dir", "inner.txt")) < 0 {
		t.Error("expanded state should be preserved across the rebuild")
	}
}

func TestRefresh_CmdIsSafeToRunConcurrentlyWithReads(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "a.txt", "b.txt", "c.txt")

	// The command must touch only its own snapshot. Under -race this fails if
	// the build ever reaches back into p.tree.
	cmd := p.refresh()
	if cmd == nil {
		t.Fatal("refresh() returned nil command")
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd()
		}()
	}

	for i := 0; i < 200; i++ {
		_ = p.tree.Len()
		if node := p.tree.GetNode(0); node != nil {
			_ = node.Name
		}
		_ = p.tree.GetExpandedPaths()
	}
	wg.Wait()
}

func TestTreeBuiltMsg_SwapsTreeIn(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "a.txt")

	oldTree := p.tree
	if err := os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	msg := runRefresh(t, p)

	p.Update(msg)

	if p.tree == oldTree {
		t.Fatal("expected the rebuilt tree to be swapped in")
	}
	if p.tree.IndexOfPath("b.txt") < 0 {
		t.Error("swapped-in tree should contain the new file")
	}
}

func TestTreeBuiltMsg_StaleEpochIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "a.txt")

	oldTree := p.tree
	msg := runRefresh(t, p)

	// Simulate a project switch landing before the build finished
	p.ctx.Epoch++

	p.Update(msg)

	if p.tree != oldTree {
		t.Error("a tree built for a previous epoch must not be swapped in")
	}
}

func TestTreeBuiltMsg_BuildErrorKeepsExistingTree(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "a.txt")

	oldTree := p.tree
	p.Update(TreeBuiltMsg{Err: os.ErrNotExist})

	if p.tree != oldTree {
		t.Error("a failed build must leave the existing tree in place")
	}
}

func TestTreeBuiltMsg_ReanchorsCursorByPath(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "b.txt", "c.txt")

	// Cursor on c.txt (sorted: b.txt, c.txt)
	p.treeCursor = p.tree.IndexOfPath("c.txt")
	if p.treeCursor < 0 {
		t.Fatal("expected c.txt in tree")
	}

	// A file sorting before the cursor appears, shifting every index down one
	if err := os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	msg := runRefresh(t, p)
	p.Update(msg)

	node := p.tree.GetNode(p.treeCursor)
	if node == nil || node.Path != "c.txt" {
		t.Errorf("cursor should still be on c.txt, got %v", node)
	}
}

func TestTreeBuiltMsg_ClampsCursorWhenTreeShrinks(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "a.txt", "b.txt", "c.txt")

	p.treeCursor = p.tree.Len() - 1
	cursorPath := p.tree.GetNode(p.treeCursor).Path

	// Everything below the cursor disappears, including the cursor's own file
	for _, f := range []string{"b.txt", "c.txt"} {
		if err := os.Remove(filepath.Join(tmpDir, f)); err != nil {
			t.Fatal(err)
		}
	}
	msg := runRefresh(t, p)
	if msg.CursorPath != cursorPath {
		t.Errorf("CursorPath = %q, want %q", msg.CursorPath, cursorPath)
	}
	p.Update(msg)

	if p.treeCursor >= p.tree.Len() {
		t.Errorf("cursor %d out of range for tree of length %d", p.treeCursor, p.tree.Len())
	}
	if p.tree.GetNode(p.treeCursor) == nil {
		t.Error("cursor should point at a real node after clamping")
	}
}

func TestTreeBuiltMsg_ClampsCursorToZeroOnEmptyTree(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "a.txt")

	p.treeCursor = 0
	if err := os.Remove(filepath.Join(tmpDir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	p.Update(runRefresh(t, p))

	if p.tree.Len() != 0 {
		t.Fatalf("expected empty tree, got %d nodes", p.tree.Len())
	}
	if p.treeCursor != 0 {
		t.Errorf("treeCursor = %d, want 0 for an empty tree", p.treeCursor)
	}
}

func TestTreeBuiltMsg_ReresolvesFileOpTarget(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "a.txt")

	oldTarget := p.tree.FindByPath("a.txt")
	if oldTarget == nil {
		t.Fatal("expected a.txt in tree")
	}
	p.fileOpMode = FileOpRename
	p.fileOpTarget = oldTarget

	p.Update(runRefresh(t, p))

	if p.fileOpTarget == oldTarget {
		t.Error("fileOpTarget still points into the discarded tree")
	}
	if p.fileOpTarget == nil || p.fileOpTarget.Path != "a.txt" {
		t.Fatalf("fileOpTarget should resolve to a.txt in the new tree, got %v", p.fileOpTarget)
	}
	if p.fileOpTarget != p.tree.FindByPath("a.txt") {
		t.Error("fileOpTarget should be the node from the current tree")
	}
}

func TestTreeBuiltMsg_KeepsFileOpTargetWhenPathNotVisible(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "dir/inner.txt")

	// A node under a collapsed directory is not in the flat list
	target := &FileNode{Path: filepath.Join("dir", "inner.txt"), Name: "inner.txt"}
	p.fileOpMode = FileOpRename
	p.fileOpTarget = target

	p.Update(runRefresh(t, p))

	if p.fileOpTarget != target {
		t.Error("fileOpTarget should be left alone when its path is not visible")
	}
}

func TestTreeBuiltMsg_KeepsExpansionChangedDuringTheBuild(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "dir/inner.txt", "open/inner.txt")

	// One directory starts open, one closed; the build snapshots that.
	open := p.tree.FindByPath("open")
	if open == nil {
		t.Fatal("expected open in tree")
	}
	if err := p.tree.Expand(open); err != nil {
		t.Fatal(err)
	}
	cmd := p.refresh()
	if cmd == nil {
		t.Fatal("refresh() returned nil command")
	}

	// While the build runs the user expands the other one and collapses this one.
	dir := p.tree.FindByPath("dir")
	if dir == nil {
		t.Fatal("expected dir in tree")
	}
	if err := p.tree.Expand(dir); err != nil {
		t.Fatal(err)
	}
	p.tree.Collapse(open)
	p.treeCursor = p.tree.IndexOfPath(filepath.Join("dir", "inner.txt"))
	if p.treeCursor < 0 {
		t.Fatal("expected dir/inner.txt to be visible")
	}

	p.Update(cmd())

	if node := p.tree.FindByPath("dir"); node == nil || !node.IsExpanded {
		t.Error("a directory expanded while the build ran was closed again")
	}
	if node := p.tree.FindByPath("open"); node == nil || node.IsExpanded {
		t.Error("a directory collapsed while the build ran was re-opened")
	}
	if node := p.tree.GetNode(p.treeCursor); node == nil || node.Path != filepath.Join("dir", "inner.txt") {
		t.Errorf("cursor moved during the build was reset, got %v", node)
	}
}

func TestTreeBuiltMsg_StaleGenerationIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "a.txt")

	// Two overlapping rebuilds; the slower first one must not land on top of
	// the newer result.
	first := runRefresh(t, p)
	if err := os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	second := runRefresh(t, p)

	p.Update(second)
	p.Update(first)

	if p.tree.IndexOfPath("b.txt") < 0 {
		t.Error("the older build overwrote the newer tree")
	}
}

func TestAppRefreshMsg_TriggersRebuild(t *testing.T) {
	tmpDir := t.TempDir()
	p := createRefreshTestPlugin(t, tmpDir, "a.txt")

	_, cmd := p.Update(app.RefreshMsg{})
	if cmd == nil {
		t.Fatal("app.RefreshMsg should trigger a rebuild")
	}
	if _, ok := cmd().(TreeBuiltMsg); !ok {
		t.Error("rebuild command should produce a TreeBuiltMsg")
	}
}

func TestRefresh_NilTreeReturnsNilCmd(t *testing.T) {
	p := &Plugin{}
	if cmd := p.refresh(); cmd != nil {
		t.Error("refresh() should be a no-op before the tree exists")
	}
}

var _ plugin.EpochMessage = TreeBuiltMsg{}

var _ tea.Msg = TreeBuiltMsg{}
