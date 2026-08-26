package overview

import (
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/tty"
)

// previewTerminalState is the global host's adapter state. The common live
// terminal state lives directly on termpanes.Leaf; only the deliberately
// test-substitutable terminal seam and this host's scrollbar gesture remain
// opaque to the shared package.
type previewTerminalState struct {
	terminal previewTerminal
	termBar  previewTermBar
}

func (m *Model) previewTerminalLeaf() *termpanes.Leaf {
	if m.preview.terminalPanes == nil {
		m.preview.terminalPanes = termpanes.New()
	}
	id := 1
	if node := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal); node != nil {
		id = node.ID
	}
	if leaf := m.preview.terminalPanes.Leaf(id); leaf != nil {
		return leaf
	}
	var existingID int
	var existing *termpanes.Leaf
	m.preview.terminalPanes.Range(func(candidateID int, leaf *termpanes.Leaf) bool {
		existingID, existing = candidateID, leaf
		return false
	})
	if existing != nil {
		return m.preview.terminalPanes.Rekey(existingID, id)
	}
	leaf := termpanes.NewLeaf(id, nil)
	m.preview.terminalPanes.Attach(leaf)
	return leaf
}

func (m *Model) previewTerminalState() *previewTerminalState {
	leaf := m.previewTerminalLeaf()
	if state, ok := leaf.HostState.(*previewTerminalState); ok {
		return state
	}
	state := &previewTerminalState{}
	leaf.HostState = state
	return state
}

func (m *Model) previewTarget() tty.Target {
	target := m.previewTerminalLeaf().Target
	return tty.Target{Session: target.Session, Pane: target.Pane}
}

func (m *Model) setPreviewTarget(target tty.Target) {
	m.previewTerminalLeaf().Target = termpanes.Target{Session: target.Session, Pane: target.Pane}
}
