package tdmonitor

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/td/pkg/monitor"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/plugin"
)

var (
	_ plugin.PaneFocusProvider   = (*Plugin)(nil)
	_ plugin.ContentLinkProvider = (*Plugin)(nil)
)

// PaneFocusStops projects td's currently rendered root panels into the app
// focus ring. Inputs, overlays, and replacement views retain Tab by exposing
// no outer stops.
func (p *Plugin) PaneFocusStops() []plugin.PaneFocusStop {
	if p.model == nil || p.model.TabOwnsFocus() {
		return nil
	}
	upstream := p.model.VisibleFocusStops()
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
	return string(p.model.CurrentFocusStop())
}

// SetPaneFocus uses td's direct setter so cursor clamping and scroll
// normalization remain owned by the embedded model. No Tab is replayed.
func (p *Plugin) SetPaneFocus(id string) tea.Cmd {
	if p.model != nil && !p.model.TabOwnsFocus() {
		p.model.SetFocusStop(monitor.FocusStopID(id))
	}
	return nil
}

// SetPaneFocusActive mutes only td's selected-panel border while an outer leaf
// owns focus. The selected panel, cursor, scroll, and rows remain untouched.
func (p *Plugin) SetPaneFocusActive(active bool) {
	changed := !p.paneFocusManaged || p.paneFocusActive != active
	p.paneFocusManaged = true
	p.paneFocusActive = active
	if changed {
		p.applyPaneFocusTheme()
	}
}

func (p *Plugin) applyPaneFocusTheme() {
	if p.model == nil {
		return
	}
	theme := buildTheme()
	if p.paneFocusManaged && !p.paneFocusActive {
		theme.BorderActive = theme.Border
	}
	_ = p.model.SetTheme(theme)
}

// ContentLinkSurfaces stays empty until td exports exact passive text
// rectangles. Its current panel bounds contain actionable rows and controls;
// scanning them would turn mutation targets into content links.
func (p *Plugin) ContentLinkSurfaces() []contentlink.Surface { return nil }
