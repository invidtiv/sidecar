package layoutapply

import (
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/uirequest"
)

// LayoutAcks fills the per-pane ack items AFTER the whole batch has run.
func LayoutAcks(h Host, items []ItemPlan, surface string, committed bool) []uirequest.AckItem {
	cells := layoutCells(h.PaneRoot())
	out := make([]uirequest.AckItem, 0, len(items))
	for i, item := range items {
		ackItem := uirequest.AckItem{
			Index:   i,
			Verdict: item.Verdict,
			Surface: surface,
			Reason:  item.Reason,
		}
		if committed && item.Verdict != uirequest.ItemVerdictDeclined {
			if leafID := h.LandedLeaf(item.Kind); leafID != 0 {
				ackItem.Pane = leafID
				ackItem.Cell = cells[leafID]
				if ackItem.Cell == "" && ackItem.Reason == "" {
					ackItem.Reason = EscapedGridReason
				}
			}
		}
		out = append(out, ackItem)
	}
	return out
}

func layoutCells(root *panelayout.Node) map[int]string {
	cells := make(map[int]string)
	grid := panelayout.GridOf(root)
	if grid == nil {
		return cells
	}
	for col, column := range grid.Columns {
		for row, leaf := range column.Cells {
			cells[leaf.ID] = panelayout.Cell{Col: col + 1, Row: row + 1}.String()
		}
	}
	return cells
}

// LandedLeafID names where a batch's pane of this kind ended up in the final
// tree: the kind's own leaf for passives, the shell leaf for shells.
func LandedLeafID(root *panelayout.Node, kind panelayout.Kind) int {
	if kind == panelayout.Shell {
		if leaf := panelayout.FirstOfKind(root, panelayout.Shell); leaf != nil {
			return leaf.ID
		}
		return 0
	}
	if leaf := panelayout.FirstOfKind(root, kind); leaf != nil {
		return leaf.ID
	}
	return 0
}
