package panelayout

import "fmt"

// The layout vocabulary is columns of stacked panes: a layout is 1..
// MaxGridColumns columns and each column stacks 1..MaxGridRows panes. Cells
// are addressed col.row, both 1-based, left to right then top to bottom, so
// "2.1" is the second column's top pane. This is a vocabulary over the binary
// tree, not a second structure: GridOf projects a tree onto it and the plan
// functions compile placements back into ordinary splits.
const (
	// MaxGridColumns bounds how many side-by-side columns one layout may span.
	// It is a sanity bound for the vocabulary, not the real constraint — the
	// per-kind floors refuse most large grids on ordinary terminals first.
	MaxGridColumns = 4
	// MaxGridRows bounds how many panes one column may stack.
	MaxGridRows = 4
)

// The caps are rules a caller reports, not clicks that quietly do nothing:
// these are the refusal strings for each cap, worded to stand alone in a toast
// or an agent-facing ack. LiveCapMessage is the planner-level twin of the
// workspace's shell-cap toast; both say the same rule so neither can drift
// into promising a fifth live terminal.
const (
	GridColumnCapMessage = "four columns of panes at a time; close one first"
	GridRowCapMessage    = "four panes in a column at a time; close one first"
	LiveCapMessage       = "two live terminals at a time; close one first"
)

// GridColumn is one column of the projected layout: Node spans the whole
// column (splitting it on Rows appends below), Cells are its leaves top to
// bottom (the cells a col.row address resolves against).
type GridColumn struct {
	Node  *Node
	Cells []*Node
}

// Grid is a tree that resolves to the columns-of-rows vocabulary.
type Grid struct {
	Columns []GridColumn
	root    *Node
}

// Root is the tree the projection came from; splitting it on Columns appends
// a column.
func (g *Grid) Root() *Node { return g.root }

// ColumnCount is how many columns the layout spans.
func (g *Grid) ColumnCount() int { return len(g.Columns) }

// RowCount is how many panes column col (1-based) stacks, or 0 when there is
// no such column.
func (g *Grid) RowCount(col int) int {
	if g == nil || col < 1 || col > len(g.Columns) {
		return 0
	}
	return len(g.Columns[col-1].Cells)
}

// Cell names the leaf occupying col.row (1-based), or nil when no pane is
// there.
func (g *Grid) Cell(col, row int) *Node {
	if g == nil || col < 1 || col > len(g.Columns) {
		return nil
	}
	cells := g.Columns[col-1].Cells
	if row < 1 || row > len(cells) {
		return nil
	}
	return cells[row-1]
}

// ColumnsAtCap reports that another column cannot open beside this layout.
func (g *Grid) ColumnsAtCap() bool { return g != nil && g.ColumnCount() >= MaxGridColumns }

// RowsAtCap reports that column col (1-based) cannot take another row.
func (g *Grid) RowsAtCap(col int) bool { return g.RowCount(col) >= MaxGridRows }

// GridColumnsAtCap reports that the tree's layout already spans every column
// the vocabulary allows. It reads the projection, exactly as LiveCapReached
// reads the leaf walk.
func GridColumnsAtCap(root *Node) bool { return GridOf(root).ColumnsAtCap() }

// GridRowAtCap reports that column col of the tree's layout is full.
func GridRowAtCap(root *Node, col int) bool { return GridOf(root).RowsAtCap(col) }

// GridOf projects a tree onto the columns-of-rows vocabulary. Same-axis
// nesting flattens: a three-pane column is Rows splits chained inside Rows,
// and columns chain inside Columns. A tree that alternates axes below the
// column/row seam — a Columns split nested inside a column's row stack —
// escapes the vocabulary and returns nil; such a tree is still valid, it just
// has no grid answer.
func GridOf(root *Node) *Grid {
	columns, ok := gridColumns(root)
	if !ok {
		return nil
	}
	return &Grid{Columns: columns, root: root}
}

// gridColumns projects node as a sequence of one or more whole columns.
func gridColumns(node *Node) ([]GridColumn, bool) {
	if node == nil {
		return nil, false
	}
	if node.Split == nil {
		return []GridColumn{{Node: node, Cells: []*Node{node}}}, true
	}
	switch node.Split.Axis {
	case Columns:
		a, ok := gridColumns(node.Split.A)
		if !ok {
			return nil, false
		}
		b, ok := gridColumns(node.Split.B)
		if !ok {
			return nil, false
		}
		return append(a, b...), true
	case Rows:
		cells, ok := gridColumnCells(node)
		if !ok {
			return nil, false
		}
		return []GridColumn{{Node: node, Cells: cells}}, true
	default:
		return nil, false
	}
}

// gridColumnCells projects node as the stack of panes inside ONE column: a
// leaf, or Rows splits all the way down. Any Columns split below this point
// would put a side-by-side pair inside a single stack, which is exactly the
// shape the vocabulary does not have a name for.
func gridColumnCells(node *Node) ([]*Node, bool) {
	if node == nil {
		return nil, false
	}
	if node.Split == nil {
		return []*Node{node}, true
	}
	if node.Split.Axis != Rows {
		return nil, false
	}
	a, ok := gridColumnCells(node.Split.A)
	if !ok {
		return nil, false
	}
	b, ok := gridColumnCells(node.Split.B)
	if !ok {
		return nil, false
	}
	return append(a, b...), true
}

// Cell addresses one pane of the grid: Col 1-based left to right, Row 1-based
// top to bottom.
type Cell struct {
	Col int
	Row int
}

// PlanOpenAt plans an open at an explicit cell, the planner entry behind the
// CLI's --at. The cell is a requirement, not a preference: a kind whose open
// would retarget an existing leaf is refused rather than placed elsewhere,
// because landing the pane somewhere else is precisely what --at must not do.
//
// Resolution against the current grid:
//
//   - An occupied cell inserts at that position: the new pane takes the cell
//     and the occupant — with everything already below it — shifts down one
//     row (a Rows split on the occupant, new pane first).
//   - One row past the column's end appends at the bottom of that column.
//   - One column past the end (row must be 1) appends a new column.
//   - Anything further out, a column past MaxGridRows, or a tree that escapes
//     the grid vocabulary is refused with the reason to show.
//
// The returned string is empty when a plan was made; otherwise it is the
// visible reason, ready for a toast or an ack.
func PlanOpenAt(root *Node, kind Kind, contentID int, cell Cell) (OpenPlan, string) {
	if kind == Primary {
		return OpenPlan{}, "the primary pane is the host's own content and cannot be opened at a cell"
	}
	if cell.Col < 1 || cell.Row < 1 || cell.Col > MaxGridColumns || cell.Row > MaxGridRows {
		return OpenPlan{}, fmt.Sprintf("cell %d.%d is outside the %dx%d layout grid",
			cell.Col, cell.Row, MaxGridColumns, MaxGridRows)
	}
	if leaf := FirstOfContent(root, kind, contentID); leaf != nil {
		return OpenPlan{}, "that content already has a pane on screen; an explicit cell cannot retarget it"
	}
	if Duplicable(kind) {
		if IsLive(kind) && LiveCapReached(root) {
			return OpenPlan{}, LiveCapMessage
		}
	} else if FirstOfKind(root, kind) != nil {
		return OpenPlan{}, "that kind already has a pane on screen; an explicit cell cannot retarget it"
	}
	grid := GridOf(root)
	if grid == nil {
		return OpenPlan{}, "the current layout does not resolve to grid columns, so no cell can be addressed"
	}
	switch {
	case cell.Col > grid.ColumnCount():
		if cell.Col == grid.ColumnCount()+1 && cell.Row == 1 {
			return OpenPlan{Split: root.ID, Axis: Columns}, ""
		}
		return OpenPlan{}, fmt.Sprintf("column %d is out of range; the layout has %d", cell.Col, grid.ColumnCount())
	case grid.RowsAtCap(cell.Col):
		// Occupied-cell inserts grow the column by one row, so a full column
		// overflows no matter which occupied row was named.
		return OpenPlan{}, GridRowCapMessage
	default:
		column := grid.Columns[cell.Col-1]
		if cell.Row <= len(column.Cells) {
			occupant := column.Cells[cell.Row-1]
			return OpenPlan{Split: occupant.ID, Axis: Rows, NewFirst: true}, ""
		}
		if cell.Row > len(column.Cells)+1 {
			return OpenPlan{}, fmt.Sprintf("cell %d.%d is out of range; column %d holds %d",
				cell.Col, cell.Row, cell.Col, len(column.Cells))
		}
		return OpenPlan{Split: column.Node.ID, Axis: Rows}, ""
	}
}
