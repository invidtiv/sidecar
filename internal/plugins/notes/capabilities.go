package notes

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
)

const (
	notesListFocusID = "list"
	notesBodyFocusID = "note"
)

var (
	_ plugin.PaneFocusProvider   = (*Plugin)(nil)
	_ plugin.ContentLinkProvider = (*Plugin)(nil)
)

func (p *Plugin) PaneFocusStops() []plugin.PaneFocusStop {
	stops := []plugin.PaneFocusStop{{ID: notesListFocusID}}
	if p.editorPaneOnScreen() {
		stops = append(stops, plugin.PaneFocusStop{ID: notesBodyFocusID})
	}
	return stops
}

func (p *Plugin) PaneFocus() string {
	if p.activePane == PaneEditor && p.editorPaneOnScreen() {
		return notesBodyFocusID
	}
	return notesListFocusID
}

func (p *Plugin) SetPaneFocus(id string) tea.Cmd {
	switch id {
	case notesListFocusID:
		p.activePane = PaneList
	case notesBodyFocusID:
		if p.editorPaneOnScreen() {
			p.focusEditorPane()
		}
	}
	return nil
}

func (p *Plugin) SetPaneFocusActive(active bool) {
	p.paneFocusManaged = true
	p.paneFocusActive = active
}

func (p *Plugin) innerPaneFocusActive() bool {
	return !p.paneFocusManaged || p.paneFocusActive
}

func (p *Plugin) ContentLinkSurfaces() []contentlink.Surface {
	if !p.contentLinksSafe() {
		return nil
	}
	p.ensureViewSurface()
	layout := p.editorLayout()
	start := p.previewScrollOff
	if start < 0 {
		start = 0
	}
	rows := len(p.viewSurface.Lines) - start
	if rows > layout.contentHeight {
		rows = layout.contentHeight
	}
	if rows <= 0 || layout.wrapColumn <= 0 {
		return nil
	}
	return []contentlink.Surface{{
		ID:          notesBodyFocusID,
		Rect:        mouse.Rect{X: p.listWidth + dividerWidth + 2 + layout.leftMargin, Y: 1 + layout.contentRow, W: layout.wrapColumn, H: rows},
		WorkDir:     p.ctx.WorkDir,
		ProjectRoot: p.ctx.ProjectRoot,
		Kinds: contentlink.NewKindSet(
			contentlink.KindFile,
			contentlink.KindIssue,
			contentlink.KindDiff,
			contentlink.KindResource,
			contentlink.KindURL,
			contentlink.KindInternal,
		),
		ReadOnly: true,
	}}
}

func (p *Plugin) contentLinksSafe() bool {
	if p.ctx == nil || p.store == nil || p.width <= 0 || p.height <= 0 || p.listWidth <= 0 ||
		p.loading || p.loadErr != nil || p.editorNote == nil || !p.previewMode {
		return false
	}
	return !p.searchMode && !p.noteSearchMode && !p.showSetupModal && !p.showTaskModal && !p.showDeleteModal && !p.showInfoModal &&
		!p.edit.Active && !p.edit.ShowExitConfirm
}
