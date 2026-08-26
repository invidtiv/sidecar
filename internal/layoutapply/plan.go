package layoutapply

import (
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/panelayout"
)

// PlanShellItem plans the batch's shell pane against the SCREEN trial: shells
// split the pane tree directly, never the deck's.
func PlanShellItem(h Host, trial *panelayout.Node, item ItemPlan) (panelayout.OpenPlan, string) {
	if !h.TerminalEnabled() {
		return panelayout.OpenPlan{}, h.TerminalOffReason()
	}
	if h.ShellVisible() || panelayout.FirstOfKind(trial, panelayout.Shell) != nil {
		return panelayout.OpenPlan{}, h.ShellCapMessage()
	}
	if item.Cell.Col != 0 {
		return panelayout.PlanOpenAt(trial, item.Kind, 0, item.Cell)
	}
	plan, ok := panelayout.PlanOpenContent(trial, item.Kind, 0, h.LastBoxes())
	if !ok {
		return panelayout.OpenPlan{}, panelayout.LiveCapMessage
	}
	return plan, ""
}

// PlanPassiveItem plans one content pane. Addressing and refusals are stated
// against the SCREEN grid; the plan itself is resolved against deckTrial.
func PlanPassiveItem(screen, deckTrial *panelayout.Node, item ItemPlan, boxes map[int]panelayout.Box) (panelayout.OpenPlan, string) {
	cell := item.Cell
	if cell.Col != 0 {
		if _, refusal := panelayout.PlanOpenAt(screen, item.Kind, 0, cell); refusal != "" {
			return panelayout.OpenPlan{}, refusal
		}
		translated, refusal := DeckCellFor(screen, cell)
		if refusal != "" {
			return panelayout.OpenPlan{}, refusal
		}
		plan, refusal := panelayout.PlanOpenAt(deckTrial, item.Kind, 0, translated)
		if refusal != "" {
			return panelayout.OpenPlan{}, refusal
		}
		return plan, ""
	}
	plan, ok := panelayout.PlanOpenContent(deckTrial, item.Kind, 0, boxes)
	if !ok {
		return panelayout.OpenPlan{}, PassivePlanRefusal(deckTrial, item.Kind)
	}
	return plan, ""
}

// DeckCellFor translates a screen cell onto the deck's grid. The deck sees the
// same layout minus every Shell leaf.
func DeckCellFor(screen *panelayout.Node, cell panelayout.Cell) (panelayout.Cell, string) {
	grid := panelayout.GridOf(screen)
	if grid == nil {
		return cell, ""
	}
	survivorsBefore := func(col int) int {
		count := 0
		for i := 0; i < col; i++ {
			for _, leaf := range grid.Columns[i].Cells {
				if leaf.Kind != panelayout.Shell {
					count++
					break
				}
			}
		}
		return count
	}
	if cell.Col > grid.ColumnCount() {
		return panelayout.Cell{Col: survivorsBefore(grid.ColumnCount()) + 1, Row: 1}, ""
	}
	column := grid.Columns[cell.Col-1]
	hasContent := false
	for _, leaf := range column.Cells {
		if leaf.Kind != panelayout.Shell {
			hasContent = true
			break
		}
	}
	if !hasContent {
		return panelayout.Cell{}, fmt.Sprintf("cell %s sits inside the live terminal's own column; close or move the terminal first", cell.String())
	}
	if cell.Row <= len(column.Cells) {
		if occupant := column.Cells[cell.Row-1]; occupant != nil && occupant.Kind == panelayout.Shell {
			return panelayout.Cell{}, fmt.Sprintf("cell %s holds the live terminal; content panes cannot take its place", cell.String())
		}
	}
	deckRow := 0
	if cell.Row > len(column.Cells) {
		for _, leaf := range column.Cells {
			if leaf.Kind != panelayout.Shell {
				deckRow++
			}
		}
		deckRow++
	} else {
		for row, leaf := range column.Cells {
			if leaf.Kind != panelayout.Shell && row+1 <= cell.Row {
				deckRow++
			}
		}
	}
	return panelayout.Cell{Col: survivorsBefore(cell.Col-1) + 1, Row: deckRow}, ""
}

// PassivePlanRefusal explains a failed deck-side plan with the planner's own
// vocabulary, worded to stand alone in a toast or an ack.
func PassivePlanRefusal(deckTrial *panelayout.Node, kind panelayout.Kind) string {
	grid := panelayout.GridOf(deckTrial)
	switch {
	case grid != nil && grid.ColumnsAtCap():
		return panelayout.GridColumnCapMessage
	case grid != nil:
		return panelayout.GridRowCapMessage
	default:
		return "no room for another " + kind.Name() + " pane"
	}
}

func specPaneIsCarried(item ItemPlan) bool {
	switch item.Kind {
	case panelayout.Primary:
		return true
	case panelayout.Shell:
		return strings.TrimSpace(item.Spec.Session) != ""
	default:
		return false
	}
}
