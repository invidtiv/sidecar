// Package layoutreport owns the versioned JSON projection `sidecar layout get`
// returns: grid, kinds, targets, sessions, caps, and floors. Both the project
// workspace and the global Sessions surface build this document from a pane
// tree so the wire shape cannot drift.
package layoutreport

import (
	"encoding/json"

	"github.com/marcus/sidecar/internal/panecodec"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
)

const Version = 1

// Report is one versioned JSON document describing a surface's pane tree.
type Report struct {
	Version  int             `json:"version"`
	Surface  string          `json:"surface,omitempty"`
	Root     string          `json:"root,omitempty"`
	Grid     *Grid           `json:"grid"`
	Tree     json.RawMessage `json:"tree,omitempty"`
	Viewport *Box            `json:"viewport,omitempty"`
	Caps     Caps            `json:"caps"`
	Floors   Floors          `json:"floors"`
}

type Grid struct {
	Columns []Column `json:"columns"`
}

type Column struct {
	Column int    `json:"column"`
	Panes  []Pane `json:"panes"`
}

type Pane struct {
	Cell     string `json:"cell"`
	Kind     string `json:"kind"`
	Pane     int    `json:"pane"`
	Provider string `json:"provider,omitempty"`
	// Collection and Query are the active tab's plugin collection, when the
	// Resource pane is showing one. They round-trip: a get → edit → apply of
	// this report reopens the same list, searched the same way.
	Collection string   `json:"collection,omitempty"`
	Query      string   `json:"query,omitempty"`
	Session    string   `json:"session,omitempty"`
	Tabs       []string `json:"tabs,omitempty"`
	Active     int      `json:"active,omitempty"`
	Box        *Box     `json:"box,omitempty"`
}

type Box struct {
	X, Y, W, H int
}

type Caps struct {
	MaxColumns int `json:"maxColumns"`
	MaxRows    int `json:"maxRows"`
	LiveLeaves int `json:"liveLeaves"`
}

// Floors reports the per-kind minimum OUTER box the frame enforces, keyed by
// the vocabulary's wire names.
type Floors map[string]Floor

type Floor struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Source is everything a host supplies to project a get answer. Layout is the
// panecodec encoding of Tree (tabs, session, raw tree); Boxes are live outer
// geometry keyed by leaf id, omitted when the tree does not fit.
type Source struct {
	Surface  string
	Root     string
	Tree     *panelayout.Node
	Viewport *panelayout.Box
	Floors   panelayout.Floors
	Layout   *state.PaneLayoutJSON
	Boxes    map[int]panelayout.Box
}

// Build projects Source onto the get-report JSON document. A tree that
// escapes the grid vocabulary reports "grid": null plus the raw tree.
func Build(src Source) json.RawMessage {
	report := Report{
		Version: Version,
		Surface: src.Surface,
		Root:    src.Root,
		Caps: Caps{
			MaxColumns: panelayout.MaxGridColumns,
			MaxRows:    panelayout.MaxGridRows,
			LiveLeaves: panelayout.LiveLeafCap,
		},
		Floors: FloorsJSON(src.Floors),
	}
	if src.Viewport != nil {
		report.Viewport = &Box{X: src.Viewport.X, Y: src.Viewport.Y, W: src.Viewport.W, H: src.Viewport.H}
	}
	report.Tree = treeJSON(src.Layout, src.Root, src.Surface)
	if grid := panelayout.GridOf(src.Tree); grid != nil {
		projected := &Grid{Columns: make([]Column, 0, grid.ColumnCount())}
		for col, column := range grid.Columns {
			projected.Columns = append(projected.Columns, Column{
				Column: col + 1,
				Panes:  cellPanes(col+1, column.Cells, src.Layout, src.Boxes),
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

func cellPanes(col int, cells []*panelayout.Node, layout *state.PaneLayoutJSON, boxes map[int]panelayout.Box) []Pane {
	panes := make([]Pane, 0, len(cells))
	for row, leaf := range cells {
		cell := Pane{
			Cell: panelayout.Cell{Col: col, Row: row + 1}.String(),
			Kind: leaf.Kind.Name(),
			Pane: leaf.ID,
		}
		cell.Tabs, cell.Active, cell.Provider, cell.Collection, cell.Query, cell.Session = leafInfo(layout, leaf.Kind)
		if box, ok := boxes[leaf.ID]; ok {
			cell.Box = &Box{X: box.X, Y: box.Y, W: box.W, H: box.H}
		}
		panes = append(panes, cell)
	}
	return panes
}

// leafInfo is one leaf's tab labels plus the plugin identity the Resource kind
// carries. collection and query are the ACTIVE tab's, and empty for every other
// kind, so a get → edit → apply round trip reopens the list the user was on.
func leafInfo(layout *state.PaneLayoutJSON, kind panelayout.Kind) (tabs []string, active int, provider, collection, query, session string) {
	saved := firstLayoutLeaf(layout, persistKind(kind))
	if saved == nil {
		return nil, 0, "", "", "", ""
	}
	session = saved.Session
	switch saved.Kind {
	case panecodec.KindDoc:
		tabs = make([]string, 0, len(saved.Tabs))
		for _, tab := range saved.Tabs {
			tabs = append(tabs, tab.Path)
		}
		return tabs, saved.Active, "", "", "", session
	case panecodec.KindIssue:
		tabs = make([]string, 0, len(saved.IssueTabs))
		for _, tab := range saved.IssueTabs {
			tabs = append(tabs, tab.Issue)
		}
		return tabs, saved.Active, "", "", "", session
	case panecodec.KindNote:
		tabs = make([]string, 0, len(saved.NoteTabs))
		for _, tab := range saved.NoteTabs {
			tabs = append(tabs, tab.Note)
		}
		return tabs, saved.Active, "", "", "", session
	case panecodec.KindDiff:
		tabs = make([]string, 0, len(saved.DiffTabs))
		for _, tab := range saved.DiffTabs {
			tabs = append(tabs, tab.Spec)
		}
		return tabs, saved.Active, "", "", "", session
	case panecodec.KindResource:
		tabs = make([]string, 0, len(saved.ResourceTabs))
		for _, tab := range saved.ResourceTabs {
			// A collection tab has no locator; naming it by its empty one would
			// print a hole in the strip. Its collection is what it is.
			label := tab.Locator
			if label == "" {
				label = tab.Collection
			}
			tabs = append(tabs, label)
			if provider == "" {
				provider = tab.Provider
			}
		}
		if saved.Active >= 0 && saved.Active < len(saved.ResourceTabs) {
			active := saved.ResourceTabs[saved.Active]
			if active.Provider != "" {
				provider = active.Provider
			}
			collection, query = active.Collection, active.Query
		}
		return tabs, saved.Active, provider, collection, query, session
	default:
		return nil, 0, "", "", "", session
	}
}

func persistKind(kind panelayout.Kind) string {
	switch kind {
	case panelayout.Primary:
		return panecodec.KindTerminal
	case panelayout.Document:
		return panecodec.KindDoc
	case panelayout.Issue:
		return panecodec.KindIssue
	case panelayout.Note:
		return panecodec.KindNote
	case panelayout.Diff:
		return panecodec.KindDiff
	case panelayout.Resource:
		return panecodec.KindResource
	case panelayout.Shell:
		return panecodec.KindShell
	default:
		return ""
	}
}

func firstLayoutLeaf(n *state.PaneLayoutJSON, kind string) *state.PaneLayoutJSON {
	if n == nil || kind == "" {
		return nil
	}
	if n.Split != nil {
		if found := firstLayoutLeaf(n.Split.A, kind); found != nil {
			return found
		}
		return firstLayoutLeaf(n.Split.B, kind)
	}
	if n.Kind == kind {
		return n
	}
	return nil
}

func treeJSON(layout *state.PaneLayoutJSON, root, surface string) json.RawMessage {
	if layout == nil {
		return nil
	}
	layout.Root = root
	layout.Surface = surface
	layout.Open = true
	out, err := json.Marshal(layout)
	if err != nil {
		return nil
	}
	return out
}

// FloorsJSON projects panelayout.Floors onto the get-report's keyed object.
func FloorsJSON(floors panelayout.Floors) Floors {
	out := Floors{}
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
		out[name] = Floor{Width: floor.Width, Height: floor.Height}
	}
	return out
}

func floorForKind(floors panelayout.Floors, kind panelayout.Kind) panelayout.Floor {
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
		if floors.Primary != (panelayout.Floor{}) {
			return floors.Primary
		}
		return floors.Terminal
	}
}

// LiveBoxes is each leaf's tiled OUTER box for the current viewport, or nil
// when the tree does not fit: a get answer then reports structure without
// pretending geometry it cannot draw.
func LiveBoxes(root *panelayout.Node, peer panelayout.Box, floors panelayout.Floors) map[int]panelayout.Box {
	leaves, _, fits := panelayout.LayoutPanes(root, peer, floors)
	if !fits {
		return nil
	}
	boxes := make(map[int]panelayout.Box, len(leaves))
	for _, placement := range leaves {
		if placement.Node == nil {
			continue
		}
		boxes[placement.Node.ID] = placement.Box
	}
	return boxes
}
