package filebrowser

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
)

// newDragTestPlugin builds a plugin over a small real tree and registers one
// tree-item hit region per visible row, mirroring what the view does:
//
//	row y=0 -> flat index 0, y=1 -> flat index 1, ...
func newDragTestPlugin(t *testing.T) *Plugin {
	t.Helper()
	tmpDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(tmpDir, "dir-a"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "dir-b"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, f := range []string{"one.go", "two.go", filepath.Join("dir-a", "inner.go")} {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("package x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	p := New()
	p.ctx = &plugin.Context{
		WorkDir: tmpDir,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	p.width, p.height = 100, 30
	p.treeWidth, p.previewWidth = 30, 60
	p.tree = NewFileTree(tmpDir)
	if err := p.tree.Build(); err != nil {
		t.Fatalf("build tree: %v", err)
	}
	p.tree.Flatten()

	p.mouseHandler.Clear()
	for i := 0; i < p.tree.Len(); i++ {
		p.mouseHandler.HitMap.AddRect(regionTreeItem, 1, i, 28, 1, i)
	}
	return p
}

func press(t *testing.T, p *Plugin, x, y int) {
	t.Helper()
	p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
}

func motion(t *testing.T, p *Plugin, x, y int) {
	t.Helper()
	p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
}

func release(t *testing.T, p *Plugin, x, y int) {
	t.Helper()
	p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
}

// A press with no motion at all is an ordinary click: the cursor moves, the
// pane focuses, and no drag is ever activated. Drag-to-move ships without a
// feature flag, so this is the guard that keeps clicking working for everyone.
func TestTreeClickWithoutMotionIsNotADrag(t *testing.T) {
	p := newDragTestPlugin(t)
	if p.tree.Len() < 3 {
		t.Fatalf("need at least 3 rows, got %d", p.tree.Len())
	}

	press(t, p, 3, 2)

	if p.treeCursor != 2 {
		t.Errorf("treeCursor = %d, want 2 (click must still move the cursor)", p.treeCursor)
	}
	if p.activePane != PaneTree {
		t.Errorf("activePane = %v, want PaneTree", p.activePane)
	}
	if p.dragActive {
		t.Error("dragActive after a press with no motion")
	}
	if !p.dragArmed {
		t.Error("press on a tree row should arm a drag")
	}

	release(t, p, 3, 2)

	if p.dragArmed || p.dragActive {
		t.Errorf("drag state survived release: armed=%v active=%v", p.dragArmed, p.dragActive)
	}
	if p.dragSourcePath != "" || p.dragDropIdx != -1 {
		t.Errorf("drag state not reset: source=%q drop=%d", p.dragSourcePath, p.dragDropIdx)
	}
	if p.treeCursor != 2 {
		t.Errorf("treeCursor = %d after release, want 2", p.treeCursor)
	}
}

// Sub-threshold jitter (the hand wobbling on a click) must remain a click.
func TestTreeDragBelowThresholdStaysAClick(t *testing.T) {
	p := newDragTestPlugin(t)

	press(t, p, 3, 1)
	motion(t, p, 4, 1) // dx=1, dy=0 - under both thresholds

	if p.dragActive {
		t.Error("dragActive after 1 cell of horizontal jitter")
	}
	if !p.dragArmed {
		t.Error("should still be armed after sub-threshold motion")
	}

	release(t, p, 4, 1)
	if p.dragActive || p.dragArmed {
		t.Error("drag state survived release")
	}
}

// Crossing the threshold in either axis promotes armed -> active.
func TestTreeDragPastThresholdActivates(t *testing.T) {
	tests := []struct {
		name       string
		toX, toY   int
		wantActive bool
	}{
		{"two rows down", 3, 3, true},
		{"two rows up", 3, -1, true},
		{"two cells right", 5, 1, true},
		{"two cells left", 1, 1, true},
		{"one cell right", 4, 1, false},
		{"one row down", 3, 2, false},
		{"one row up", 3, 0, false},
		{"no motion", 3, 1, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newDragTestPlugin(t)
			press(t, p, 3, 1)
			motion(t, p, tc.toX, tc.toY)

			if p.dragActive != tc.wantActive {
				t.Errorf("dragActive = %v, want %v (moved to %d,%d)",
					p.dragActive, tc.wantActive, tc.toX, tc.toY)
			}
			if tc.wantActive {
				if p.dragArmed {
					t.Error("dragArmed should be false once the drag is active")
				}
				if p.dragSourcePath == "" {
					t.Error("dragSourcePath should name the dragged node")
				}
				if p.dragHoverSince.IsZero() {
					t.Error("dragHoverSince should be set when the drag activates")
				}
			}
		})
	}
}

// Once active, a drag stays active across further motion, and ends cleanly.
func TestTreeDragEndClearsState(t *testing.T) {
	p := newDragTestPlugin(t)

	press(t, p, 3, 1)
	motion(t, p, 3, 3)
	if !p.dragActive {
		t.Fatal("expected an active drag")
	}
	motion(t, p, 3, 4)
	if !p.dragActive {
		t.Fatal("drag should stay active across further motion")
	}

	release(t, p, 3, 4)

	if p.dragActive || p.dragArmed {
		t.Errorf("drag state survived drag end: armed=%v active=%v", p.dragArmed, p.dragActive)
	}
	if p.dragSourcePath != "" || p.dragDropIdx != -1 {
		t.Errorf("drag state not reset: source=%q drop=%d", p.dragSourcePath, p.dragDropIdx)
	}
	if !p.dragHoverSince.IsZero() {
		t.Error("dragHoverSince should be reset on drag end")
	}
}

// Dragging dir-b onto a top-level file resolves to the project root, which is
// already dir-b's parent - a no-op move. Nothing on disk may change.
func TestTreeDragOntoOwnParentPerformsNoMove(t *testing.T) {
	p := newDragTestPlugin(t)
	before := snapshotPaths(t, p.ctx.WorkDir)

	press(t, p, 3, 1)
	motion(t, p, 3, 3)
	release(t, p, 3, 3)

	after := snapshotPaths(t, p.ctx.WorkDir)
	if len(before) != len(after) {
		t.Fatalf("file set changed: %v -> %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("file set changed: %v -> %v", before, after)
		}
	}
}

// A keystroke mid-gesture (which is how panes and modes get switched) drops the
// drag rather than leaving it half-armed.
func TestKeyPressClearsDragState(t *testing.T) {
	p := newDragTestPlugin(t)

	press(t, p, 3, 1)
	motion(t, p, 3, 3)
	if !p.dragActive {
		t.Fatal("expected an active drag")
	}

	p.handleKey(tea.KeyPressMsg{Code: 'j', Text: "j"})

	if p.dragActive || p.dragArmed {
		t.Errorf("drag state survived a keypress: armed=%v active=%v", p.dragArmed, p.dragActive)
	}
	if p.dragSourcePath != "" {
		t.Errorf("dragSourcePath = %q, want empty", p.dragSourcePath)
	}
}

// The root is not part of the flat list, but draggableNode - the check
// handleMouseClick actually runs - must reject anything that resolves to it or
// to an out-of-range row.
func TestDraggableNodeRejectsRootAndOutOfRange(t *testing.T) {
	p := newDragTestPlugin(t)

	if p.draggableNode(-1) != nil {
		t.Error("negative index should not be draggable")
	}
	if p.draggableNode(p.tree.Len()) != nil {
		t.Error("out-of-range index should not be draggable")
	}
	if p.draggableNode(0) == nil {
		t.Error("a normal row should be draggable")
	}

	// A tree whose flat list somehow exposes the root must not offer it.
	p.tree.FlatList = append([]*FileNode{p.tree.Root}, p.tree.FlatList...)
	if p.draggableNode(0) != nil {
		t.Error("root node must never be draggable")
	}
}

// The press on a non-draggable row must not arm a drag, but must still click.
func TestNonDraggableRowStillClicks(t *testing.T) {
	p := newDragTestPlugin(t)
	p.tree.FlatList = append([]*FileNode{p.tree.Root}, p.tree.FlatList...)
	p.mouseHandler.Clear()
	for i := 0; i < p.tree.Len(); i++ {
		p.mouseHandler.HitMap.AddRect(regionTreeItem, 1, i, 28, 1, i)
	}

	press(t, p, 3, 0)

	if p.treeCursor != 0 {
		t.Errorf("treeCursor = %d, want 0", p.treeCursor)
	}
	if p.dragArmed || p.dragActive {
		t.Error("root row must not arm a drag")
	}
	if p.mouseHandler.IsDragging() {
		t.Error("root row must not start a handler drag")
	}
}

// Drag end resolves its source region from the action, because the handler has
// already cleared its own drag state by then.
func TestDragEndUsesActionDragStartID(t *testing.T) {
	p := newDragTestPlugin(t)
	press(t, p, 3, 1)
	motion(t, p, 3, 3)

	action := p.mouseHandler.HandleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 3, Y: 3, Button: tea.MouseLeft}))
	if action.DragStartID != regionTreeItem {
		t.Fatalf("DragStartID = %q, want %q", action.DragStartID, regionTreeItem)
	}
	if p.mouseHandler.DragRegion() != "" {
		t.Fatal("handler drag region should be cleared by EndDrag")
	}

	p.handleMouseDragEnd(action)
	if p.dragActive || p.dragArmed || p.dragSourcePath != "" {
		t.Error("handleMouseDragEnd did not clear drag state")
	}
}

// motionNoButton simulates the button-less motion the terminal streams in
// all-motion mode (the app runs tea.MouseModeAllMotion).
func motionNoButton(t *testing.T, p *Plugin, x, y int) {
	t.Helper()
	p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: x, Y: y}))
}

// If the release is lost (released outside the window, focus stolen), the next
// button-less motion must cancel the gesture instead of dragging the file
// around under a pointer with no button held.
func TestButtonlessMotionCancelsDrag(t *testing.T) {
	p := newDragTestPlugin(t)

	press(t, p, 3, 1)
	motion(t, p, 3, 3)
	if !p.dragActive {
		t.Fatal("expected an active drag")
	}

	motionNoButton(t, p, 3, 5) // release never arrived; user just moves the mouse

	if p.dragActive || p.dragArmed {
		t.Errorf("drag survived button-less motion: armed=%v active=%v", p.dragArmed, p.dragActive)
	}
	if p.mouseHandler.IsDragging() {
		t.Error("mouse handler still dragging after button-less motion")
	}
	if p.dragSourcePath != "" {
		t.Errorf("source not reset: path=%q", p.dragSourcePath)
	}

	// Further motion stays hover, so hover-driven UI keeps working.
	motionNoButton(t, p, 3, 6)
	if p.dragActive {
		t.Error("drag re-activated from hover motion")
	}
}

// The plugin flags and the mouse handler's own drag state must never disagree:
// clearDragState owns both halves.
func TestClearDragStateEndsHandlerDrag(t *testing.T) {
	p := newDragTestPlugin(t)

	press(t, p, 3, 1)
	if !p.mouseHandler.IsDragging() {
		t.Fatal("press on a tree row should start a handler drag")
	}

	p.clearDragState()

	if p.mouseHandler.IsDragging() {
		t.Error("handler still dragging after clearDragState")
	}
	if p.mouseHandler.DragRegion() != "" {
		t.Errorf("handler DragRegion = %q, want empty", p.mouseHandler.DragRegion())
	}
}

// clearDragState must not cancel gestures it does not own (divider resize,
// preview selection), which are driven by the same handler.
func TestClearDragStateLeavesOtherGesturesAlone(t *testing.T) {
	p := newDragTestPlugin(t)
	p.mouseHandler.StartDrag(30, 5, regionPaneDivider, p.treeWidth)

	p.clearDragState()

	if !p.mouseHandler.IsDragging() {
		t.Error("divider drag was cancelled by clearDragState")
	}
	if p.mouseHandler.DragRegion() != regionPaneDivider {
		t.Errorf("DragRegion = %q, want %q", p.mouseHandler.DragRegion(), regionPaneDivider)
	}
}

// A release swallowed by a modal never reaches the drag-end dispatch, so
// opening a modal has to drop the gesture itself.
func TestModalConsumingReleaseClearsDragState(t *testing.T) {
	p := newDragTestPlugin(t)

	press(t, p, 3, 1)
	motion(t, p, 3, 3)
	if !p.dragActive {
		t.Fatal("expected an active drag")
	}

	p.quickOpenMode = true
	release(t, p, 3, 3)

	if p.dragActive || p.dragArmed {
		t.Errorf("drag survived a modal-swallowed release: armed=%v active=%v", p.dragArmed, p.dragActive)
	}
	if p.mouseHandler.IsDragging() {
		t.Error("handler still dragging after a modal-swallowed release")
	}
}

// A press on empty space (no hit region) is still a new gesture and supersedes
// whatever was armed before.
func TestPressOnEmptySpaceClearsDragState(t *testing.T) {
	p := newDragTestPlugin(t)

	press(t, p, 3, 1)
	if !p.dragArmed {
		t.Fatal("expected an armed drag")
	}

	press(t, p, 90, 25) // outside every registered region

	if p.dragArmed || p.dragActive || p.dragSourcePath != "" {
		t.Errorf("drag state survived a press on empty space: armed=%v active=%v src=%q",
			p.dragArmed, p.dragActive, p.dragSourcePath)
	}
}

// The watcher can rebuild the tree mid-gesture, renumbering every flat index.
// The drag has to follow the path, not the index, or the drop moves the wrong
// file.
func TestTreeRebuildMidDragReanchorsSourceByPath(t *testing.T) {
	p := newDragTestPlugin(t)

	// Rows: dir-a, dir-b, one.go, two.go
	press(t, p, 3, 3)
	motion(t, p, 3, 5)
	if !p.dragActive {
		t.Fatal("expected an active drag")
	}
	if p.dragSourcePath != "two.go" {
		t.Fatalf("dragSourcePath = %q, want two.go", p.dragSourcePath)
	}

	// Something else writes a file that sorts before the dragged one.
	newFile := filepath.Join(p.ctx.WorkDir, "aaa-new.go")
	if err := os.WriteFile(newFile, []byte("package x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rebuilt, err := BuildTree(BuildSpec{RootDir: p.ctx.WorkDir, SortMode: p.tree.SortMode, ShowIgnored: p.tree.ShowIgnored})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	p.applyBuiltTree(rebuilt, "")

	if p.dragSourcePath != "two.go" {
		t.Errorf("dragSourcePath = %q, want two.go", p.dragSourcePath)
	}
	// The source is tracked by path, so it still resolves to the right node in
	// the renumbered tree.
	idx := p.tree.IndexOfPath(p.dragSourcePath)
	if node := p.tree.GetNode(idx); node == nil || node.Path != "two.go" {
		t.Errorf("dragSourcePath resolves to %#v in the new tree, want two.go", node)
	}
	if !p.dragActive {
		t.Error("drag should survive a rebuild that still contains the source")
	}
}

// If the dragged path is gone after a rebuild, the gesture is cancelled rather
// than left pointing at whatever now occupies that row.
func TestTreeRebuildDroppingSourceCancelsDrag(t *testing.T) {
	p := newDragTestPlugin(t)

	press(t, p, 3, 3)
	motion(t, p, 3, 5)
	if !p.dragActive || p.dragSourcePath != "two.go" {
		t.Fatalf("expected an active drag on two.go, got active=%v path=%q", p.dragActive, p.dragSourcePath)
	}

	if err := os.Remove(filepath.Join(p.ctx.WorkDir, "two.go")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	rebuilt, err := BuildTree(BuildSpec{RootDir: p.ctx.WorkDir, SortMode: p.tree.SortMode, ShowIgnored: p.tree.ShowIgnored})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	p.applyBuiltTree(rebuilt, "")

	if p.dragActive || p.dragArmed {
		t.Errorf("drag survived deletion of its source: armed=%v active=%v", p.dragArmed, p.dragActive)
	}
	if p.dragSourcePath != "" {
		t.Errorf("source not reset: path=%q", p.dragSourcePath)
	}
}

// A rebuild with no gesture in flight must not disturb anything.
func TestTreeRebuildWithoutDragIsInert(t *testing.T) {
	p := newDragTestPlugin(t)

	rebuilt, err := BuildTree(BuildSpec{RootDir: p.ctx.WorkDir, SortMode: p.tree.SortMode, ShowIgnored: p.tree.ShowIgnored})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	p.applyBuiltTree(rebuilt, "")

	if p.dragArmed || p.dragActive {
		t.Error("rebuild armed a drag out of nowhere")
	}
}

// A plugin switch can be driven by a key this plugin never sees, so blur has to
// end the gesture too.
func TestBlurClearsDragState(t *testing.T) {
	p := newDragTestPlugin(t)

	press(t, p, 3, 1)
	motion(t, p, 3, 3)
	if !p.dragActive {
		t.Fatal("expected an active drag")
	}

	p.SetFocused(false)

	if p.dragActive || p.dragArmed || p.dragSourcePath != "" {
		t.Errorf("drag survived blur: armed=%v active=%v src=%q", p.dragArmed, p.dragActive, p.dragSourcePath)
	}
	if p.mouseHandler.IsDragging() {
		t.Error("handler still dragging after blur")
	}
}

// Only New()/Init() install the -1 sentinels, so a Plugin built as a bare
// literal has dragDropIdx == 0. Drag code must key off dragArmed/dragActive,
// never off an index, or row 0 looks permanently armed.
func TestZeroValuePluginIsNotArmed(t *testing.T) {
	p := &Plugin{} // dragDropIdx == 0

	if p.dragArmed || p.dragActive {
		t.Fatal("zero-value plugin should not look armed")
	}

	p.handleTreeItemDrag(mouse.MouseAction{DragDX: 0, DragDY: 20})

	if p.dragActive {
		t.Error("handleTreeItemDrag activated a drag that was never armed")
	}
}

func snapshotPaths(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}
