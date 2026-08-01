package filebrowser

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/state"
)

// The drag-end switch used to be unreachable: the handler cleared its drag
// region before handleMouseDragEnd read it, so nothing in it ever ran. Now the
// source region travels on the action, so these behaviors are live and need
// covering.

// newPreviewTestPlugin gives the plugin enough preview content for
// previewSelectionAtXY to resolve a click, plus one hit region per preview row.
func newPreviewTestPlugin(t *testing.T) *Plugin {
	t.Helper()
	p := newDragTestPlugin(t)
	p.previewLines = []string{
		"package main",
		"",
		"func main() {}",
		"// trailing",
	}
	p.previewWrapEnabled = false
	p.previewScroll = 0
	p.activePane = PanePreview

	p.mouseHandler.Clear()
	// previewSelectionAtXY maps y=3 to preview line 0 (border + header offset).
	for i := range p.previewLines {
		p.mouseHandler.HitMap.AddRect(regionPreviewLine, 31, 3+i, 60, 1, i)
	}
	return p
}

// The copy hint fires on the first completed drag-select and never again.
func TestPreviewDragSelectShowsCopyHintOnce(t *testing.T) {
	p := newPreviewTestPlugin(t)

	press(t, p, 40, 3)
	motion(t, p, 46, 4)
	_, cmd := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 46, Y: 4, Button: tea.MouseLeft}))

	if !p.selection.HasSelection() {
		t.Fatal("expected a selection after a drag-select")
	}
	if cmd == nil {
		t.Fatal("first drag-select should return the copy-hint toast command")
	}
	toast, ok := cmd().(appmsg.ToastMsg)
	if !ok {
		t.Fatalf("command produced %T, want ToastMsg", cmd())
	}
	if !strings.Contains(toast.Message, "copy selection") {
		t.Errorf("toast = %q, want the copy hint", toast.Message)
	}
	if !p.selectionCopyHintShown {
		t.Error("selectionCopyHintShown should be set after the hint")
	}

	// Second selection: no repeat.
	press(t, p, 40, 5)
	motion(t, p, 46, 6)
	_, cmd2 := p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 46, Y: 6, Button: tea.MouseLeft}))
	if cmd2 != nil {
		t.Errorf("second drag-select returned a command (%T), want nil", cmd2())
	}
}

// A plain click in the preview pane (press, release, no motion) leaves no
// selection behind.
func TestPreviewClickWithoutMotionLeavesNoSelection(t *testing.T) {
	p := newPreviewTestPlugin(t)

	// Establish a selection first.
	press(t, p, 40, 3)
	motion(t, p, 46, 4)
	release(t, p, 46, 4)
	if !p.selection.HasSelection() {
		t.Fatal("expected a selection to clear")
	}

	press(t, p, 40, 5)
	release(t, p, 40, 5)

	if p.selection.HasSelection() {
		t.Error("a plain click must not leave a selection")
	}
	if p.selection.Active {
		t.Error("selection should not be active after a plain click")
	}
}

// Releasing the pane divider persists the new tree width.
func TestDividerDragEndPersistsTreeWidth(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatalf("state init: %v", err)
	}

	p := newDragTestPlugin(t)
	p.mouseHandler.Clear()
	p.mouseHandler.HitMap.AddRect(regionPaneDivider, p.treeWidth, 0, 1, 20, nil)

	startWidth := p.treeWidth
	press(t, p, startWidth, 5)
	motion(t, p, startWidth+6, 5)
	if p.treeWidth == startWidth {
		t.Fatalf("divider drag did not resize the tree pane (still %d)", startWidth)
	}
	release(t, p, startWidth+6, 5)

	if got := state.GetFileBrowserTreeWidth(); got != p.treeWidth {
		t.Errorf("persisted tree width = %d, want %d", got, p.treeWidth)
	}
}
