package overview

import (
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/termpreview"
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

func (m *Model) primaryTerminalLeaf() *termpanes.Leaf {
	if m.preview.terminalPanes == nil {
		m.preview.terminalPanes = termpanes.New()
	}
	id := 1
	if node := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal); node != nil {
		id = node.ID
	}
	if leaf := m.preview.terminalPanes.Leaf(id); leaf != nil {
		leaf.Target.Source = "primary"
		return leaf
	}
	var existingID int
	var existing *termpanes.Leaf
	m.preview.terminalPanes.Range(func(candidateID int, leaf *termpanes.Leaf) bool {
		if leaf.Target.Source == "primary" {
			existingID, existing = candidateID, leaf
			return false
		}
		return true
	})
	if existing != nil {
		return m.preview.terminalPanes.Rekey(existingID, id)
	}
	leaf := termpanes.NewLeaf(id, nil)
	leaf.Target.Source = "primary"
	m.preview.terminalPanes.Attach(leaf)
	return leaf
}

func (m *Model) previewTerminalLeaf() *termpanes.Leaf {
	if node := panelayout.Find(m.preview.paneRoot, m.preview.paneFocus); node != nil && node.Split == nil && panelayout.IsLive(node.Kind) {
		return m.terminalLeaf(node.ID)
	}
	return m.primaryTerminalLeaf()
}

func (m *Model) terminalLeaf(id int) *termpanes.Leaf {
	if id <= 0 {
		return m.primaryTerminalLeaf()
	}
	if m.preview.terminalPanes == nil {
		m.preview.terminalPanes = termpanes.New()
	}
	if leaf := m.preview.terminalPanes.Leaf(id); leaf != nil {
		return leaf
	}
	leaf := termpanes.NewLeaf(id, nil)
	leaf.RowAnalyzer = &termpreview.RowAnalyzer{}
	m.preview.terminalPanes.Attach(leaf)
	return leaf
}

func (m *Model) terminalState(id int) *previewTerminalState {
	leaf := m.terminalLeaf(id)
	if state, ok := leaf.HostState.(*previewTerminalState); ok {
		return state
	}
	state := &previewTerminalState{}
	leaf.HostState = state
	return state
}

func (m *Model) previewTerminalState() *previewTerminalState {
	return m.terminalState(m.previewTerminalLeaf().ID)
}

func (m *Model) primaryTerminalState() *previewTerminalState {
	return m.terminalState(m.primaryTerminalLeaf().ID)
}

func (m *Model) previewTarget() tty.Target {
	target := m.previewTerminalLeaf().Target
	return tty.Target{Session: target.Session, Pane: target.Pane, Host: target.Host}
}

func (m *Model) primaryTarget() tty.Target {
	target := m.primaryTerminalLeaf().Target
	return tty.Target{Session: target.Session, Pane: target.Pane, Host: target.Host}
}

func (m *Model) setPrimaryTarget(target tty.Target) {
	leaf := m.primaryTerminalLeaf()
	source := leaf.Target.Source
	leaf.Target = termpanes.Target{Session: target.Session, Pane: target.Pane, Host: target.Host, Source: source}
}
