package workspace

import (
	tea "charm.land/bubbletea/v2"

	app "github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/textselect"
)

// Text selection in this surface's document panes.
//
// Everything about what a gesture means belongs to the viewer's own binding
// (internal/docview/select.go), which the global Workspaces browser binds to as
// well. What is left here is surface-local routing: where a leaf was drawn, and
// which of this plugin's mouse and key events reach it. A rule added to this
// file rather than to docview is a rule the other surface will not have.

// bindDocSelection tells a viewer where it is on screen and which chords it
// answers. It runs from the leaf's render, which is the only place that knows
// both, and the only place that runs for exactly the panes that are drawn.
func (p *Plugin) bindDocSelection(view *docview.Model, origin Box) {
	if view == nil {
		return
	}
	config := p.terminalConfig()
	view.SetSelection(config.SelectionKeys(), config.CopyOnSelect)
	// The body sits below the leaf's own header row, the same subtraction
	// SetSize makes for the viewport it hands the viewer.
	view.SetOrigin(origin.X, origin.Y+terminalHeaderRows)
}

// docSelectionView is the viewer a document leaf is showing, or nil for a leaf
// that is not a document or has nothing in it.
func (p *Plugin) docSelectionView(leafID int) *docview.Model {
	leaf := FindPane(p.paneRoot, leafID)
	if leaf == nil || leaf.Split != nil || leaf.Kind != PaneDoc {
		return nil
	}
	doc := p.docs[leaf.ContentID]
	if doc == nil {
		return nil
	}
	return doc.view()
}

// clearDocSelectionsExcept keeps one selection alive at a time, the way a
// native app does: starting one anywhere drops the one before it.
func (p *Plugin) clearDocSelectionsExcept(view *docview.Model) {
	for _, doc := range p.docs {
		if doc == nil {
			continue
		}
		for _, item := range doc.tabs.Items {
			if item.View != nil && item.View != view {
				item.View.ClearSelection()
			}
		}
	}
}

// pressDocSelection arms a selection gesture in a document leaf.
//
// Focus has already followed the press, and a press that resolves to a click
// still does exactly what it did before — this only decides what the motion
// after it means. The drag is registered with the shared handler because that
// is what turns the release into a drag end the gesture can be finished by.
func (p *Plugin) pressDocSelection(leafID int, action mouse.MouseAction) tea.Cmd {
	view := p.docSelectionView(leafID)
	if view == nil {
		return nil
	}
	p.clearDocSelectionsExcept(view)
	result := view.HandleSelectionMouse(action)
	if !result.Handled {
		// The press landed on the header, the gutter or the padding: not a
		// selection, and not this file's business.
		return nil
	}
	p.mouseHandler.StartDrag(action.X, action.Y, regionPaneLeaf, leafID)
	p.docSelectLeaf = leafID
	return p.docSelectionResult(view, result)
}

// dragDocSelection extends the gesture the press armed. The pointer routinely
// leaves the pane mid-drag; the leaf it started in answers anyway.
func (p *Plugin) dragDocSelection(action mouse.MouseAction) tea.Cmd {
	view := p.docSelectionView(p.docSelectLeaf)
	if view == nil {
		return nil
	}
	return p.docSelectionResult(view, view.HandleSelectionMouse(action))
}

// finishDocSelection resolves the release: a copy under copy-on-select, or a
// click that was never a drag and has already had its effect.
func (p *Plugin) finishDocSelection(action mouse.MouseAction) tea.Cmd {
	view := p.docSelectionView(p.docSelectLeaf)
	p.docSelectLeaf = 0
	p.lastDragRegion = ""
	if view == nil {
		return nil
	}
	return p.docSelectionResult(view, view.HandleSelectionMouse(action))
}

// abandonDocSelection ends a gesture whose release was lost off-window.
func (p *Plugin) abandonDocSelection() {
	if view := p.docSelectionView(p.docSelectLeaf); view != nil {
		view.AbandonSelection()
	}
	p.docSelectLeaf = 0
}

// handleDocSelectionKey answers the chords that act on a document's selection.
// Escape clears it, which is why it is asked before the pane's own esc.
func (p *Plugin) handleDocSelectionKey(view *docview.Model, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if view == nil {
		return nil, false
	}
	if msg.String() == "esc" && view.HasSelection() {
		view.ClearSelection()
		return nil, true
	}
	result := view.HandleSelectionKey(msg)
	if !result.Handled {
		return nil, false
	}
	return p.docSelectionResult(view, result), true
}

// docSelectionResult is what this surface owes the engine's answer: a copy,
// phrased and delivered as this plugin's own toast.
func (p *Plugin) docSelectionResult(view *docview.Model, result textselect.Result) tea.Cmd {
	if !result.CopyAsked {
		return nil
	}
	return view.SelectionKeys().CopySelectionCmd(result.Copy, func(notice textselect.CopyNotice) tea.Msg {
		return app.ToastMsg{
			Message: notice.Message, Duration: notice.Duration, IsError: notice.IsError,
		}
	})
}
