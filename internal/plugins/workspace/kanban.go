package workspace

import (
	"image/color"
	"strings"

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
		DoneTTL:      agentstatus.DefaultDoneTTL,
	}
	if wt.Agent != nil {
		in.ProviderSupported = supportsAgentActivity(wt.Agent.Type)
		in.Activity = wt.Agent.Activity
		in.CapturedAt = wt.Agent.ActivityCapturedAt
		in.Now = wt.Agent.ActivityCapturedAt
	}
	return agentstatus.Resolve(in)
}

func shellAgentStatusPresentation(shell *ShellSession) agentstatus.Presentation {
	if shell == nil {
		return agentstatus.Resolve(agentstatus.Input{Unavailable: true})
	}
	in := agentstatus.Input{Orphaned: shell.IsOrphaned, LegacyStatus: StatusPaused.String(), LegacyIcon: "○", DoneTTL: agentstatus.DefaultDoneTTL}
	if shell.Agent != nil {
		in.LegacyStatus = StatusActive.String()
		in.LegacyIcon = "●"
		in.ProviderSupported = supportsAgentActivity(shell.Agent.Type)
		in.Activity = shell.Agent.Activity
		in.CapturedAt = shell.Agent.ActivityCapturedAt
		in.Now = shell.Agent.ActivityCapturedAt
	}
	return agentstatus.Resolve(in)
}

func (p *Plugin) kanbanColumnItemCount(col int, columns map[kanbanLane][]*Worktree) int {
	if col == kanbanShellColumnIndex {
		return len(p.plainKanbanShells())
	}
	lane, ok := kanbanLaneForColumn(col)
	if !ok {
		return 0
	}
	n := len(columns[lane])
	for _, shell := range p.shells {
		if shellLane, ok := shellKanbanActivityLane(shell); ok && shellLane == lane {
			n++
		}
	}
	return n
}

func (p *Plugin) kanbanShellAt(row int) *ShellSession {
	shells := p.plainKanbanShells()
	if row < 0 || row >= len(shells) {
		return nil
	}
	return shells[row]
}

// plainKanbanShells are shells without a supported live agent identity. Agent
// shells live in the activity lanes with worktrees (same model as Overview).
func (p *Plugin) plainKanbanShells() []*ShellSession {
	out := make([]*ShellSession, 0, len(p.shells))
	for _, shell := range p.shells {
		if _, ok := shellKanbanActivityLane(shell); ok {
			continue
		}
		out = append(out, shell)
	}
	return out
}

// shellKanbanActivityLane returns the activity lane for a shell that has a
// supported live agent type. Plain shells and unknown types return false so
// they stay in the Shells column.
func shellKanbanActivityLane(shell *ShellSession) (kanbanLane, bool) {
	if shell == nil || shell.Agent == nil || !supportsAgentActivity(shell.Agent.Type) {
		return "", false
	}
	return shellAgentStatusPresentation(shell).Lane, true
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

func shellKanbanCardID(shell *ShellSession) string {
	id := shell.TmuxName
	if id == "" {
		id = shell.Name
	}
	return "shell:" + id
}

// workspaceLanePalette is this board's lane hues. They are the ones the project
// board has always drawn, and they are stated here rather than taken from the
// theme's lane colours because the global browser's palette is a different one:
// sharing what a lane is must not re-theme a surface that already had an answer.
func workspaceLanePalette(lane agentstatus.LaneID) color.Color {
	switch lane {
	case kanbanLaneWorking:
		return styles.StatusCompleted.GetForeground()
	case kanbanLaneBlocked:
		return styles.StatusModified.GetForeground()
	case kanbanLaneDone:
		return styles.Secondary
	default:
		return styles.TextMuted
	}
}

func (p *Plugin) workspaceKanbanBoard() boardkanban.Board {
	lanes := make([]boardkanban.Lane, 0, kanbanColumnCount())
	shells := boardkanban.Lane{ID: "shells", Label: "Shells", HeaderColor: styles.Muted.GetForeground()}
	for _, shell := range p.plainKanbanShells() {
		shells.Cards = append(shells.Cards, boardkanban.Card{ID: shellKanbanCardID(shell), Title: shell.Name})
	}
	lanes = append(lanes, shells)
	columns := p.getKanbanColumns()
	agentShellsByLane := make(map[kanbanLane][]*ShellSession, len(kanbanLaneOrder))
	for _, shell := range p.shells {
		if lane, ok := shellKanbanActivityLane(shell); ok {
			agentShellsByLane[lane] = append(agentShellsByLane[lane], shell)
		}
	}
	for _, laneID := range kanbanLaneOrder {
		// Wording and glyph are the shared definition's; the global board draws
		// the same lanes from it. The hues stay this board's own.
		lane := boardkanban.AgentLane(laneID, workspaceLanePalette)
		for _, wt := range columns[laneID] {
			lane.Cards = append(lane.Cards, boardkanban.Card{ID: "worktree:" + wt.IdentityKey(), Title: wt.Name, Detail: wt.TaskID})
		}
		for _, shell := range agentShellsByLane[laneID] {
			lane.Cards = append(lane.Cards, boardkanban.Card{ID: shellKanbanCardID(shell), Title: shell.Name})
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
	card, ok := p.workspaceKanbanBoard().CardAt(boardkanban.Selection{Column: p.kanbanCol, Row: p.kanbanRow})
	if !ok || !strings.HasPrefix(card.ID, "worktree:") {
		return nil
	}
	return p.kanbanWorktreeByID(card.ID)
}

func (p *Plugin) selectedKanbanShell() *ShellSession {
	card, ok := p.workspaceKanbanBoard().CardAt(boardkanban.Selection{Column: p.kanbanCol, Row: p.kanbanRow})
	if !ok || !strings.HasPrefix(card.ID, "shell:") {
		return nil
	}
	return p.kanbanShellByID(card.ID)
}

func (p *Plugin) syncKanbanToList() {
	if shell := p.selectedKanbanShell(); shell != nil {
		for i, s := range p.shells {
			if s == shell || (s.TmuxName != "" && s.TmuxName == shell.TmuxName) || s.Name == shell.Name {
				p.selectTopShellAt(i)
				return
			}
		}
		return
	}
	wt := p.selectedKanbanWorktree()
	if wt == nil {
		return
	}
	for i, w := range p.worktrees {
		if w.IdentityKey() == wt.IdentityKey() {
			p.selectWorktreeAt(i)
			return
		}
	}
}

func (p *Plugin) applyKanbanSelectionChange(oldShellSelected bool, oldShellIdx, oldWorktreeIdx int) bool {
	selectionChanged := p.shellSelected != oldShellSelected ||
		(p.shellSelected && p.selectedShellIdx != oldShellIdx) ||
		(!p.shellSelected && p.selectedIdx != oldWorktreeIdx) ||
		p.selectedNestedTmux != ""
	if selectionChanged {
		p.resetPreviewScroll()
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
	card, ok := p.workspaceKanbanBoard().CardAt(boardkanban.Selection{Column: col, Row: row})
	if !ok || !strings.HasPrefix(card.ID, "worktree:") {
		return nil
	}
	return p.kanbanWorktreeByID(card.ID)
}

func (p *Plugin) syncListToKanban() {
	if p.shellSelected {
		shell := p.getSelectedShell()
		if shell == nil {
			p.kanbanCol, p.kanbanRow = 0, 0
			return
		}
		if pos, ok := p.workspaceKanbanBoard().PositionOf(shellKanbanCardID(shell)); ok {
			p.kanbanCol, p.kanbanRow = pos.Column, pos.Row
			return
		}
		p.kanbanCol, p.kanbanRow = 0, 0
		return
	}
	wt := p.selectedWorktree()
	if wt == nil {
		p.kanbanCol, p.kanbanRow = 0, 0
		return
	}
	if pos, ok := p.workspaceKanbanBoard().PositionOf("worktree:" + wt.IdentityKey()); ok {
		p.kanbanCol, p.kanbanRow = pos.Column, pos.Row
		return
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
