package workspace

import (
	"encoding/json"

	"github.com/marcus/sidecar/internal/layoutreport"
	"github.com/marcus/sidecar/internal/panelayout"
)

func (p *Plugin) buildLayoutReport(root, surface string) json.RawMessage {
	var viewport *panelayout.Box
	if peer, placed := p.previewPeerBox(); placed {
		box := panelayout.Box(peer)
		viewport = &box
	}
	layout := p.paneLayoutJSON(p.paneRoot)
	if layout != nil {
		layout.Root = root
		layout.Surface = surface
		layout.Open = true
	}
	return layoutreport.Build(layoutreport.Source{
		Surface:  surface,
		Root:     root,
		Tree:     p.paneRoot,
		Viewport: viewport,
		Floors:   paneTreeFloors(),
		Layout:   layout,
		Boxes:    p.liveLeafBoxes(),
	})
}

func (p *Plugin) liveLeafBoxes() map[int]panelayout.Box {
	peer, placed := p.previewPeerBox()
	if !placed {
		return nil
	}
	return layoutreport.LiveBoxes(p.paneRoot, peer, paneTreeFloors())
}
