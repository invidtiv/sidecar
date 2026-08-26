package overview

import (
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
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
