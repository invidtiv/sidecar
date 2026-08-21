package workspace

import tea "charm.land/bubbletea/v2"

func (p *Plugin) hideContentPane(leafID int) tea.Cmd {
	root, surface, ok := p.selectedTerminalSurface()
	if ok {
		p.rememberHiddenPaneLayout(root, surface)
	}
	if p.contentDeck != nil && p.contentDeck.FocusLeaf(leafID) {
		p.contentDeck.HideFocused()
	}
	if !p.closeContentLeaf(leafID) {
		p.hiddenPaneLayout = nil
		return nil
	}
	p.activePane = PanePreview
	p.saveSelectionState()
	return p.resizeDocTerminalCmd()
}

// forgetContentPane forgets a content leaf outright: unlike hide, the hidden
// snapshot goes with it, which is what makes last-x forget the tab set that
// q/esc keeps. closeContentPane is the click dispatcher that routes here per
// kind; this is what every one of those routes ends in.
func (p *Plugin) forgetContentPane(leafID int) tea.Cmd {
	if p.contentDeck != nil {
		p.contentDeck.ForgetLeaf(leafID)
	}
	if !p.closeContentLeaf(leafID) {
		return nil
	}
	p.hiddenPaneLayout = nil
	p.activePane = PanePreview
	p.saveSelectionState()
	return p.resizeDocTerminalCmd()
}
