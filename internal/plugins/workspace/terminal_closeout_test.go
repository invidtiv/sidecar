package workspace

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	boardkanban "github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/tty"
)

// The board words and colours nothing itself: every shared lane is the one
// definition the global browser draws the same lanes from, so a rename or a
// re-theme cannot land on one board alone.
func TestKanbanLanesComeFromTheSharedLaneDefinition(t *testing.T) {
	p := &Plugin{}
	lanes := p.workspaceKanbanBoard().Lanes
	if len(lanes) != kanbanColumnCount() {
		t.Fatalf("lanes = %d, want %d", len(lanes), kanbanColumnCount())
	}
	// The first column is this board's own; the rest are the shared lanes.
	for i, laneID := range kanbanLaneOrder {
		shared := boardkanban.AgentLane(laneID)
		got := lanes[i+1]
		if got.Label != shared.Label {
			t.Fatalf("lane %q label = %q, want the shared %q", laneID, got.Label, shared.Label)
		}
		if got.HeaderColor != shared.HeaderColor {
			t.Fatalf("lane %q colour = %v, want the shared %v", laneID, got.HeaderColor, shared.HeaderColor)
		}
	}
	if lanes[kanbanShellColumnIndex].Label != "Shells" {
		t.Fatalf("shells column = %q, want the board's own lane kept", lanes[kanbanShellColumnIndex].Label)
	}
	// A lane header the component appends a count to has to fit the narrowest
	// column this board lays out.
	for _, laneID := range kanbanLaneOrder {
		if width := len([]rune(boardkanban.AgentLane(laneID).Label)) + 2; width > minKanbanColumnWidth {
			t.Fatalf("lane %q header needs %d columns, have %d", laneID, width, minKanbanColumnWidth)
		}
	}
}

// The whole contract with the terminal component is one value. A hook wired
// field by field is a hook the other embedding surface can gain without this one
// noticing.
func TestWorkspaceTerminalStatesItsWholeHostContract(t *testing.T) {
	p := &Plugin{}
	hooks := p.terminalHooks()
	if hooks.OnKey == nil || hooks.BeforeSend == nil || hooks.OnExit == nil ||
		hooks.OnAttach == nil || hooks.OnSessionEnded == nil {
		t.Fatalf("hooks = %+v, want every callback this surface owns", hooks)
	}
	// This surface keeps drawing the pane after the user leaves it, so leaving
	// releases the keyboard and nothing else.
	if hooks.ExitAction != tty.ExitReleasesInput {
		t.Fatalf("exit action = %v, want the terminal kept open", hooks.ExitAction)
	}
	model := p.newWorkspaceTerminal()
	if model.OnKey == nil || model.BeforeSend == nil || model.OnExit == nil ||
		model.OnAttach == nil || model.OnSessionEnded == nil ||
		model.ExitAction != hooks.ExitAction {
		t.Fatal("the component was built without the contract this host states")
	}
}

// A resize arriving inside the debounce window is owed, not swallowed: the pane
// is still drawn at a size it has not been given. One retry stands for the whole
// burst, or a window drag becomes a chain of resizes.
func TestDebouncedInteractivePaneResizeIsRetriedOnceForABurst(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40
	p.interactiveState.TargetPane = "%9"
	p.interactiveState.LastResizeAt = time.Now()

	armed := 0
	for _, size := range [][2]int{{10, 5}, {11, 6}, {12, 7}, {13, 8}} {
		cmd := p.maybeResizeInteractivePane(size[0], size[1])
		if cmd == nil {
			t.Fatalf("a resize inside the debounce window was dropped at %dx%d", size[0], size[1])
		}
		if deferredResizeArmed(cmd) {
			armed++
		}
	}
	if armed != 1 {
		t.Fatalf("a burst of sizes armed %d retries, want exactly 1", armed)
	}
	if !p.interactiveState.ResizeRetryPending {
		t.Fatal("nothing recorded that a deferred assertion is pending")
	}
}

// deferredResizeArmed reports whether cmd carries the deferred re-assertion. The
// resize path batches it with the lease touch, so both are drained.
func deferredResizeArmed(cmd tea.Cmd) bool {
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, inner := range batch {
			if deferredResizeArmed(inner) {
				return true
			}
		}
		return false
	}
	_, ok := msg.(deferredPaneResizeMsg)
	return ok
}

// A drag-select pins the window for the duration of the gesture. The end of the
// gesture has to release it, or the pane has stopped following its agent's
// output for good — with nothing on screen to say why.
func TestDragSelectLeavesThePaneFollowingOutput(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40

	p.freezeTerminalSelectionViewport()
	if p.autoScrollOutput {
		t.Fatal("the window was not pinned for the gesture")
	}
	p.finishInteractiveSelection()
	if !p.autoScrollOutput {
		t.Fatal("the pane stopped following output after a drag-select that ended at the live edge")
	}
	if p.terminalSelectionFrozen {
		t.Fatal("the window stayed pinned after the gesture that pinned it ended")
	}
}

// A gesture that pinned a window already scrolled back leaves it where the user
// put it: thawing places the rows on screen back against the live bottom, it
// does not drag them to it.
func TestThawKeepsAWindowTheGestureScrolledBack(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40
	p.terminalSelectionFrozen = true
	p.autoScrollOutput = false
	p.previewOffset = 3

	p.thawTerminalSelectionViewport()
	if p.previewOffset != min(3, p.getMaxScrollOffset()) {
		t.Fatalf("previewOffset = %d, want the rows the gesture left on screen", p.previewOffset)
	}
}

// The chords that enter a live pane are the shared ones, so the browser and this
// surface cannot drift into answering different keys for the same act.
func TestEnterInteractiveChordsAreTheSharedOnes(t *testing.T) {
	if tty.EnterInteractiveKey != "i" || tty.EnterInteractiveKeyAlt != "E" {
		t.Fatalf("enter chords = %q/%q", tty.EnterInteractiveKey, tty.EnterInteractiveKeyAlt)
	}
}
