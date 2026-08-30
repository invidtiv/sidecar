package overview

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panelayout"
)

// paneLayoutShortcutLeaf resolves M from the active Workspaces window. The
// preview targets its focused leaf; the list targets the selected row's
// Primary leaf. Input and overlay states retain printable keys.
func (m *Model) paneLayoutShortcutLeaf() int {
	if m == nil || !features.IsEnabled(features.PaneMove.Name) || m.preview.paneRoot == nil ||
		m.paneLayoutModal != nil || m.PreviewInteractive() || m.renameOpen || m.createOpen ||
		m.deleteOpen || m.viewFlyoutOpen || m.WorkspacesFilterFocused() ||
		m.previewDocEditing() || m.previewDocSearchActive() || m.previewDocFindActive() ||
		m.terminalSearch.InputActive {
		return 0
	}
	var leaf *panelayout.Node
	if m.PreviewFocused() {
		leaf = panelayout.Find(m.preview.paneRoot, m.preview.paneFocus)
	} else {
		leaf = panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal)
	}
	if leaf == nil || leaf.Split != nil {
		return 0
	}
	return leaf.ID
}

func (m *Model) handlePaneMoveKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if msg.String() != "M" {
		return false, nil
	}
	leafID := m.paneLayoutShortcutLeaf()
	if leafID == 0 {
		return false, nil
	}
	return true, m.openPaneLayoutModal(leafID)
}
