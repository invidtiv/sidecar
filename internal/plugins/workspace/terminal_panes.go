package workspace

import "github.com/marcus/sidecar/internal/termpanes"

func (p *Plugin) ensureTerminalPanes() *termpanes.Deck {
	if p.terminalPanes == nil {
		p.terminalPanes = termpanes.New()
	}
	return p.terminalPanes
}

func (p *Plugin) primaryTermPane() *termpanes.Leaf {
	id := terminalLeafID(p.paneRoot)
	if id <= 0 {
		id = 1
	}
	deck := p.ensureTerminalPanes()
	if leaf := deck.Leaf(id); leaf != nil {
		return leaf
	}
	leaf := termpanes.NewLeaf(id, nil)
	deck.Attach(leaf)
	return leaf
}

func (p *Plugin) shellTermPane() *termpanes.Leaf {
	leafNode := p.shellLeaf()
	if leafNode == nil {
		return nil
	}
	deck := p.ensureTerminalPanes()
	if leaf := deck.Leaf(leafNode.ID); leaf != nil {
		return leaf
	}
	if staged := deck.Leaf(0); staged != nil {
		if !staged.Requested {
			return staged
		}
		if leaf := deck.Rekey(0, leafNode.ID); leaf != nil {
			return leaf
		}
	}
	oldID := 0
	deck.Range(func(id int, leaf *termpanes.Leaf) bool {
		if id != leafNode.ID && leaf != nil && leaf.Requested {
			oldID = id
			return false
		}
		return true
	})
	if oldID != 0 {
		if leaf := deck.Rekey(oldID, leafNode.ID); leaf != nil {
			return leaf
		}
	}
	leaf := termpanes.NewLeaf(leafNode.ID, nil)
	deck.Attach(leaf)
	return leaf
}

func (p *Plugin) requireShellTermPane() *termpanes.Leaf {
	if leaf := p.shellTermPane(); leaf != nil {
		return leaf
	}
	deck := p.ensureTerminalPanes()
	if leaf := deck.Leaf(0); leaf != nil {
		return leaf
	}
	leaf := termpanes.NewLeaf(0, nil)
	deck.Attach(leaf)
	return leaf
}

func (p *Plugin) requestShellLeaf() { p.requireShellTermPane().Requested = true }

func (p *Plugin) shellLeafRequested() bool {
	if p.terminalPanes == nil {
		return false
	}
	if leaf := p.terminalPanes.Leaf(0); leaf != nil && leaf.Requested {
		return true
	}
	leaf := p.shellLeaf()
	if leaf != nil {
		if state := p.terminalPanes.Leaf(leaf.ID); state != nil && state.Requested {
			return true
		}
	}
	requested := false
	p.terminalPanes.Range(func(_ int, state *termpanes.Leaf) bool {
		if state != nil && state.Requested {
			requested = true
			return false
		}
		return true
	})
	return requested
}

func (p *Plugin) releaseShellTermPane() {
	if p.terminalPanes == nil {
		return
	}
	ids := []int{0}
	p.terminalPanes.Range(func(id int, leaf *termpanes.Leaf) bool {
		if leaf != nil && leaf.Requested && id != 0 {
			ids = append(ids, id)
		}
		return true
	})
	if leaf := p.shellLeaf(); leaf != nil {
		ids = append(ids, leaf.ID)
	}
	for _, id := range ids {
		p.terminalPanes.Release(id)
	}
}

func (p *Plugin) hideShellTermPane() {
	leaf := p.requireShellTermPane()
	leaf.Requested = false
	leaf.Close()
	if leaf.ID > 0 {
		p.ensureTerminalPanes().Rekey(leaf.ID, 0)
	}
}

func (p *Plugin) shellLeafVisible() bool { return p.shellLeaf() != nil }

func (p *Plugin) shellLeafFocused() bool {
	leaf := p.shellLeaf()
	return leaf != nil && p.activePane == PanePreview && p.paneFocus == leaf.ID
}

func (p *Plugin) setShellLeafFocused(focused bool) {
	leaf := p.shellLeaf()
	if focused {
		if leaf != nil {
			p.activePane = PanePreview
			p.paneFocus = leaf.ID
		}
		return
	}
	if leaf != nil && p.paneFocus == leaf.ID {
		p.paneFocus = terminalLeafID(p.paneRoot)
	}
}

func (p *Plugin) terminalPane(termPanel bool) *termpanes.Leaf {
	if termPanel {
		return p.requireShellTermPane()
	}
	return p.primaryTermPane()
}

func (p *Plugin) terminalLeafID(termPanel bool) int {
	if termPanel {
		if leaf := p.shellLeaf(); leaf != nil {
			return leaf.ID
		}
		return 0
	}
	return terminalLeafID(p.paneRoot)
}

func (p *Plugin) terminalPaneIsPanel(id int) bool {
	leaf := p.shellLeaf()
	if leaf != nil {
		return leaf.ID == id
	}
	primaryID := terminalLeafID(p.paneRoot)
	return id > 0 && id != primaryID
}

func (p *Plugin) rebindTerminalPaneTree(oldRoot, newRoot *PaneNode) {
	deck := p.ensureTerminalPanes()
	var primary, shell *termpanes.Leaf
	if id := terminalLeafID(oldRoot); id > 0 {
		primary = deck.Leaf(id)
	}
	if node := firstPaneLeafOfKind(oldRoot, PaneShell); node != nil {
		shell = deck.Leaf(node.ID)
	}
	if shell == nil {
		shell = deck.Leaf(0)
	}
	next := termpanes.New()
	if id := terminalLeafID(newRoot); id > 0 {
		if primary == nil {
			primary = termpanes.NewLeaf(id, nil)
		}
		primary.ID = id
		next.Attach(primary)
	}
	if node := firstPaneLeafOfKind(newRoot, PaneShell); node != nil && shell != nil {
		shell.ID = node.ID
		next.Attach(shell)
	} else if shell != nil {
		shell.ID = 0
		next.Attach(shell)
	}
	p.terminalPanes = next
}
