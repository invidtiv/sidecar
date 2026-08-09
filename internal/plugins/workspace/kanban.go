package workspace

import (
	"image/color"

	"github.com/marcus/sidecar/internal/agentstatus"
	boardkanban "github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/styles"
)

// kanbanLane is a presentation grouping, deliberately separate from the
// legacy WorktreeStatus transport. Supported agents are grouped from the same
// semantic activity tracker used by List; unsupported agents retain the
// legacy projection.
type kanbanLane = agentstatus.LaneID

const (
	kanbanLaneWorking = agentstatus.LaneWorking
	kanbanLaneBlocked = agentstatus.LaneBlocked
	kanbanLaneDone    = agentstatus.LaneDone
	kanbanLaneIdle    = agentstatus.LaneIdle
	kanbanLanePaused  = agentstatus.LanePaused
)

var kanbanLaneOrder = []kanbanLane{
	kanbanLaneWorking,
	kanbanLaneBlocked,
	kanbanLaneDone,
	kanbanLaneIdle,
	kanbanLanePaused,
}

type kanbanWorktreePresentation struct {
	lane       kanbanLane
	icon       string
	statusText string
	health     bool
}

const kanbanShellColumnIndex = 0

const (
	kanbanCardHeight     = 4
	minKanbanColumnWidth = 16
)

func kanbanColumnCount() int { return len(kanbanLaneOrder) + 1 }

func kanbanMinimumWidth() int {
	numCols := kanbanColumnCount()
	return boardkanban.MinimumWidth(numCols, minKanbanColumnWidth, 4)
}

func kanbanUsesListFallback(width int) bool {
	return boardkanban.UsesCompact(width, kanbanColumnCount(), minKanbanColumnWidth, 4)
}

func kanbanVisibleCardCount(height int) int {
	return boardkanban.CalculateLayout(kanbanMinimumWidth(), height, kanbanColumnCount(), minKanbanColumnWidth, kanbanCardHeight).MaxCards
}

func kanbanLaneForColumn(col int) (kanbanLane, bool) {
	if col <= kanbanShellColumnIndex {
		return "", false
	}
	idx := col - 1
	if idx < 0 || idx >= len(kanbanLaneOrder) {
		return "", false
	}
	return kanbanLaneOrder[idx], true
}

func kanbanLaneForWorktree(wt *Worktree) kanbanLane {
	return kanbanPresentationForWorktree(wt).lane
}

// kanbanPresentationForWorktree is the single precedence decision for both
// lane grouping and card rendering. Health/liveness wins over stale activity.
func kanbanPresentationForWorktree(wt *Worktree) kanbanWorktreePresentation {
	if wt == nil {
		return kanbanWorktreePresentation{lane: kanbanLanePaused, icon: "?", statusText: "unavailable", health: true}
	}
	p := agentStatusPresentation(wt)
	return kanbanWorktreePresentation{lane: p.Lane, icon: p.Icon, statusText: p.Label, health: p.Health}
}

func agentStatusPresentation(wt *Worktree) agentstatus.Presentation {
	if wt == nil {
		return agentstatus.Resolve(agentstatus.Input{Err: true})
	}
	in := agentstatus.Input{
		Missing:      wt.IsMissing,
		Orphaned:     wt.IsOrphaned,
		Paused:       wt.Status == StatusPaused,
		Err:          wt.Status == StatusError,
		LegacyStatus: wt.Status.String(),
		LegacyIcon:   wt.Status.Icon(),
	}
	if wt.Agent != nil {
		in.ProviderSupported = supportsAgentActivity(wt.Agent.Type)
		in.Activity = wt.Agent.Activity
		in.CapturedAt = wt.Agent.ActivityCapturedAt
	}
	return agentstatus.Resolve(in)
}

func shellAgentStatusPresentation(shell *ShellSession) agentstatus.Presentation {
	if shell == nil {
		return agentstatus.Resolve(agentstatus.Input{Unavailable: true})
	}
	in := agentstatus.Input{Orphaned: shell.IsOrphaned, LegacyStatus: StatusPaused.String(), LegacyIcon: "○"}
	if shell.Agent != nil {
		in.LegacyStatus = StatusActive.String()
		in.LegacyIcon = "●"
		in.ProviderSupported = supportsAgentActivity(shell.Agent.Type)
		in.Activity = shell.Agent.Activity
		in.CapturedAt = shell.Agent.ActivityCapturedAt
	}
	return agentstatus.Resolve(in)
}

func (p *Plugin) kanbanColumnItemCount(col int, columns map[kanbanLane][]*Worktree) int {
	if col == kanbanShellColumnIndex {
		return len(p.shells)
	}
	lane, ok := kanbanLaneForColumn(col)
	if !ok {
		return 0
	}
	return len(columns[lane])
}

func (p *Plugin) kanbanShellAt(row int) *ShellSession {
	if row < 0 || row >= len(p.shells) {
		return nil
	}
	return p.shells[row]
}

func (p *Plugin) getKanbanColumns() map[kanbanLane][]*Worktree {
	columns := make(map[kanbanLane][]*Worktree, len(kanbanLaneOrder))
	for _, lane := range kanbanLaneOrder {
		columns[lane] = []*Worktree{}
	}
	for _, wt := range p.worktrees {
		lane := kanbanLaneForWorktree(wt)
		columns[lane] = append(columns[lane], wt)
	}
	return columns
}

func (p *Plugin) workspaceKanbanBoard() boardkanban.Board {
	lanes := make([]boardkanban.Lane, 0, kanbanColumnCount())
	shells := boardkanban.Lane{ID: "shells", Label: "Shells", HeaderColor: styles.Muted.GetForeground()}
	for _, shell := range p.shells {
		id := shell.TmuxName
		if id == "" {
			id = shell.Name
		}
		shells.Cards = append(shells.Cards, boardkanban.Card{ID: "shell:" + id, Title: shell.Name})
	}
	lanes = append(lanes, shells)
	columns := p.getKanbanColumns()
	labels := map[kanbanLane]string{
		kanbanLaneWorking: "● Working",
		kanbanLaneBlocked: "◆ Blocked",
		kanbanLaneDone:    "✓ Done",
		kanbanLaneIdle:    "○ Idle",
		kanbanLanePaused:  "⏸ Paused",
	}
	colors := map[kanbanLane]color.Color{
		kanbanLaneWorking: styles.StatusCompleted.GetForeground(),
		kanbanLaneBlocked: styles.StatusModified.GetForeground(),
		kanbanLaneDone:    styles.Secondary,
		kanbanLaneIdle:    styles.TextMuted,
		kanbanLanePaused:  styles.TextMuted,
	}
	for _, laneID := range kanbanLaneOrder {
		lane := boardkanban.Lane{ID: boardkanban.LaneID(laneID), Label: labels[laneID], HeaderColor: colors[laneID]}
		for _, wt := range columns[laneID] {
			lane.Cards = append(lane.Cards, boardkanban.Card{ID: "worktree:" + wt.IdentityKey(), Title: wt.Name, Detail: wt.TaskID})
		}
		lanes = append(lanes, lane)
	}
	return boardkanban.Board{Lanes: lanes}
}

func (p *Plugin) syncKanbanComponent() {
	board := p.workspaceKanbanBoard()
	requested := boardkanban.Selection{Column: p.kanbanCol, Row: p.kanbanRow}
	if p.kanban.Board().ColumnCount() == 0 {
		p.kanban.SetBoard(board)
		p.kanban.Select(requested)
	} else {
		p.kanban.Select(requested)
		p.kanban.SetBoard(board)
	}
	selection := p.kanban.Selection()
	p.kanbanCol, p.kanbanRow = selection.Column, selection.Row
}

func (p *Plugin) selectedKanbanWorktree() *Worktree {
	columns := p.getKanbanColumns()
	if p.kanbanCol == kanbanShellColumnIndex {
		return nil
	}
	lane, ok := kanbanLaneForColumn(p.kanbanCol)
	if !ok {
		return nil
	}
	items := columns[lane]
	if p.kanbanRow < 0 || p.kanbanRow >= len(items) {
		return nil
	}
	return items[p.kanbanRow]
}

func (p *Plugin) syncKanbanToList() {
	if p.kanbanCol == kanbanShellColumnIndex {
		shell := p.kanbanShellAt(p.kanbanRow)
		if shell == nil {
			return
		}
		p.shellSelected = true
		p.selectedShellIdx = p.kanbanRow
		return
	}
	wt := p.selectedKanbanWorktree()
	if wt == nil {
		return
	}
	for i, w := range p.worktrees {
		if w.Name == wt.Name {
			p.shellSelected = false
			p.selectedIdx = i
			return
		}
	}
}

func (p *Plugin) applyKanbanSelectionChange(oldShellSelected bool, oldShellIdx, oldWorktreeIdx int) bool {
	selectionChanged := p.shellSelected != oldShellSelected ||
		(p.shellSelected && p.selectedShellIdx != oldShellIdx) ||
		(!p.shellSelected && p.selectedIdx != oldWorktreeIdx)
	if selectionChanged {
		p.previewOffset = 0
		p.autoScrollOutput = true
		p.taskLoading = false
		p.exitInteractiveMode()
		p.saveSelectionState()
	}
	return selectionChanged
}

func (p *Plugin) moveKanbanColumn(delta int) {
	p.syncKanbanComponent()
	p.kanban.MoveColumn(delta)
	next := p.kanban.Selection()
	p.kanbanCol, p.kanbanRow = next.Column, next.Row
}

func (p *Plugin) moveKanbanRow(delta int) {
	p.syncKanbanComponent()
	p.kanban.MoveRow(delta)
	next := p.kanban.Selection()
	p.kanbanCol, p.kanbanRow = next.Column, next.Row
}

func (p *Plugin) getKanbanWorktree(col, row int) *Worktree {
	columns := p.getKanbanColumns()
	if col == kanbanShellColumnIndex {
		return nil
	}
	lane, ok := kanbanLaneForColumn(col)
	if !ok {
		return nil
	}
	items := columns[lane]
	if row >= 0 && row < len(items) {
		return items[row]
	}
	return nil
}

func (p *Plugin) syncListToKanban() {
	if p.shellSelected {
		p.kanbanCol = kanbanShellColumnIndex
		if p.selectedShellIdx >= 0 && p.selectedShellIdx < len(p.shells) {
			p.kanbanRow = p.selectedShellIdx
		} else {
			p.kanbanRow = 0
		}
		return
	}
	wt := p.selectedWorktree()
	if wt == nil {
		p.kanbanCol, p.kanbanRow = 0, 0
		return
	}
	columns := p.getKanbanColumns()
	for colIdx, lane := range kanbanLaneOrder {
		for rowIdx, item := range columns[lane] {
			if item.IdentityKey() == wt.IdentityKey() {
				p.kanbanCol, p.kanbanRow = colIdx+1, rowIdx
				return
			}
		}
	}
	p.kanbanCol, p.kanbanRow = 0, 0
}

func (p *Plugin) selectKanbanFromList() {
	p.syncListToKanban()
	requested := boardkanban.Selection{Column: p.kanbanCol, Row: p.kanbanRow}
	p.syncKanbanComponent()
	p.kanban.Select(requested)
	selection := p.kanban.Selection()
	p.kanbanCol, p.kanbanRow = selection.Column, selection.Row
}
