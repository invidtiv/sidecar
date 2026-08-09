// Package overview owns the app-level cross-project Overview model.
package overview

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	minColumnWidth = 17
	cardHeight     = 4
)

type Project struct{ Name, Path string }

type NavigateMsg struct{ Workspace workspaceinventory.Workspace }
type ValidationMsg struct {
	Workspace workspaceinventory.Workspace
	Err       error
}
type panesMsg struct {
	Generation int
	Panes      []workspaceinventory.Pane
	Err        error
}
type projectMsg struct {
	Generation int
	Result     workspaceinventory.ProjectResult
}

func IsAsyncMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case panesMsg, projectMsg:
		return true
	default:
		return false
	}
}

type Model struct {
	collector  workspaceinventory.Collector
	projects   []Project
	roots      []string
	generation int
	loading    bool
	tmuxErr    error
	results    map[string]workspaceinventory.ProjectResult
	board      kanban.Component
	cards      map[string]workspaceinventory.Workspace
	mouse      *mouse.Handler
	width      int
	height     int
}

func New(collector workspaceinventory.Collector) *Model {
	collector = collector.WithDefaults()
	return &Model{collector: collector, results: make(map[string]workspaceinventory.ProjectResult), cards: make(map[string]workspaceinventory.Workspace), mouse: mouse.NewHandler()}
}

func (m *Model) Start(projects []Project) tea.Cmd {
	m.generation++
	m.projects = append([]Project(nil), projects...)
	m.roots = m.roots[:0]
	for _, project := range projects {
		m.roots = append(m.roots, project.Path)
	}
	m.loading, m.tmuxErr = true, nil
	m.results = make(map[string]workspaceinventory.ProjectResult)
	m.syncBoard()
	generation := m.generation
	return func() tea.Msg {
		panes, err := m.collector.ListPanes(context.Background())
		return panesMsg{Generation: generation, Panes: panes, Err: err}
	}
}

func (m *Model) Stop() { m.generation++; m.loading = false }

func (m *Model) Validate(workspace workspaceinventory.Workspace) tea.Cmd {
	return func() tea.Msg {
		return ValidationMsg{Workspace: workspace, Err: m.collector.ValidateWorkspace(context.Background(), workspace)}
	}
}

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case panesMsg:
		if msg.Generation != m.generation {
			return nil
		}
		m.tmuxErr = msg.Err
		cmds := make([]tea.Cmd, 0, len(m.projects))
		for _, project := range m.projects {
			project := project
			panes := append([]workspaceinventory.Pane(nil), msg.Panes...)
			generation := m.generation
			cmds = append(cmds, func() tea.Msg {
				return projectMsg{Generation: generation, Result: m.collector.CollectProject(context.Background(), project.Name, project.Path, m.roots, panes)}
			})
		}
		if len(cmds) == 0 {
			m.loading = false
			m.syncBoard()
		}
		return tea.Batch(cmds...)
	case projectMsg:
		if msg.Generation != m.generation {
			return nil
		}
		m.results[msg.Result.ProjectKey] = msg.Result
		m.loading = len(m.results) < len(m.projects)
		m.syncBoard()
		return nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "left", "h":
			m.board.MoveColumn(-1)
		case "right", "l":
			m.board.MoveColumn(1)
		case "up", "k":
			m.board.MoveRow(-1)
		case "down", "j":
			m.board.MoveRow(1)
		case "enter":
			return m.activate()
		case "r":
			return m.Start(m.projects)
		}
	case tea.MouseMsg:
		action := m.mouse.HandleMouse(msg)
		if action.Region == nil {
			return nil
		}
		region, ok := action.Region.Data.(kanban.HitRegion)
		if !ok {
			return nil
		}
		switch action.Type {
		case mouse.ActionClick:
			m.board.HandlePointer(kanban.PointerClick, region)
		case mouse.ActionDoubleClick:
			if m.board.HandlePointer(kanban.PointerDoubleClick, region).Kind == kanban.ActionActivated {
				return m.activate()
			}
		case mouse.ActionHover:
			m.board.HandlePointer(kanban.PointerHover, region)
		}
	}
	return nil
}

func (m *Model) activate() tea.Cmd {
	card, ok := m.board.Board().CardAt(m.board.Selection())
	if !ok {
		return nil
	}
	workspace, ok := m.cards[card.ID]
	if !ok {
		return nil
	}
	return func() tea.Msg { return NavigateMsg{Workspace: workspace} }
}

func (m *Model) View(width, height int) string {
	m.width, m.height = width, height
	result := m.board.Render(kanban.RenderOptions{Width: width, Height: height, Header: "Agent Overview", HeaderRight: m.summary(), MinColumnWidth: minColumnWidth, CardHeight: cardHeight, RenderCard: m.renderCard})
	m.mouse.Clear()
	if result.Compact {
		return m.renderCompact(width, height)
	}
	for _, region := range result.Regions {
		m.mouse.HitMap.AddRect("overview-card", region.X, region.Y, region.W, region.H, region)
	}
	return result.View
}

func (m *Model) summary() string {
	if m.loading {
		return fmt.Sprintf("Loading %d/%d", len(m.results), len(m.projects))
	}
	if m.tmuxErr != nil {
		return "tmux unavailable"
	}
	return fmt.Sprintf("%d projects", len(m.results))
}

func (m *Model) syncBoard() {
	lanes := []kanban.Lane{
		{ID: "working", Label: "Working", State: kanban.CellReady}, {ID: "blocked", Label: "Needs attention", State: kanban.CellReady},
		{ID: "done", Label: "Done", State: kanban.CellReady}, {ID: "idle", Label: "Idle", State: kanban.CellReady}, {ID: "paused", Label: "Paused", State: kanban.CellReady},
	}
	m.cards = make(map[string]workspaceinventory.Workspace)
	for _, project := range m.projects {
		key := clean(project.Path)
		result, loaded := m.results[key]
		if !loaded {
			if m.loading {
				lanes[4].State, lanes[4].Message = kanban.CellLoading, "Loading "+project.Name+"…"
			}
			continue
		}
		if result.Err != nil {
			card := kanban.Card{ID: "error:" + key, Title: project.Name, Subtitle: "project unavailable", Detail: result.Err.Error()}
			lanes[4].Cards = append(lanes[4].Cards, card)
			continue
		}
		for _, workspace := range result.Workspaces {
			m.cards[workspace.ID] = workspace
			card := kanban.Card{ID: workspace.ID, Title: workspace.ProjectName + " / " + workspace.Name, Subtitle: workspace.Provider + " · " + workspace.Presentation.Label, Detail: choose(workspace.TaskID, workspace.Branch), Meta: string(workspace.Presentation.Freshness)}
			for i := range lanes {
				if lanes[i].ID == kanban.LaneID(workspace.Presentation.Lane) {
					lanes[i].Cards = append(lanes[i].Cards, card)
					break
				}
			}
		}
	}
	for i := range lanes {
		if len(lanes[i].Cards) == 0 && lanes[i].State == kanban.CellReady {
			lanes[i].State, lanes[i].Message = kanban.CellEmpty, "No agents"
		}
	}
	m.board.SetBoard(kanban.Board{Lanes: lanes})
}

func (m *Model) renderCard(card kanban.Card, line, width int, selected, _ bool) string {
	values := []string{" " + card.Title, " " + card.Subtitle, " " + card.Detail, " " + card.Meta}
	value := ""
	if line < len(values) {
		value = ansi.Truncate(values[line], width, "")
	}
	style := lipgloss.NewStyle().Width(width)
	if selected {
		style = styles.ListItemSelected.Width(width)
	} else if line > 0 {
		style = styles.Muted.Width(width)
	}
	return style.Render(value)
}

func (m *Model) renderCompact(width, height int) string {
	var lines []string
	lines = append(lines, styles.Title.Render("Agent Overview")+"  "+styles.Muted.Render(m.summary()))
	selected, _ := m.board.Board().CardAt(m.board.Selection())
	for column, lane := range m.board.Board().Lanes {
		for row, card := range lane.Cards {
			line := fmt.Sprintf(" %-15s %s  %s", lane.Label, card.Title, card.Subtitle)
			style := lipgloss.NewStyle()
			if card.ID == selected.ID {
				style = styles.ListItemSelected
			}
			lines = append(lines, style.Render(ansi.Truncate(line, max(1, width-2), "")))
			m.mouse.HitMap.AddRect("overview-card", 0, len(lines)-1, width, 1, kanban.HitRegion{Kind: kanban.RegionCard, Column: column, Row: row, CardID: card.ID, X: 0, Y: len(lines) - 1, W: width, H: 1})
		}
	}
	if len(lines) == 1 {
		lines = append(lines, styles.Muted.Render(" No agent-backed workspaces found"))
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(strings.Join(lines, "\n"))
}

func (m *Model) Commands() []struct{ Key, Name string } {
	return []struct{ Key, Name string }{{"enter", "Open"}, {"r", "Refresh"}}
}

func clean(path string) string { return workspaceinventory.CanonicalPath(path) }
func choose(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Keep the shared semantic dependency visible at this boundary: Overview cards
// are projections of agentstatus, not a second status reducer.
var _ agentstatus.LaneID
