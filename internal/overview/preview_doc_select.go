package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/textselect"
)

// Text selection in the global browser's document pane.
//
// This is the parity half of the workspace plugin's doc_select.go. Both bind to
// the one binding in internal/docview: what a gesture means, what is
// highlighted and what reaches the clipboard is decided there, and each surface
// answers only where its leaf was drawn and which of its own events reach it.

// bindPreviewDocSelection tells the viewer where it is on screen and which
// chords it answers. It runs from the leaf's render, the only place that knows
// both and the only place that runs for exactly the pane that is drawn.
func (m *Model) bindPreviewDocSelection(view *docview.Model, box termpreview.Box) {
	if view == nil {
		return
	}
	config := m.TerminalConfig()
	view.SetSelection(config.SelectionKeys(), config.CopyOnSelect)
	// The body sits below the leaf's own header row.
	view.SetOrigin(box.X, box.Y+termpreview.HeaderRows)
}

// previewDocSelectionView is the document viewer currently on screen, or nil.
func (m *Model) previewDocSelectionView() *docview.Model {
	if m.preview.doc == nil {
		return nil
	}
	return m.preview.doc.view()
}

// clearPreviewDocSelections drops every document selection this surface holds
// but keep's. It is what a terminal gesture calls, with nothing to keep, to take
// the one live selection for itself.
func (m *Model) clearPreviewDocSelections(keep *docview.Model) {
	if m.preview.doc == nil {
		return
	}
	m.preview.doc.tabs.ClearSelectionsExcept(keep)
}

// pressPreviewDocSelection arms a selection gesture over the document's text.
// A press that resolves to a click still does what it did before; this only
// decides what the motion after it means.
func (m *Model) pressPreviewDocSelection(action mouse.MouseAction) tea.Cmd {
	view := m.previewDocSelectionView()
	if view == nil {
		return nil
	}
	m.clearPreviewDocSelections(view)
	// One selection at a time means one on this whole surface: the terminal is
	// drawn beside this pane, so its highlight goes with the rest.
	m.clearPreviewSelection()
	result := view.HandleSelectionMouse(action)
	if !result.Handled {
		return nil
	}
	// Registered with the shared handler because that is what turns the release
	// into a drag end this gesture can be finished by.
	m.workspacesMouse.StartDrag(action.X, action.Y, previewDocRegionKind, 0)
	return m.previewDocSelectionResult(view, result)
}

// handlePreviewDocGesture answers the half of a selection gesture that does not
// arrive as a region hit: the motion, which routinely leaves the pane, and the
// release, which is the drag's rather than the region's under the pointer.
func (m *Model) handlePreviewDocGesture(action mouse.MouseAction, wasDragging bool, dragSourceBefore string) (tea.Cmd, bool) {
	if action.DragStartID != previewDocRegionKind && dragSourceBefore != previewDocRegionKind {
		return nil, false
	}
	view := m.previewDocSelectionView()
	switch action.Type {
	case mouse.ActionDrag, mouse.ActionDragEnd:
		if view == nil {
			return nil, true
		}
		return m.previewDocSelectionResult(view, view.HandleSelectionMouse(action)), true
	case mouse.ActionHover:
		// A release lost off-window: the handler ends the drag on the first
		// button-less motion, and the gesture ends with it.
		if wasDragging && !m.workspacesMouse.IsDragging() {
			if view != nil {
				view.AbandonSelection()
			}
			return nil, true
		}
	}
	return nil, false
}

// handlePreviewDocSelectionKey answers the chords that act on the selection —
// including escape, which the viewer answers so it clears a selection before the
// pane's own esc can mean anything else.
func (m *Model) handlePreviewDocSelectionKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	view := m.previewDocSelectionView()
	if view == nil {
		return nil, false
	}
	result := view.HandleSelectionKey(msg)
	if !result.Handled {
		return nil, false
	}
	return m.previewDocSelectionResult(view, result), true
}

// previewDocSelectionResult is what this surface owes the engine's answer: a
// copy, delivered as this surface's own toast. Nothing persists this pane's
// scroll offset, so a drag that scrolled it leaves nothing to save.
func (m *Model) previewDocSelectionResult(view *docview.Model, result textselect.Result) tea.Cmd {
	return view.SelectionCopyCmd(result, func(notice textselect.CopyNotice) tea.Msg {
		// A copy that worked is a flash; one that failed is a notification.
		if notice.IsError {
			return appmsg.ToastMsg{Message: notice.Message, Duration: notice.Duration, IsError: true}
		}
		return appmsg.FlashMsg{Text: notice.Message}
	})
}
