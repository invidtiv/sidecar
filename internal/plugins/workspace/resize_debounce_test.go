package workspace

import (
	"os"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
)

// Policy (td-59ce8a): No SIGWINCH until drop.
//
// During an active divider drag (IsDragging / DragRegion is regionPaneDivider,
// regionPaneTreeDivider, regionTermPanelDivider, or regionDiffTabDivider):
//   - Handle/box geometry updates every event (handleMouseDrag).
//   - handleMouseDrag emits no resize cmd.
//   - maybeResizeInteractivePane / maybeResizeVisiblePane must not issue a
//     tmux resize. Touching the geometry lease is fine.
//   - Sidecar View may keep painting so the handle follows the pointer.
//     Local rewrap of already-captured lines is not a SIGWINCH; the agent
//     redraw is the resize.
//   - On drop: immediate tmux resize and full paint. A leftover
//     deferredPaneResizeMsg is a no-op so it cannot fire a second resize.
//
// 0 debounce is today's per-event paint + poll-driven resize. Keyboard +/-
// and host WindowSizeMsg are not divider drags and stay immediate.
func TestDividerDragBurstEmitsNoResizeUntilRelease(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# drag\n")
	p := docPaneTestPlugin(t, root, true)
	p.ctx.ProjectRoot = root
	var saved state.WorkspaceState
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { saved = next; return nil },
	}
	p.openTerminalPath("README.md", 0)
	splitID := p.paneRoot.ID
	p.handleMouseClick(mouse.MouseAction{Region: &mouse.Region{ID: regionPaneTreeDivider, Data: splitID}, X: 70, Y: 5})

	var lastRatio int
	for i, dx := range []int{4, 8, 12, 16} {
		if cmd := p.handleMouseDrag(mouse.MouseAction{DragStartID: regionPaneTreeDivider, DragDX: dx}); cmd != nil {
			t.Fatalf("drag %d emitted a tmux resize command", i)
		}
		lastRatio = p.paneRoot.Split.Ratio
	}
	if lastRatio == 50 {
		t.Fatal("burst of drag events left the starting ratio")
	}

	cmd := p.handleMouseDragEnd(mouse.MouseAction{
		DragStartID: regionPaneTreeDivider,
		Region:      &mouse.Region{ID: regionPreviewPane},
	})
	if cmd == nil {
		t.Fatal("drag-end did not flush an immediate resize")
	}
	if _, ok := cmd().(paneResizedMsg); !ok {
		t.Fatalf("divider release command = %T, want one direct terminal resize", cmd())
	}
	if p.paneRoot.Split.Ratio != lastRatio {
		t.Fatalf("release changed the last drag ratio %d -> %d", lastRatio, p.paneRoot.Split.Ratio)
	}
}

func TestMaybeResizeInteractivePaneSkipsTmuxDuringDividerDrag(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40
	p.interactiveState.TargetPane = "%9"
	p.mouseHandler = mouse.NewHandler()
	p.mouseHandler.StartDrag(10, 5, regionPaneTreeDivider, 50)

	cmd := p.maybeResizeInteractivePane(1, 1)
	if cmd == nil {
		t.Fatal("lease touch should still run during a divider drag")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("divider-drag poll issued work: %#v", msg)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logged), "resize-") {
		t.Fatalf("SIGWINCH during divider drag: %s", logged)
	}
	if !strings.Contains(string(logged), leaseOptionForTest) {
		t.Fatalf("divider-drag poll did not tick the lease: %q", logged)
	}

	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p.setResizeDebounce(0)
	cmd = p.maybeResizeInteractivePane(1, 1)
	if cmd == nil {
		t.Fatal("debounce 0 dropped the poll-driven resize")
	}
	if _, ok := cmd().(paneResizedMsg); !ok {
		t.Fatal("debounce 0 should still resize during a drag")
	}
	logged, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "resize-") {
		t.Fatalf("debounce 0 did not resize during drag: %s", logged)
	}
}

func TestMaybeResizeVisiblePaneSkipsTmuxDuringDividerDrag(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40
	p.mouseHandler = mouse.NewHandler()
	p.mouseHandler.StartDrag(10, 5, regionPaneDivider, 40)

	if cmd := p.maybeResizeVisiblePane("%9", 1, 1, false); cmd != nil {
		t.Fatal("visible-pane poll issued a tmux resize during a divider drag")
	}

	p.setResizeDebounce(0)
	if cmd := p.maybeResizeVisiblePane("%9", 1, 1, false); cmd == nil {
		t.Fatal("debounce 0 should still resize a visible pane during a drag")
	}
}

func TestDividerDragEndCancelsPendingDeferredResize(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40
	p.interactiveState.TargetPane = "%9"
	p.interactiveState.ResizeRetryPending = true
	p.mouseHandler = mouse.NewHandler()
	p.lastDragRegion = regionPaneTreeDivider

	armedGen := p.resizeGeneration
	p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPaneTreeDivider})
	if p.interactiveState.ResizeRetryPending {
		t.Fatal("drag-end left a deferred resize pending")
	}
	if p.resizeGeneration == armedGen {
		t.Fatal("drag-end did not invalidate the leftover tick")
	}

	_, cmd := p.Update(deferredPaneResizeMsg{Generation: armedGen})
	if cmd != nil {
		t.Fatal("leftover deferred tick issued a second resize after drop")
	}
}

func TestResizeDebounceDefaultMatchesConfig(t *testing.T) {
	p := New()
	if p.resizeDebounce() != tty.DefaultResizeDebounce {
		t.Fatalf("plugin default = %v, want %v", p.resizeDebounce(), tty.DefaultResizeDebounce)
	}
	p.setResizeDebounce(0)
	if p.resizeDebounce() != 0 {
		t.Fatalf("explicit 0 = %v", p.resizeDebounce())
	}
}
