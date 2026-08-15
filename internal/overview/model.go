// Package overview owns the app-level cross-project Overview model.
package overview

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/activitystore"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

const (
	minColumnWidth = 17
	cardHeight     = 4
	maxProjects    = 4
	maxCaptures    = 4
	livePollEvery  = 5 * time.Second
	readyPollEvery = 10 * time.Second
	idlePollEvery  = 30 * time.Second
)

type Project struct {
	Name, Path, Key string
	Index           int
}

type refreshPhase string

const (
	phaseIdentity  refreshPhase = "identity"
	phaseInventory refreshPhase = "inventory"
	phaseStatus    refreshPhase = "status"
)

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
	Generation int
	Projects   []Project
	Panes      []workspaceinventory.Pane
	LiveOnly   bool
	Err        error
}
type projectMsg struct {
	Generation int
	Project    Project
	Phase      refreshPhase
	Result     workspaceinventory.ProjectResult
}
type pollMsg struct{ Generation int }

func IsAsyncMessage(msg tea.Msg) bool {
	if IsSharedDiffMessage(msg) {
		return true
	}
	switch msg.(type) {
	case panesMsg, projectMsg, pollMsg, previewAutoScrollTickMsg, workspacePulseTickMsg,
		previewDocLoadedMsg, previewIssueLoadedMsg, previewHistoryLoadedMsg,
		renameShellDoneMsg, globalShellCreatedMsg, projectMutationRefreshMsg:
		return true
	default:
		return false
	}
}

// IsSharedDiffMessage reports whether msg is a workspacediff load result.
// These are the one async family this browser does not own: a project
// plugin's Diff pane hosts the same views and issues the same loads, so the
// result belongs to every host that is waiting on it. A caller that routes it
// here alone leaves those panes on "Loading diff…" forever — the same shape as
// the issueview.LoadedMsg the preview modal used to claim.
func IsSharedDiffMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case workspacediff.SnapshotMsg, workspacediff.CommitDetailMsg,
		workspacediff.RangeMsg, workspacediff.CommitFileDiffMsg:
		return true
	default:
		return false
	}
}

type Model struct {
	collector           workspaceinventory.Collector
	refreshCollector    workspaceinventory.Collector
	projects            []Project
	roots               []string
	generation          int
	requestID           uint64
	loading             bool
	tmuxErr             error
	results             map[string]workspaceinventory.ProjectResult
	projectErrors       map[string]error
	stale               map[string]bool
	completed           map[int]bool
	pending             []Project
	pendingInventory    []Project
	phase               refreshPhase
	identityProjects    map[int]Project
	inventoryOrder      []Project
	inventoryScheduled  map[string]bool
	inventoryProjects   map[string]Project
	inventoryResults    map[string]workspaceinventory.ProjectResult
	statusInputs        map[string]workspaceinventory.ProjectResult
	active              int
	currentPanes        []workspaceinventory.Pane
	shellClaims         workspaceinventory.ShellClaims
	liveOnly            bool
	ctx                 context.Context
	cancel              context.CancelFunc
	traceWriter         io.Writer
	cycleStart          time.Time
	configured          int
	firstResult         bool
	maxActive           int
	pollScheduled       bool
	configuredPaths     []string
	board               kanban.Component
	cards               map[string]workspaceinventory.Workspace
	agentCount          int
	compactScroll       int
	mouse               *mouse.Handler
	workspaces          workspacelist.Model
	workspacesMouse     *mouse.Handler
	sidebarWidth        int
	sidebarVisible      bool
	catalog             map[string]workspaceinventory.Workspace
	preview             previewState
	diff                workspacediff.View
	terminalConfig      tty.Config
	config              *config.Config
	width               int
	height              int
	previewSpecResolver func(string, string) (string, bool)

	// Working/blocked markers breathe on their own clock, independent of the
	// refresh poll. The generation lets a tick in flight be discarded.
	pulseFrame      int
	pulseScheduled  bool
	pulseGeneration uint64

	// A coalesced terminal wheel event that was held changed no visible state.
	// Reuse the preceding Workspaces frame once rather than rebuilding it.
	reuseWorkspacesViewOnce bool
	workspacesViewCache     string
	workspacesViewCacheW    int
	workspacesViewCacheH    int
	workspacesViewCacheOK   bool

	// showIdleWorktrees is the global-list visibility flag. Off by default;
	// the sort/filter fly-out is the only control that turns it on.
	showIdleWorktrees bool
	viewFlyout        *modal.Modal
	viewFlyoutOpen    bool
	viewFlyoutWidth   int
	viewFlyoutSortIdx int
	viewFlyoutMouse   *mouse.Handler

	pendingViews map[string]*pendingView
	// openSplit is the request-scoped --split axis override ("right"/"below").
	openSplit string

	renameOpen       bool
	renameWorkspace  workspaceinventory.Workspace
	renameInput      textinput.Model
	renameError      string
	renameModal      *modal.Modal
	renameModalWidth int
	renameMouse      *mouse.Handler

	createOpen         bool
	createProjectIndex int
	createProjectKey   string
	createNameInput    textinput.Model
	createError        string
	createBusy         bool
	createModal        *modal.Modal
	createModalWidth   int
	createMouse        *mouse.Handler
	pendingCreatedTmux string
}

// ActivityStorePath is overridable so tests never touch the user's state dir.
var ActivityStorePath = func() string {
	return filepath.Join(config.StateDir(), activitystore.FileName)
}

// Sidebar preference access is overridable so interaction tests can prove a
// drag release without reading or writing the developer's real state file.
var (
	loadWorkspaceSidebarWidth   = state.GetWorkspaceSidebarWidth
	saveWorkspaceSidebarWidth   = state.SetWorkspaceSidebarWidth
	loadShowIdleWorktrees       = state.GetShowIdleWorktrees
	saveShowIdleWorktrees       = state.SetShowIdleWorktrees
	loadPinnedWorkspaceIDs      = state.GetPinnedWorkspaceIDs
	savePinnedWorkspaceIDs      = state.SetPinnedWorkspaceIDs
	loadWorkspaceListSort       = state.GetWorkspaceListSort
	saveWorkspaceListSort       = state.SetWorkspaceListSort
	loadLastGlobalCreateProject = state.GetLastGlobalCreateProject
	saveLastGlobalCreateProject = state.SetLastGlobalCreateProject
)

func New(collector workspaceinventory.Collector) *Model {
	collector = collector.WithDefaults()
	// Restored idle state is what lets a card say "idle 3h" instead of "25s"
	// on the first cycle after launch, and lets a turn that finished while
	// Sidecar was closed still land in the done lane.
	if path := ActivityStorePath(); path != "" {
		collector = collector.SeedTrackers(activitystore.Load(path, time.Now()))
	}
	m := &Model{collector: collector, results: make(map[string]workspaceinventory.ProjectResult), projectErrors: make(map[string]error), stale: make(map[string]bool), completed: make(map[int]bool), cards: make(map[string]workspaceinventory.Workspace), catalog: make(map[string]workspaceinventory.Workspace), mouse: mouse.NewHandler(), workspacesMouse: mouse.NewHandler(), viewFlyoutMouse: mouse.NewHandler(), renameMouse: mouse.NewHandler(), createMouse: mouse.NewHandler(), sidebarWidth: defaultWorkspaceSidebarPercent, sidebarVisible: true, showIdleWorktrees: loadShowIdleWorktrees()}
	if savedWidth := loadWorkspaceSidebarWidth(); savedWidth > 0 {
		m.sidebarWidth = savedWidth
	}
	m.workspaces.SetEmptyText(workspacesEmptyText(m.showIdleWorktrees))
	m.workspaces.SetPinned(loadPinnedWorkspaceIDs())
	// The chosen order is as much a part of "where I left off" as the pins and
	// the sidebar width beside it. Without this the list reshuffled itself on
	// every launch, which is the one moment a user is least able to tell a
	// reset apart from something having actually changed.
	if mode, ok := workspacelist.SortFromLabel(loadWorkspaceListSort(), workspacelist.SortModes); ok {
		m.workspaces.SetSort(mode)
	}
	if value := os.Getenv("SIDECAR_OVERVIEW_TRACE"); value == "1" || value == "stderr" {
		m.traceWriter = os.Stderr
	}
	return m
}

// SetConfig hands the global host the same app-owned configuration project
// plugins receive, without instantiating or temporarily switching a plugin.
func (m *Model) SetConfig(cfg *config.Config) { m.config = cfg }

// persistActivity writes committed trackers after a completed cycle. Failure
// is silent by design: the store is a convenience, and a state directory that
// cannot be written should not interrupt the board.
func (m *Model) persistActivity() {
	path := ActivityStorePath()
	if path == "" {
		return
	}
	_ = activitystore.Save(path, m.collector.TrackerSnapshot(), time.Now())
}

func (m *Model) Start(projects []Project) tea.Cmd {
	return m.start(projects, "refresh")
}

// Ensure starts collection only when the shared catalog has nothing live
// behind it. The Agents board and the global Workspaces list are two
// projections of one cache: whichever becomes visible first starts the cycle,
// and the other reuses its results, its trackers, and its poll. A second
// collector here would double every project's tmux and Git fan-out for a view
// that already has the data.
func (m *Model) Ensure(projects []Project) tea.Cmd {
	if m.cancel == nil || !sameConfiguredProjects(m.configuredPaths, projects) {
		return m.start(projects, "refresh")
	}
	if m.loading || m.pollScheduled {
		return nil
	}
	return m.start(projects, "refresh")
}

func sameConfiguredProjects(paths []string, projects []Project) bool {
	if len(paths) != len(projects) {
		return false
	}
	for i, project := range projects {
		if paths[i] != project.Path {
			return false
		}
	}
	return true
}

func (m *Model) start(projects []Project, reason string) tea.Cmd {
	if m.cancel != nil {
		if m.pollScheduled {
			m.tracef("cycle generation=%d poll_cancel_requested", m.generation)
			m.pollScheduled = false
		}
		if m.loading || m.active > 0 {
			m.tracef("cycle generation=%d canceled active_projects=%d", m.generation, m.active)
		}
		m.cancel()
	}
	m.generation++
	m.requestID++
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.loading, m.tmuxErr = true, nil
	m.completed = make(map[int]bool)
	// Keep last-good cards on screen during a poll/refresh cycle. Relative age
	// already communicates freshness; a full-board "refreshing…" rewrite was
	// too noisy on the 5s live poll.
	if len(m.projects) > 0 {
		m.syncBoard()
	}
	m.cycleStart, m.configured, m.firstResult, m.maxActive = time.Now(), len(projects), false, 0
	m.configuredPaths = m.configuredPaths[:0]
	for _, project := range projects {
		m.configuredPaths = append(m.configuredPaths, project.Path)
	}
	m.tracef("cycle generation=%d reason=%s configured=%d start", m.generation, reason, len(projects))
	generation := m.generation
	ctx := m.ctx
	configured := append([]Project(nil), projects...)
	return func() tea.Msg {
		liveOnly := reason == "poll"
		panes, err := m.collector.ListPanes(ctx)
		return panesMsg{Generation: generation, Projects: configured, Panes: panes, LiveOnly: liveOnly, Err: err}
	}
}

func (m *Model) Stop() {
	if m.cancel != nil {
		if m.pollScheduled {
			m.tracef("cycle generation=%d poll_cancel_requested", m.generation)
			m.pollScheduled = false
		}
		if m.loading || m.active > 0 {
			m.tracef("cycle generation=%d canceled active_projects=%d", m.generation, m.active)
		}
		m.cancel()
		m.cancel = nil
	}
	m.generation++
	m.requestID++
	m.loading = false
	// Stopping the cycle stops the preview with it: a tab nobody is looking at
	// has no reason to retain a pane's producer or memory-only output.
	m.preview.visible = false
	m.releasePreview()
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

// Update handles one message and, on the way out, keeps the working/blocked
// marker animation armed. Arming here rather than at a single entry point is
// what makes the pulse unconditional: whatever brought a live row on screen —
// a refresh, a filter keystroke, scrolling, opening the tab — re-checks the
// clock instead of leaving the row frozen until the next refresh.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	cmd := m.update(msg)
	if pulse := m.pulseCmd(); pulse != nil {
		return tea.Batch(cmd, pulse)
	}
	return cmd
}

// workspacePulseTickMsg advances the shared marker animation by one frame.
type workspacePulseTickMsg struct{ generation uint64 }

func (m *Model) pulseCmd() tea.Cmd {
	if m.pulseScheduled || !m.workspaces.NeedsPulse() {
		return nil
	}
	m.pulseScheduled = true
	generation := m.pulseGeneration
	return tea.Tick(workspacelist.PulseInterval, func(time.Time) tea.Msg {
		return workspacePulseTickMsg{generation: generation}
	})
}

func (m *Model) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case workspacePulseTickMsg:
		if msg.generation != m.pulseGeneration {
			return nil
		}
		m.pulseScheduled = false
		m.pulseFrame++
		m.workspaces.SetPulseFrame(m.pulseFrame)
		return nil
	case panesMsg:
		if msg.Generation != m.generation {
			return nil
		}
		m.tmuxErr = msg.Err
		m.currentPanes = append(m.currentPanes[:0], msg.Panes...)
		m.liveOnly = msg.LiveOnly
		m.completed = make(map[int]bool, len(msg.Projects))
		m.active = 0
		if msg.LiveOnly {
			m.phase = phaseStatus
			m.pending = indexedProjects(msg.Projects)
			m.refreshCollector = m.collector.ForRefresh(maxCaptures, m.shellClaims)
		} else {
			m.phase = phaseIdentity
			m.pending = indexedProjects(msg.Projects)
			m.pendingInventory = nil
			m.identityProjects = make(map[int]Project, len(msg.Projects))
			m.inventoryOrder = nil
			m.inventoryScheduled = make(map[string]bool, len(msg.Projects))
			m.inventoryProjects = make(map[string]Project, len(msg.Projects))
			m.inventoryResults = make(map[string]workspaceinventory.ProjectResult, len(msg.Projects))
			m.refreshCollector = m.collector.ForRefresh(maxCaptures)
		}
		m.tracef("cycle generation=%d configured=%d tmux_inventories=1 phase=%s", m.generation, m.configured, m.phase)
		if len(m.pending) == 0 {
			return m.finishPhase()
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
		if msg.Phase != phaseIdentity && !m.firstResult {
			m.firstResult = true
			m.tracef("cycle generation=%d first_result_ms=%d", m.generation, time.Since(m.cycleStart).Milliseconds())
		}
		m.completed[msg.Project.Index] = true
		switch msg.Phase {
		case phaseIdentity:
			m.identityProjects[msg.Project.Index] = msg.Project
			if !m.inventoryScheduled[msg.Project.Key] {
				m.inventoryScheduled[msg.Project.Key] = true
				m.pendingInventory = append(m.pendingInventory, msg.Project)
			}
		case phaseInventory:
			m.inventoryProjects[msg.Project.Key] = msg.Project
			m.inventoryResults[msg.Project.Key] = msg.Result
			m.applyInventoryIncrement(msg.Project, msg.Result)
		default:
			m.applyStatusResult(msg.Result)
		}
		m.syncBoard()
		// The list's selection can move when incremental results arrive (the
		// first result selects a row at all), so the preview follows it here
		// rather than waiting for the user to press a key.
		preview := m.previewSync()
		if len(m.pendingInventory) > 0 || len(m.pending) > 0 || m.active > 0 {
			return tea.Batch(m.dispatchProjects(), preview)
		}
		return tea.Batch(m.finishPhase(), preview)
	case previewAutoScrollTickMsg:
		return m.advancePreviewAutoScroll(msg)
	case previewDocLoadedMsg:
		m.applyPreviewDocLoaded(msg)
		return nil
	case previewIssueLoadedMsg:
		m.applyPreviewIssueLoaded(msg)
		return nil
	case previewHistoryLoadedMsg:
		return m.applyPreviewHistory(msg)
	case workspacediff.SnapshotMsg:
		return m.applyDiffSnapshot(msg)
	case workspacediff.CommitDetailMsg:
		m.applyCommitDetail(msg)
		return nil
	case workspacediff.RangeMsg:
		return m.applyPreviewDiffRange(msg)
	case workspacediff.CommitFileDiffMsg:
		cmd := m.diff.ApplyCommitFileDiff(msg)
		return tea.Batch(cmd, m.applyPreviewDiffFile(msg))
	case renameShellDoneMsg:
		m.applyRenameShell(msg)
		return nil
	case globalShellCreatedMsg:
		m.createBusy = false
		if msg.Err != nil {
			m.createError = msg.Err.Error()
			m.createModal = nil
			return nil
		}
		m.closeCreateShell()
		return m.refreshProjectAfterMutation(msg.Project)
	case projectMutationRefreshMsg:
		return m.applyProjectMutationRefresh(msg)
	case uirequest.RequestMsg:
		return m.handleUIRequest(msg.Request)
	case pollMsg:
		if msg.Generation != m.generation || m.ctx == nil {
			m.tracef("cycle generation=%d poll_drained stale_generation=%d", m.generation, msg.Generation)
			return nil
		}
		m.pollScheduled = false
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
		case mouse.ActionScrollUp, mouse.ActionScrollDown:
			delta := action.Delta
			if delta == 0 {
				if action.Type == mouse.ActionScrollUp {
					delta = -1
				} else {
					delta = 1
				}
			}
			m.board.MoveInColumn(region.Column, delta)
		}
	}
	return nil
}

func (m *Model) dispatchProjects() tea.Cmd {
	cmds := make([]tea.Cmd, 0, maxProjects)
	for m.active < maxProjects && (len(m.pendingInventory) > 0 || len(m.pending) > 0) {
		phase := m.phase
		var project Project
		if m.phase == phaseIdentity && len(m.pendingInventory) > 0 {
			project = m.pendingInventory[0]
			m.pendingInventory = m.pendingInventory[1:]
			phase = phaseInventory
		} else {
			project = m.pending[0]
			m.pending = m.pending[1:]
		}
		m.active++
		m.maxActive = max(m.maxActive, m.active)
		generation, ctx := m.generation, m.ctx
		roots := append([]string(nil), m.roots...)
		inventory := append([]workspaceinventory.Pane(nil), m.currentPanes...)
		collector := m.refreshCollector
		previous := m.results[projectKey(project)]
		if !m.liveOnly && phase == phaseStatus {
			previous = m.statusInputs[projectKey(project)]
		}
		cmds = append(cmds, func() tea.Msg {
			if phase == phaseIdentity {
				project = normalizeProject(project)
				return projectMsg{Generation: generation, Project: project, Phase: phase, Result: workspaceinventory.ProjectResult{ProjectKey: project.Key, ProjectName: project.Name, ProjectRoot: project.Path}}
			}
			if phase == phaseInventory {
				return projectMsg{Generation: generation, Project: project, Phase: phase, Result: collector.CollectProjectInventory(ctx, project.Name, project.Path)}
			}
			return projectMsg{Generation: generation, Project: project, Phase: phase, Result: collector.RefreshProjectStatus(ctx, previous, roots, inventory)}
		})
	}
	return tea.Batch(cmds...)
}

func (m *Model) finishPhase() tea.Cmd {
	if m.phase == phaseIdentity {
		seen := make(map[string]bool, len(m.identityProjects))
		m.inventoryOrder = make([]Project, 0, len(m.identityProjects))
		for index := 0; index < m.configured; index++ {
			project, ok := m.identityProjects[index]
			if !ok || seen[project.Key] {
				continue
			}
			seen[project.Key] = true
			project.Index = len(m.inventoryOrder)
			m.inventoryOrder = append(m.inventoryOrder, project)
		}
		m.tracef("cycle generation=%d identities=%d unique=%d inventory_complete", m.generation, m.configured, len(m.inventoryOrder))
		m.phase = phaseInventory
	}
	if m.phase == phaseInventory {
		seen := make(map[string]bool, len(m.inventoryResults))
		projects := make([]Project, 0, len(m.inventoryResults))
		claimResults := make([]workspaceinventory.ProjectResult, 0, len(m.inventoryResults))
		m.statusInputs = make(map[string]workspaceinventory.ProjectResult, len(m.inventoryResults))
		for _, ordered := range m.inventoryOrder {
			if _, ok := m.inventoryProjects[ordered.Key]; !ok {
				continue
			}
			project := ordered
			result := withProjectIdentity(m.inventoryResults[ordered.Key], project)
			if seen[result.ProjectKey] {
				continue
			}
			seen[result.ProjectKey] = true
			projects = append(projects, project)
			m.statusInputs[result.ProjectKey] = result
			if previous, ok := m.results[result.ProjectKey]; ok {
				m.results[result.ProjectKey] = withProjectIdentity(previous, project)
			}
			claimResult := result
			if result.Err != nil {
				if previous, ok := m.results[result.ProjectKey]; ok && len(previous.Workspaces) > 0 {
					claimResult = previous
				}
			}
			claimResults = append(claimResults, claimResult)
		}
		m.projects = projects
		m.roots = m.roots[:0]
		for _, project := range projects {
			m.roots = append(m.roots, project.Path)
		}
		for key := range m.results {
			if !seen[key] {
				delete(m.results, key)
				delete(m.projectErrors, key)
				delete(m.stale, key)
			}
		}
		m.shellClaims = workspaceinventory.BuildShellClaims(claimResults)
		m.refreshCollector = m.refreshCollector.WithShellClaims(m.shellClaims)
		m.phase = phaseStatus
		m.pending = append(m.pending[:0], projects...)
		m.completed = make(map[int]bool, len(projects))
		m.active = 0
		m.tracef("cycle generation=%d deduped=%d phase=status", m.generation, len(projects))
		m.syncBoard()
		if len(m.pending) > 0 {
			return m.dispatchProjects()
		}
	}
	m.loading = false
	m.refreshCollector.CommitTrackers()
	m.persistActivity()
	metrics := m.refreshCollector.Metrics()
	m.tracef("cycle generation=%d complete_ms=%d project_ops=%d captures=%d max_project_concurrency=%d max_capture_concurrency=%d", m.generation, time.Since(m.cycleStart).Milliseconds(), metrics.ProjectOps, metrics.Captures, m.maxActive, metrics.MaxCaptures)
	m.syncBoard()
	return m.pollCmd()
}

func (m *Model) applyInventoryIncrement(project Project, result workspaceinventory.ProjectResult) {
	key := result.ProjectKey
	if !containsProject(m.projects, key) {
		m.projects = append(m.projects, project)
	}
	if result.Err != nil {
		m.applyFailure(key, result, result.Err)
		return
	}
	if previous, ok := m.results[key]; !ok || previous.Err != nil {
		m.results[key] = result
	}
}

func withProjectIdentity(result workspaceinventory.ProjectResult, project Project) workspaceinventory.ProjectResult {
	result.ProjectKey = project.Key
	result.ProjectName = project.Name
	result.ProjectRoot = project.Path
	workspaces := append([]workspaceinventory.Workspace(nil), result.Workspaces...)
	for i := range workspaces {
		workspaces[i].ProjectKey = project.Key
		workspaces[i].ProjectName = project.Name
		workspaces[i].ProjectRoot = project.Path
	}
	result.Workspaces = workspaces
	return result
}

func (m *Model) applyStatusResult(result workspaceinventory.ProjectResult) {
	key := result.ProjectKey
	if m.tmuxErr != nil {
		m.applyFailure(key, result, m.tmuxErr)
		return
	}
	if result.Err != nil {
		m.applyFailure(key, result, result.Err)
		return
	}
	m.results[key] = result
	delete(m.projectErrors, key)
	delete(m.stale, key)
}

func (m *Model) applyFailure(key string, result workspaceinventory.ProjectResult, err error) {
	if previous, ok := m.results[key]; ok && previous.Err == nil {
		for i := range previous.Workspaces {
			previous.Workspaces[i].Presentation = stalePresentation(previous.Workspaces[i].Presentation)
		}
		m.results[key] = previous
		m.stale[key] = true
	} else {
		m.results[key] = result
		m.stale[key] = false
	}
	m.projectErrors[key] = err
}

func stalePresentation(p agentstatus.Presentation) agentstatus.Presentation {
	p.Freshness = agentstatus.FreshnessStale
	p.Attention = false
	p.Lane = agentstatus.LanePaused
	p.Label = "stale"
	p.Icon = "?"
	return p
}

func indexedProjects(projects []Project) []Project {
	indexed := append([]Project(nil), projects...)
	for i := range indexed {
		indexed[i].Index = i
	}
	return indexed
}

func containsProject(projects []Project, key string) bool {
	for _, project := range projects {
		if project.Key == key {
			return true
		}
	}
	return false
}

func (m *Model) tracef(format string, args ...any) {
	if m.traceWriter != nil {
		_, _ = fmt.Fprintf(m.traceWriter, "overview "+format+"\n", args...)
	}
}

func (m *Model) pollCmd() tea.Cmd {
	m.pollScheduled = true
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

// pollInterval trades refresh cost against how long a stale badge can persist.
// Working and blocked panes change often enough to want the live cadence. An
// idle or done pane is not inert, though: it is an agent sitting at a prompt
// that can start a turn at any moment, and polling those at the quiet cadence
// meant an all-idle board could take a full idlePollEvery to notice work had
// begun. Only a board with nothing live at all earns the quiet cadence.
func (m *Model) pollInterval() time.Duration {
	interval := idlePollEvery
	for _, result := range m.results {
		for _, workspace := range result.Workspaces {
			switch workspace.Presentation.Lane {
			case agentstatus.LaneWorking, agentstatus.LaneBlocked:
				return livePollEvery
			case agentstatus.LaneIdle, agentstatus.LaneDone:
				interval = readyPollEvery
			}
		}
	}
	return interval
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
	result := m.board.Render(kanban.RenderOptions{Width: width, Height: height, Header: "Agent Overview", HeaderRight: m.summary(), MinColumnWidth: minColumnWidth, CardHeight: cardHeight})
	m.mouse.Clear()
	if result.Compact {
		return m.renderCompact(width, height)
	}
	for _, region := range result.Regions {
		m.mouse.HitMap.AddRect("overview-card", region.X, region.Y, region.W, region.H, region)
	}
	return result.View
}

// summary is the header-right text. Refreshes are frequent and mostly
// instantaneous, so a "Loading n/m" counter there reads as flicker and pulls
// the eye for nothing. Loading is not an abnormal state: while it runs we keep
// showing the last known counts (nothing at all on the very first load), and
// only genuinely abnormal state — tmux being unavailable — replaces them.
func (m *Model) summary() string {
	if m.tmuxErr != nil {
		return "tmux unavailable"
	}
	if m.loading && len(m.results) == 0 {
		return ""
	}
	return fmt.Sprintf("%d projects · %d agents", len(m.results), m.agentCount)
}

// cardOrder is the sort key syncBoard attaches to every card it builds:
// project group in configured project order, then most-recent-first within
// the group. Error cards carry a zero ChangedAt, which sorts them last
// within their project group.
type cardOrder struct {
	project   int
	changedAt time.Time
}

// boardLane is a shared lane as this board draws it: the theme's lane colours,
// and CellReady as a sentinel meaning neither loading nor errored (syncBoard
// converts card-less ready lanes to CellEmpty). Loading and error states are
// set over it per refresh.
func boardLane(lane agentstatus.LaneID) kanban.Lane {
	built := kanban.AgentLane(lane, kanban.ThemeLanePalette)
	built.State = kanban.CellReady
	return built
}

func (m *Model) syncBoard() {
	// The lanes are the shared definition's — the project board draws the same
	// ones — in this board's own colours and cell state. The count is left to
	// the Kanban component, which appends its own.
	lanes := []kanban.Lane{
		boardLane(agentstatus.LaneWorking),
		boardLane(agentstatus.LaneBlocked),
		boardLane(agentstatus.LaneDone),
		boardLane(agentstatus.LaneIdle),
		boardLane(agentstatus.LanePaused),
	}
	m.cards = make(map[string]workspaceinventory.Workspace)
	order := make(map[string]cardOrder)
	now := time.Now()
	for i, project := range m.projects {
		key := projectKey(project)
		result, loaded := m.results[key]
		if !loaded {
			if m.loading {
				lanes[4].State, lanes[4].Message = kanban.CellLoading, "Loading "+project.Name+"…"
			}
			continue
		}
		if result.Err != nil && len(result.Workspaces) == 0 {
			id := "error:" + key
			order[id] = cardOrder{project: i}
			card := kanban.Card{ID: id, Lines: errorCardLines(project.Name, result.Err)}
			lanes[4].Cards = append(lanes[4].Cards, card)
			continue
		}
		for _, workspace := range result.Workspaces {
			// The board is the agent-only projection of the shared catalog.
			// Untyped shell definitions are live-discovery candidates, and plain
			// worktrees have no agent semantics at all; both belong to the
			// Workspaces list, not to a Kanban lane.
			if !workspace.HasAgent() {
				continue
			}
			m.cards[workspace.ID] = workspace
			order[workspace.ID] = cardOrder{project: i, changedAt: workspace.Presentation.ChangedAt}
			card := kanban.Card{ID: workspace.ID, Lines: cardLines(workspace, m.stale[key], now)}
			for i := range lanes {
				if lanes[i].ID == kanban.LaneID(workspace.Presentation.Lane) {
					lanes[i].Cards = append(lanes[i].Cards, card)
					break
				}
			}
		}
	}
	m.agentCount = len(m.cards)
	for i := range lanes {
		sort.SliceStable(lanes[i].Cards, func(a, b int) bool {
			left, right := order[lanes[i].Cards[a].ID], order[lanes[i].Cards[b].ID]
			if left.project != right.project {
				return left.project < right.project
			}
			return left.changedAt.After(right.changedAt)
		})
	}
	for i := range lanes {
		if len(lanes[i].Cards) == 0 && lanes[i].State == kanban.CellReady {
			lanes[i].State = kanban.CellEmpty
		}
	}
	m.board.SetBoard(kanban.Board{Lanes: lanes})
	// One collection, two projections: the list is rebuilt from the same
	// results map, in the same pass, so the tabs cannot disagree.
	m.syncWorkspaces()
}

// spineGlyph is the per-kind left accent every content line carries: solid
// for a worktree, hairline for a shell. Redundant with kindGlyph on purpose —
// colourblind-safe.
func spineGlyph(kind workspaceinventory.Kind) string {
	if kind == workspaceinventory.KindShell {
		return "▏"
	}
	return "▌"
}

func kindGlyph(kind workspaceinventory.Kind) string {
	if kind == workspaceinventory.KindShell {
		return workspacelist.KindGlyph(workspacelist.KindShell)
	}
	return workspacelist.KindGlyph(workspacelist.KindWorktree)
}

// cardLines builds the three styled content rows for a live workspace card.
// stale reflects the owning project's freshness tracker; abnormal
// Presentation.Freshness (e.g. "unavailable") falls through to a plain word
// when the tracker does not apply. Mid-cycle polls no longer rewrite cards
// with a "refreshing…" flash — relative age already communicates freshness.
func cardLines(workspace workspaceinventory.Workspace, stale bool, now time.Time) []kanban.Line {
	hue := styles.ProjectHue(workspace.ProjectKey)
	spine := spineGlyph(workspace.Kind)
	dormant := isDormant(workspace.Presentation, now)
	nameColor := styles.TextPrimary
	if dormant {
		nameColor = styles.TextMuted
	}
	line1 := kanban.Line{Spans: []kanban.Span{
		{Text: spine, Foreground: hue},
		{Text: " " + workspace.ProjectName, Foreground: hue, Bold: true},
		{Text: " " + kindGlyph(workspace.Kind), Foreground: styles.TextMuted},
		{Text: " " + workspace.Name, Foreground: nameColor},
	}}

	status := workspace.Presentation.Label
	if workspace.Presentation.Attention {
		status = "▲ " + status
	}
	// "~" marks an idle this provider inferred from silence rather than read
	// from a completion marker, so a card that never reaches done reads as a
	// known limitation instead of a missing signal.
	if workspace.Presentation.Inferred {
		status += " ~"
	}
	if age := relativeAge(workspace.Presentation.ChangedAt, now); age != "" {
		status += " · " + age
	}
	statusColor := styles.LaneColor(string(workspace.Presentation.Lane))
	if dormant {
		statusColor = styles.TextMuted
	}
	line2 := kanban.Line{Spans: []kanban.Span{
		{Text: spine, Foreground: hue},
		{Text: " " + styles.AgentLabel(workspace.Provider), Foreground: styles.AgentColor(workspace.Provider), Background: styles.AgentChipFill()},
		{Text: " " + status, Foreground: statusColor, Bold: workspace.Presentation.Lane == agentstatus.LaneDone},
	}}

	// A shell has neither task nor branch; its detail line stays empty rather
	// than carrying the tmux session name, which is an identity key only.
	parts := make([]string, 0, 2)
	if detail := choose(workspace.TaskID, workspace.Branch); detail != "" {
		parts = append(parts, detail)
	}
	switch {
	case stale:
		parts = append(parts, "stale")
	case workspace.Presentation.Freshness != "" && workspace.Presentation.Freshness != agentstatus.FreshnessCurrent:
		parts = append(parts, string(workspace.Presentation.Freshness))
	}
	line3 := kanban.Line{Spans: []kanban.Span{{Text: spine, Foreground: hue}}}
	if len(parts) > 0 {
		line3.Spans = append(line3.Spans, kanban.Span{Text: " " + strings.Join(parts, " · "), Foreground: styles.TextMuted})
	}
	return []kanban.Line{line1, line2, line3}
}

// errorCardLines renders a project-unavailable card with a muted spine —
// there is no live workspace to hang a project hue off of.
func errorCardLines(projectName string, err error) []kanban.Line {
	spine := spineGlyph(workspaceinventory.KindWorktree)
	return []kanban.Line{
		{Spans: []kanban.Span{{Text: spine, Foreground: styles.TextMuted}, {Text: " " + projectName, Foreground: styles.TextPrimary}}},
		{Spans: []kanban.Span{{Text: spine, Foreground: styles.TextMuted}, {Text: " project unavailable", Foreground: styles.TextMuted}}},
		{Spans: []kanban.Span{{Text: spine, Foreground: styles.TextMuted}, {Text: " " + err.Error(), Foreground: styles.TextMuted}}},
	}
}

// DormantAfter is when an idle session stops competing for attention. The idle
// lane holds both "finished a minute ago" and "untouched since Tuesday";
// dimming past this threshold separates them without a sixth lane.
const DormantAfter = time.Hour

func isDormant(p agentstatus.Presentation, now time.Time) bool {
	if p.Lane != agentstatus.LaneIdle || p.ChangedAt.IsZero() {
		return false
	}
	return now.Sub(p.ChangedAt) > DormantAfter
}

// relativeAge formats the gap between changedAt and now as the small units
// the board cards use: "12s", "3m", "1h", "2d". Anything under 5s reads
// "now"; a zero changedAt renders nothing.
func relativeAge(changedAt, now time.Time) string {
	if changedAt.IsZero() {
		return ""
	}
	d := now.Sub(changedAt)
	if d < 0 {
		d = 0
	}
	switch {
	case d < 5*time.Second:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func (m *Model) renderCompact(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	type compactCard struct {
		column, row int
		lane        string
		card        kanban.Card
	}
	board := m.board.Board()
	items := make([]compactCard, 0)
	selectedIndex := -1
	selection := m.board.Selection()
	for column, lane := range board.Lanes {
		for row, card := range lane.Cards {
			if column == selection.Column && row == selection.Row {
				selectedIndex = len(items)
			}
			items = append(items, compactCard{column: column, row: row, lane: lane.Label, card: card})
		}
	}
	visibleRows := max(0, height-1)
	if selectedIndex >= 0 && visibleRows > 0 {
		if selectedIndex < m.compactScroll {
			m.compactScroll = selectedIndex
		} else if selectedIndex >= m.compactScroll+visibleRows {
			m.compactScroll = selectedIndex - visibleRows + 1
		}
	}
	maxScroll := max(0, len(items)-visibleRows)
	m.compactScroll = min(max(0, m.compactScroll), maxScroll)

	header := styles.Title.Render("Agent Overview") + "  " + styles.Muted.Render(m.summary())
	lines := []string{fitCompactLine(header, width)}
	end := min(len(items), m.compactScroll+visibleRows)
	for index := m.compactScroll; index < end; index++ {
		item := items[index]
		line := fitCompactLine(compactCardText(item.lane, item.card, m.cards[item.card.ID]), width)
		if index == selectedIndex {
			// Same darker fill as the board kanban: multi-coloured card text
			// washes out on ListItemSelected's BgTertiary lift.
			line = styles.CardSelected.Render(line)
		}
		lines = append(lines, line)
		y := len(lines) - 1
		m.mouse.HitMap.AddRect("overview-card", 0, y, width, 1, kanban.HitRegion{Kind: kanban.RegionCard, Column: item.column, Row: item.row, CardID: item.card.ID, X: 0, Y: y, W: width, H: 1})
	}
	if len(items) == 0 && len(lines) < height {
		lines = append(lines, fitCompactLine(styles.Muted.Render(" No agent-backed workspaces found"), width))
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines[:height], "\n")
}

// compactCardText renders the one-line compact fallback for a card, picking
// up the project hue and agent colour when a live workspace backs the card.
// Cards built outside syncBoard (tests, or a card with no Lines) fall back to
// the plain Title/Subtitle fields.
func compactCardText(lane string, card kanban.Card, workspace workspaceinventory.Workspace) string {
	if workspace.ID == "" {
		return fmt.Sprintf(" %-15s %s  %s", lane, card.Title, card.Subtitle)
	}
	project := lipgloss.NewStyle().Foreground(styles.ProjectHue(workspace.ProjectKey)).Render(workspace.ProjectName + " / " + workspace.Name)
	agent := lipgloss.NewStyle().Foreground(styles.AgentColor(workspace.Provider)).Render(styles.AgentLabel(workspace.Provider))
	return fmt.Sprintf(" %-15s %s  %s · %s", lane, project, agent, workspace.Presentation.Label)
}

func fitCompactLine(line string, width int) string {
	line = ansi.Truncate(line, width, "")
	if gap := width - ansi.StringWidth(line); gap > 0 {
		line += strings.Repeat(" ", gap)
	}
	return line
}

func clean(path string) string { return workspaceinventory.CanonicalPath(path) }

func normalizeProject(project Project) Project {
	root := workspaceinventory.CanonicalProjectPath(project.Path)
	project.Path = root
	project.Key = root
	return project
}

func projectKey(project Project) string {
	if project.Key != "" {
		return project.Key
	}
	return clean(project.Path)
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
