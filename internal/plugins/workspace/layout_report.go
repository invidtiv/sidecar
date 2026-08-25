package workspace

import (
	"encoding/json"

	"github.com/marcus/sidecar/internal/panelayout"
)

// The layout get report: one versioned JSON document describing the focused
// surface's pane tree. Grid is the columns-of-rows projection — null when the
// tree escapes that vocabulary, which stays valid, with the raw tree reported
// beside it. Caps and floors state the rules an apply will be held to, and
// every cell carries its box so an agent can reason about geometry without
// guessing at the frame's chrome.
const layoutReportVersion = 1

type layoutReport struct {
	Version  int              `json:"version"`
	Surface  string           `json:"surface,omitempty"`
	Root     string           `json:"root,omitempty"`
	Grid     *layoutGridJSON  `json:"grid"`
	Tree     json.RawMessage  `json:"tree,omitempty"`
	Viewport *layoutBox       `json:"viewport,omitempty"`
	Caps     layoutCapsJSON   `json:"caps"`
	Floors   layoutFloorsJSON `json:"floors"`
}

type layoutGridJSON struct {
	Columns []layoutColumnJSON `json:"columns"`
}

type layoutColumnJSON struct {
	Column int              `json:"column"`
	Panes  []layoutPaneJSON `json:"panes"`
}

type layoutPaneJSON struct {
	Cell string `json:"cell"`
	Kind string `json:"kind"`
	Pane int    `json:"pane"`
	// Provider names the configured instance behind a resource pane. It is
	// reported because the spec grammar REQUIRES it: without it a get answer
	// could be read but not spoken back, and "get → edit → apply is a round
	// trip without translation" would be false for exactly one kind.
	Provider string     `json:"provider,omitempty"`
	Session  string     `json:"session,omitempty"`
	Tabs     []string   `json:"tabs,omitempty"`
	Active   int        `json:"active,omitempty"`
	Box      *layoutBox `json:"box,omitempty"`
}

type layoutBox struct {
	X, Y, W, H int
}

type layoutCapsJSON struct {
	MaxColumns int `json:"maxColumns"`
	MaxRows    int `json:"maxRows"`
	LiveLeaves int `json:"liveLeaves"`
}

// layoutFloorsJSON reports the per-kind minimum OUTER box the frame enforces,
// keyed by the vocabulary's wire names. These are the real constraint behind
// every "needs a larger window" refusal.
type layoutFloorsJSON map[string]layoutFloorJSON

type layoutFloorJSON struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (p *Plugin) buildLayoutReport(root, surface string) json.RawMessage {
	report := layoutReport{
		Version: layoutReportVersion,
		Surface: surface,
		Root:    root,
		Caps: layoutCapsJSON{
			MaxColumns: panelayout.MaxGridColumns,
			MaxRows:    panelayout.MaxGridRows,
			LiveLeaves: panelayout.LiveLeafCap,
		},
		Floors: floorsReport(paneTreeFloors()),
	}
	if peer, placed := p.previewPeerBox(); placed {
		report.Viewport = &layoutBox{X: peer.X, Y: peer.Y, W: peer.W, H: peer.H}
	}
	boxes := p.liveLeafBoxes()
	report.Tree = p.layoutTreeJSON(root, surface)
	if grid := panelayout.GridOf(p.paneRoot); grid != nil {
		projected := &layoutGridJSON{Columns: make([]layoutColumnJSON, 0, grid.ColumnCount())}
		for col, column := range grid.Columns {
			projected.Columns = append(projected.Columns, layoutColumnJSON{
				Column: col + 1,
				Panes:  p.cellPanes(col+1, column.Cells, boxes),
			})
		}
		report.Grid = projected
	}
	out, err := json.Marshal(report)
	if err != nil {
		return nil
	}
	return out
}

// liveLeafBoxes is each leaf's tiled OUTER box for the current viewport, or
// nil when the tree does not fit (the zoomed case): a get answer then reports
// structure without pretending geometry it cannot draw.
func (p *Plugin) liveLeafBoxes() map[int]layoutBox {
	peer, placed := p.previewPeerBox()
	if !placed {
		return nil
	}
	leaves, _, fits := LayoutPanes(p.paneRoot, peer, paneTreeFloors())
	if !fits {
		return nil
	}
	boxes := make(map[int]layoutBox, len(leaves))
	for _, placement := range leaves {
		if placement.Node == nil {
			continue
		}
		boxes[placement.Node.ID] = layoutBox{
			X: placement.Box.X, Y: placement.Box.Y,
			W: placement.Box.W, H: placement.Box.H,
		}
	}
	return boxes
}

func (p *Plugin) cellPanes(col int, cells []*panelayout.Node, boxes map[int]layoutBox) []layoutPaneJSON {
	panes := make([]layoutPaneJSON, 0, len(cells))
	for row, leaf := range cells {
		cell := layoutPaneJSON{
			Cell: panelayout.Cell{Col: col, Row: row + 1}.String(),
			Kind: leaf.Kind.Name(),
			Pane: leaf.ID,
		}
		if leaf.Kind == panelayout.Shell {
			cell.Session = p.termPanelSession
		}
		cell.Tabs, cell.Active, cell.Provider = p.leafTabs(leaf)
		if box, ok := boxes[leaf.ID]; ok {
			boxCopy := box
			cell.Box = &boxCopy
		}
		panes = append(panes, cell)
	}
	return panes
}

// leafTabs reads one leaf's tabs, active index, and — for a resource pane —
// the provider instance its tabs belong to, from the live encoder, so a get
// answer and a relaunch restore can never disagree about what a pane holds.
// Provider is empty for every other kind, which is exactly what the spec
// grammar expects there.
func (p *Plugin) leafTabs(leaf *panelayout.Node) ([]string, int, string) {
	saved := p.encodePaneNode(leaf)
	if saved == nil {
		return nil, 0, ""
	}
	switch saved.Kind {
	case contentKindDoc:
		labels := make([]string, 0, len(saved.Tabs))
		for _, tab := range saved.Tabs {
			labels = append(labels, tab.Path)
		}
		return labels, saved.Active, ""
	case contentKindIssue:
		labels := make([]string, 0, len(saved.IssueTabs))
		for _, tab := range saved.IssueTabs {
			labels = append(labels, tab.Issue)
		}
		return labels, saved.Active, ""
	case contentKindNote:
		labels := make([]string, 0, len(saved.NoteTabs))
		for _, tab := range saved.NoteTabs {
			labels = append(labels, tab.Note)
		}
		return labels, saved.Active, ""
	case contentKindDiff:
		labels := make([]string, 0, len(saved.DiffTabs))
		for _, tab := range saved.DiffTabs {
			labels = append(labels, tab.Spec)
		}
		return labels, saved.Active, ""
	case contentKindResource:
		labels := make([]string, 0, len(saved.ResourceTabs))
		provider := ""
		for _, tab := range saved.ResourceTabs {
			labels = append(labels, tab.Locator)
			// One leaf holds one provider's tabs; the active tab names it, and
			// the first is the fallback for a pane whose active index drifted.
			if provider == "" {
				provider = tab.Provider
			}
		}
		if saved.Active >= 0 && saved.Active < len(saved.ResourceTabs) {
			if active := saved.ResourceTabs[saved.Active].Provider; active != "" {
				provider = active
			}
		}
		return labels, saved.Active, provider
	default:
		return nil, 0, ""
	}
}

// layoutTreeJSON is the raw persisted shape of the live tree, Root/Surface/
// Open filled exactly as a stored layout carries them. This is what answers
// when the grid projection is null, and what M4's --spec will consume.
func (p *Plugin) layoutTreeJSON(root, surface string) json.RawMessage {
	saved := p.encodePaneNode(p.paneRoot)
	if saved == nil {
		return nil
	}
	saved.Root = root
	saved.Surface = surface
	saved.Open = true
	out, err := json.Marshal(saved)
	if err != nil {
		return nil
	}
	return out
}

func floorsReport(floors Floors) layoutFloorsJSON {
	out := layoutFloorsJSON{}
	for kind, name := range map[panelayout.Kind]string{
		panelayout.Primary:  panelayout.KindNamePrimary,
		panelayout.Document: panelayout.KindNameFile,
		panelayout.Issue:    panelayout.KindNameIssue,
		panelayout.Diff:     panelayout.KindNameDiff,
		panelayout.Resource: panelayout.KindNameResource,
		panelayout.Shell:    panelayout.KindNameShell,
		panelayout.Note:     panelayout.KindNameNote,
	} {
		floor := floorForKind(floors, kind)
		out[name] = layoutFloorJSON{Width: floor.Width, Height: floor.Height}
	}
	return out
}

func floorForKind(floors Floors, kind panelayout.Kind) PaneFloor {
	switch kind {
	case panelayout.Document:
		return floors.Doc
	case panelayout.Issue:
		return floors.Issue
	case panelayout.Diff:
		return floors.Diff
	case panelayout.Resource:
		return floors.Resource
	case panelayout.Shell:
		return floors.Shell
	case panelayout.Note:
		return floors.Note
	default:
		if floors.Primary != (PaneFloor{}) {
			return floors.Primary
		}
		return floors.Terminal
	}
}
