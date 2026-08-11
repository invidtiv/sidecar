package workspace

// paneGeometry is the tmux geometry last observed for a pane. It is what tmux
// reported, not what sidecar asked for: another sidecar instance can be driving
// the same session from a differently sized terminal, so the requested size is
// only ever a request (td-73fa86).
type paneGeometry struct {
	Width  int
	Height int
	// AltScreen rides along with the geometry because it shares the same source
	// and the same key: it is read by preview panes, which never populate
	// interactiveState, so it cannot live there.
	AltScreen bool
}

func (g paneGeometry) known() bool { return g.Width > 0 && g.Height > 0 }

// recordPaneGeometry stores the geometry reported by a capture. The key space is
// the one terminal history already uses ("agent"/"shell"/"panel" + target), so a
// pane has one identity across both caches.
func (p *Plugin) recordPaneGeometry(kind, target string, width, height int, altScreen bool) {
	if target == "" || width <= 0 || height <= 0 {
		return
	}
	if p.paneGeometry == nil {
		p.paneGeometry = make(map[string]paneGeometry)
	}
	p.paneGeometry[terminalHistoryKey(kind, target)] = paneGeometry{
		Width: width, Height: height, AltScreen: altScreen,
	}
}

// paneGeometryFor returns the observed geometry of the pane currently rendered
// into the given viewport, or the zero value when nothing has been observed yet.
func (p *Plugin) paneGeometryFor(termPanel bool) paneGeometry {
	if p.paneGeometry == nil {
		return paneGeometry{}
	}
	source, ok := p.terminalHistoryFor(termPanel)
	if !ok {
		return paneGeometry{}
	}
	return p.paneGeometry[source.Key]
}
