package workspace

import "github.com/marcus/sidecar/internal/panelayout"

const (
	paneMinRatio = 15
	paneMaxRatio = 85
)

type PaneKind = panelayout.Kind

const (
	PaneTerminal = panelayout.Terminal
	PaneDoc      = panelayout.Document
	PaneIssue    = panelayout.Issue
	PaneDiff     = panelayout.Diff
	PaneResource = panelayout.Resource
	// PaneShell is a live terminal peer of the primary terminal. The terminal
	// panel is the first one: see shell_leaf.go.
	PaneShell = panelayout.Shell
)

type SplitAxis = panelayout.Axis

const (
	SplitCols = panelayout.Columns
	SplitRows = panelayout.Rows
)

type PaneNode = panelayout.Node
type PaneSplit = panelayout.Split
type Box = panelayout.Box
type Placement = panelayout.Placement
type Divider = panelayout.Divider
type PaneFloor = panelayout.Floor
type Floors = panelayout.Floors
type PaneLayout = panelayout.Layout

func LayoutPanes(root *PaneNode, box Box, floors Floors) ([]Placement, []Divider, bool) {
	return panelayout.LayoutPanes(root, box, floors)
}

func LayoutPaneTree(root *PaneNode, box Box, floors Floors, focus int) (PaneLayout, bool) {
	return panelayout.LayoutTree(root, box, floors, focus)
}

func SplitLeaf(root *PaneNode, leafID int, axis SplitAxis, leaf *PaneNode) (*PaneNode, int) {
	return panelayout.SplitLeaf(root, leafID, axis, leaf)
}

func ClosePane(root *PaneNode, leafID int) (*PaneNode, int) { return panelayout.Close(root, leafID) }
func FindPane(root *PaneNode, id int) *PaneNode             { return panelayout.Find(root, id) }
func SetRatio(root *PaneNode, id, ratio int) bool           { return panelayout.SetRatio(root, id, ratio) }
func maxPaneID(root *PaneNode) int                          { return panelayout.MaxID(root) }
func clampPaneRatio(ratio int) int                          { return panelayout.ClampRatio(ratio) }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
