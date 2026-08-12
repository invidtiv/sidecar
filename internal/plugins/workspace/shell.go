package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tty"
)

// Shell session constants
const (
	shellSessionPrefix = "sidecar-sh-" // Distinct from worktree prefix "sidecar-ws-"
)

// launchedInsideTmux is true if sidecar was started from within an existing
// tmux session (i.e. TMUX env var was set at process start). This is captured
// at package init time, before main() unsets TMUX for nested session support.
var launchedInsideTmux = os.Getenv("TMUX") != ""

// tmuxInstalled caches whether tmux is available in PATH.
// Checked once and cached to avoid repeated exec calls.
var (
	tmuxInstalledOnce   sync.Once
	tmuxInstalledCached bool

	tmuxPrefixOnce   sync.Once
	tmuxPrefixCached string
)

// isTmuxInstalled returns true if tmux is available in PATH.
// Result is cached after first check.
func isTmuxInstalled() bool {
	tmuxInstalledOnce.Do(func() {
		_, err := exec.LookPath("tmux")
		tmuxInstalledCached = err == nil
	})
	return tmuxInstalledCached
}

// getTmuxPrefix returns the user's tmux prefix key in human-readable format.
// Queries `tmux show-options -g prefix` and converts notation (C-b → Ctrl-b).
// Falls back to "Ctrl-b" if detection fails. Result is cached.
func getTmuxPrefix() string {
	tmuxPrefixOnce.Do(func() {
		tmuxPrefixCached = "Ctrl-b" // default fallback

		if !isTmuxInstalled() {
			return
		}

		out, err := exec.Command("tmux", "show-options", "-g", "prefix").Output()
		if err != nil {
			return
		}

		// Output format: "prefix C-b" or "prefix C-a"
		line := strings.TrimSpace(string(out))
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return
		}

		tmuxPrefixCached = tmuxNotationToHuman(parts[1])
	})
	return tmuxPrefixCached
}

// getTmuxDetachHint returns the key sequence hint for detaching from a nested
// tmux session. When sidecar was launched inside an existing tmux session, the
// user needs to press the prefix twice (once to reach the inner session) before
// pressing d. Otherwise a single prefix + d suffices.
func getTmuxDetachHint() string {
	prefix := getTmuxPrefix()
	if launchedInsideTmux {
		return prefix + " " + prefix + " d"
	}
	return prefix + " d"
}

// tmuxNotationToHuman converts tmux key notation to human-readable format.
// Examples: C-b → Ctrl-b, C-a → Ctrl-a, M-x → Alt-x
func tmuxNotationToHuman(notation string) string {
	if len(notation) < 2 {
		return notation
	}

	// Handle C- prefix (Ctrl)
	if strings.HasPrefix(notation, "C-") {
		return "Ctrl-" + notation[2:]
	}

	// Handle M- prefix (Meta/Alt)
	if strings.HasPrefix(notation, "M-") {
		return "Alt-" + notation[2:]
	}

	return notation
}

// getTmuxInstallInstructions returns platform-specific tmux install instructions.
func getTmuxInstallInstructions() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install tmux"
	case "linux":
		return "sudo apt install tmux  # or: sudo dnf install tmux"
	default:
		return "Install tmux from your package manager"
	}
}

// Shell session messages
type (
	// ShellCreatedMsg signals shell session was created
	ShellCreatedMsg struct {
		SessionName string    // tmux session name
		DisplayName string    // Display name (e.g., "Shell 1")
		PaneID      string    // tmux pane ID (e.g., "%12") for interactive mode
		Err         error     // Non-nil if creation failed
		AgentType   AgentType // td-16b2b5: Agent to start (AgentNone if plain shell)
		SkipPerms   bool      // td-16b2b5: Whether to skip permissions for agent
		// KeepSelection leaves the sidebar selection where it is instead of
		// selecting the new shell. Set for shells created without the user
		// explicitly asking for one (auto-create on first focus).
		KeepSelection bool
	}

	// ShellDetachedMsg signals user detached from shell session
	ShellDetachedMsg struct {
		Err error
	}

	// ShellKilledMsg signals shell session was terminated
	ShellKilledMsg struct {
		SessionName string // tmux session name that was killed
	}

	// ShellSessionDeadMsg signals shell session was externally terminated
	// (e.g., user typed 'exit' in the shell)
	ShellSessionDeadMsg struct {
		TmuxName   string // Session name for cleanup (stable identifier)
		Generation int    // Poll owner; zero for non-poll lifecycle checks
	}

	// ShellAgentStartedMsg signals agent was started in a shell session.
	// td-21a2d8: Sent after agent command is sent to tmux.
	ShellAgentStartedMsg struct {
		TmuxName  string    // Shell's tmux session name
		AgentType AgentType // Agent type that was started
		SkipPerms bool      // Whether skip permissions was enabled
	}

	// ShellAgentErrorMsg signals agent failed to start in a shell session.
	// td-21a2d8: Sent when agent command fails to execute.
	ShellAgentErrorMsg struct {
		TmuxName string // Shell's tmux session name
		Err      error  // Error that occurred
	}

	// ShellOutputMsg signals shell output was captured (for polling)
	ShellOutputMsg struct {
		TmuxName       string // Session name (stable identifier)
		Generation     int
		AgentType      AgentType // Live provider inferred from the captured pane
		Output         string
		Err            error
		Activity       agentactivity.Result
		CapturedAt     time.Time
		PaneTitle      string
		CurrentCommand string
		// Cursor position captured atomically with output (only set in interactive mode)
		CursorRow     int
		CursorCol     int
		CursorVisible bool
		HasCursor     bool // True if cursor position was captured
		PaneHeight    int  // Tmux pane height for cursor offset calculation
		PaneWidth     int  // Tmux pane width for display alignment
		HistorySize   int
		CaptureBase   int
		HasHistory    bool
		// RowsJoined says the capture was taken with -J, so it carries no usable
		// history/pane split.
		RowsJoined bool
	}

	// RenameShellDoneMsg signals shell rename operation completed
	RenameShellDoneMsg struct {
		TmuxName string // Session name (stable identifier)
		NewName  string // New display name
		Err      error  // Non-nil if rename failed
	}

	// pollShellByNameMsg triggers a poll for a specific shell's output by name.
	// Includes generation for timer leak prevention (td-83dc22).
	pollShellByNameMsg struct {
		TmuxName   string
		Generation int // Generation at time of scheduling; ignore if stale
	}

	// shellAttachAfterCreateMsg triggers attachment after shell creation
	shellAttachAfterCreateMsg struct {
		Index int // Index of the shell to attach to
	}
)

// pollShellMsg triggers a shell output poll (legacy, polls selected shell).
type pollShellMsg struct{}

// shellDiscoveryPattern matches exactly the session names this instance's
// discovery could ever produce for workDir. It doubles as the "could I have
// discovered that?" predicate during reconciliation: a manifest entry this
// pattern rejects belongs to some other working directory (a sibling worktree,
// say), so our not seeing it is no evidence at all that it died (td-8d18de).
func shellDiscoveryPattern(workDir string) *regexp.Regexp {
	basePrefix := shellSessionPrefix + sanitizeName(filepath.Base(workDir))
	return regexp.MustCompile(`^` + regexp.QuoteMeta(basePrefix) + `(?:-(\d+))?$`)
}

// discoverTmuxSessionNamesForWorkDir lists this instance's shell sessions.
//
// The error is part of the contract (td-8d18de): "tmux told me there are no
// sessions" and "I could not ask tmux" must never look the same, because the
// first is permission to prune a manifest entry and the second is not. tmux
// exits 1 with "no server running" when the server is simply down — that is a
// genuine empty answer — while a missing binary, a broken socket or a fork
// failure is an unknown answer and must prune nothing.
func discoverTmuxSessionNamesForWorkDir(workDir string) ([]string, error) {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
	output, err := cmd.Output()
	if err != nil {
		if !tmuxReportedNoServer(err) {
			return nil, fmt.Errorf("tmux list-sessions: %w", err)
		}
		return nil, nil
	}

	var result []string
	indexPattern := shellDiscoveryPattern(workDir)

	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if indexPattern.MatchString(line) {
			result = append(result, line)
		}
	}

	return result, nil
}

// tmuxReportedNoServer reports whether the failure was tmux answering "there is
// no server", which is a real, trustworthy "no sessions" answer. Anything else
// — exec.ErrNotFound, a permissions problem, a fork failure — is unknown.
func tmuxReportedNoServer(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	stderr := strings.ToLower(string(exitErr.Stderr))
	return strings.Contains(stderr, "no server running") ||
		strings.Contains(stderr, "no sessions")
}

// syncShellsFromManifest reloads the manifest and syncs the shell list.
// Called when the manifest file changes (from another sidecar instance).
func (p *Plugin) syncShellsFromManifest(scope shellStartupScope) tea.Cmd {
	if p.shellManifest == nil || !p.shellScopeCurrent(scope) {
		return nil
	}
	manifestPath := p.shellManifest.Path()
	baseRevision := p.shellManifest.Revision()
	workDir := p.ctx.WorkDir
	hooks := p.shellStartupHooks.withDefaults()
	return func() tea.Msg {
		// Reload manifest from disk
		newManifest, err := hooks.loadManifest(manifestPath)
		if err != nil {
			return nil
		}

		// If we cannot ask tmux, we know nothing about what is running, and a
		// merge fed an empty Running map would orphan every shell on screen.
		// Say nothing instead (td-8d18de).
		names, discoveryErr := hooks.discoverSessions(workDir)
		if discoveryErr != nil {
			return nil
		}

		running := make(map[string]bool)
		paneIDs := make(map[string]string)
		for _, name := range names {
			running[name] = true
			paneIDs[name] = hooks.getPaneID(name)
		}
		return shellManifestSyncMsg{
			Scope:        scope,
			Manifest:     newManifest,
			Running:      running,
			PaneIDs:      paneIDs,
			Namespace:    hooks.namespace(),
			BaseRevision: baseRevision,
		}
	}
}

// shellManifestSyncMsg carries the reloaded manifest for syncing.
type shellManifestSyncMsg struct {
	Scope    shellStartupScope
	Manifest *ShellManifest
	Running  map[string]bool
	PaneIDs  map[string]string
	// Namespace is this instance's tmux server identity, resolved on the
	// command goroutine so Update never touches the environment.
	Namespace string
	// BaseRevision is the manifest revision this snapshot was taken against.
	// A local delete that lands while the sync is in flight moves the revision,
	// and applying the stale snapshot would resurrect the deleted shell — so
	// the handler re-runs the sync instead of applying it (td-8d18de).
	BaseRevision uint64
}

// applyManifestSync syncs the in-memory shell list with the manifest.
// Called after receiving shellManifestSyncMsg. The union rules live in
// mergeShellState; this is the adapter that applies them to plugin state.
//
// It returns the work that must happen off the Update loop: healing the file
// (a locked, retrying disk write, which would otherwise freeze the TUI for up
// to lockTimeout in exactly the contended cross-instance case this exists for)
// and polling shells that just came back to life.
func (p *Plugin) applyManifestSync(sync shellManifestSyncMsg) tea.Cmd {
	if p.shellManifest == nil {
		return nil
	}

	result := mergeShellState(shellMergeInput{
		Existing:  p.shells,
		Manifest:  p.shellManifest.Shells,
		Running:   sync.Running,
		PaneID:    func(name string) string { return sync.PaneIDs[name] },
		WorkDir:   p.ctx.WorkDir,
		Namespace: sync.Namespace,
	})

	p.shells = result.Shells

	// Only shells that vanished from the manifest *and* are not running here
	// reach Dropped, i.e. an explicit delete elsewhere.
	for _, name := range result.Dropped {
		delete(p.managedSessions, name)
		globalPaneCache.remove(name)
		globalActiveRegistry.remove(name)
	}
	if p.managedSessions == nil {
		p.managedSessions = make(map[string]bool)
	}
	for _, shell := range p.shells {
		if sync.Running[shell.TmuxName] {
			p.managedSessions[shell.TmuxName] = true
		}
	}

	// Adjust selection if needed
	if p.shellSelected && p.selectedShellIdx >= len(p.shells) {
		if len(p.shells) > 0 {
			p.selectedShellIdx = len(p.shells) - 1
		} else if len(p.worktrees) > 0 {
			p.shellSelected = false
			p.selectedIdx = 0
		}
	}

	var commands []tea.Cmd
	// Heal the file: put back the live sessions the writer did not know about.
	// This converges rather than ping-pongs — the peer instance re-reads a
	// superset manifest, finds nothing missing, and writes nothing.
	if len(result.Restored) > 0 {
		manifest := p.shellManifest
		restored := result.Restored
		logger := p.ctx.Logger
		commands = append(commands, func() tea.Msg {
			if _, err := manifest.EnsureShells(restored); err != nil && logger != nil {
				logger.Warn("shell manifest heal failed", "error", err)
			}
			return nil
		})
	}
	for _, shell := range result.Revived {
		commands = append(commands, p.pollShellSessionByName(shell.TmuxName))
	}
	return tea.Batch(commands...)
}

// nextShellIndex returns the next available shell index based on existing sessions.
func (p *Plugin) nextShellIndex() int {
	projectName := filepath.Base(p.ctx.WorkDir)
	basePrefix := shellSessionPrefix + sanitizeName(projectName)

	maxIdx := 0
	indexPattern := regexp.MustCompile(`-(\d+)$`)

	for _, shell := range p.shells {
		matches := indexPattern.FindStringSubmatch(shell.TmuxName)
		if matches != nil {
			idx, _ := strconv.Atoi(matches[1])
			if idx > maxIdx {
				maxIdx = idx
			}
		} else if shell.TmuxName == basePrefix {
			if maxIdx < 1 {
				maxIdx = 1
			}
		}
	}

	return maxIdx + 1
}

// nextShellDisplayName returns the default display name for the next shell.
func (p *Plugin) nextShellDisplayName() string {
	return fmt.Sprintf("Shell %d", p.nextShellIndex())
}

// generateShellSessionName creates a unique tmux session name for a new shell.
func (p *Plugin) generateShellSessionName() string {
	projectName := filepath.Base(p.ctx.WorkDir)
	basePrefix := shellSessionPrefix + sanitizeName(projectName)
	return fmt.Sprintf("%s-%d", basePrefix, p.nextShellIndex())
}

// shellCreateOpts describes a shell session to create.
type shellCreateOpts struct {
	CustomName    string    // Display name; empty means auto-generated "Shell N"
	AgentType     AgentType // Agent to start after creation (AgentNone for a plain shell)
	SkipPerms     bool      // Whether to pass the agent's skip-permissions flag
	KeepSelection bool      // Leave the sidebar selection alone (see ShellCreatedMsg)
}

// createShell creates a new detached tmux session for a shell. The returned
// command reports the outcome as a ShellCreatedMsg; the update handler owns all
// state bookkeeping (manifest, selection, polling).
func (p *Plugin) createShell(opts shellCreateOpts) tea.Cmd {
	if !isTmuxInstalled() {
		return func() tea.Msg {
			return ShellCreatedMsg{Err: fmt.Errorf("tmux not installed: %s", getTmuxInstallInstructions())}
		}
	}

	sessionName := p.generateShellSessionName()
	displayName := strings.TrimSpace(opts.CustomName)
	if displayName == "" {
		displayName = p.nextShellDisplayName()
	}
	workDir := p.ctx.WorkDir
	// Size the pane at creation. Without -x/-y tmux uses default-size (80x24),
	// and anything the user starts before the follow-up resize lands — an editor
	// especially — lays itself out for 24 rows (td-9b181e). The shell is not
	// selected yet, which no longer matters: every terminal surface reserves the
	// same single header row, so the preview size does not depend on the kind.
	previewWidth, previewHeight := p.calculatePreviewDimensions()

	created := ShellCreatedMsg{
		SessionName:   sessionName,
		DisplayName:   displayName,
		AgentType:     opts.AgentType,
		SkipPerms:     opts.SkipPerms,
		KeepSelection: opts.KeepSelection,
	}

	return func() tea.Msg {
		// Check if session already exists (shouldn't happen with unique names)
		if sessionExists(sessionName) {
			created.PaneID = getPaneID(sessionName)
			return created
		}

		// Create new detached session in project directory
		args := []string{
			"new-session",
			"-d",              // Detached
			"-s", sessionName, // Session name
			"-c", workDir, // Working directory
		}
		if previewWidth > 0 && previewHeight > 0 {
			args = append(args, "-x", strconv.Itoa(previewWidth), "-y", strconv.Itoa(previewHeight))
		}
		if err := newShellSession(args, sessionName, displayName); err != nil {
			created.Err = fmt.Errorf("create shell session: %w", err)
			return created
		}

		// Capture pane ID for interactive mode support
		created.PaneID = getPaneID(sessionName)
		return created
	}
}

// createNewShell creates a plain shell session. If customName is non-empty, it is
// used as the display name instead of the auto-generated "Shell N".
func (p *Plugin) createNewShell(customName string) tea.Cmd {
	return p.createShell(shellCreateOpts{CustomName: customName})
}

// createShellWithAgent creates a new shell session with optional agent startup.
// td-16b2b5: Captures agent info from type selector state, creates shell, and includes
// agent info in the message so the handler can start the agent after shell creation.
func (p *Plugin) createShellWithAgent() tea.Cmd {
	return p.createShell(shellCreateOpts{
		CustomName: p.typeSelectorNameInput.Value(),
		AgentType:  p.typeSelectorAgentType,
		SkipPerms:  p.typeSelectorSkipPerms,
	})
}

// resolveShellAgentType returns the agent to launch in a shell created outside
// the type-selector modal. Unlike worktrees, a shell with no configured default
// stays a plain shell rather than falling back to Claude.
func (p *Plugin) resolveShellAgentType() AgentType {
	if p == nil || p.ctx == nil || p.ctx.Config == nil {
		return AgentNone
	}
	agentType := AgentType(strings.TrimSpace(p.ctx.Config.Plugins.Workspace.DefaultAgentType))
	if isKnownAgentType(agentType) {
		return agentType
	}
	return AgentNone
}

// maybeAutoCreateShell creates a default shell the first time the workspaces tab
// is focused with no shell sessions, when plugins.workspace.autoCreateShell is
// set. Returns nil in every other case.
//
// Called from both the plugin-focused path (tab switched to) and the first
// worktree refresh (workspaces was the startup tab, which emits no focus
// message). The check is only consumed once it actually runs while focused, so a
// background workspaces tab never spawns a session.
func (p *Plugin) maybeAutoCreateShell() tea.Cmd {
	if p.shellStartupLoading || p.autoShellChecked || !p.focused {
		return nil
	}
	if p.ctx == nil || p.ctx.Config == nil || !p.ctx.Config.Plugins.Workspace.AutoCreateShell {
		return nil
	}
	p.autoShellChecked = true
	if len(p.shells) > 0 || !isTmuxInstalled() {
		return nil
	}
	return p.createDefaultShell(true)
}

// createDefaultShell creates a shell using the configured default agent. Used by
// the ctrl+n binding and by auto-create on first focus.
func (p *Plugin) createDefaultShell(keepSelection bool) tea.Cmd {
	// SkipPerms stays false: the type-selector modal defaults it off, and a shell
	// the user did not explicitly configure should not launch an agent with
	// permission prompts disabled.
	return p.createShell(shellCreateOpts{
		AgentType:     p.resolveShellAgentType(),
		KeepSelection: keepSelection,
	})
}

// recreateOrphanedShell recreates a tmux session for an orphaned shell.
// td-f88fdd: Called when user tries to attach/interact with an orphaned shell.
func (p *Plugin) recreateOrphanedShell(idx int) tea.Cmd {
	if idx < 0 || idx >= len(p.shells) {
		return nil
	}

	shell := p.shells[idx]
	if !shell.IsOrphaned {
		return nil // Not orphaned, nothing to do
	}

	sessionName := shell.TmuxName
	workDir := p.ctx.WorkDir
	previewWidth, previewHeight := p.calculatePreviewDimensions()

	return func() tea.Msg {
		// Create new detached session
		args := []string{"new-session", "-d", "-s", sessionName, "-c", workDir}
		if previewWidth > 0 && previewHeight > 0 {
			args = append(args, "-x", strconv.Itoa(previewWidth), "-y", strconv.Itoa(previewHeight))
		}
		if err := newShellSession(args, sessionName, shell.Name); err != nil {
			return ShellCreatedMsg{
				SessionName: sessionName,
				DisplayName: shell.Name,
				Err:         fmt.Errorf("recreate shell session: %w", err),
			}
		}

		tty.SetWindowSizeManual(sessionName)

		// Capture pane ID
		paneID := getPaneID(sessionName)

		return ShellCreatedMsg{
			SessionName: sessionName,
			DisplayName: shell.Name,
			PaneID:      paneID,
			AgentType:   shell.ChosenAgent,
			SkipPerms:   shell.SkipPerms,
		}
	}
}

// startAgentInShell sends an agent command to an existing shell's tmux session.
// td-21a2d8: Called after shell is created when an agent was selected.
func (p *Plugin) startAgentInShell(tmuxName string, agentType AgentType, skipPerms bool) tea.Cmd {
	return func() tea.Msg {
		workDir := ""
		if p.ctx != nil {
			workDir = p.ctx.WorkDir
		}

		// Get the base command for this agent family, allowing workspace-level override.
		// Note: shell sessions pass p.ctx.WorkDir (the main workspace directory) as the
		// search path for .sidecar-agent-start, unlike worktree sessions which pass wt.Path
		// (the worktree-specific directory). This means .sidecar-agent-start in a worktree
		// does NOT affect shell session agent commands — only the workspace root file does.
		baseCmd := p.resolveAgentBaseCommand(workDir, agentType)
		if strings.TrimSpace(baseCmd) == "" {
			return ShellAgentErrorMsg{
				TmuxName: tmuxName,
				Err:      fmt.Errorf("empty agent command for type: %s", agentType),
			}
		}

		// Add skip permissions flag if enabled
		if skipPerms {
			if flag := SkipPermissionsFlags[agentType]; flag != "" {
				baseCmd = baseCmd + " " + flag
			}
		}
		baseCmd = withShellNamingInstruction(baseCmd, agentType)

		// Send the command to the shell's tmux session
		cmd := exec.Command("tmux", "send-keys", "-t", tmuxName, baseCmd, "Enter")
		if err := cmd.Run(); err != nil {
			return ShellAgentErrorMsg{
				TmuxName: tmuxName,
				Err:      fmt.Errorf("failed to start agent: %w", err),
			}
		}

		return ShellAgentStartedMsg{
			TmuxName:  tmuxName,
			AgentType: agentType,
			SkipPerms: skipPerms,
		}
	}
}

// shellEnvArgs are the new-session flags that publish a shell's identity into
// its own environment. An agent can then read $SIDECAR_SHELL_NAME to tell a
// default name from a deliberate one without asking anybody.
func shellEnvArgs(sessionName, displayName string) []string {
	return []string{
		"-e", shellstate.SessionEnv + "=" + sessionName,
		"-e", shellstate.NameEnv + "=" + displayName,
	}
}

// newShellSession creates a shell session with its identity environment,
// falling back to a plain create if this tmux rejects new-session -e (added in
// tmux 3.2). Losing the environment cue is a degraded shell; failing to create
// the session is a broken one, and the fallback still publishes the values to
// the session environment for any pane opened later.
func newShellSession(args []string, sessionName, displayName string) error {
	withEnv := append(append([]string(nil), args...), shellEnvArgs(sessionName, displayName)...)
	if err := tty.NewSession(withEnv...); err == nil {
		return nil
	}
	if err := tty.NewSession(args...); err != nil {
		return err
	}
	setShellEnv(sessionName, displayName)
	return nil
}

// setShellEnv publishes the display name to the tmux session environment.
// Panes already running keep the value they were created with — the manifest
// and `sidecar shell rename` remain the authority, this is only the cue.
func setShellEnv(sessionName, displayName string) {
	if sessionName == "" {
		return
	}
	_ = tty.SetSessionEnv(sessionName, shellstate.SessionEnv, sessionName)
	_ = tty.SetSessionEnv(sessionName, shellstate.NameEnv, displayName)
}

// withShellNamingInstruction appends the shell-naming guidance to an agent
// launch as a session-scoped system prompt. Documentation only reaches the
// harness that reads that particular file; the launch command reaches every
// agent Sidecar starts itself. Harnesses with no append flag are returned
// unchanged and rely on the SIDECAR_SHELL_NAME environment cue.
func withShellNamingInstruction(baseCmd string, agentType AgentType) string {
	flag := SystemPromptAppendFlags[agentType]
	if flag == "" {
		return baseCmd
	}
	return baseCmd + " " + flag + " " + shellQuote(shellstate.NamingInstruction)
}

// attachToShellByIndex attaches to a specific shell session by index.
func (p *Plugin) attachToShellByIndex(idx int) tea.Cmd {
	if idx < 0 || idx >= len(p.shells) {
		return nil
	}

	shell := p.shells[idx]
	sessionName := shell.TmuxName
	displayName := shell.Name

	target := ""
	if shell.Agent != nil && shell.Agent.TmuxPane != "" {
		target = shell.Agent.TmuxPane
	} else {
		target = sessionName
	}

	// Resize to full terminal before attaching so no dot borders appear
	return p.attachWithResize(target, sessionName, displayName, func(err error) tea.Msg {
		return ShellDetachedMsg{Err: err}
	})
}

// ensureShellAndAttachByIndex creates shell session if needed, then attaches.
func (p *Plugin) ensureShellAndAttachByIndex(idx int) tea.Cmd {
	if idx < 0 || idx >= len(p.shells) {
		return nil
	}

	shell := p.shells[idx]
	sessionName := shell.TmuxName

	// If session already exists, attach directly
	if sessionExists(sessionName) {
		return p.attachToShellByIndex(idx)
	}

	// Session doesn't exist but we have a record - recreate it
	workDir := p.ctx.WorkDir
	previewWidth, previewHeight := p.calculatePreviewDimensions()
	return tea.Sequence(
		func() tea.Msg {
			args := []string{"new-session", "-d", "-s", sessionName, "-c", workDir}
			if previewWidth > 0 && previewHeight > 0 {
				args = append(args, "-x", strconv.Itoa(previewWidth), "-y", strconv.Itoa(previewHeight))
			}
			if err := newShellSession(args, sessionName, shell.Name); err != nil {
				return ShellCreatedMsg{
					SessionName: sessionName,
					DisplayName: shell.Name,
					Err:         fmt.Errorf("recreate shell session: %w", err),
				}
			}
			tty.SetWindowSizeManual(sessionName)
			// Capture pane ID for interactive mode support
			paneID := getPaneID(sessionName)
			return ShellCreatedMsg{SessionName: sessionName, DisplayName: shell.Name, PaneID: paneID}
		},
		func() tea.Msg {
			if !waitForSession(sessionName) {
				return ShellCreatedMsg{
					SessionName: sessionName,
					DisplayName: shell.Name,
					Err:         fmt.Errorf("shell session failed to become ready"),
				}
			}
			return shellAttachAfterCreateMsg{Index: idx}
		},
	)
}

// waitForSession waits for a tmux session to become available using exponential backoff.
// Returns true if session exists, false if max attempts exceeded.
func waitForSession(sessionName string) bool {
	const maxAttempts = 10
	delay := 10 * time.Millisecond

	for range maxAttempts {
		if sessionExists(sessionName) {
			return true
		}
		time.Sleep(delay)
		delay *= 2 // Exponential backoff: 10, 20, 40, 80, 160, 320, 640ms...
		if delay > 200*time.Millisecond {
			delay = 200 * time.Millisecond // Cap at 200ms per attempt
		}
	}
	return false
}

// killShellSessionByName terminates a specific shell tmux session.
func (p *Plugin) killShellSessionByName(sessionName string) tea.Cmd {
	if sessionName == "" {
		return nil
	}

	return func() tea.Msg {
		// Kill the session
		cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
		_ = cmd.Run() // Ignore errors (session may already be dead)

		// Clean up pane cache
		globalPaneCache.remove(sessionName)

		return ShellKilledMsg{SessionName: sessionName}
	}
}

// pollShellSessionByName captures output from a specific shell session by name.
// Uses cached capture to avoid blocking subprocess calls (td-c2961e).
func (p *Plugin) pollShellSessionByName(tmuxName string) tea.Cmd {
	return p.scheduleShellPollByName(tmuxName, 0)
}

func (p *Plugin) captureShellSessionByName(tmuxName string, generation int) tea.Cmd {
	// Find the shell by TmuxName
	var shell *ShellSession
	for _, s := range p.shells {
		if s.TmuxName == tmuxName {
			shell = s
			break
		}
	}
	if shell == nil || shell.Agent == nil {
		return nil
	}

	// Capture references before spawning closure to avoid data races
	maxBytes := p.tmuxCaptureMaxBytes
	agentType := shell.Agent.Type
	modelPresentation := p.primaryTerminalOwns("shell", tmuxName)
	semanticScreen := shellSemanticNeedsScreen(agentType)
	ctx := p.ctx
	selectedShell := p.getSelectedShell()
	interactiveCapture := p.viewMode == ViewModeInteractive &&
		p.interactiveState != nil &&
		p.interactiveState.Active &&
		p.shellSelected &&
		selectedShell != nil &&
		selectedShell.TmuxName == tmuxName
	if interactiveCapture {
		if remaining, scrolling := p.interactiveScrollDelay(); scrolling {
			return p.scheduleShellPollByName(tmuxName, remaining)
		}
	}

	// When feature is enabled, skip -J for the selected shell so content wraps
	// at the pane width (matching interactive mode). Resize inline to avoid races.
	directCapture := false
	var resizeTarget string
	var previewWidth, previewHeight int
	if !interactiveCapture && features.IsEnabled(features.TmuxInteractiveInput.Name) {
		if selectedShell != nil && selectedShell.TmuxName == tmuxName {
			directCapture = true
			if p.termPanelVisible {
				previewWidth, previewHeight = p.calculateAgentPaneDimensions()
			} else {
				previewWidth, previewHeight = p.calculatePreviewDimensions()
			}
			previewWidth = p.terminalContentWidth(previewWidth)
			resizeTarget = p.previewResizeTarget()
		}
	}

	// Capture cursor target for atomic cursor position query
	var cursorTarget string
	if interactiveCapture && p.interactiveState != nil {
		cursorTarget = p.interactiveState.TargetPane
		if cursorTarget == "" {
			cursorTarget = p.interactiveState.TargetSession
		}
	}

	return func() tea.Msg {
		if ctx != nil {
			traceTerminalCapture(ctx.Logger, "workspace", "shell", "semantic_activity", generation)
		}
		if modelPresentation && !semanticScreen {
			capture, err := capturePaneEvidence(tmuxName)
			if err != nil {
				return ShellOutputMsg{TmuxName: tmuxName, Generation: generation, Err: err}
			}
			// Shared runtimes (agent/node) cannot be identified from the
			// process name alone. Fall through to a full bottom-buffer capture
			// so Cursor launched as `agent` upgrades off Type=shell.
			if !shellNeedsIdentityScreen(capture.CurrentCommand) {
				capturedAt := time.Now()
				observation := agentactivity.Observation{
					PaneTitle: capture.PaneTitle, CurrentCommand: capture.CurrentCommand, CapturedAt: capturedAt,
				}
				observedAgentType := AgentType(agentactivity.Identify(observation))
				if observedAgentType == "" {
					observedAgentType = agentType
				}
				return ShellOutputMsg{
					TmuxName: tmuxName, Generation: generation, AgentType: observedAgentType,
					CapturedAt: capturedAt, PaneTitle: capture.PaneTitle, CurrentCommand: capture.CurrentCommand,
					PaneHeight: capture.PaneHeight, PaneWidth: capture.PaneWidth,
				}
			}
		}
		// Ensure pane is at preview width before capturing (avoids race with async resize)
		if directCapture && resizeTarget != "" {
			if w, h, ok := tty.QueryPaneSize(resizeTarget); !ok || w != previewWidth || h != previewHeight {
				tty.ResizeTmuxPane(resizeTarget, previewWidth, previewHeight)
			} else {
				// Already the right size; still tick the geometry lease so a
				// settled owner does not go stale (td-ee222a).
				tty.TouchGeometryLease(resizeTarget)
			}
		}

		// Use direct capture for shells (no batch), preserving wraps in interactive mode.
		// Shell sessions have prefix "sidecar-sh-" not "sidecar-ws-" so batch capture skips them.
		joinWrapped := !interactiveCapture && !directCapture
		var output string
		var cursor capturedCursor
		var capture capturedPaneMetadata
		var err error
		if interactiveCapture && cursorTarget != "" {
			output, cursor, err = capturePaneDirectWithJoinAndCursor(tmuxName, cursorTarget, joinWrapped)
			capture = cursor.capturedPaneMetadata
		} else {
			output, capture, err = capturePaneDirectWithJoinMetadata(tmuxName, joinWrapped)
		}
		if err != nil {
			// Capture error - check error message to determine if session is dead
			// Avoid synchronous sessionExists() call which would block (td-c2961e)
			errStr := err.Error()
			if strings.Contains(errStr, "can't find") ||
				strings.Contains(errStr, "no server") ||
				strings.Contains(errStr, "no such session") ||
				strings.Contains(errStr, "session not found") {
				return ShellSessionDeadMsg{TmuxName: tmuxName, Generation: generation}
			}
			// Other errors (timeout, etc.) - return empty output and schedule retry
			return ShellOutputMsg{TmuxName: tmuxName, Generation: generation, Err: err}
		}

		// Trim to max bytes
		var removedRows int
		output, removedRows = trimCapturedOutputRows(output, maxBytes)
		if capture.Valid {
			capture.CaptureBase += removedRows
		}
		capturedAt := time.Now()
		observation := agentactivity.Observation{
			Screen: output, PaneTitle: capture.PaneTitle,
			CurrentCommand: capture.CurrentCommand, CapturedAt: capturedAt,
		}
		observedAgentType := AgentType(agentactivity.Identify(observation))
		if observedAgentType == "" {
			observedAgentType = agentType
		}
		activity := agentactivity.Result{}
		if supportsAgentActivity(observedAgentType) {
			observation.Agent = string(observedAgentType)
			activity = agentactivity.Detect(observation)
		}

		return ShellOutputMsg{
			TmuxName:       tmuxName,
			Generation:     generation,
			AgentType:      observedAgentType,
			Output:         output,
			CursorRow:      cursor.Row,
			CursorCol:      cursor.Col,
			CursorVisible:  cursor.Visible,
			HasCursor:      cursor.Valid,
			PaneHeight:     capture.PaneHeight,
			PaneWidth:      capture.PaneWidth,
			HistorySize:    capture.HistorySize,
			CaptureBase:    capture.CaptureBase,
			HasHistory:     capture.Valid,
			RowsJoined:     capture.RowsJoined,
			Activity:       activity,
			CapturedAt:     capturedAt,
			PaneTitle:      capture.PaneTitle,
			CurrentCommand: capture.CurrentCommand,
		}
	}
}

// scheduleShellPollByName schedules a poll for a specific shell's output by name.
// Uses generation tracking (td-83dc22) to invalidate stale timers when shells are removed.
func (p *Plugin) scheduleShellPollByName(tmuxName string, delay time.Duration) tea.Cmd {
	return p.pollScheduler.Schedule(shellPollKey(tmuxName), delay, func(gen int) tea.Msg {
		return pollShellByNameMsg{TmuxName: tmuxName, Generation: gen}
	})
}

// shellSemanticNeedsScreen reports whether a known live agent needs the bottom
// buffer for activity rules. Plain shells stay on metadata until the process
// name alone cannot identify the program (see shellNeedsIdentityScreen).
func shellSemanticNeedsScreen(agentType AgentType) bool {
	return supportsAgentActivity(agentType)
}

// shellNeedsIdentityScreen is true when pane_current_command is a shared
// runtime whose provider can only be recovered from live UI chrome. Cursor's
// official CLI is often installed as `agent` and re-execs bundled node.
func shellNeedsIdentityScreen(currentCommand string) bool {
	switch strings.ToLower(strings.TrimSpace(currentCommand)) {
	case "agent", "node", "bun":
		return true
	default:
		return false
	}
}

// findShellByName returns the shell with the given TmuxName, or nil if not found.
func (p *Plugin) findShellByName(tmuxName string) *ShellSession {
	for _, s := range p.shells {
		if s.TmuxName == tmuxName {
			return s
		}
	}
	return nil
}

// getSelectedShell returns the currently selected shell, or nil if none.
func (p *Plugin) getSelectedShell() *ShellSession {
	if !p.shellSelected || p.selectedShellIdx < 0 || p.selectedShellIdx >= len(p.shells) {
		return nil
	}
	return p.shells[p.selectedShellIdx]
}

// handleResumeConversation processes ResumeConversationMsg from conversations plugin (td-aa4136).
// Creates a new shell or worktree based on msg.Type and starts the resume flow.
func (p *Plugin) handleResumeConversation(msg ResumeConversationMsg) (*Plugin, tea.Cmd) {
	switch msg.Type {
	case "shell":
		return p, p.createShellWithResume(msg.ResumeCmd)
	case "worktree":
		return p, p.createWorktreeWithResume(msg)
	default:
		return p, nil
	}
}

// createShellWithResume creates a new shell and injects the resume command.
// The command is typed into the shell but not executed - user presses Enter.
func (p *Plugin) createShellWithResume(resumeCmd string) tea.Cmd {
	// Store pending resume command to inject after shell creation
	p.pendingResumeCmd = resumeCmd

	// Create new shell (ShellCreatedMsg will trigger command injection)
	return p.createNewShell("")
}

// sendResumeCommandToShell injects a command into the shell without executing it.
func (p *Plugin) sendResumeCommandToShell(tmuxSession string, resumeCmd string) tea.Cmd {
	if !isTmuxInstalled() {
		return nil
	}

	return func() tea.Msg {
		// Use tmux send-keys to type the command without pressing Enter
		// This lets the user review before executing
		cmd := exec.Command("tmux", "send-keys", "-t", tmuxSession, resumeCmd)
		if err := cmd.Run(); err != nil {
			return shellResumeErrorMsg{Err: err}
		}
		return shellResumeInjectedMsg{TmuxSession: tmuxSession}
	}
}

// shellResumeInjectedMsg signals that resume command was injected into shell.
type shellResumeInjectedMsg struct {
	TmuxSession string
}

// shellResumeErrorMsg signals an error injecting resume command.
type shellResumeErrorMsg struct {
	Err error
}

// worktreeResumeCreatedMsg signals that a worktree for resume was created (td-aa4136).
type worktreeResumeCreatedMsg struct {
	OperationScope
	Worktree  *Worktree
	ResumeCmd string
	AgentType AgentType
	SkipPerms bool
	Err       error
}

// createWorktreeWithResume creates a new worktree and starts the agent with the resume command.
func (p *Plugin) createWorktreeWithResume(msg ResumeConversationMsg) tea.Cmd {
	ctx, scope := p.newLifecycleScope(nil)
	name := msg.WorktreeName
	baseBranch := msg.BaseBranch
	agentType := msg.AgentType
	skipPerms := msg.SkipPerms
	resumeCmd := msg.ResumeCmd

	if name == "" {
		return func() tea.Msg {
			return worktreeResumeCreatedMsg{OperationScope: scope, Err: fmt.Errorf("workspace name is required")}
		}
	}
	ctxCopy := *p.ctx
	runner := *p
	runner.ctx = &ctxCopy

	return func() tea.Msg {
		// Create the worktree (reuse doCreateWorktree)
		wt, err := runner.doCreateWorktreeContext(ctx, name, baseBranch, "", "", agentType)
		if err != nil {
			return worktreeResumeCreatedMsg{OperationScope: scope, Err: err}
		}

		return worktreeResumeCreatedMsg{
			OperationScope: scope,
			Worktree:       wt,
			ResumeCmd:      resumeCmd,
			AgentType:      agentType,
			SkipPerms:      skipPerms,
		}
	}
}

// startAgentWithResumeCmd starts an agent in a worktree with a resume command instead of normal startup.
func (p *Plugin) startAgentWithResumeCmd(wt *Worktree, agentType AgentType, skipPerms bool, resumeCmd string) tea.Cmd {
	epoch := p.ctx.Epoch // Capture epoch for stale detection
	workDir := p.ctx.WorkDir
	name, path := wt.Name, wt.Path
	return func() tea.Msg {
		sessionName := tmuxSessionPrefix + sanitizeName(name)

		// Check if session already exists
		checkCmd := exec.Command("tmux", "has-session", "-t", sessionName)
		if checkCmd.Run() == nil {
			// Session exists - should not happen for new resume worktree
			paneID := getPaneID(sessionName)
			return AgentStartedMsg{
				Epoch:         epoch,
				WorkspaceName: name,
				SessionName:   sessionName,
				PaneID:        paneID,
				AgentType:     agentType,
				Reconnected:   true,
			}
		}

		// Create new detached session with working directory
		args := []string{
			"new-session",
			"-d",              // Detached
			"-s", sessionName, // Session name
			"-c", path, // Working directory
		}

		if err := tty.NewSession(args...); err != nil {
			return AgentStartedMsg{Epoch: epoch, Err: fmt.Errorf("create session: %w", err)}
		}

		// Set TD_SESSION_ID environment variable for td session tracking
		tdEnvCmd := fmt.Sprintf("export TD_SESSION_ID=%s", shellQuote(sessionName))
		_ = exec.Command("tmux", "send-keys", "-t", sessionName, tdEnvCmd, "Enter").Run()

		// Apply environment isolation
		envOverrides := BuildEnvOverrides(workDir)
		if envCmd := GenerateSingleEnvCommand(envOverrides); envCmd != "" {
			_ = exec.Command("tmux", "send-keys", "-t", sessionName, envCmd, "Enter").Run()
		}

		// Small delay to ensure env is set
		time.Sleep(100 * time.Millisecond)

		// Send the resume command instead of the normal agent command
		sendCmd := exec.Command("tmux", "send-keys", "-t", sessionName, resumeCmd, "Enter")
		if err := sendCmd.Run(); err != nil {
			// Try to kill the session if we failed to start the agent
			_ = exec.Command("tmux", "kill-session", "-t", sessionName).Run()
			return AgentStartedMsg{Epoch: epoch, Err: fmt.Errorf("start agent with resume: %w", err)}
		}

		// Capture pane ID for interactive mode support
		paneID := getPaneID(sessionName)

		return AgentStartedMsg{
			Epoch:         epoch,
			WorkspaceName: name,
			SessionName:   sessionName,
			PaneID:        paneID,
			AgentType:     agentType,
		}
	}
}
