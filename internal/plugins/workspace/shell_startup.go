package workspace

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/tty"
)

// shellManifestWatcher is deliberately small so startup behavior can be
// exercised without constructing a real fsnotify watcher.
type shellManifestWatcher interface {
	Start() <-chan tea.Msg
	Stop()
}

type shellStartupScope struct {
	owner       *Plugin
	epoch       uint64
	version     uint64
	projectRoot string
}

type shellStartupHooks struct {
	resolveProjectDir func(string) (string, error)
	loadManifest      func(string) (*ShellManifest, error)
	discoverSessions  func(string) ([]string, tmuxserver.Incarnation, error)
	getPaneID         func(string) string
	newWatcher        func(string) (shellManifestWatcher, error)
	getWorkspaceState func(string) state.WorkspaceState
	setWorkspaceState func(string, state.WorkspaceState) error
	now               func() time.Time
	namespace         func() string
}

func defaultShellStartupHooks() shellStartupHooks {
	return shellStartupHooks{
		resolveProjectDir: projectdir.Resolve,
		loadManifest:      LoadShellManifest,
		discoverSessions:  discoverTmuxSessionNamesForWorkDir,
		getPaneID:         getPaneID,
		newWatcher: func(path string) (shellManifestWatcher, error) {
			return NewShellWatcher(path)
		},
		getWorkspaceState: state.GetWorkspaceState,
		setWorkspaceState: state.SetWorkspaceState,
		now:               time.Now,
		namespace:         tmuxenv.Namespace,
	}
}

func (h shellStartupHooks) withDefaults() shellStartupHooks {
	defaults := defaultShellStartupHooks()
	if h.resolveProjectDir == nil {
		h.resolveProjectDir = defaults.resolveProjectDir
	}
	if h.loadManifest == nil {
		h.loadManifest = defaults.loadManifest
	}
	if h.discoverSessions == nil {
		h.discoverSessions = defaults.discoverSessions
	}
	if h.getPaneID == nil {
		h.getPaneID = defaults.getPaneID
	}
	if h.newWatcher == nil {
		h.newWatcher = defaults.newWatcher
	}
	if h.getWorkspaceState == nil {
		h.getWorkspaceState = defaults.getWorkspaceState
	}
	if h.setWorkspaceState == nil {
		h.setWorkspaceState = defaults.setWorkspaceState
	}
	if h.now == nil {
		h.now = defaults.now
	}
	if h.namespace == nil {
		h.namespace = defaults.namespace
	}
	return h
}

type shellStartupResultMsg struct {
	scope           shellStartupScope
	manifest        *ShellManifest
	shells          []*ShellSession
	managedSessions map[string]bool
	watcher         shellManifestWatcher
	err             error
	watcherErr      error
	discoveryErr    error
	server          tmuxserver.Incarnation
}

type shellManifestChangedMsg struct {
	scope shellStartupScope
}

func (p *Plugin) currentShellStartupScope() shellStartupScope {
	projectRoot := ""
	if p.ctx != nil {
		projectRoot = p.ctx.ProjectRoot
	}
	return shellStartupScope{
		owner:       p,
		epoch:       p.shellStartupEpoch,
		version:     p.shellStartupVersion,
		projectRoot: projectRoot,
	}
}

func (p *Plugin) shellScopeCurrent(scope shellStartupScope) bool {
	return p != nil && p.ctx != nil &&
		scope.owner == p &&
		scope.epoch == p.ctx.Epoch &&
		scope.epoch == p.shellStartupEpoch &&
		scope.version == p.shellStartupVersion &&
		scope.projectRoot == p.ctx.ProjectRoot
}

// invalidateShellStartup rejects all in-flight startup and watcher messages.
// Watcher shutdown can wait for fsnotify/timer goroutines, so it is detached
// from the lifecycle path and drained asynchronously.
func (p *Plugin) invalidateShellStartup() {
	p.shellStartupVersion++
	p.shellStartupLoading = false
	if watcher := p.shellWatcher; watcher != nil {
		p.shellWatcher = nil
		p.shellWatcherMessages = nil
		go watcher.Stop()
	}
}

func stopShellWatcherCmd(watcher shellManifestWatcher) tea.Cmd {
	if watcher == nil {
		return nil
	}
	return func() tea.Msg {
		watcher.Stop()
		return nil
	}
}

// loadShellStartup returns immediately. Every filesystem and tmux operation is
// confined to the returned command, which only constructs result data.
func (p *Plugin) loadShellStartup() tea.Cmd {
	if p == nil || p.ctx == nil {
		return nil
	}
	scope := p.currentShellStartupScope()
	workDir := p.ctx.WorkDir
	projectRoot := p.ctx.ProjectRoot
	hooks := p.shellStartupHooks.withDefaults()

	return func() tea.Msg {
		result := shellStartupResultMsg{
			scope:           scope,
			managedSessions: make(map[string]bool),
		}

		projectDir, err := hooks.resolveProjectDir(projectRoot)
		if err != nil {
			result.err = fmt.Errorf("resolve project dir for shell manifest: %w", err)
			return result
		}
		manifestPath := filepath.Join(projectDir, "shells.json")
		manifest, err := hooks.loadManifest(manifestPath)
		if err != nil {
			result.err = fmt.Errorf("load shell manifest: %w", err)
			return result
		}

		// A discovery failure — or an Absent server — is not an empty tmux
		// inventory. Passing either through as "nothing is running" used to
		// prune every same-namespace entry and save the truncated file
		// (td-8d18de, 2026-08-22). Reconcile is additive either way; empty
		// session names only affect which rows render as live.
		sessions, inc, discoveryErr := hooks.discoverSessions(workDir)
		result.discoveryErr = discoveryErr
		result.server = inc
		result.manifest = manifest
		result.shells, result.managedSessions = reconcileShellStartup(
			manifest,
			sessions,
			discoveryFailed(inc, discoveryErr),
			workDir,
			projectRoot,
			hooks,
		)

		result.watcher, result.watcherErr = hooks.newWatcher(manifestPath)
		return result
	}
}

func discoveryFailed(inc tmuxserver.Incarnation, err error) bool {
	return err != nil || inc.IsAbsent()
}

// reconcileShellStartup preserves shells.json as the source of truth while
// retaining the upgrade path for tmux sessions created before the manifest.
// All persistence happens on the command goroutine, and is additive: this
// path may add definitions and update fields on existing ones, never shrink
// the list. A shell that is not running becomes an offline row.
func reconcileShellStartup(
	manifest *ShellManifest,
	sessionNames []string,
	discoveryFailed bool,
	workDir string,
	projectRoot string,
	hooks shellStartupHooks,
) ([]*ShellSession, map[string]bool) {
	_ = discoveryFailed // retained so callers still distinguish Absent/error from a listing; never used to drop rows
	running := make(map[string]bool, len(sessionNames))
	for _, name := range sessionNames {
		running[name] = true
	}

	// live is a snapshot: the loop below consumes `running` as it matches
	// manifest entries, but the final construction still needs to know which
	// retained definitions are actually alive.
	live := make(map[string]bool, len(running))
	for name := range running {
		live[name] = true
	}

	pattern := shellDiscoveryPattern(workDir)
	ns := hooks.namespace()
	original := manifest.Shells

	definitions := make([]ShellDefinition, 0, len(original)+len(running))
	var toUpdate []ShellDefinition
	for _, definition := range original {
		if running[definition.TmuxName] {
			updated := false
			if definition.Namespace != ns {
				definition.Namespace = ns
				updated = true
			}
			if definition.WorkDir == "" {
				definition.WorkDir = workDir
				updated = true
			}
			definitions = append(definitions, definition)
			delete(running, definition.TmuxName)
			if updated {
				toUpdate = append(toUpdate, definition)
			}
			continue
		}
		// Not live here. Startup never treats absence as death: a down or
		// restarted tmux server looks exactly like "this name is gone", and
		// deleting the record deletes the only recreate path (td-e27291).
		definitions = append(definitions, definition)
	}

	discovered := make([]string, 0, len(running))
	for name := range running {
		discovered = append(discovered, name)
	}
	sort.Strings(discovered)
	forgotten := tombstoneTmuxNames(manifest.Tombstones)
	var toEnsure []ShellDefinition
	for _, name := range discovered {
		if forgotten[name] {
			// sidecar shell forget is record-only: the tmux session may still
			// be running. That is not a missing definition (td-61117e).
			continue
		}
		now := hooks.now()
		def := ShellDefinition{
			TmuxName:    name,
			DisplayName: deriveShellDisplayName(workDir, name),
			Namespace:   ns,
			CreatedAt:   now,
			WorkDir:     workDir,
		}
		definitions = append(definitions, def)
		toEnsure = append(toEnsure, def)
	}

	legacy := hooks.getWorkspaceState(projectRoot)
	migrated := false
	if len(legacy.ShellDisplayNames) > 0 {
		for i := range definitions {
			if name := strings.TrimSpace(legacy.ShellDisplayNames[definitions[i].TmuxName]); name != "" {
				if definitions[i].DisplayName != name {
					definitions[i].DisplayName = name
					toUpdate = append(toUpdate, definitions[i])
				}
				migrated = true
			}
		}
	}

	if len(toEnsure) > 0 {
		_, _ = manifest.EnsureShells(toEnsure)
	}
	for _, def := range toUpdate {
		_ = manifest.UpdateShell(def)
	}
	manifest.Shells = definitions
	if migrated {
		legacy.ShellDisplayNames = nil
		_ = hooks.setWorkspaceState(projectRoot, legacy)
	}

	// Survival and visibility are different questions. Definitions retained
	// above because we could not prove they died may belong to a sibling
	// worktree sharing this shells.json; listing them here would fill every
	// worktree's sidebar with offline rows whose only action — recreate — fails
	// against a session name that already exists elsewhere (td-8d18de).
	shells := make([]*ShellSession, 0, len(definitions))
	managed := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		running := live[definition.TmuxName]
		if !running && !pattern.MatchString(definition.TmuxName) {
			continue
		}
		shells = append(shells, shellSessionFromDefinition(definition, running, hooks.getPaneID))
		if running {
			managed[definition.TmuxName] = true
		}
	}
	return shells, managed
}

func shellSessionFromDefinition(
	definition ShellDefinition,
	running bool,
	paneID func(string) string,
) *ShellSession {
	shell := &ShellSession{
		Name:        definition.DisplayName,
		TmuxName:    definition.TmuxName,
		WorkDir:     definition.WorkDir,
		CreatedAt:   definition.CreatedAt,
		ChosenAgent: definitionToAgentType(definition.AgentType),
		SkipPerms:   definition.SkipPerms,
		IsOrphaned:  !running,
	}
	if !running {
		return shell
	}
	attachAgentToShell(shell, definition, paneID)
	return shell
}

// attachAgentToShell gives a live shell the Agent every interactive path
// requires. It is shared by first construction and by revival during a manifest
// sync, so a shell that comes back is as usable as one that never left.
func attachAgentToShell(shell *ShellSession, definition ShellDefinition, paneID func(string) string) {
	// Live type starts as a shell. ChosenAgent is launch preference; seeding
	// Type from it makes every defaultAgentType=cursor session look like
	// Cursor until Identify proves otherwise — and empty Identify retains
	// that seed forever. The first poll fills in the real owner.
	startedAt := definition.CreatedAt
	if startedAt.IsZero() {
		startedAt = shell.CreatedAt
	}
	shell.Agent = &Agent{
		Type:        AgentShell,
		TmuxSession: definition.TmuxName,
		TmuxPane:    paneID(definition.TmuxName),
		OutputBuf:   tty.NewOutputBuffer(outputBufferCap),
		StartedAt:   startedAt,
	}
}

func deriveShellDisplayName(workDir, tmuxName string) string {
	projectName := filepath.Base(workDir)
	basePrefix := shellSessionPrefix + sanitizeName(projectName)
	indexPattern := regexp.MustCompile(`-(\d+)$`)

	if matches := indexPattern.FindStringSubmatch(tmuxName); matches != nil {
		index, _ := strconv.Atoi(matches[1])
		return fmt.Sprintf("Shell %d", index)
	}
	if tmuxName == basePrefix {
		return "Shell 1"
	}
	return "Shell"
}

func (p *Plugin) applyShellStartup(result shellStartupResultMsg) tea.Cmd {
	if !p.shellScopeCurrent(result.scope) {
		return stopShellWatcherCmd(result.watcher)
	}

	p.shellStartupLoading = false
	if result.err != nil {
		if p.ctx.Logger != nil {
			p.ctx.Logger.Warn("shell startup failed", "error", result.err)
		}
		return tea.Batch(p.completeInitialWorkspaceLoad()...)
	}
	if result.watcherErr != nil && p.ctx.Logger != nil {
		p.ctx.Logger.Debug("shell manifest watcher unavailable", "error", result.watcherErr)
	}
	if result.discoveryErr != nil && p.ctx.Logger != nil {
		p.ctx.Logger.Warn("tmux session discovery failed; kept every manifest entry",
			"error", result.discoveryErr)
	}

	p.shellManifest = result.manifest
	p.shells = result.shells
	p.rebuildNestedShellsFromState()
	p.applyPendingWorkspaceSelection()
	pendingAction := p.TakePendingWorkspaceAction()
	if p.managedSessions == nil {
		p.managedSessions = make(map[string]bool)
	}
	p.observeTmuxServer(result.server)
	for sessionName := range result.managedSessions {
		p.managedSessions[sessionName] = true
		// Discovery listed this session, which is the positive liveness a later
		// probe requires before it may close the shell (td-6a4100).
		p.noteShellAlive(sessionName)
	}

	var commands []tea.Cmd
	if pendingAction != nil {
		commands = append(commands, pendingAction)
	}
	if cmd := p.backfillWorkDirsCmd(); cmd != nil {
		commands = append(commands, cmd)
	}
	if result.watcher != nil {
		p.shellWatcher = result.watcher
		p.shellWatcherMessages = result.watcher.Start()
		commands = append(commands, listenForShellManifestChanges(result.scope, p.shellWatcherMessages))
	}
	for _, shell := range p.shells {
		if shell.Agent != nil {
			commands = append(commands, p.pollShellSessionByName(shell.TmuxName))
		}
	}
	commands = append(commands, p.completeInitialWorkspaceLoad()...)
	if command := p.maybeAutoCreateShell(); command != nil {
		commands = append(commands, command)
	}
	return tea.Batch(commands...)
}

func listenForShellManifestChanges(scope shellStartupScope, messages <-chan tea.Msg) tea.Cmd {
	if messages == nil {
		return nil
	}
	return func() tea.Msg {
		if _, ok := <-messages; !ok {
			return nil
		}
		return shellManifestChangedMsg{scope: scope}
	}
}
