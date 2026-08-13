package workspace

import (
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	boardkanban "github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/styles"
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
	// Stated as literals, not read back from the definition under test: a test
	// that builds its expectation from the production table passes a rename
	// through silently, which is the drift the shared table exists to catch.
	wantLabel := map[kanbanLane]string{
		kanbanLaneWorking: "● Working",
		kanbanLaneBlocked: "◆ Blocked",
		kanbanLaneDone:    "✓ Done",
		kanbanLaneIdle:    "○ Idle",
		kanbanLanePaused:  "⏸ Paused",
	}
	wantHue := map[kanbanLane]color.Color{
		kanbanLaneWorking: styles.StatusCompleted.GetForeground(),
		kanbanLaneBlocked: styles.StatusModified.GetForeground(),
		kanbanLaneDone:    styles.Secondary,
		kanbanLaneIdle:    styles.TextMuted,
		kanbanLanePaused:  styles.TextMuted,
	}
	// The first column is this board's own; the rest are the shared lanes.
	for i, laneID := range kanbanLaneOrder {
		got := lanes[i+1]
		if got.Label != wantLabel[laneID] {
			t.Fatalf("lane %q label = %q, want %q", laneID, got.Label, wantLabel[laneID])
		}
		if got.HeaderColor != wantHue[laneID] {
			t.Fatalf("lane %q hue = %v, want this board's own %v", laneID, got.HeaderColor, wantHue[laneID])
		}
		// A lane's cell state is the board's own, and this board leaves every
		// lane — shared and Shells alike — at the zero state.
		if got.State != lanes[kanbanShellColumnIndex].State {
			t.Fatalf("lane %q state = %q, want the Shells lane's %q", laneID, got.State, lanes[kanbanShellColumnIndex].State)
		}
	}
	if lanes[kanbanShellColumnIndex].Label != "Shells" {
		t.Fatalf("shells column = %q, want the board's own lane kept", lanes[kanbanShellColumnIndex].Label)
	}
	// A lane header the component appends a count to has to fit the narrowest
	// column this board lays out.
	for _, laneID := range kanbanLaneOrder {
		if width := len([]rune(boardkanban.AgentLane(laneID, workspaceLanePalette).Label)) + 2; width > minKanbanColumnWidth {
			t.Fatalf("lane %q header needs %d columns, have %d", laneID, width, minKanbanColumnWidth)
		}
	}
}

// The hues are this board's own and predate the shared lane definition. Sharing
// what a lane *is* must not re-theme a board that already had an answer, so they
// are pinned here: the global browser draws the same lanes in the theme's lane
// colours, and this board must not follow it there.
func TestProjectBoardKeepsItsOwnLaneHues(t *testing.T) {
	want := map[kanbanLane]color.Color{
		kanbanLaneWorking: styles.StatusCompleted.GetForeground(),
		kanbanLaneBlocked: styles.StatusModified.GetForeground(),
		kanbanLaneDone:    styles.Secondary,
		kanbanLaneIdle:    styles.TextMuted,
		kanbanLanePaused:  styles.TextMuted,
	}
	p := &Plugin{}
	lanes := p.workspaceKanbanBoard().Lanes
	for i, laneID := range kanbanLaneOrder {
		if got := lanes[i+1].HeaderColor; got != want[laneID] {
			t.Fatalf("lane %q hue = %v, want this board's own %v", laneID, got, want[laneID])
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
	givePaneScrollableOutput(p, 120)

	p.freezeTerminalSelectionViewport()
	if p.autoScrollOutput {
		t.Fatal("the window was not pinned for the gesture")
	}
	// The gesture ended where it started, against the live edge — and there is a
	// live edge to be at: a pane with no output makes every bound zero, and an
	// assertion against zero cannot tell following from stuck.
	if _, maxOffset := p.terminalWindowBounds(); maxOffset == 0 {
		t.Fatal("the fixture has no scrollback, so the window cannot be off the live edge at all")
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
	givePaneScrollableOutput(p, 120)

	p.freezeTerminalSelectionViewport()
	// The drag ran up past the top of the window, taking it back through
	// scrollback. Those are the rows the user is reading when they let go.
	scrolledBack := p.previewOffset - 20
	if scrolledBack <= 0 {
		t.Fatalf("the pinned window is only %d rows from the top of the buffer", p.previewOffset)
	}
	p.previewOffset = scrolledBack

	p.thawTerminalSelectionViewport()
	if p.previewOffset != scrolledBack {
		t.Fatalf("previewOffset = %d, want the %d rows the gesture left on screen", p.previewOffset, scrolledBack)
	}
	if p.autoScrollOutput {
		t.Fatal("a window left in scrollback was dragged back to the live edge")
	}
}

// Freeze and thaw read one derivation of the window's bounds. Where two
// disagree — interactive mode does not trim trailing rows, and a pane shorter
// than the viewport is followed from its own top rather than from the furthest
// offset — merely releasing a drag moves the rows the gesture was holding still.
func TestReleasingADragLeavesTheWindowWhereTheFreezePinnedIt(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40
	givePaneScrollableOutput(p, 120)

	p.freezeTerminalSelectionViewport()
	pinned := p.previewOffset

	p.thawTerminalSelectionViewport()
	if p.previewOffset != pinned {
		t.Fatalf("releasing the drag moved the window from %d to %d", pinned, p.previewOffset)
	}
	if !p.autoScrollOutput {
		t.Fatal("a window pinned at the live edge stopped following output when the drag ended")
	}
}

// givePaneScrollableOutput gives the selected pane a buffer long enough that the
// window can actually sit back from the live edge. A fixture with no output
// makes every bound zero, and freeze/thaw assertions against zero hold against
// an implementation that does nothing at all.
func givePaneScrollableOutput(p *Plugin, lines int) *tty.OutputBuffer {
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.ApplySnapshot(tty.CaptureSnapshot(tty.CaptureInput{
		Output:     strings.Repeat("agent output line\n", lines),
		PaneHeight: 10,
	}))
	p.shellSelected = true
	p.shells = []*ShellSession{{Name: "one", TmuxName: "sc-one", Agent: &Agent{OutputBuf: buffer}}}
	return buffer
}

// E is the remaining shared explicit type key. i is find-TD-task, not a way in.
func TestEnterInteractiveChordsAreTheSharedOnes(t *testing.T) {
	if tty.EnterInteractiveKeyAlt != "E" {
		t.Fatalf("enter alternate = %q, want E", tty.EnterInteractiveKeyAlt)
	}
}

// The armed flag is what stops a burst from becoming a chain of resizes, so a
// retry that arrives and is not acted on must still clear it. Left set, the
// surface believes a retry it will never get is still coming and swallows every
// resize after it.
func TestDroppedPaneResizeRetryDoesNotWedgeTheFlag(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40
	p.interactiveState.TargetPane = "%9"
	p.interactiveState.ResizeRetryPending = true
	// The mode ended between the retry being armed and the tick delivering it.
	p.interactiveState.Active = false

	p.Update(deferredPaneResizeMsg{})

	if p.interactiveState.ResizeRetryPending {
		t.Fatal("a retry dropped by an inactive pane left the flag armed forever")
	}
}
