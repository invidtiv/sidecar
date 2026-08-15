package notes

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	sharedscroll "github.com/marcus/sidecar/internal/scroll"
)

// listBounds describes the note list's wheel position: the wheel moves the
// cursor over the currently displayed (filtered) notes.
func (p *Plugin) listBounds() sharedscroll.Bounds {
	return sharedscroll.Bounds{Position: p.cursor, Maximum: len(p.getDisplayNotes()) - 1}
}

// previewBounds describes the markdown preview's scroll position using the
// same rendered maximum the renderer clamps to.
func (p *Plugin) previewBounds() sharedscroll.Bounds {
	height, width := p.previewViewport()
	return sharedscroll.Bounds{Position: p.previewScrollOff, Maximum: p.previewMaxScroll(height, width)}
}

// WheelAtBoundary implements plugin.WheelBoundaryConsumer for the Notes list
// and markdown preview. It mirrors handleMouseScroll's routing without moving
// the cursor, loading a note, or rendering.
//
// Textarea edit mode and the inline tmux editor stay unknown: the textarea
// exposes no exact viewport boundary, and the inline editor's wheel belongs to
// the embedded application. Modal and exit-confirmation modes stay unknown
// until the shared modal contract can answer for them.
func (p *Plugin) WheelAtBoundary(msg tea.MouseWheelMsg) bool {
	if p == nil || p.mouseHandler == nil || p.store == nil || p.loading || p.loadErr != nil {
		return false
	}
	if p.inlineEditMode || p.showExitConfirmation || p.showTaskModal || p.showDeleteModal || p.showInfoModal {
		return false
	}
	action := p.mouseHandler.HandleMouse(msg)
	if action.Type != mouse.ActionScrollUp && action.Type != mouse.ActionScrollDown {
		return false
	}
	inListPane := action.X < p.listWidth
	if action.Region != nil {
		switch action.Region.ID {
		case regionListPane, regionNoteItem:
			inListPane = true
		case regionEditorPane, regionEditorLine:
			inListPane = false
		default:
			return false
		}
	}
	delta := 3
	if action.Type == mouse.ActionScrollUp {
		delta = -3
	}
	if inListPane {
		return p.listBounds().AtBoundary(delta)
	}
	if p.editorNote == nil {
		// The placeholder pane absorbs the wheel without state of its own.
		return true
	}
	if !p.previewMode {
		// The textarea owns its viewport; do not guess.
		return false
	}
	if len(p.previewLines) == 0 {
		return true
	}
	return p.previewBounds().AtBoundary(delta)
}
