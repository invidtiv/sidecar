package workspace

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
)

// paneLayoutShortcutLeaf resolves M from the window that owns the keyboard.
// A focused preview targets that leaf; the sidebar targets the selected row's
// Primary leaf. Text inputs and overlays keep printable keys for themselves.
func (p *Plugin) paneLayoutShortcutLeaf() int {
	if p == nil || !features.IsEnabled(features.PaneMove.Name) || p.viewMode != ViewModeList ||
		p.paneRoot == nil || p.paneLayoutModal != nil || p.docInfo != nil ||
		p.viewFlyoutActive() || p.docEditActive() || p.docSearchActive() || p.docFindActive() ||
		p.terminalSearch.InputActive || p.filterFocused() {
		return 0
	}
	var leaf *panelayout.Node
	switch p.activePane {
	case PaneSidebar:
		leaf = panelayout.FirstOfKind(p.paneRoot, panelayout.Terminal)
	case PanePreview:
		leaf = panelayout.Find(p.paneRoot, p.paneFocus)
	}
	if leaf == nil || leaf.Split != nil {
		return 0
	}
	return leaf.ID
}

// handlePaneMoveKey keeps the existing command ID and feature flag while M's
// user contract is the transactional reposition modal exposed by the header.
func (p *Plugin) handlePaneMoveKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if msg.String() != "M" {
		return false, nil
	}
	leafID := p.paneLayoutShortcutLeaf()
	if leafID == 0 {
		return false, nil
	}
	return true, p.openPaneLayoutModal(leafID)
}

func (p *Plugin) showPaneMoveNotice(reason string) {
	p.toastMessage = panereposition.Reason(reason)
	p.toastTime = time.Now()
}
