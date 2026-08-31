package workspace

import (
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/tty"
)

const terminalTraceEnv = "SIDECAR_TERMINAL_TRACE"

type terminalOwnershipLease struct {
	mu         sync.RWMutex
	generation atomic.Uint64
	active     atomic.Bool
}

// These seams keep lifecycle barrier tests on the production command paths
// while replacing only the final tmux geometry/capture effect.
var (
	workspaceQueryPaneSize       = tty.QueryPaneSize
	workspaceResizeTmuxPane      = tty.ResizeTmuxPane
	workspaceTouchGeometryLease  = tty.TouchGeometryLease
	workspaceHoldGeometryLease   = tty.HoldGeometryLease
	workspaceReleaseGeometryHold = tty.ReleaseGeometryHold
	workspaceCapturePaneRange    = tty.CapturePaneRange
	workspaceSendSGRWheel        = tty.SendSGRWheel
	workspaceBeforeDeactivate    func()
)

// traceTerminalCapture records transport metadata only. It deliberately omits
// terminal content, commands, paths, titles, and provider payloads.
func traceTerminalCapture(logger *slog.Logger, surface, role, reason string, generation int) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(terminalTraceEnv))) {
	case "1", "true", "yes", "on":
	default:
		return
	}
	if logger == nil {
		return
	}
	logger.Info("terminal capture trace",
		"surface", surface,
		"role", role,
		"reason", reason,
		"generation", generation,
	)
}

// workspaceTerminalRole names the two independently visible terminal surfaces
// owned by Workspaces. Transport, model presentation, capture fallback, and
// delivery generations live in tty.Model; this package owns only which target
// each surface projects and where it is laid out.
type workspaceTerminalRole uint8

const (
	workspaceTerminalPrimary workspaceTerminalRole = iota + 1
	workspaceTerminalPanel
)

type workspaceTerminalTarget = termpanes.Target

// Update routes terminal-owned lifecycle messages through the shared component
// before applying workspace product state. Ordinary keys and mouse events are
// routed explicitly by interactive mode so a visible preview never captures
// input intended for workspace navigation.
func (p *Plugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	if _, sidebarOnly := msg.(activityAnimationTickMsg); !sidebarOnly {
		p.bumpProjectPreviewRevision()
	}
	_, seedingLaneTrackers := msg.(notify.SeedLaneTrackersMsg)
	// Hold must be on the models before a forwarded deferredResizeMsg can
	// assert, and again after mouse handling so reconcile sees the drag state.
	p.syncTerminalResizeHold()
	// Whatever the last render's leaf sizing answered is dispatched here, the
	// first update that has a runtime to dispatch it with.
	cmds := p.takePaneSizeCmds()
	if cmd := p.takePaneRestoreCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	switch msg.(type) {
	case tea.FocusMsg:
		p.applicationFocused = true
		p.setTerminalFocus(true)
	case tea.BlurMsg:
		p.applicationFocused = false
		p.setTerminalFocus(false)
	case tea.KeyPressMsg, tea.PasteMsg, tea.MouseMsg:
		// Input is routed by workspace interactive mode after its own navigation,
		// selection, panel-toggle, and coordinate policy has run.
	default:
		cmds = append(cmds, p.updateTerminalModels(msg)...)
	}

	// Live-refresh messages are handled before the product update so a watcher
	// signal cannot be mistaken for anything else, and so the refresh commands
	// they produce batch with the rest of this tick.
	if cmd, handled := p.handleLiveWatchMsg(msg); handled {
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	} else {
		_, cmd := p.update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	// The rule a pane search lives by, swept once per update rather than trusted
	// to each of the dozen places that write focus: the surface belongs to the
	// pane holding the keyboard. setFocusTarget applies it for every gesture;
	// this catches the paths that move focus without it — a shell selection
	// landing, a layout being restored — so none of them can leave a surface
	// drawn over a pane that no longer takes keys.
	if cmd := p.closeUnfocusedDocSearches(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	p.syncTerminalResizeHold()
	cmds = append(cmds, p.reconcileTerminalModels()...)
	p.syncTerminalModels()
	// Swept once per update for the same reason as the focus rule above: the
	// watch set must match the open pane set, and there are too many places that
	// open, close, retarget or restore a pane to trust each of them to say so.
	if cmd := p.reconcileLiveWatches(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// Agent lane transitions are swept here for the same reason: too many paths
	// mutate an agent's activity to trust each of them to notice a transition.
	if !seedingLaneTrackers {
		if cmd := p.notifyAgentTransitions(p.now()); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return p, tea.Batch(cmds...)
}

func (p *Plugin) newWorkspaceTerminal(role workspaceTerminalRole) *tty.Model {
	config := p.terminalConfig()
	config.ScrollbackLines = outputBufferCap
	model := tty.New(&config)
	// tty.New treats an empty AttachKey as "use default". Honour the resolved
	// empty chord so ctrl+] stays the pane's when full attach is off.
	model.Config.AttachKey = config.AttachKey
	model.SetResizeDebounce(p.resizeDebounce())
	model.SetHooks(p.terminalHooks(role))
	return model
}

func (p *Plugin) applyResizeDebounceToTerminals() {
	d := p.resizeDebounce()
	if p.primaryTermPane().Terminal != nil {
		p.primaryTermPane().Terminal.SetResizeDebounce(d)
	}
	if p.requireShellTermPane().Terminal != nil {
		p.requireShellTermPane().Terminal.SetResizeDebounce(d)
	}
}

// terminalHooks is everything this surface owns about a live pane, said once, to
// the component that owns the rest. It is one value rather than field-by-field
// assignment so a hook cannot be added to one embedding host and forgotten in
// the other — the global browser states its contract the same way.
func (p *Plugin) terminalHooks(role workspaceTerminalRole) tty.Hooks {
	// A split terminal's session end closes its leaf; the primary's does not.
	// Routing both through the same component hook is what keeps "the pane
	// died" one signal with two surface answers rather than two detectors.
	sessionEnded := p.noteSessionEnded
	if role == workspaceTerminalPanel {
		sessionEnded = p.noteShellLeafSessionEnded
	}
	return tty.Hooks{
		OnKey:          p.interactiveKey,
		BeforeSend:     p.beforeInteractiveSend,
		OnExit:         p.leaveInteractiveMode,
		OnAttach:       p.attachFromInteractive,
		OnSessionEnded: sessionEnded,
		// This surface draws the pane whether or not the user is typing into it,
		// so leaving the mode releases the keyboard and nothing else: closing here
		// would drop the loaded scrollback the user just read and reconciliation
		// would reopen the pane with an empty buffer on the same update.
		ExitAction: tty.ExitReleasesInput,
	}
}

func (p *Plugin) resetTerminalModels() {
	if p.primaryTermPane().Terminal != nil {
		p.primaryTermPane().Terminal.Close()
	}
	if p.requireShellTermPane().Terminal != nil {
		p.requireShellTermPane().Terminal.Close()
	}
	p.primaryTermPane().Terminal = p.newWorkspaceTerminal(workspaceTerminalPrimary)
	p.requireShellTermPane().Terminal = p.newWorkspaceTerminal(workspaceTerminalPanel)
	p.primaryTermPane().Target = workspaceTerminalTarget{}
	p.requireShellTermPane().Target = workspaceTerminalTarget{}
}

func (p *Plugin) stopTerminalModels() {
	if p.primaryTermPane().Terminal != nil {
		p.primaryTermPane().Terminal.Close()
	}
	if p.requireShellTermPane().Terminal != nil {
		p.requireShellTermPane().Terminal.Close()
	}
	p.primaryTermPane().Target = workspaceTerminalTarget{}
	p.requireShellTermPane().Target = workspaceTerminalTarget{}
}

func (p *Plugin) activateTerminalOwnership() {
	lease := p.ensureTerminalOwnershipLease()
	lease.mu.Lock()
	defer lease.mu.Unlock()
	lease.generation.Add(1)
	lease.active.Store(true)
}

func (p *Plugin) deactivateTerminalOwnership() {
	// Publish revocation before closing models or invalidating queues so a
	// concurrently starting command cannot slip a capture/resize through the
	// synchronous project-to-global handoff.
	if lease := p.terminalOwnership; lease != nil {
		if workspaceBeforeDeactivate != nil {
			workspaceBeforeDeactivate()
		}
		lease.mu.Lock()
		lease.active.Store(false)
		lease.generation.Add(1)
		lease.mu.Unlock()
	}
	p.pollScheduler.Reset()
	p.sessionValidationGeneration++
	p.sessionValidationScheduled = false
	p.cancelDeferredPaneResize()
	p.cancelAllTerminalHistoryLoads()
	p.stopTerminalModels()
}

func (p *Plugin) currentTerminalOwnership() uint64 {
	lease := p.ensureTerminalOwnershipLease()
	lease.mu.RLock()
	if !lease.active.Load() {
		lease.mu.RUnlock()
		// A few construction paths (and lightweight host/test embeddings) set the
		// established Plugin focus bit before the first reconciliation rather than
		// calling SetFocused. Adopt that visible state once. Hide publishes
		// focused=false before revocation, so stale async work cannot use this path
		// to resurrect a covered surface.
		if !p.focused {
			return 0
		}
		lease.mu.Lock()
		defer lease.mu.Unlock()
		if lease.active.Load() {
			return lease.generation.Load()
		}
		lease.generation.Add(1)
		lease.active.Store(true)
		return lease.generation.Load()
	}
	generation := lease.generation.Load()
	lease.mu.RUnlock()
	return generation
}

func (p *Plugin) ownsTerminalOwnership(generation uint64) bool {
	lease := p.terminalOwnership
	if lease == nil || generation == 0 {
		return false
	}
	lease.mu.RLock()
	defer lease.mu.RUnlock()
	return lease.active.Load() && lease.generation.Load() == generation
}

func (p *Plugin) withTerminalOwnership(generation uint64, run func() tea.Msg) tea.Msg {
	release, ok := p.acquireTerminalOwnership(generation)
	if !ok {
		return nil
	}
	defer release()
	return run()
}

func (p *Plugin) acquireTerminalOwnership(generation uint64) (func(), bool) {
	lease := p.terminalOwnership
	if lease == nil || generation == 0 {
		return nil, false
	}
	lease.mu.RLock()
	if !lease.active.Load() || lease.generation.Load() != generation {
		lease.mu.RUnlock()
		return nil, false
	}
	return lease.mu.RUnlock, true
}

func (p *Plugin) ensureTerminalOwnershipLease() *terminalOwnershipLease {
	if p.terminalOwnership == nil {
		p.terminalOwnership = &terminalOwnershipLease{}
	}
	return p.terminalOwnership
}

func (p *Plugin) terminalOwnershipIsActive() bool {
	lease := p.terminalOwnership
	if lease == nil {
		return false
	}
	lease.mu.RLock()
	defer lease.mu.RUnlock()
	return lease.active.Load()
}

// noteTerminalMouseActivity records a mouse event against both terminal
// surfaces. It is deliberately not scoped to whichever one is live: the gate it
// feeds asks whether a mouse event reached this host at all.
func (p *Plugin) noteTerminalMouseActivity() {
	if p.primaryTermPane().Terminal != nil {
		p.primaryTermPane().Terminal.NoteMouseActivity()
	}
	if p.requireShellTermPane().Terminal != nil {
		p.requireShellTermPane().Terminal.NoteMouseActivity()
	}
}

func (p *Plugin) setTerminalFocus(focused bool) {
	if p.primaryTermPane().Terminal != nil {
		p.primaryTermPane().Terminal.SetFocused(focused && p.focused)
	}
	if p.requireShellTermPane().Terminal != nil {
		p.requireShellTermPane().Terminal.SetFocused(focused && p.focused)
	}
}

func (p *Plugin) updateTerminalModels(msg tea.Msg) []tea.Cmd {
	var cmds []tea.Cmd
	if p.primaryTermPane().Terminal != nil {
		if cmd := p.primaryTermPane().Terminal.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if p.requireShellTermPane().Terminal != nil {
		if cmd := p.requireShellTermPane().Terminal.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func (p *Plugin) terminalOutputSurfaceVisible() bool {
	if !p.focused {
		return false
	}
	if p.viewMode != ViewModeList && p.viewMode != ViewModeInteractive {
		return false
	}
	return true
}

func (p *Plugin) desiredPanelTerminal() (workspaceTerminalTarget, bool) {
	if !p.shellLeafVisible() || !p.terminalOutputSurfaceVisible() ||
		p.requireShellTermPane().Session == "" || p.requireShellTermPane().PaneID == "" {
		return workspaceTerminalTarget{}, false
	}
	width, height, ok := p.calculateTermPanelDimensions()
	if !ok {
		return workspaceTerminalTarget{}, false
	}
	width = p.terminalContentWidth(width)
	return workspaceTerminalTarget{
		Session: p.requireShellTermPane().Session, Pane: p.requireShellTermPane().PaneID,
		Width: width, Height: height, Source: "panel", SourceID: p.requireShellTermPane().Session,
	}, true
}

func (p *Plugin) desiredPrimaryTerminal() (workspaceTerminalTarget, bool) {
	if !p.terminalOutputSurfaceVisible() {
		return workspaceTerminalTarget{}, false
	}
	width, height := p.calculateAgentPaneDimensions()
	width = p.terminalContentWidth(width)
	if p.selectingShell() {
		shell := p.getSelectedShell()
		if shell == nil || shell.IsOrphaned || shell.Agent == nil ||
			shell.Agent.TmuxSession == "" {
			return workspaceTerminalTarget{}, false
		}
		return workspaceTerminalTarget{
			Session: shell.Agent.TmuxSession, Pane: shell.Agent.TmuxPane,
			Width: width, Height: height, Source: "shell", SourceID: shell.TmuxName,
		}, true
	}
	wt := p.selectedWorktree()
	if wt == nil || wt.IsOrphaned || wt.Agent == nil ||
		wt.Agent.TmuxSession == "" || wt.Agent.TmuxPane == "" {
		return workspaceTerminalTarget{}, false
	}
	return workspaceTerminalTarget{
		Session: wt.Agent.TmuxSession, Pane: wt.Agent.TmuxPane,
		Width: width, Height: height, Source: "agent", SourceID: wt.IdentityKey(),
	}, true
}

func (p *Plugin) reconcileTerminalModels() []tea.Cmd {
	if p.primaryTermPane().Terminal == nil || p.requireShellTermPane().Terminal == nil {
		p.resetTerminalModels()
	}
	var cmds []tea.Cmd
	primary, primaryWanted := p.desiredPrimaryTerminal()
	cmds = append(cmds, p.reconcileTerminalModel(workspaceTerminalPrimary, primary, primaryWanted)...)
	panel, panelWanted := p.desiredPanelTerminal()
	cmds = append(cmds, p.reconcileTerminalModel(workspaceTerminalPanel, panel, panelWanted)...)
	return cmds
}

func (p *Plugin) reconcileTerminalModel(role workspaceTerminalRole, desired workspaceTerminalTarget, wanted bool) []tea.Cmd {
	model, current := p.terminalModelAndTarget(role)
	if model == nil {
		return nil
	}
	if !wanted {
		if model.IsActive() {
			model.Close()
			p.setTerminalTarget(role, workspaceTerminalTarget{})
		}
		return nil
	}

	sameTarget := model.IsActive() && current.Session == desired.Session &&
		current.Pane == desired.Pane && current.Source == desired.Source && current.SourceID == desired.SourceID
	if !sameTarget {
		model.Close()
		model.Width, model.Height = desired.Width, desired.Height
		p.setTerminalTarget(role, desired)
		cmd := model.Open(tty.Target{Session: desired.Session, Pane: desired.Pane})
		p.bindTerminalBuffer(role, desired, model)
		if cmd != nil {
			return []tea.Cmd{cmd}
		}
		return nil
	}
	if current.Width == desired.Width && current.Height == desired.Height {
		return nil
	}
	p.setTerminalTarget(role, desired)
	if cmd := model.Resize(desired.Width, desired.Height); cmd != nil {
		return []tea.Cmd{cmd}
	}
	return nil
}

func (p *Plugin) terminalModelAndTarget(role workspaceTerminalRole) (*tty.Model, workspaceTerminalTarget) {
	if role == workspaceTerminalPanel {
		return p.requireShellTermPane().Terminal, p.requireShellTermPane().Target
	}
	return p.primaryTermPane().Terminal, p.primaryTermPane().Target
}

func (p *Plugin) setTerminalTarget(role workspaceTerminalRole, target workspaceTerminalTarget) {
	if role == workspaceTerminalPanel {
		p.requireShellTermPane().Target = target
		return
	}
	p.primaryTermPane().Target = target
}

func (p *Plugin) bindTerminalBuffer(role workspaceTerminalRole, target workspaceTerminalTarget, model *tty.Model) {
	if model == nil || model.State == nil || model.State.OutputBuf == nil {
		return
	}
	if role == workspaceTerminalPanel {
		if p.requireShellTermPane().Buffer != model.State.OutputBuf {
			p.releaseTerminalDocProjection(true)
		}
		p.requireShellTermPane().Buffer = model.State.OutputBuf
		return
	}
	switch target.Source {
	case "agent":
		if wt := p.findWorktree(target.SourceID); wt != nil && wt.Agent != nil &&
			wt.Agent.TmuxSession == target.Session {
			if wt.Agent.OutputBuf != model.State.OutputBuf {
				p.releaseTerminalDocProjection(false)
			}
			wt.Agent.OutputBuf = model.State.OutputBuf
		}
	case "shell":
		if shell := p.findShellByName(target.SourceID); shell != nil && shell.Agent != nil &&
			shell.Agent.TmuxSession == target.Session {
			if shell.Agent.OutputBuf != model.State.OutputBuf {
				p.releaseTerminalDocProjection(false)
			}
			shell.Agent.OutputBuf = model.State.OutputBuf
		}
	}
}

func (p *Plugin) syncTerminalModels() {
	p.syncTerminalModel(workspaceTerminalPrimary)
	p.syncTerminalModel(workspaceTerminalPanel)
}

func (p *Plugin) syncTerminalModel(role workspaceTerminalRole) {
	model, target := p.terminalModelAndTarget(role)
	if model == nil || !model.IsActive() || model.State == nil {
		return
	}
	p.bindTerminalBuffer(role, target, model)
	history := model.History()
	historyID := target.SourceID
	if target.Source == "agent" {
		historyID = target.Session
	}
	if history.HasHistory {
		p.recordTerminalHistory(target.Source, historyID, history.HistorySize)
	}
	p.recordPaneGeometry(target.Source, historyID, model.State.PaneWidth, model.State.PaneHeight)
	// A model closes whenever its surface stops being drawn — a hidden panel, a
	// split too small to host one — and the pane it was reading is still the pane
	// a notch over that surface lands on. Who owns the notch has to outlive the
	// component that observed it, for every pane identity a surface can hold.
	p.recordPaneMouseReporting(target.Source, historyID, model.PaneMouseReporting())
	if p.interactiveState == nil || !p.interactiveState.Active ||
		(p.terminalPaneIsPanel(p.interactiveState.LeafID) != (role == workspaceTerminalPanel)) {
		return
	}
	state := model.State
	p.interactiveState.CursorRow = state.CursorRow
	p.interactiveState.CursorCol = state.CursorCol
	p.interactiveState.CursorVisible = state.CursorVisible
	p.interactiveState.PaneHeight = state.PaneHeight
	p.interactiveState.PaneWidth = state.PaneWidth
	p.interactiveState.BracketedPasteEnabled = state.BracketedPasteEnabled
	p.interactiveState.MouseReportingEnabled = state.MouseReportingEnabled
	p.interactiveState.LastKeyTime = state.LastKeyTime
}

func (p *Plugin) activeInteractiveTerminal() *tty.Model {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}
	if p.terminalPaneIsPanel(p.interactiveState.LeafID) {
		return p.requireShellTermPane().Terminal
	}
	return p.primaryTermPane().Terminal
}

func (p *Plugin) primaryTerminalOwns(source, sourceID string) bool {
	return p.primaryTermPane().Terminal != nil && p.primaryTermPane().Terminal.IsActive() &&
		p.primaryTermPane().Target.Source == source && p.primaryTermPane().Target.SourceID == sourceID
}

func (p *Plugin) panelTerminalOwns() bool {
	return p.requireShellTermPane().Terminal != nil && p.requireShellTermPane().Terminal.IsActive() &&
		p.requireShellTermPane().Target.Session == p.requireShellTermPane().Session
}

// semanticAgentPollInterval is deliberately independent of terminal frame
// delivery. Provider activity remains evidence-driven from its own tmux/provider
// observation cadence and is never inferred from emulator output.
func (p *Plugin) semanticAgentPollInterval() time.Duration {
	if !p.applicationFocused || !p.focused {
		return pollIntervalUnfocused
	}
	return pollIntervalIdle
}
