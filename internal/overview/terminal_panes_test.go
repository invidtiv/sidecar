package overview

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestPreviewTerminalStateFollowsTreeLeafIdentity(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.preview.paneRoot = &panelayout.Node{ID: 4, Kind: panelayout.Terminal}
	leaf := m.previewTerminalLeaf()
	leaf.Scroll = 9
	leaf.Interactive = true
	leaf.Selection.SelectRange(
		ui.SelectionPoint{Line: 3, Col: 1},
		ui.SelectionPoint{Line: 3, Col: 4},
		false,
	)
	state := m.previewTerminalState()
	state.terminal = &fakeTerminal{active: true}
	state.termBar = previewTermBar{active: true}

	m.preview.paneRoot = &panelayout.Node{ID: 7, Kind: panelayout.Terminal}
	got := m.previewTerminalLeaf()
	if got != leaf || got.ID != 7 || m.preview.terminalPanes.Leaf(4) != nil {
		t.Fatal("terminal state did not follow the live tree leaf ID")
	}
	if got.Scroll != 9 || !got.Interactive || !got.Selection.HasSelection() || m.previewTerminalState() != state || !state.termBar.active {
		t.Fatal("terminal leaf rekey dropped viewport, input, selection, or gesture state")
	}

	// An interactive terminal only owns input while its own leaf owns focus.
	m.preview.paneFocus = got.ID
	if !m.PreviewInteractive() {
		t.Fatal("the focused live terminal leaf did not own interactive input")
	}
	m.preview.paneFocus = got.ID + 1
	if m.PreviewInteractive() {
		t.Fatal("an unfocused terminal leaf retained interactive ownership")
	}
}

func TestReviewRowSwitchClearsTerminalLeafGestures(t *testing.T) {
	m, _ := previewModel(t)
	leaf := m.previewTerminalLeaf()
	at := time.Unix(300, 0)
	if _, ok := leaf.Wheel.Add(1, at); !ok {
		t.Fatal("test premise: first wheel event was unexpectedly held")
	}
	if _, ok := leaf.Wheel.Add(1, at.Add(tty.WheelDebounceInterval/2)); ok || leaf.Wheel.Pending() == 0 {
		t.Fatal("test premise: second wheel event did not leave a pending burst")
	}
	m.previewTerminalState().termBar = previewTermBar{active: true}

	m.workspaces.SelectID("b")
	m.bindPreview(false)

	if got := m.previewTerminalLeaf(); got != leaf {
		t.Fatal("test premise: row switch did not reuse the N=1 terminal leaf")
	}
	if pending := leaf.Wheel.Pending(); pending != 0 {
		t.Fatalf("row switch retained pending terminal wheel delta %d", pending)
	}
	if m.previewTerminalState().termBar.active {
		t.Fatal("row switch retained the previous row's scrollbar gesture")
	}
}
