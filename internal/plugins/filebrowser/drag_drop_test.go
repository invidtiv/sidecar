package filebrowser

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
)

// newDropTestPlugin builds a plugin over a tree shaped for drop targeting:
//
//	dir-x/          (row 0, collapsed, contains child.txt)
//	foo/            (row 1, collapsed, contains inner.txt and sub/)
//	foobar/         (row 2, collapsed) - the near-miss for the descendant check
//	alpha.txt       (row 3)
//	beta.txt        (row 4)
//
// Hit regions are registered exactly where the view puts them, so the tests
// exercise the same coordinate math the running app does.
func newDropTestPlugin(t *testing.T) *Plugin {
	t.Helper()
	root := t.TempDir()

	for _, dir := range []string{"dir-x", "foo", filepath.Join("foo", "sub"), "foobar"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	files := []string{
		"alpha.txt",
		"beta.txt",
		filepath.Join("dir-x", "child.txt"),
		filepath.Join("foo", "inner.txt"),
		filepath.Join("foo", "sub", "deep.txt"),
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	p := New()
	p.ctx = &plugin.Context{
		WorkDir: root,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	p.width, p.height = 100, 30
	p.treeWidth, p.previewWidth = 30, 60
	p.tree = NewFileTree(root)
	if err := p.tree.Build(); err != nil {
		t.Fatalf("build tree: %v", err)
	}
	registerTreeRows(p)
	return p
}

// registerTreeRows registers the tree hit regions through the production render
// path. Deriving them from treeRowsViewport() instead would make every
// coordinate assertion below circular: the bug these tests exist to catch is
// precisely the viewport helper disagreeing with what the view draws.
func registerTreeRows(p *Plugin) {
	p.renderView()
}

// rowY returns the screen row a flat tree index is drawn on.
func rowY(p *Plugin, idx int) int {
	topY, _ := p.treeRowsViewport()
	return topY + (idx - p.treeScrollOff)
}

// startDragOn begins (and activates) a drag on the given flat row.
func startDragOn(t *testing.T, p *Plugin, idx int) {
	t.Helper()
	press(t, p, 3, rowY(p, idx))
	if !p.dragArmed {
		t.Fatalf("press on row %d did not arm a drag", idx)
	}
	// Cross the threshold without leaving the row: a pure horizontal move.
	motion(t, p, 3+dragThresholdDX, rowY(p, idx))
	if !p.dragActive {
		t.Fatalf("row %d: drag did not activate", idx)
	}
}

func rowIndex(t *testing.T, p *Plugin, path string) int {
	t.Helper()
	idx := p.tree.IndexOfPath(path)
	if idx < 0 {
		t.Fatalf("path %q is not a visible row", path)
	}
	return idx
}

// Dropping on a directory row targets that directory.
func TestResolveDropTargetOnDirectory(t *testing.T) {
	p := newDropTestPlugin(t)
	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))

	dir, idx := p.resolveDropTarget(rowIndex(t, p, "foo"))

	if dir != "foo" {
		t.Errorf("target dir = %q, want %q", dir, "foo")
	}
	if want := rowIndex(t, p, "foo"); idx != want {
		t.Errorf("highlight row = %d, want %d", idx, want)
	}
}

// Dropping on a file row targets that file's parent directory (Finder/VS Code
// behavior), and highlights the parent's row so the feedback names the real
// destination.
func TestResolveDropTargetOnFileUsesParentDir(t *testing.T) {
	p := newDropTestPlugin(t)
	if err := p.tree.Expand(p.tree.FindByPath("foo")); err != nil {
		t.Fatalf("expand foo: %v", err)
	}
	registerTreeRows(p)

	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))

	dir, idx := p.resolveDropTarget(rowIndex(t, p, filepath.Join("foo", "inner.txt")))

	if dir != "foo" {
		t.Errorf("target dir = %q, want %q (the file's parent)", dir, "foo")
	}
	if want := rowIndex(t, p, "foo"); idx != want {
		t.Errorf("highlight row = %d, want %d (the parent dir row)", idx, want)
	}
}

// A file at the top level resolves to the project root, which has no row of its
// own, so the hovered row stays the highlight.
func TestResolveDropTargetOnTopLevelFileIsRoot(t *testing.T) {
	p := newDropTestPlugin(t)
	if err := p.tree.Expand(p.tree.FindByPath("foo")); err != nil {
		t.Fatalf("expand foo: %v", err)
	}
	registerTreeRows(p)

	// Source lives inside foo/, so the root is not its parent.
	src := rowIndex(t, p, filepath.Join("foo", "inner.txt"))
	startDragOn(t, p, src)

	hovered := rowIndex(t, p, "beta.txt")
	dir, idx := p.resolveDropTarget(hovered)

	if dir != "" {
		t.Errorf("target dir = %q, want \"\" (project root)", dir)
	}
	if idx != hovered {
		t.Errorf("highlight row = %d, want the hovered row %d", idx, hovered)
	}
}

// Every rejection case: each of these would be a no-op or would corrupt the
// tree, and must report an invalid drop.
func TestResolveDropTargetRejections(t *testing.T) {
	tests := []struct {
		name   string
		source string // path being dragged
		hover  string // path of the row under the cursor
		// wantReason is the explanation the released drag has to give. A
		// rejection that resolves to -1 but says nothing is indistinguishable
		// from a dropped event, so the message is part of the behavior.
		wantReason string
	}{
		{"target is the source directory itself", "foo", "foo", "Can't move a folder into itself"},
		{"target is the source file's own directory", "alpha.txt", "beta.txt", "alpha.txt is already in ./"},
		{"target is the source directory's parent", "foo", "alpha.txt", "foo is already in ./"},
		{"direct child of the dragged directory", "foo", filepath.Join("foo", "sub"), "Can't move a folder into itself"},
		{"file inside the dragged directory", "foo", filepath.Join("foo", "inner.txt"), "Can't move a folder into itself"},
		{"grandchild of the dragged directory", "foo", filepath.Join("foo", "sub", "deep.txt"), "Can't move a folder into itself"},
		{"file into the directory it already lives in", filepath.Join("foo", "inner.txt"), "foo", "inner.txt is already in foo/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newDropTestPlugin(t)
			// Expand everything so nested rows are addressable.
			for _, dir := range []string{"foo", filepath.Join("foo", "sub")} {
				if err := p.tree.Expand(p.tree.FindByPath(dir)); err != nil {
					t.Fatalf("expand %s: %v", dir, err)
				}
			}
			registerTreeRows(p)

			startDragOn(t, p, rowIndex(t, p, tc.source))

			dir, idx, reason := p.resolveDropTargetReason(rowIndex(t, p, tc.hover))
			if idx != -1 {
				t.Errorf("drop resolved to dir=%q row=%d, want rejected (-1)", dir, idx)
			}
			if reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}

// The near-miss the separator guard exists for: "foobar" is NOT inside "foo",
// so dragging foo into foobar is a legitimate move.
func TestResolveDropTargetAllowsPrefixNearMiss(t *testing.T) {
	p := newDropTestPlugin(t)
	startDragOn(t, p, rowIndex(t, p, "foo"))

	dir, idx := p.resolveDropTarget(rowIndex(t, p, "foobar"))

	if dir != "foobar" || idx < 0 {
		t.Errorf("foo -> foobar resolved to dir=%q row=%d, want foobar accepted", dir, idx)
	}
}

// pathWithin is the descendant check the dangerous rejection rests on.
func TestPathWithin(t *testing.T) {
	sep := string(filepath.Separator)
	tests := []struct {
		child, parent string
		want          bool
	}{
		{"foo", "foo", true},
		{"foo" + sep + "sub", "foo", true},
		{"foo" + sep + "sub" + sep + "deep", "foo", true},
		{"foobar", "foo", false},                       // the near-miss
		{"foobar" + sep + "x", "foo", false},           // still not inside foo
		{"foo", "foobar", false},                       // and not the other way round
		{"other", "foo", false},                        // unrelated
		{"foo", "", true},                              // the root contains everything
		{"", "foo", false},                             // the root is inside nothing
		{"." + sep + "foo" + sep + "sub", "foo", true}, // uncleaned input still matches
	}

	for _, tc := range tests {
		if got := pathWithin(tc.child, tc.parent); got != tc.want {
			t.Errorf("pathWithin(%q, %q) = %v, want %v", tc.child, tc.parent, got, tc.want)
		}
	}
}

// Hovering something that is not a tree row (the preview pane, empty space) is
// not a drop target.
func TestResolveDropTargetOffTreeIsInvalid(t *testing.T) {
	p := newDropTestPlugin(t)
	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))

	if _, idx := p.resolveDropTarget(-1); idx != -1 {
		t.Errorf("off-tree hover resolved to row %d, want -1", idx)
	}
	if _, idx := p.resolveDropTarget(p.tree.Len()); idx != -1 {
		t.Errorf("out-of-range hover resolved to row %d, want -1", idx)
	}
}

// With no drag in flight there is never a drop target, whatever the cursor is
// over.
func TestResolveDropTargetRequiresActiveDrag(t *testing.T) {
	p := newDropTestPlugin(t)
	if _, idx := p.resolveDropTarget(rowIndex(t, p, "foo")); idx != -1 {
		t.Errorf("resolved a drop target without an active drag (row %d)", idx)
	}
}

// A full gesture: drag a file onto a directory row and release. The move runs
// and the result is reported as a toast.
func TestDropOnDirectoryMovesFile(t *testing.T) {
	p := newDropTestPlugin(t)
	srcRow := rowIndex(t, p, "alpha.txt")
	dstRow := rowIndex(t, p, "dir-x")

	startDragOn(t, p, srcRow)
	motion(t, p, 3, rowY(p, dstRow))
	if p.dragDropIdx != dstRow {
		t.Fatalf("dragDropIdx = %d, want %d (dir-x)", p.dragDropIdx, dstRow)
	}
	if p.dragDropDir != "dir-x" {
		t.Fatalf("dragDropDir = %q, want dir-x", p.dragDropDir)
	}

	_, cmd := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 3, Y: rowY(p, dstRow), Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("release on a valid target returned no command")
	}
	result := cmd()
	moved, ok := result.(DragMoveResultMsg)
	if !ok {
		t.Fatalf("file op produced %T (%v), want DragMoveResultMsg", result, result)
	}
	if moved.Err != nil {
		t.Fatalf("move reported an error: %v", moved.Err)
	}
	if moved.Name != "alpha.txt" || moved.Dir != "dir-x" {
		t.Errorf("moved = {Name:%q Dir:%q}, want {alpha.txt dir-x}", moved.Name, moved.Dir)
	}

	if _, err := os.Stat(filepath.Join(p.ctx.WorkDir, "dir-x", "alpha.txt")); err != nil {
		t.Errorf("alpha.txt not at its destination: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.ctx.WorkDir, "alpha.txt")); !os.IsNotExist(err) {
		t.Errorf("alpha.txt still at its old path (err=%v)", err)
	}
	if p.dragActive || p.dragArmed || p.dragSourcePath != "" || p.dragDropIdx != -1 {
		t.Errorf("drag state survived the drop: armed=%v active=%v src=%q drop=%d",
			p.dragArmed, p.dragActive, p.dragSourcePath, p.dragDropIdx)
	}

	// The result message reports the move.
	_, doneCmd := p.Update(moved)
	if doneCmd == nil {
		t.Fatal("DragMoveResultMsg after a drop produced no command")
	}
	if !containsToast(t, doneCmd, "Moved alpha.txt") {
		t.Error("no toast reporting the move")
	}
}

// Dropping a nested file onto a top-level file moves it to the project root.
// The root is the one destination with no row and an empty path, so it is the
// case where an "" means invalid confusion would silently do nothing.
func TestDropOnTopLevelFileMovesToProjectRoot(t *testing.T) {
	p := newDropTestPlugin(t)
	if err := p.tree.Expand(p.tree.FindByPath("foo")); err != nil {
		t.Fatalf("expand foo: %v", err)
	}
	registerTreeRows(p)

	srcRow := rowIndex(t, p, filepath.Join("foo", "inner.txt"))
	dstRow := rowIndex(t, p, "beta.txt")

	startDragOn(t, p, srcRow)
	motion(t, p, 3, rowY(p, dstRow))
	if p.dragDropIdx < 0 {
		t.Fatalf("drop onto a top-level file was rejected (dir=%q)", p.dragDropDir)
	}
	if p.dragDropDir != "" {
		t.Errorf("dragDropDir = %q, want \"\" (the project root)", p.dragDropDir)
	}

	_, cmd := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 3, Y: rowY(p, dstRow), Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("release on the root returned no command")
	}
	moved, ok := cmd().(DragMoveResultMsg)
	if !ok {
		t.Fatalf("release produced %T, want DragMoveResultMsg", moved)
	}
	if moved.Err != nil {
		t.Fatalf("move into the root failed: %v", moved.Err)
	}
	if _, err := os.Stat(filepath.Join(p.ctx.WorkDir, "inner.txt")); err != nil {
		t.Errorf("inner.txt is not at the project root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.ctx.WorkDir, "foo", "inner.txt")); !os.IsNotExist(err) {
		t.Errorf("inner.txt still inside foo/ (err=%v)", err)
	}

	// The root reads as "./" rather than as an empty name in the toast.
	_, doneCmd := p.Update(moved)
	if !containsToast(t, doneCmd, "Moved inner.txt → ./") {
		t.Error("no toast naming the project root as the destination")
	}
}

// The whole point of the separator guard, end to end: foo is not inside foobar
// and foobar is not inside foo, so this move must actually happen on disk. An
// over-eager descendant check would reject it and nothing would move.
func TestDropDirectoryOntoPrefixNearMissMoves(t *testing.T) {
	p := newDropTestPlugin(t)
	srcRow := rowIndex(t, p, "foo")
	dstRow := rowIndex(t, p, "foobar")

	startDragOn(t, p, srcRow)
	motion(t, p, 3, rowY(p, dstRow))

	_, cmd := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 3, Y: rowY(p, dstRow), Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("foo -> foobar was rejected: the near-miss guard is too broad")
	}
	moved, ok := cmd().(DragMoveResultMsg)
	if !ok {
		t.Fatalf("release produced %T, want DragMoveResultMsg", moved)
	}
	if moved.Err != nil {
		t.Fatalf("foo -> foobar failed: %v", moved.Err)
	}
	// The subtree came along with it.
	if _, err := os.Stat(filepath.Join(p.ctx.WorkDir, "foobar", "foo", "sub", "deep.txt")); err != nil {
		t.Errorf("the moved directory lost its contents: %v", err)
	}
}

// Releasing a directory onto a row inside its own subtree moves nothing and
// says why. This is the rejection with real consequences: os.Rename of a
// directory into its own descendant either fails or detaches the subtree.
func TestDropOntoOwnDescendantIsRefusedWithReason(t *testing.T) {
	p := newDropTestPlugin(t)
	for _, dir := range []string{"foo", filepath.Join("foo", "sub")} {
		if err := p.tree.Expand(p.tree.FindByPath(dir)); err != nil {
			t.Fatalf("expand %s: %v", dir, err)
		}
	}
	registerTreeRows(p)
	before := snapshotPaths(t, p.ctx.WorkDir)

	srcRow := rowIndex(t, p, "foo")
	dstRow := rowIndex(t, p, filepath.Join("foo", "sub"))

	startDragOn(t, p, srcRow)
	motion(t, p, 3, rowY(p, dstRow))
	if p.dragDropIdx != -1 {
		t.Fatalf("dragDropIdx = %d, want -1 for a descendant target", p.dragDropIdx)
	}

	_, cmd := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 3, Y: rowY(p, dstRow), Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("a refused descendant drop gave no feedback at all")
	}
	if !containsToast(t, cmd, "Can't move a folder into itself") {
		t.Errorf("descendant drop toast did not explain why: %v", cmd())
	}
	assertSamePaths(t, before, snapshotPaths(t, p.ctx.WorkDir))
	if p.dragActive || p.dragArmed || p.dragSourcePath != "" || p.dragDropIdx != -1 {
		t.Errorf("drag state survived a refused drop: armed=%v active=%v src=%q drop=%d",
			p.dragArmed, p.dragActive, p.dragSourcePath, p.dragDropIdx)
	}
}

// The project root is never a drag source. It has no row of its own, but a
// gesture that somehow carries the empty path must resolve to nothing rather
// than treating "" as a real node.
func TestRootIsNeverADragSource(t *testing.T) {
	p := newDropTestPlugin(t)
	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))

	// Force the source back to the root the way a corrupted re-anchor would.
	p.dragSourcePath = ""

	dir, idx, reason := p.resolveDropTargetReason(rowIndex(t, p, "dir-x"))
	if idx != -1 {
		t.Errorf("root as drag source resolved to dir=%q row=%d, want rejected", dir, idx)
	}
	if reason != "" {
		t.Errorf("reason = %q, want empty (there is nothing to explain)", reason)
	}

	if p.draggableNode(-1) != nil || p.draggableNode(p.tree.Len()) != nil {
		t.Error("draggableNode accepted an out-of-range row")
	}
}

// A file operation the user started elsewhere (the rename bar) must never be
// reported as the drag's result, however the two interleave.
func TestConcurrentFileOpResultIsNotStolenByDrag(t *testing.T) {
	p := newDropTestPlugin(t)
	srcRow := rowIndex(t, p, "alpha.txt")
	dstRow := rowIndex(t, p, "dir-x")

	startDragOn(t, p, srcRow)
	motion(t, p, 3, rowY(p, dstRow))
	_, cmd := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 3, Y: rowY(p, dstRow), Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("release on a valid target returned no command")
	}

	// An unrelated rename fails while the drag's move is still in flight.
	p.fileOpMode = FileOpRename
	_, errCmd := p.Update(FileOpErrorMsg{Err: os.ErrExist})
	if errCmd != nil {
		t.Errorf("an unrelated file-op error produced a command (%T)", errCmd())
	}
	if p.fileOpError == "" {
		t.Error("the rename's error was swallowed instead of shown in the file-op bar")
	}
}

// A failing move reports through a toast too: a drag-drop has no file-op bar to
// render fileOpError into, so without this the failure would be silent.
func TestDropFailureShowsToast(t *testing.T) {
	p := newDropTestPlugin(t)

	_, cmd := p.Update(DragMoveResultMsg{Name: "alpha.txt", Dir: "dir-x", Err: os.ErrPermission})
	if cmd == nil {
		t.Fatal("a failed drag move produced no command")
	}
	if !containsToast(t, cmd, "Move failed") {
		t.Error("no toast reporting the failure")
	}
	if p.fileOpError != "" {
		t.Errorf("fileOpError = %q, want empty (there is no bar to show it in)", p.fileOpError)
	}
}

// Releasing on an invalid target cancels silently: no move, no command.
func TestDropOnInvalidTargetDoesNothing(t *testing.T) {
	p := newDropTestPlugin(t)
	before := snapshotPaths(t, p.ctx.WorkDir)

	srcRow := rowIndex(t, p, "alpha.txt")
	dstRow := rowIndex(t, p, "beta.txt") // same directory: a no-op move

	startDragOn(t, p, srcRow)
	motion(t, p, 3, rowY(p, dstRow))
	if p.dragDropIdx != -1 {
		t.Fatalf("dragDropIdx = %d, want -1 for a no-op target", p.dragDropIdx)
	}

	_, cmd := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 3, Y: rowY(p, dstRow), Button: tea.MouseLeft}))
	// Nothing moves, but the gesture is acknowledged: a released drag that
	// produces no feedback at all reads as a dropped event.
	if cmd == nil {
		t.Fatal("a rejected drop gave the user no feedback at all")
	}
	if !containsToast(t, cmd, "already in") {
		t.Errorf("rejected drop toast did not explain why: %v", cmd())
	}
	assertSamePaths(t, before, snapshotPaths(t, p.ctx.WorkDir))
	if p.dragActive || p.dragArmed {
		t.Error("drag state survived an invalid drop")
	}
}

// Releasing outside the tree entirely is also a cancel.
func TestDropOutsideTreeDoesNothing(t *testing.T) {
	p := newDropTestPlugin(t)
	before := snapshotPaths(t, p.ctx.WorkDir)

	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))
	_, cmd := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 80, Y: 20, Button: tea.MouseLeft}))

	if cmd != nil {
		t.Errorf("drop on empty space returned a command (%T)", cmd())
	}
	assertSamePaths(t, before, snapshotPaths(t, p.ctx.WorkDir))
}

// The tree can be rebuilt by the watcher between the last motion event and the
// release, so the target is re-resolved at release time rather than trusted
// from the motion that set it.
func TestDropRevalidatesAtReleaseTime(t *testing.T) {
	p := newDropTestPlugin(t)
	srcRow := rowIndex(t, p, "alpha.txt")
	dstRow := rowIndex(t, p, "dir-x")

	startDragOn(t, p, srcRow)
	motion(t, p, 3, rowY(p, dstRow))
	if p.dragDropIdx < 0 {
		t.Fatal("expected a valid drop target before the rebuild")
	}

	// The destination disappears from under the cursor.
	if err := os.RemoveAll(filepath.Join(p.ctx.WorkDir, "dir-x")); err != nil {
		t.Fatalf("remove dir-x: %v", err)
	}
	rebuilt, err := BuildTree(BuildSpec{RootDir: p.ctx.WorkDir, SortMode: p.tree.SortMode, ShowIgnored: p.tree.ShowIgnored})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	p.applyBuiltTree(rebuilt, "")
	before := snapshotPaths(t, p.ctx.WorkDir)

	// The release still carries the old row index; re-resolving it against the
	// new tree lands on foo/ - which is not where the stale index pointed, so
	// the only safe answer is to move nothing the user did not aim at.
	_, cmd := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 3, Y: rowY(p, dstRow), Button: tea.MouseLeft}))
	if cmd != nil {
		// If a move does happen it must be to the row the release actually
		// resolves to in the *current* tree, never the deleted directory.
		if _, err := os.Stat(filepath.Join(p.ctx.WorkDir, "dir-x")); err == nil {
			t.Fatal("dir-x came back from the dead")
		}
		result := cmd()
		if res, ok := result.(DragMoveResultMsg); ok && res.Err != nil {
			t.Fatalf("release moved into a stale target: %v", res.Err)
		}
	} else {
		assertSamePaths(t, before, snapshotPaths(t, p.ctx.WorkDir))
	}
}

// Esc cancels an in-flight drag and does nothing else: a gesture with no
// keyboard way out is a trap.
func TestEscapeCancelsDrag(t *testing.T) {
	p := newDropTestPlugin(t)
	before := snapshotPaths(t, p.ctx.WorkDir)

	srcRow := rowIndex(t, p, "alpha.txt")
	startDragOn(t, p, srcRow)
	motion(t, p, 3, rowY(p, rowIndex(t, p, "dir-x")))
	if p.dragDropIdx < 0 {
		t.Fatal("expected a valid drop target before Esc")
	}

	p.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})

	if p.dragActive || p.dragArmed {
		t.Errorf("drag survived Esc: armed=%v active=%v", p.dragArmed, p.dragActive)
	}
	if p.dragDropIdx != -1 || p.dragSourcePath != "" {
		t.Errorf("drag state not cleared by Esc: drop=%d path=%q",
			p.dragDropIdx, p.dragSourcePath)
	}
	if p.mouseHandler.IsDragging() {
		t.Error("handler still dragging after Esc")
	}

	// The release that follows the cancelled gesture must not move anything.
	release(t, p, 3, rowY(p, rowIndex(t, p, "dir-x")))
	assertSamePaths(t, before, snapshotPaths(t, p.ctx.WorkDir))
}

// Resting over a collapsed directory expands it mid-drag, so one gesture can
// reach a nested destination.
func TestSpringLoadExpandsHoveredDirectory(t *testing.T) {
	p := newDropTestPlugin(t)
	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))

	fooRow := rowIndex(t, p, "foo")
	cmd := p.trackDragHover(fooRow)
	if cmd == nil {
		t.Fatal("hovering a collapsed directory should schedule a spring-load tick")
	}
	if p.tree.FindByPath("foo").IsExpanded {
		t.Fatal("directory expanded immediately, without the delay")
	}

	tick, ok := cmd().(DragSpringLoadMsg)
	if !ok {
		t.Fatalf("scheduled command produced %T, want DragSpringLoadMsg", cmd())
	}
	p.Update(tick)

	if !p.tree.FindByPath("foo").IsExpanded {
		t.Error("foo/ did not spring open")
	}
	if p.tree.IndexOfPath(filepath.Join("foo", "inner.txt")) < 0 {
		t.Error("children of the sprung-open directory are not addressable")
	}
}

// Moving to another row restarts the timer, and the tick scheduled for the row
// the cursor left must not expand anything.
func TestSpringLoadTickForLeftRowIsIgnored(t *testing.T) {
	p := newDropTestPlugin(t)
	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))

	cmd := p.trackDragHover(rowIndex(t, p, "foo"))
	if cmd == nil {
		t.Fatal("expected a spring-load tick for foo/")
	}
	staleTick := cmd()

	// The cursor moves on before the tick lands.
	firstHoverAt := p.dragHoverSince
	p.trackDragHover(rowIndex(t, p, "foobar"))
	if p.dragHoverSince.Before(firstHoverAt) {
		t.Error("hover timer was not restarted on the new row")
	}

	p.Update(staleTick)

	if p.tree.FindByPath("foo").IsExpanded {
		t.Error("a stale tick expanded the row the cursor had already left")
	}
	// This is the assertion the generation counter actually protects.
	// handleDragSpringLoad expands p.dragHoverIdx - the row the cursor is on
	// *now* - so without the generation check the stale tick pops open whatever
	// the cursor has moved to, after no rest at all. Sweeping across a column of
	// collapsed folders would open every one of them and reflow the tree
	// mid-drag.
	if p.tree.FindByPath("foobar").IsExpanded {
		t.Error("a stale tick expanded the row the cursor had just arrived on")
	}
}

// The motion path expands too, for a cursor whose tick was lost.
func TestSpringLoadExpandsOnMotionAfterDelay(t *testing.T) {
	p := newDropTestPlugin(t)
	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))

	fooRow := rowIndex(t, p, "foo")
	p.trackDragHover(fooRow)
	p.dragHoverSince = time.Now().Add(-2 * dragSpringLoadDelay) // rested long enough
	p.trackDragHover(fooRow)

	if !p.tree.FindByPath("foo").IsExpanded {
		t.Error("resting on a collapsed directory did not expand it")
	}
}

// A spring-load tick that arrives after the drag ended must do nothing.
func TestSpringLoadAfterDragEndIsInert(t *testing.T) {
	p := newDropTestPlugin(t)
	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))
	cmd := p.trackDragHover(rowIndex(t, p, "foo"))
	tick := cmd()

	p.clearDragState()
	p.Update(tick)

	if p.tree.FindByPath("foo").IsExpanded {
		t.Error("a tick expanded a directory after the drag was over")
	}
}

// Dragging to the bottom edge scrolls the tree so off-screen targets are
// reachable; the offset stays inside the scrollable range.
func TestDragAutoScrollAtEdges(t *testing.T) {
	p := newDropTestPlugin(t)
	// Squeeze the viewport so the tree (5 rows) actually overflows it.
	p.height = 5 + 3 // visibleContentHeight() == height - 5
	if got := p.visibleContentHeight(); got != 3 {
		t.Fatalf("visibleContentHeight() = %d, want 3", got)
	}
	registerTreeRows(p)
	startDragOn(t, p, 0)

	topY, height := p.treeRowsViewport()
	bottomY := topY + height - 1

	for i := 0; i < 10; i++ {
		p.dragLastScroll = time.Time{} // ignore the throttle in the test
		p.autoScrollForDrag(3, bottomY)
	}
	maxOff := p.tree.Len() - height
	if p.treeScrollOff != maxOff {
		t.Errorf("treeScrollOff = %d after scrolling to the bottom, want %d", p.treeScrollOff, maxOff)
	}

	for i := 0; i < 10; i++ {
		p.dragLastScroll = time.Time{}
		p.autoScrollForDrag(3, topY)
	}
	if p.treeScrollOff != 0 {
		t.Errorf("treeScrollOff = %d after scrolling back to the top, want 0", p.treeScrollOff)
	}

	// The middle of the pane does not scroll at all.
	p.treeScrollOff = 1
	p.dragLastScroll = time.Time{}
	if got := p.autoScrollForDrag(3, topY+1); got != 0 || p.treeScrollOff != 1 {
		t.Errorf("mid-pane cursor scrolled the tree (delta=%d, off=%d)", got, p.treeScrollOff)
	}

	// Neither does the edge row of the *preview* pane.
	p.dragLastScroll = time.Time{}
	if got := p.autoScrollForDrag(p.treeWidth+5, bottomY); got != 0 || p.treeScrollOff != 1 {
		t.Errorf("cursor in the preview pane scrolled the tree (delta=%d, off=%d)", got, p.treeScrollOff)
	}
}

// The tree pane renders the flat search-match list while the hit regions still
// carry tree indices, so a press there must not arm a drag.
func TestSearchModeDoesNotArmDrag(t *testing.T) {
	p := newDropTestPlugin(t)
	p.searchMode = true

	press(t, p, 3, rowY(p, rowIndex(t, p, "alpha.txt")))

	if p.dragArmed || p.dragActive {
		t.Errorf("search-mode press armed a drag: armed=%v active=%v", p.dragArmed, p.dragActive)
	}
	if p.mouseHandler.IsDragging() {
		t.Error("search-mode press started a handler drag")
	}
}

// Auto-scroll is throttled: motion events arrive far faster than a readable
// scroll rate. The tree has to overflow the viewport by several rows, or the
// second call returns 0 because the offset is already clamped at its maximum
// and the test passes whether or not the throttle exists at all.
func TestDragAutoScrollIsThrottled(t *testing.T) {
	p := newDropTestPlugin(t)
	// Expanding both directories gives 9 rows against a 4-row viewport, so
	// maxOff is 5 and there is plenty of room left after the first scroll.
	for _, dir := range []string{"foo", filepath.Join("foo", "sub")} {
		if err := p.tree.Expand(p.tree.FindByPath(dir)); err != nil {
			t.Fatalf("expand %s: %v", dir, err)
		}
	}
	p.height = 8
	registerTreeRows(p)
	startDragOn(t, p, 0)

	topY, height := p.treeRowsViewport()
	bottomY := topY + height - 1
	if maxOff := p.tree.Len() - height; maxOff < 3 {
		t.Fatalf("fixture only allows %d rows of scrolling; the throttle would not be observable", maxOff)
	}

	if got := p.autoScrollForDrag(3, bottomY); got != 1 {
		t.Fatalf("first edge motion scrolled %d rows, want 1", got)
	}
	if p.treeScrollOff != 1 {
		t.Fatalf("treeScrollOff = %d after the first scroll, want 1", p.treeScrollOff)
	}
	if got := p.autoScrollForDrag(3, bottomY); got != 0 {
		t.Errorf("second edge motion scrolled %d rows, want 0 (throttled)", got)
	}
	if p.treeScrollOff != 1 {
		t.Errorf("throttled motion still moved the offset to %d, want 1", p.treeScrollOff)
	}

	// Once the interval has passed the next motion scrolls again.
	p.dragLastScroll = time.Now().Add(-2 * dragAutoScrollInterval)
	if got := p.autoScrollForDrag(3, bottomY); got != 1 {
		t.Errorf("motion after the throttle interval scrolled %d rows, want 1", got)
	}
	if p.treeScrollOff != 2 {
		t.Errorf("treeScrollOff = %d after the throttle expired, want 2", p.treeScrollOff)
	}
}

// The drag viewport must describe exactly the rows the view registers. When it
// does not, auto-scroll fires on ordinary mid-list rows, never fires at the real
// bottom edge, and - worse - the view registers hit regions on the pane border
// and below it, so a release there commits a move into a directory the user
// never saw.
func TestTreeViewportMatchesRenderedRows(t *testing.T) {
	for _, tc := range []struct {
		name          string
		height        int
		contentSearch bool
		fileOp        FileOpMode
		fileOpError   string
		suggestions   []string
	}{
		{name: "plain", height: 30},
		{name: "odd height", height: 24},
		{name: "content search bar", height: 30, contentSearch: true},
		{name: "file op bar", height: 30, fileOp: FileOpRename},
		{name: "file op bar with error", height: 30, fileOp: FileOpRename, fileOpError: "nope"},
		{name: "file op bar with suggestions", height: 30, fileOp: FileOpMove,
			suggestions: []string{"dir-x", "foo", "foo/sub"}},
		{name: "file op bar with suggestions and error", height: 30, fileOp: FileOpMove,
			fileOpError: "nope", suggestions: []string{"dir-x", "foo"}},
		{name: "content search plus suggestions", height: 30, contentSearch: true, fileOp: FileOpMove,
			suggestions: []string{"dir-x", "foo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newDropTestPlugin(t)
			p.height = tc.height
			p.contentSearchMode = tc.contentSearch
			p.fileOpMode = tc.fileOp
			p.fileOpError = tc.fileOpError
			p.fileOpSuggestions = tc.suggestions
			p.fileOpShowSuggestions = len(tc.suggestions) > 0
			p.fileOpSuggestionIdx = -1
			// Enough rows to overflow any of these viewports.
			for _, dir := range []string{"foo", filepath.Join("foo", "sub")} {
				if err := p.tree.Expand(p.tree.FindByPath(dir)); err != nil {
					t.Fatalf("expand %s: %v", dir, err)
				}
			}
			out := p.renderView()
			lines := strings.Split(out, "\n")

			topY, height := p.treeRowsViewport()

			minY, maxY, count := -1, -1, 0
			for y := 0; y < tc.height+4; y++ {
				region := p.mouseHandler.HitMap.Test(3, y)
				if region == nil || region.ID != regionTreeItem {
					continue
				}
				count++
				if minY < 0 || y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
			if count == 0 {
				t.Fatal("no tree rows were registered at all")
			}
			if minY != topY {
				t.Errorf("first registered row at y=%d, viewport says y=%d", minY, topY)
			}
			if want := topY + height - 1; maxY > want {
				t.Errorf("rows registered down to y=%d, past the viewport's last row y=%d", maxY, want)
			}
			if count > height {
				t.Errorf("%d rows registered, viewport only claims %d", count, height)
			}
			// Every registered row must be a row the user can actually see: the
			// node it names has to be drawn on that screen line.
			for y := minY; y <= maxY; y++ {
				region := p.mouseHandler.HitMap.Test(3, y)
				if region == nil || region.ID != regionTreeItem {
					t.Fatalf("gap in the registered rows at y=%d", y)
				}
				idx := region.Data.(int)
				node := p.tree.GetNode(idx)
				if node == nil {
					t.Fatalf("y=%d registers row %d, which is not in the tree", y, idx)
				}
				if y >= len(lines) {
					t.Fatalf("y=%d registers row %q but the view only renders %d lines", y, node.Name, len(lines))
				}
				if !strings.Contains(lines[y], node.Name) {
					t.Errorf("y=%d registers %q but that line renders %q", y, node.Name, lines[y])
				}
			}
		})
	}
}

// Status text is the primary drag signal, since the row highlight is subtle in
// some themes.
func TestDragStatusLine(t *testing.T) {
	p := newDropTestPlugin(t)
	if got := p.renderDragStatusLine(); got != "" {
		t.Errorf("status line without a drag = %q, want empty", got)
	}

	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))
	motion(t, p, 3, rowY(p, rowIndex(t, p, "dir-x")))

	valid := p.renderDragStatusLine()
	if !strings.Contains(valid, "alpha.txt") || !strings.Contains(valid, "dir-x/") {
		t.Errorf("valid drop status = %q, want it to name the file and destination", valid)
	}

	motion(t, p, 3, rowY(p, rowIndex(t, p, "beta.txt"))) // same dir: rejected
	invalid := p.renderDragStatusLine()
	if !strings.Contains(invalid, "can't drop here") {
		t.Errorf("invalid drop status = %q, want a clear refusal", invalid)
	}
}

// The drop target row and the dragged row are rendered differently from each
// other and from an ordinary cursor row.
func TestRenderTreeNodeDragHighlights(t *testing.T) {
	p := newDropTestPlugin(t)
	srcRow := rowIndex(t, p, "alpha.txt")
	dstRow := rowIndex(t, p, "dir-x")

	source := p.tree.GetNode(srcRow)
	target := p.tree.GetNode(dstRow)

	idle := p.renderTreeNode(target, false, 20)
	idleCursor := p.renderTreeNode(source, true, 20)

	startDragOn(t, p, srcRow)
	motion(t, p, 3, rowY(p, dstRow))

	dragTarget := p.renderTreeNode(target, false, 20)
	if dragTarget == idle {
		t.Error("drop target row renders identically to an unhighlighted row")
	}
	if dragTarget == idleCursor {
		t.Error("drop target highlight is indistinguishable from the cursor highlight")
	}

	// The source row is the cursor row, so it must not simply keep the cursor
	// highlight - it has to read as the thing in flight.
	dragSource := p.renderTreeNode(source, true, 20)
	if dragSource == idleCursor {
		t.Error("dragged row renders identically to a normal cursor row")
	}
}

func containsToast(t *testing.T, cmd tea.Cmd, want string) bool {
	t.Helper()
	return toastFound(cmd(), want)
}

// toastFound asks whether the user was told something, whichever tier said it:
// a notification, a source-specific alert, or a status flash.
func toastFound(m tea.Msg, want string) bool {
	switch v := m.(type) {
	case appmsg.ToastMsg:
		return strings.Contains(v.Message, want)
	case appmsg.FlashMsg:
		return strings.Contains(v.Text, want)
	case notify.PostMsg:
		return strings.Contains(v.Notification.Title, want)
	case tea.BatchMsg:
		for _, c := range v {
			if c == nil {
				continue
			}
			if toastFound(c(), want) {
				return true
			}
		}
	}
	return false
}

func assertSamePaths(t *testing.T, before, after []string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("file set changed: %v -> %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("file set changed: %v -> %v", before, after)
		}
	}
}

// A move between two directories whose names differ only in case is a real
// move, not a case-only rename, so it must still refuse to clobber an existing
// destination file. Skipped where the two directories cannot coexist.
func TestDoFileOpCaseDifferingDirsStillChecksDestination(t *testing.T) {
	p := newDropTestPlugin(t)
	root := p.ctx.WorkDir
	lower := filepath.Join(root, "case-a")
	upper := filepath.Join(root, "CASE-A")
	if err := os.Mkdir(lower, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Mkdir(upper, 0o755); err != nil {
		t.Skipf("filesystem is case-insensitive, the two directories cannot coexist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lower, "x.txt"), []byte("source"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(upper, "x.txt"), []byte("victim"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	result := p.doFileOp(filepath.Join(lower, "x.txt"), filepath.Join(upper, "x.txt"))()
	if _, ok := result.(FileOpErrorMsg); !ok {
		t.Fatalf("move produced %T (%v), want a refusal", result, result)
	}
	body, err := os.ReadFile(filepath.Join(upper, "x.txt"))
	if err != nil || string(body) != "victim" {
		t.Errorf("existing destination file was overwritten (body=%q err=%v)", body, err)
	}
}

// A true case-only rename inside one directory still takes the two-step path.
func TestDoFileOpCaseOnlyRenameStillWorks(t *testing.T) {
	p := newDropTestPlugin(t)
	src := filepath.Join(p.ctx.WorkDir, "alpha.txt")
	dst := filepath.Join(p.ctx.WorkDir, "Alpha.txt")

	result := p.doFileOp(src, dst)()
	if _, ok := result.(FileOpSuccessMsg); !ok {
		t.Fatalf("case-only rename produced %T (%v), want success", result, result)
	}
}

// A row can outlive the directory it names. Recreating it with MkdirAll would
// resurrect a directory the user deliberately deleted, holding one file.
func TestDropOnDeletedDirectoryRefusesToRecreateIt(t *testing.T) {
	p := newDropTestPlugin(t)
	srcRow := rowIndex(t, p, "alpha.txt")
	dstRow := rowIndex(t, p, "dir-x")

	startDragOn(t, p, srcRow)
	motion(t, p, 3, rowY(p, dstRow))
	if p.dragDropIdx < 0 {
		t.Fatal("expected a valid drop target")
	}

	// The destination disappears; the tree has not been rebuilt yet.
	if err := os.RemoveAll(filepath.Join(p.ctx.WorkDir, "dir-x")); err != nil {
		t.Fatalf("remove dir-x: %v", err)
	}

	_, cmd := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 3, Y: rowY(p, dstRow), Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("dropping on a vanished directory gave no feedback")
	}
	if !containsToast(t, cmd, "no longer exists") {
		t.Errorf("expected a 'destination no longer exists' toast, got %v", cmd())
	}
	if _, err := os.Stat(filepath.Join(p.ctx.WorkDir, "dir-x")); err == nil {
		t.Error("dir-x was recreated by the drop")
	}
	if _, err := os.Stat(filepath.Join(p.ctx.WorkDir, "alpha.txt")); err != nil {
		t.Errorf("alpha.txt moved anyway: %v", err)
	}
}

// A drop released on a row the pane never drew (the bottom border, the footer)
// must not move anything: the user cannot have aimed at a row they cannot see.
func TestDropOnOffscreenRowIsRefused(t *testing.T) {
	p := newDropTestPlugin(t)
	before := snapshotPaths(t, p.ctx.WorkDir)
	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))

	// Forge the hit region the old over-registration used to create: a row
	// scrolled past the bottom of the viewport.
	offscreen := p.treeScrollOff + p.treeItemRows()
	if offscreen >= p.tree.Len() {
		// Shrink the viewport until a real row is off-screen.
		p.height = 4 + 4
		offscreen = p.treeScrollOff + p.treeItemRows()
		if offscreen >= p.tree.Len() {
			t.Fatalf("could not push a row off-screen (rows=%d, len=%d)", p.treeItemRows(), p.tree.Len())
		}
	}
	topY, height := p.treeRowsViewport()
	p.mouseHandler.HitMap.AddRect(regionTreeItem, 1, topY+height, p.treeWidth-3, 1, offscreen)

	_, cmd := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 3, Y: topY + height, Button: tea.MouseLeft}))
	if cmd != nil {
		t.Errorf("a drop on an off-screen row produced a command (%T)", cmd())
	}
	assertSamePaths(t, before, snapshotPaths(t, p.ctx.WorkDir))
}

// Spring-loading must leave the affordance agreeing with what a release would
// do: before this, the status line read "can't drop here" while releasing moved
// the file.
func TestSpringLoadReresolvesDropTarget(t *testing.T) {
	p := newDropTestPlugin(t)
	startDragOn(t, p, rowIndex(t, p, "alpha.txt"))

	fooRow := rowIndex(t, p, "foo")
	p.trackDragHover(fooRow)
	p.springLoadDir(fooRow)

	if !p.tree.FindByPath("foo").IsExpanded {
		t.Fatal("foo/ did not spring open")
	}
	if p.dragDropIdx != fooRow || p.dragDropDir != "foo" {
		t.Errorf("after spring-load: dragDropIdx=%d dragDropDir=%q, want %d and \"foo\"",
			p.dragDropIdx, p.dragDropDir, fooRow)
	}
	if strings.Contains(p.renderDragStatusLine(), "can't drop here") {
		t.Error("status line refuses a drop that would in fact succeed")
	}
}

// Dropping on a top-level file resolves to the project root, which has no row.
// Highlighting the hovered file there would claim the file is the destination.
func TestRootDropDoesNotHighlightTheHoveredFile(t *testing.T) {
	p := newDropTestPlugin(t)
	if err := p.tree.Expand(p.tree.FindByPath("foo")); err != nil {
		t.Fatalf("expand foo: %v", err)
	}
	registerTreeRows(p)

	startDragOn(t, p, rowIndex(t, p, filepath.Join("foo", "inner.txt")))
	hovered := rowIndex(t, p, "beta.txt")
	p.dragDropDir, p.dragDropIdx = p.resolveDropTarget(hovered)
	if p.dragDropIdx < 0 || p.dragDropDir != "" {
		t.Fatalf("expected a valid root drop, got dir=%q idx=%d", p.dragDropDir, p.dragDropIdx)
	}

	node := p.tree.GetNode(hovered)
	p.dragActive = false
	plain := p.renderTreeNode(node, false, 20)
	p.dragActive = true
	if got := p.renderTreeNode(node, false, 20); got != plain {
		t.Errorf("hovered file rendered as the drop target for a root drop: %q", got)
	}
	// The status line still names the destination.
	if !strings.Contains(p.renderDragStatusLine(), "./") {
		t.Error("status line does not name the project root as the destination")
	}
}

// Padding is measured in display cells: a CJK filename is wider than its byte
// length, and padding by bytes leaves the highlight visibly short.
func TestDropTargetHighlightPadsByDisplayWidth(t *testing.T) {
	p := newDropTestPlugin(t)
	node := &FileNode{Name: "日本語のファイル名.txt", Path: "日本語のファイル名.txt"}

	const maxWidth = 35
	line := ansi.Strip(p.renderTreeNode(node, true, maxWidth))
	if got := ansi.StringWidth(line); got != maxWidth {
		t.Errorf("highlighted row is %d cells wide, want %d", got, maxWidth)
	}
}

// Truncation is also by cells, and must never cut a rune in half.
func TestTreeNodeTruncatesWideNamesCleanly(t *testing.T) {
	p := newDropTestPlugin(t)
	node := &FileNode{Name: "日本語のファイル名がとても長い場合.txt", Path: "x"}

	const maxWidth = 12
	line := ansi.Strip(p.renderTreeNode(node, false, maxWidth))
	if got := ansi.StringWidth(line); got > maxWidth {
		t.Errorf("truncated row is %d cells wide, want at most %d", got, maxWidth)
	}
	if !utf8.ValidString(line) {
		t.Errorf("truncation cut a rune in half: %q", line)
	}
}

// The preview pane's click/drag-select mapping has to read the same geometry
// helpers the renderer does. It used to carry its own copy of the input-bar
// formula, which went stale when the bars grew to two rows: a click on the top
// visible preview line then selected the line below it, and a drag-select
// anchored one line off. The first content row must always map to previewScroll.
func TestPreviewSelectionMatchesRenderedFirstLine(t *testing.T) {
	for _, tc := range []struct {
		name          string
		contentSearch bool
		lineJump      bool
		fileOp        FileOpMode
		fileOpError   string
		suggestions   []string
	}{
		{name: "plain"},
		{name: "content search bar", contentSearch: true},
		{name: "line jump bar", lineJump: true},
		{name: "file op bar", fileOp: FileOpRename},
		{name: "file op bar with error", fileOp: FileOpRename, fileOpError: "nope"},
		{name: "file op bar with suggestions", fileOp: FileOpMove, suggestions: []string{"dir-x", "foo"}},
		{name: "content search plus line jump", contentSearch: true, lineJump: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newDropTestPlugin(t)
			p.contentSearchMode = tc.contentSearch
			p.lineJumpMode = tc.lineJump
			p.fileOpMode = tc.fileOp
			p.fileOpError = tc.fileOpError
			p.fileOpSuggestions = tc.suggestions
			p.fileOpShowSuggestions = len(tc.suggestions) > 0
			p.fileOpSuggestionIdx = -1
			p.previewFile = "alpha.txt"
			p.previewLines = []string{"one", "two", "three", "four", "five", "six"}
			p.previewHighlighted = p.previewLines
			p.previewScroll = 2
			p.renderView()

			// The renderer puts the preview's first content line at the same
			// offset it puts the tree's first row: border(1) + header(2).
			topY := p.inputBarHeight() + 3
			previewX := p.treeWidth + dividerWidth + 1 + 5 + 1

			lineIdx, _, ok := p.previewSelectionAtXY(previewX, topY)
			if !ok {
				t.Fatalf("no selection resolved at the first preview content row (y=%d)", topY)
			}
			if lineIdx != p.previewScroll {
				t.Errorf("y=%d resolved line %d, want %d (the first visible line)", topY, lineIdx, p.previewScroll)
			}
			if _, _, ok := p.previewSelectionAtXY(previewX, topY-1); ok {
				t.Errorf("y=%d is above the first content row but still resolved a line", topY-1)
			}
		})
	}
}

// The case-only-rename narrowing decides whether doFileOp takes the two-step
// temp-file path, which skips the destination-exists check. Its filesystem-level
// test can only run where "case-a" and "CASE-A" coexist (not macOS APFS by
// default), so the predicate itself is table-tested here and runs everywhere.
func TestIsCaseOnlyRename(t *testing.T) {
	j := filepath.Join
	root := j("proj")
	for _, tc := range []struct {
		name     string
		src, dst string
		want     bool
	}{
		{"case-only in one dir", j(root, "File.txt"), j(root, "file.txt"), true},
		{"case-only at root of tree", j(root, "foo", "A.go"), j(root, "foo", "a.go"), true},
		{"identical paths", j(root, "a.txt"), j(root, "a.txt"), false},
		{"plain rename", j(root, "a.txt"), j(root, "b.txt"), false},
		{"move between dirs", j(root, "foo", "x.txt"), j(root, "bar", "x.txt"), false},
		// The one the narrowing exists for: same base name, directories that
		// differ only in case. On a case-sensitive filesystem these are two
		// real directories, so this is a move and must keep the
		// destination-exists check.
		{"case-differing dirs, same base", j(root, "foo", "x.txt"), j(root, "Foo", "x.txt"), false},
		{"case-differing dirs and base", j(root, "foo", "x.txt"), j(root, "Foo", "X.txt"), false},
		{"move into subdir", j(root, "x.txt"), j(root, "foo", "x.txt"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCaseOnlyRename(tc.src, tc.dst); got != tc.want {
				t.Errorf("isCaseOnlyRename(%q, %q) = %v, want %v", tc.src, tc.dst, got, tc.want)
			}
		})
	}
}

// The move rules are shared, so the keyboard 'm' dialog answers exactly what a
// drag would. Before validateMove existed, typing a folder's own subtree into
// the dialog reached os.Rename and surfaced a raw "invalid argument".
func TestKeyboardMoveRejectsMoveIntoOwnSubtree(t *testing.T) {
	p := newDropTestPlugin(t)
	before := snapshotPaths(t, p.ctx.WorkDir)

	p.treeCursor = rowIndex(t, p, "foo")
	p.handleKey(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if p.fileOpMode != FileOpMove {
		t.Fatalf("'m' did not open the move dialog (mode=%v)", p.fileOpMode)
	}
	p.fileOpTextInput.SetValue(filepath.Join("foo", "sub", "foo"))

	if _, cmd := p.executeFileOp(); cmd != nil {
		t.Errorf("refused move still produced a command: %v", cmd())
	}
	if p.fileOpError != "Can't move a folder into itself" {
		t.Errorf("fileOpError = %q, want the same reason a drag gives", p.fileOpError)
	}
	assertSamePaths(t, before, snapshotPaths(t, p.ctx.WorkDir))
}

// The no-op move ("already in this directory") is the other rule the two
// surfaces used to disagree on: the dialog reported "destination already exists".
func TestKeyboardMoveRejectsNoOpMove(t *testing.T) {
	p := newDropTestPlugin(t)

	p.treeCursor = rowIndex(t, p, "alpha.txt")
	p.handleKey(tea.KeyPressMsg{Code: 'm', Text: "m"})
	p.fileOpTextInput.SetValue("alpha.txt")

	if _, cmd := p.executeFileOp(); cmd != nil {
		t.Errorf("no-op move still produced a command: %v", cmd())
	}
	if want := "alpha.txt is already in ./"; p.fileOpError != want {
		t.Errorf("fileOpError = %q, want %q", p.fileOpError, want)
	}
}

// Renaming in place through the move dialog (same directory, new name) is a
// legitimate move and must not be caught by the no-op rule.
func TestKeyboardMoveAllowsRenameInPlace(t *testing.T) {
	p := newDropTestPlugin(t)

	p.treeCursor = rowIndex(t, p, "alpha.txt")
	p.handleKey(tea.KeyPressMsg{Code: 'm', Text: "m"})
	p.fileOpTextInput.SetValue("renamed.txt")

	_, cmd := p.executeFileOp()
	if p.fileOpError != "" {
		t.Fatalf("rename in place was refused: %q", p.fileOpError)
	}
	if cmd == nil {
		t.Fatal("rename in place produced no command")
	}
	if res := cmd(); func() bool { _, ok := res.(FileOpSuccessMsg); return !ok }() {
		t.Fatalf("rename in place produced %T (%v), want success", res, res)
	}
	if _, err := os.Stat(filepath.Join(p.ctx.WorkDir, "renamed.txt")); err != nil {
		t.Errorf("renamed.txt is not on disk: %v", err)
	}
}

// The suggestion dropdown's hit regions have to sit on the rows it actually
// draws. They were registered at y=i+1, one row below the bar's first line,
// while the bar itself is two rows tall - so clicking a suggestion picked the
// one above it (or nothing).
func TestFileOpSuggestionRegionsMatchRenderedRows(t *testing.T) {
	for _, tc := range []struct {
		name          string
		contentSearch bool
		fileOpError   string
	}{
		{name: "suggestions only"},
		{name: "with an error line", fileOpError: "nope"},
		{name: "below a content search bar", contentSearch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newDropTestPlugin(t)
			p.contentSearchMode = tc.contentSearch
			p.fileOpMode = FileOpMove
			p.fileOpError = tc.fileOpError
			p.fileOpSuggestions = []string{"dir-x", "foobar", "foo/sub"}
			p.fileOpShowSuggestions = true
			p.fileOpSuggestionIdx = -1

			lines := strings.Split(p.renderView(), "\n")
			for i, suggestion := range p.fileOpSuggestions {
				y := p.fileOpSuggestionsTopY() + i
				region := p.mouseHandler.HitMap.Test(2, y)
				if region == nil || region.ID != regionFileOpSuggestion {
					t.Fatalf("no suggestion region at y=%d (suggestion %q)", y, suggestion)
				}
				if idx, ok := region.Data.(int); !ok || idx != i {
					t.Errorf("y=%d registers suggestion %v, want %d", y, region.Data, i)
				}
				if y >= len(lines) || !strings.Contains(lines[y], suggestion) {
					t.Errorf("y=%d registers %q but that line renders %q", y, suggestion, lines[min(y, len(lines)-1)])
				}
			}
		})
	}
}

// Keyboard navigation scrolls against the same viewport the renderer draws.
// It used to use visibleContentHeight(), one row smaller in the plain layout
// and one row *larger* with a file-op bar open - so arrowing down could put the
// cursor on a row that was never drawn, and during a drag the keyboard scroll
// and the drag auto-scroll would fight over treeScrollOff.
func TestKeyboardTreeScrollUsesRenderedViewport(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fileOp      FileOpMode
		fileOpError string
	}{
		{name: "plain"},
		{name: "file op bar", fileOp: FileOpRename},
		{name: "file op bar with error", fileOp: FileOpRename, fileOpError: "nope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newDropTestPlugin(t)
			p.fileOpMode = tc.fileOp
			p.fileOpError = tc.fileOpError
			for _, dir := range []string{"foo", filepath.Join("foo", "sub")} {
				if err := p.tree.Expand(p.tree.FindByPath(dir)); err != nil {
					t.Fatalf("expand %s: %v", dir, err)
				}
			}
			p.height = 10 // fewer rows than the tree has

			for cursor := 0; cursor < p.tree.Len(); cursor++ {
				p.treeCursor = cursor
				p.ensureTreeCursorVisible()
				if !p.treeRowVisible(p.treeCursor) {
					t.Fatalf("cursor %d is outside the drawn viewport after scrolling (off=%d rows=%d)",
						cursor, p.treeScrollOff, p.treeItemRows())
				}
			}
			// Walking back up must keep it visible too.
			for cursor := p.tree.Len() - 1; cursor >= 0; cursor-- {
				p.treeCursor = cursor
				p.ensureTreeCursorVisible()
				if !p.treeRowVisible(p.treeCursor) {
					t.Fatalf("cursor %d is outside the drawn viewport on the way back up (off=%d rows=%d)",
						cursor, p.treeScrollOff, p.treeItemRows())
				}
			}
		})
	}
}
