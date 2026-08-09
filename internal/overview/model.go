// Package overview owns the app-level cross-project Overview model.
package overview

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

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
	maxProjects    = 4
	maxCaptures    = 4
	livePollEvery  = 5 * time.Second
	idlePollEvery  = 30 * time.Second
)

type Project struct{ Name, Path, Key string }

type NavigateMsg struct {
	Workspace  workspaceinventory.Workspace
	Generation int
	RequestID  uint64
}
type ValidationMsg struct {
	Workspace  workspaceinventory.Workspace
	Generation int
	RequestID  uint64
	Err        error
}
type panesMsg struct {
	Generation  int
	Projects    []Project
	Panes       []workspaceinventory.Pane
	ShellClaims workspaceinventory.ShellClaims
	LiveOnly    bool
	Err         error
}
type projectMsg struct {
	Generation int
	Result     workspaceinventory.ProjectResult
}
type pollMsg struct{ Generation int }

func IsAsyncMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case panesMsg, projectMsg, pollMsg:
		return true
	default:
		return false
	}
}

type Model struct {
	collector        workspaceinventory.Collector
	refreshCollector workspaceinventory.Collector
	projects         []Project
	roots            []string
	generation       int
	requestID        uint64
	loading          bool
	tmuxErr          error
	results          map[string]workspaceinventory.ProjectResult
	projectErrors    map[string]error
	stale            map[string]bool
	refreshing       map[string]bool
	completed        map[string]bool
	pending          []Project
	active           int
	currentPanes     []workspaceinventory.Pane
	shellClaims      workspaceinventory.ShellClaims
	liveOnly         bool
	ctx              context.Context
	cancel           context.CancelFunc
	traceWriter      io.Writer
	cycleStart       time.Time
	configured       int
	firstResult      bool
	maxActive        int
	board            kanban.Component
	cards            map[string]workspaceinventory.Workspace
	mouse            *mouse.Handler
	width            int
	height           int
}

func New(collector workspaceinventory.Collector) *Model {
	collector = collector.WithDefaults()
	m := &Model{collector: collector, results: make(map[string]workspaceinventory.ProjectResult), projectErrors: make(map[string]error), stale: make(map[string]bool), refreshing: make(map[string]bool), completed: make(map[string]bool), cards: make(map[string]workspaceinventory.Workspace), mouse: mouse.NewHandler()}
	if value := os.Getenv("SIDECAR_OVERVIEW_TRACE"); value == "1" || value == "stderr" {
		m.traceWriter = os.Stderr
	}
	return m
}

func (m *Model) Start(projects []Project) tea.Cmd {
	return m.start(projects, "refresh")
}

func (m *Model) start(projects []Project, reason string) tea.Cmd {
	if m.cancel != nil {
		if m.loading || m.active > 0 {
			m.tracef("cycle generation=%d canceled active_projects=%d", m.generation, m.active)
		}
		m.cancel()
	}
	m.generation++
	m.requestID++
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.loading, m.tmuxErr = true, nil
	m.completed = make(map[string]bool)
	for key := range m.results {
		m.refreshing[key] = true
	}
	if len(m.projects) > 0 {
		m.syncBoard()
	}
	m.cycleStart, m.configured, m.firstResult, m.maxActive = time.Now(), len(projects), false, 0
	m.tracef("cycle generation=%d reason=%s configured=%d start", m.generation, reason, len(projects))
	generation := m.generation
	ctx := m.ctx
	configured := append([]Project(nil), projects...)
	cachedShellClaims := cloneShellClaims(m.shellClaims)
	return func() tea.Msg {
		normalized := configured
		liveOnly := reason == "poll"
		if !liveOnly {
			normalized = normalizeProjects(configured)
		}
		roots := make([]string, 0, len(normalized))
		for _, project := range normalized {
			roots = append(roots, project.Path)
		}
		panes, err := m.collector.ListPanes(ctx)
		shellClaims := cachedShellClaims
		if !liveOnly {
			shellClaims = workspaceinventory.AgentShellClaims(roots)
		}
		return panesMsg{Generation: generation, Projects: normalized, Panes: panes, ShellClaims: shellClaims, LiveOnly: liveOnly, Err: err}
	}
}

func (m *Model) Stop() {
	if m.cancel != nil {
		if m.loading || m.active > 0 {
			m.tracef("cycle generation=%d canceled active_projects=%d", m.generation, m.active)
		}
		m.cancel()
		m.cancel = nil
	}
	m.generation++
	m.requestID++
	m.loading = false
}

// RequestNavigation binds a card activation to the current Overview lifecycle
// and supersedes any prior in-flight destination validation.
func (m *Model) RequestNavigation(workspace workspaceinventory.Workspace) tea.Cmd {
	m.requestID++
	msg := NavigateMsg{Workspace: workspace, Generation: m.generation, RequestID: m.requestID}
	return func() tea.Msg { return msg }
}

func (m *Model) IsCurrentNavigation(generation int, requestID uint64) bool {
	return generation == m.generation && requestID == m.requestID
}

// ConsumeValidation accepts a result at most once. A later duplicate or a
// result superseded by another activation cannot navigate.
func (m *Model) ConsumeValidation(generation int, requestID uint64) bool {
	if !m.IsCurrentNavigation(generation, requestID) {
		return false
	}
	m.requestID++
	return true
}

func (m *Model) Validate(msg NavigateMsg) tea.Cmd {
	return func() tea.Msg {
		return ValidationMsg{
			Workspace:  msg.Workspace,
			Generation: msg.Generation,
			RequestID:  msg.RequestID,
			Err:        m.collector.ValidateWorkspace(context.Background(), msg.Workspace),
		}
	}
}

func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case panesMsg:
		if msg.Generation != m.generation {
			return nil
		}
		m.projects = msg.Projects
		m.roots = m.roots[:0]
		keep := make(map[string]bool, len(m.projects))
		for _, project := range m.projects {
			key := projectKey(project)
			m.roots = append(m.roots, project.Path)
			keep[key] = true
			m.refreshing[key] = true
		}
		for key := range m.results {
			if !keep[key] {
				delete(m.results, key)
				delete(m.projectErrors, key)
				delete(m.stale, key)
				delete(m.refreshing, key)
			}
		}
		m.tmuxErr = msg.Err
		m.pending = append(m.pending[:0], m.projects...)
		m.completed = make(map[string]bool, len(m.projects))
		m.active = 0
		m.currentPanes = append(m.currentPanes[:0], msg.Panes...)
		m.shellClaims = msg.ShellClaims
		m.liveOnly = msg.LiveOnly
		m.refreshCollector = m.collector.ForRefresh(maxCaptures, msg.ShellClaims)
		m.tracef("cycle generation=%d configured=%d deduped=%d tmux_inventories=1", m.generation, m.configured, len(m.projects))
		m.syncBoard()
		if len(m.pending) == 0 {
			m.loading = false
			m.tracef("cycle generation=%d complete_ms=%d project_ops=0 captures=0 max_project_concurrency=0 max_capture_concurrency=0", m.generation, time.Since(m.cycleStart).Milliseconds())
			return m.pollCmd()
		}
		return m.dispatchProjects()
	case projectMsg:
		if msg.Generation != m.generation {
			m.tracef("cycle generation=%d drained stale_generation=%d", m.generation, msg.Generation)
			return nil
		}
		if m.active > 0 {
			m.active--
		}
		key := msg.Result.ProjectKey
		if !m.firstResult {
			m.firstResult = true
			m.tracef("cycle generation=%d first_result_ms=%d", m.generation, time.Since(m.cycleStart).Milliseconds())
		}
		m.completed[key] = true
		m.refreshing[key] = false
		if m.tmuxErr != nil {
			if previous, ok := m.results[key]; ok && previous.Err == nil {
				for i := range previous.Workspaces {
					previous.Workspaces[i].Presentation.Freshness = agentstatus.FreshnessStale
					previous.Workspaces[i].Presentation.Attention = false
				}
				m.results[key] = previous
				m.projectErrors[key] = m.tmuxErr
				m.stale[key] = true
			} else {
				m.results[key] = msg.Result
				m.projectErrors[key] = m.tmuxErr
				m.stale[key] = false
			}
		} else if msg.Result.Err == nil {
			m.results[key] = msg.Result
			delete(m.projectErrors, key)
			delete(m.stale, key)
		} else if previous, ok := m.results[key]; ok && previous.Err == nil {
			for i := range previous.Workspaces {
				previous.Workspaces[i].Presentation.Freshness = agentstatus.FreshnessStale
				previous.Workspaces[i].Presentation.Attention = false
			}
			m.results[key] = previous
			m.projectErrors[key] = msg.Result.Err
			m.stale[key] = true
		} else {
			m.results[key] = msg.Result
			m.projectErrors[key] = msg.Result.Err
			m.stale[key] = false
		}
		m.loading = len(m.completed) < len(m.projects)
		m.syncBoard()
		if m.loading {
			return m.dispatchProjects()
		}
		metrics := m.refreshCollector.Metrics()
		m.tracef("cycle generation=%d complete_ms=%d project_ops=%d captures=%d max_project_concurrency=%d max_capture_concurrency=%d", m.generation, time.Since(m.cycleStart).Milliseconds(), metrics.ProjectOps, metrics.Captures, m.maxActive, metrics.MaxCaptures)
		return m.pollCmd()
	case pollMsg:
		if msg.Generation != m.generation || m.ctx == nil {
			return nil
		}
		return m.start(m.projects, "poll")
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

func (m *Model) dispatchProjects() tea.Cmd {
	cmds := make([]tea.Cmd, 0, maxProjects)
	for m.active < maxProjects && len(m.pending) > 0 {
		project := m.pending[0]
		m.pending = m.pending[1:]
		m.active++
		m.maxActive = max(m.maxActive, m.active)
		generation, ctx := m.generation, m.ctx
		roots := append([]string(nil), m.roots...)
		inventory := append([]workspaceinventory.Pane(nil), m.currentPanes...)
		collector := m.refreshCollector
		previous, hasPrevious := m.results[projectKey(project)]
		liveOnly := m.liveOnly && hasPrevious && previous.Err == nil
		cmds = append(cmds, func() tea.Msg {
			if liveOnly {
				return projectMsg{Generation: generation, Result: collector.RefreshProjectStatus(ctx, previous, roots, inventory)}
			}
			return projectMsg{Generation: generation, Result: collector.CollectProject(ctx, project.Name, project.Path, roots, inventory)}
		})
	}
	return tea.Batch(cmds...)
}

func (m *Model) tracef(format string, args ...any) {
	if m.traceWriter != nil {
		_, _ = fmt.Fprintf(m.traceWriter, "overview "+format+"\n", args...)
	}
}

func (m *Model) pollCmd() tea.Cmd {
	generation, ctx, delay := m.generation, m.ctx, m.pollInterval()
	return func() tea.Msg {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			return pollMsg{Generation: generation}
		case <-ctx.Done():
			return pollMsg{Generation: generation}
		}
	}
}

func (m *Model) pollInterval() time.Duration {
	for _, result := range m.results {
		for _, workspace := range result.Workspaces {
			if workspace.Presentation.Lane == agentstatus.LaneWorking || workspace.Presentation.Lane == agentstatus.LaneBlocked {
				return livePollEvery
			}
		}
	}
	return idlePollEvery
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
	return m.RequestNavigation(workspace)
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
		return fmt.Sprintf("Loading %d/%d", len(m.completed), len(m.projects))
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
		key := projectKey(project)
		result, loaded := m.results[key]
		if !loaded {
			if m.loading {
				lanes[4].State, lanes[4].Message = kanban.CellLoading, "Loading "+project.Name+"…"
			}
			continue
		}
		if result.Err != nil && len(result.Workspaces) == 0 {
			card := kanban.Card{ID: "error:" + key, Title: project.Name, Subtitle: "project unavailable", Detail: result.Err.Error()}
			lanes[4].Cards = append(lanes[4].Cards, card)
			continue
		}
		for _, workspace := range result.Workspaces {
			m.cards[workspace.ID] = workspace
			freshness := string(workspace.Presentation.Freshness)
			if m.refreshing[key] {
				freshness = "refreshing"
			} else if m.stale[key] {
				freshness = "stale · refresh failed"
			}
			card := kanban.Card{ID: workspace.ID, Title: workspace.ProjectName + " / " + workspace.Name, Subtitle: workspace.Provider + " · " + workspace.Presentation.Label, Detail: choose(workspace.TaskID, workspace.Branch), Meta: freshness}
			for i := range lanes {
				if lanes[i].ID == kanban.LaneID(workspace.Presentation.Lane) {
					lanes[i].Cards = append(lanes[i].Cards, card)
					break
				}
			}
		}
	}
	projectOrder := make(map[string]int, len(m.projects))
	for i, project := range m.projects {
		projectOrder[projectKey(project)] = i
	}
	for i := range lanes {
		sort.SliceStable(lanes[i].Cards, func(a, b int) bool {
			left, lok := m.cards[lanes[i].Cards[a].ID]
			right, rok := m.cards[lanes[i].Cards[b].ID]
			if lok && rok && !left.Presentation.ChangedAt.Equal(right.Presentation.ChangedAt) {
				return left.Presentation.ChangedAt.After(right.Presentation.ChangedAt)
			}
			if lok && rok && projectOrder[left.ProjectKey] != projectOrder[right.ProjectKey] {
				return projectOrder[left.ProjectKey] < projectOrder[right.ProjectKey]
			}
			return false
		})
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

func normalizeProjects(configured []Project) []Project {
	seen := make(map[string]bool, len(configured))
	projects := make([]Project, 0, len(configured))
	for _, project := range configured {
		root := workspaceinventory.CanonicalProjectPath(project.Path)
		if seen[root] {
			continue
		}
		seen[root] = true
		project.Path = root
		project.Key = root
		projects = append(projects, project)
	}
	return projects
}

func projectKey(project Project) string {
	if project.Key != "" {
		return project.Key
	}
	return clean(project.Path)
}

func cloneShellClaims(claims workspaceinventory.ShellClaims) workspaceinventory.ShellClaims {
	cloned := workspaceinventory.ShellClaims{Sessions: make(map[string]bool, len(claims.Sessions)), Owners: make(map[string]string, len(claims.Owners))}
	for value, present := range claims.Sessions {
		cloned.Sessions[value] = present
	}
	for value, owner := range claims.Owners {
		cloned.Owners[value] = owner
	}
	return cloned
}
func choose(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Keep the shared semantic dependency visible at this boundary: Overview cards
// are projections of agentstatus, not a second status reducer.
var _ agentstatus.LaneID
