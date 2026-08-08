package workspace

// kanbanLane is a presentation grouping, deliberately separate from the
// legacy WorktreeStatus transport. Supported agents are grouped from the same
// semantic activity tracker used by List; unsupported agents retain the
// legacy projection.
type kanbanLane int

const (
	kanbanLaneWorking kanbanLane = iota
	kanbanLaneBlocked
	kanbanLaneDone
	kanbanLaneIdle
	kanbanLanePaused
)

var kanbanLaneOrder = []kanbanLane{
	kanbanLaneWorking,
	kanbanLaneBlocked,
	kanbanLaneDone,
	kanbanLaneIdle,
	kanbanLanePaused,
}

const kanbanShellColumnIndex = 0

func kanbanColumnCount() int { return len(kanbanLaneOrder) + 1 }

func kanbanLaneForColumn(col int) (kanbanLane, bool) {
	if col <= kanbanShellColumnIndex {
		return 0, false
	}
	idx := col - 1
	if idx < 0 || idx >= len(kanbanLaneOrder) {
		return 0, false
	}
	return kanbanLaneOrder[idx], true
}

func kanbanLaneForWorktree(wt *Worktree) kanbanLane {
	// Health and liveness are orthogonal to activity and always win.
	if wt == nil || wt.IsMissing || wt.IsOrphaned || wt.Status == StatusError {
		return kanbanLanePaused
	}
	if displayState, ok := activityDisplayState(wt.Agent); ok {
		switch displayState {
		case "working":
			return kanbanLaneWorking
		case "blocked":
			return kanbanLaneBlocked
		case "done":
			return kanbanLaneDone
		case "idle":
			return kanbanLaneIdle
		default:
			return kanbanLanePaused
		}
	}
	// Legacy fallback for agents without semantic activity probes.
	switch wt.Status {
	case StatusActive, StatusThinking:
		return kanbanLaneWorking
	case StatusWaiting:
		return kanbanLaneBlocked
	case StatusDone:
		return kanbanLaneDone
	default:
		return kanbanLanePaused
	}
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
	columns := p.getKanbanColumns()
	newCol := p.kanbanCol + delta
	if newCol < 0 {
		newCol = 0
	}
	if newCol >= kanbanColumnCount() {
		newCol = kanbanColumnCount() - 1
	}
	if newCol != p.kanbanCol {
		p.kanbanCol = newCol
		count := p.kanbanColumnItemCount(p.kanbanCol, columns)
		if count == 0 {
			p.kanbanRow = 0
		} else if p.kanbanRow >= count {
			p.kanbanRow = count - 1
		}
	}
}

func (p *Plugin) moveKanbanRow(delta int) {
	columns := p.getKanbanColumns()
	count := p.kanbanColumnItemCount(p.kanbanCol, columns)
	if count == 0 {
		return
	}
	newRow := p.kanbanRow + delta
	if newRow < 0 {
		newRow = 0
	}
	if newRow >= count {
		newRow = count - 1
	}
	p.kanbanRow = newRow
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
			if item.Name == wt.Name {
				p.kanbanCol, p.kanbanRow = colIdx+1, rowIdx
				return
			}
		}
	}
	p.kanbanCol, p.kanbanRow = 0, 0
}
