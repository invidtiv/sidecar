package tasks

import (
	tea "charm.land/bubbletea/v2"
	tasksui "github.com/marcus/tasks/pkg/tui"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/plugin"
)

var (
	_ plugin.PaneFocusProvider   = (*Plugin)(nil)
	_ plugin.ContentLinkProvider = (*Plugin)(nil)
)

// PaneFocusStops projects Tasks' visible list/detail panes in rendered order.
// Prompt input and overlays own Tab, so the outer ring disappears while they
// are active instead of replaying or double-handling the key.
func (p *Plugin) PaneFocusStops() []plugin.PaneFocusStop {
	if p.model == nil || p.model.TabOwnsFocus() {
		return nil
	}
	upstream := p.model.VisibleSpatialFocusStops()
	stops := make([]plugin.PaneFocusStop, 0, len(upstream))
	for _, stop := range upstream {
		stops = append(stops, plugin.PaneFocusStop{ID: string(stop.ID)})
	}
	return stops
}

func (p *Plugin) PaneFocus() string {
	if p.model == nil {
		return ""
	}
	return string(p.model.CurrentSpatialFocus())
}

// SetPaneFocus delegates to Tasks' direct focus contract. Unknown, hidden, or
// input-owned stops are refused by the embedded model without mutation.
func (p *Plugin) SetPaneFocus(id string) tea.Cmd {
	if p.model != nil && !p.model.TabOwnsFocus() {
		p.model.SetSpatialFocus(tasksui.SpatialFocus(id))
	}
	return nil
}

func (p *Plugin) SetPaneFocusActive(active bool) {
	p.paneFocusManaged = true
	p.paneFocusActive = active
}

// ContentLinkSurfaces stays empty until Tasks exports exact passive text
// rectangles. Its current spatial rectangles include rows and prompt/detail
// controls, so whole-frame scanning is intentionally unsafe.
func (p *Plugin) ContentLinkSurfaces() []contentlink.Surface { return nil }
