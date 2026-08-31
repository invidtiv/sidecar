// Package workspaceinventory provides the narrow, read-only inventory used by
// cross-project surfaces. It never creates or reconciles Sidecar state.
package workspaceinventory

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentresolve"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tty"
)

type Kind string

const (
	KindWorktree Kind = "worktree"
	KindShell    Kind = "shell"
)

type Pane struct {
	ID, Session, Path, Command, Title string
	PID                               int
	// ServerPID is the pid of the tmux server that listed this pane, from the
	// server-scoped `#{pid}` format. It is the same for every pane in one
	// listing and it is here because it is free: the refresh already takes this
	// listing, so carrying the server identity on it is what lets shell
	// cold-restore eligibility be recorded without a single extra tmux spawn.
	//
	// Unlike the socket's inode and ctime, a server pid is stable for the
	// server's whole lifetime and new after a restart, which is what makes it
	// the part of the identity that may be written to disk.
	ServerPID int
	Dead      bool
}

type Workspace struct {
	ID, ProjectKey, ProjectName, ProjectRoot string
	// HostID names the machine this workspace lives on. Empty means this
	// machine, which is what every locally collected workspace is — the field
	// exists so a remote row can be an ordinary workspace rather than a
	// parallel type with its own rendering. Anything that acts on a workspace
	// rather than merely displaying it must check it: an action resolved
	// against a local path would operate on the wrong machine's filesystem.
	HostID string
	// Preview is capture text the collector on the owning machine already
	// took. Empty for local workspaces, which read their preview live rather
	// than carrying it.
	Preview                               string
	Kind                                  Kind
	Key, Name, Path, Branch, TaskID       string
	TmuxName, PaneID, Provider, Namespace string
	// Plain marks a workspace collected with no agent evidence at all: a Git
	// worktree with no recorded agent. It is the catalog's honest answer to
	// "is there an agent here?", and it is what keeps the Agents projection
	// identical to what it collected before the catalog carried plain rows.
	Plain bool
	// IsMain is explicit inventory identity, resolved while collecting Git's
	// worktree list. Presentation callers must not guess it from a name.
	IsMain bool
	// The remaining worktree state git reports. They exist so a presentation
	// surface can refuse an action for the same reasons the project surface
	// refuses it: a locked worktree cannot be removed, a bare or detached one
	// has no branch to act on, and a prunable or missing one is only
	// metadata. IsMissing means the directory is gone, whether git has noticed
	// (prunable) or not.
	IsBare, IsDetached, IsLocked, IsPrunable, IsMissing bool
	// Live and Ambiguous describe session health, which is not the same
	// question as agent activity: a plain shell or an agentless worktree can
	// own a live pane while having no agent semantics at all. Keeping them
	// separate is what lets the catalog carry plain workspaces without
	// fabricating an agentstatus value for them.
	Live, Ambiguous bool
	Presentation    agentstatus.Presentation
	ObservedAt      time.Time
	// CreatedAt is the shell manifest's record of when this identity was
	// written. Empty for worktrees, which have no such record.
	CreatedAt time.Time
}

// Remote reports whether this workspace lives on another machine. Every
// mutating path must refuse a remote workspace until Phase C gives the host
// protocol a request channel; until then an action resolved here would run
// against a local path that either does not exist or, worse, does.
func (w Workspace) Remote() bool { return w.HostID != "" }

// Remote reports whether this catalog row lives on another machine.
func (i Item) Remote() bool { return i.HostID != "" }

// HasAgent reports durable or detected agent evidence. A worktree earns it
// from its recorded `agent` file; a shell earns it from a configured agent
// type or from live identification, which is why an unidentified shell can
// still become an agent on a later status poll. Everything else is plain.
func (w Workspace) HasAgent() bool {
	if w.Kind == KindShell {
		return strings.TrimSpace(w.Provider) != ""
	}
	return !w.Plain
}

// Item is the read-only catalog row shared by the Agents board and the global
// Workspaces browser. Agent is nil for plain shells and worktrees: they are
// given presentation buckets by the list projection rather than a fabricated
// semantic state.
type Item struct {
	ID                      string
	HostID                  string
	Preview                 string
	ProjectKey, ProjectName string
	ProjectRoot             string
	Kind                    Kind
	Key, Name, Path, Branch string
	TaskID                  string
	Provider                string
	PaneID, TmuxName        string
	Live, Ambiguous         bool
	IsMain                  bool
	Agent                   *agentstatus.Presentation
	ObservedAt              time.Time
}

// Item projects one collected workspace into its catalog row.
func (w Workspace) Item() Item {
	item := Item{
		ID: w.ID, HostID: w.HostID, Preview: w.Preview,
		ProjectKey: w.ProjectKey, ProjectName: w.ProjectName, ProjectRoot: w.ProjectRoot,
		Kind: w.Kind, Key: w.Key, Name: w.Name, Path: w.Path, Branch: w.Branch, TaskID: w.TaskID,
		Provider: w.Provider, PaneID: w.PaneID, TmuxName: w.TmuxName,
		Live: w.Live, Ambiguous: w.Ambiguous, IsMain: w.IsMain, ObservedAt: w.ObservedAt,
	}
	if w.HasAgent() {
		presentation := w.Presentation
		item.Agent = &presentation
	}
	return item
}

// Catalog projects a whole project result: every durable shell and Git
// worktree it holds, agent-backed or not.
func Catalog(result ProjectResult) []Item {
	items := make([]Item, 0, len(result.Workspaces))
	for _, workspace := range result.Workspaces {
		items = append(items, workspace.Item())
	}
	return items
}

// AgentWorkspaces is the agent-only projection the Kanban board consumes. It
// excludes exactly the items with no agent evidence and changes nothing else,
// so the board keeps the full shared semantic matrix it had before the catalog
// carried plain workspaces.
func AgentWorkspaces(workspaces []Workspace) []Workspace {
	agents := make([]Workspace, 0, len(workspaces))
	for _, workspace := range workspaces {
		if workspace.HasAgent() {
			agents = append(agents, workspace)
		}
	}
	return agents
}

type ProjectResult struct {
	ProjectKey, ProjectName, ProjectRoot string
	Workspaces                           []Workspace
	ObservedAt                           time.Time
	Err                                  error
}

// ValidateWorkspace rechecks a card's exact durable identity without creating,
// migrating, reconciling, or mutating project state.
func (c Collector) ValidateWorkspace(ctx context.Context, workspace Workspace) error {
	c = c.defaults()
	if canonical(workspace.ProjectRoot) != workspace.ProjectKey {
		return fmt.Errorf("project identity changed")
	}
	info, err := os.Stat(workspace.ProjectRoot)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("project is no longer available")
	}
	switch workspace.Kind {
	case KindWorktree:
		out, err := c.Runner.Output(ctx, "git", "--no-optional-locks", "-C", workspace.ProjectRoot, "worktree", "list", "--porcelain")
		if err != nil {
			return fmt.Errorf("revalidate worktrees: %w", err)
		}
		found := false
		for _, wt := range parseWorktrees(string(out)) {
			if canonical(wt.Path) == workspace.Key && canonical(wt.Path) == canonical(workspace.Path) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("worktree is no longer available")
		}
		// A plain worktree — the main worktree, or one nobody has run an agent
		// in — is a real destination for the global browser, and it has no
		// agent identity to recheck. Demanding one here would refuse to open
		// exactly the rows the catalog added. Git's own worktree list above is
		// the whole identity for those, and it is the same fact the project's
		// Workspaces plugin resolves the pending selection against.
		if !workspace.HasAgent() {
			return nil
		}
		stateDir, ok := lookupWorktree(workspace.ProjectRoot, workspace.Path)
		if !ok {
			return fmt.Errorf("worktree agent identity is no longer available")
		}
		agent, err := readRegularFile(filepath.Join(stateDir, "agent"))
		if err != nil || strings.TrimSpace(string(agent)) == "" {
			return fmt.Errorf("worktree agent identity changed")
		}
	case KindShell:
		projectState, ok := lookupProject(workspace.ProjectRoot)
		if !ok {
			return fmt.Errorf("shell project identity is no longer available")
		}
		for _, shell := range readShells(filepath.Join(projectState, "shells.json")) {
			if shell.TmuxName == workspace.Key && shell.TmuxName == workspace.TmuxName {
				return nil
			}
		}
		return fmt.Errorf("shell identity is no longer available")
	default:
		return fmt.Errorf("unknown workspace identity")
	}
	return nil
}

type Runner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// CaptureFunc reads one pane: its capture text and the geometry observed with
// it. The geometry travels with the capture because a capture's rows alone
// cannot say where the live grid starts, and a consumer that has to ask
// separately pairs rows from one instant with a height from another.
type CaptureFunc func(target string, lines int) (string, tty.PaneState, error)

type Collector struct {
	Runner             Runner
	Capture            CaptureFunc
	Now                func() time.Time
	DoneTTL            time.Duration
	trackers           *trackerStore
	captures           chan struct{}
	metrics            *RefreshMetrics
	reservedSessions   map[string]bool
	shellOwners        map[string]string
	trackerBase        *trackerStore
	beforeTrackerApply func()
	hostPaneID         string

	// lifecycle supplies provider lifecycle reports to the shared resolver.
	// Nil is the ordinary state today and means pure screen detection, which is
	// byte-for-byte the behavior this collector had before the resolver was
	// extracted.
	lifecycle agentresolve.Source
}

// RefreshMetrics contains privacy-safe operation and concurrency counters for
// an in-memory refresh. It never includes project paths, pane IDs, or output.
type RefreshMetrics struct {
	projectOps     atomic.Int64
	captures       atomic.Int64
	activeCaptures atomic.Int64
	maxCaptures    atomic.Int64
	trackerCommits atomic.Int64
}

type MetricsSnapshot struct {
	ProjectOps, Captures, MaxCaptures, TrackerCommits int64
}

type trackerStore struct {
	mu     sync.Mutex
	values map[string]agentactivity.Tracker
}

func (c Collector) defaults() Collector {
	if c.Runner == nil {
		c.Runner = ExecRunner{}
	}
	if c.Capture == nil {
		c.Capture = tty.CapturePaneWithState
	}
	if c.hostPaneID == "" {
		// The pane hosting this process is not a workspace: a row correlated to
		// it hands the preview a resize target that is sidecar's own screen.
		// See tmuxenv.HostingPane. Tests set it explicitly through HostPane so a
		// fake pane list can exercise the exclusion without an outer tmux.
		c.hostPaneID = tmuxenv.HostingPane()
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.lifecycle == nil {
		// The same default the project surface installs, for the same reason:
		// with no lifecycle log on disk this answers "no evidence" and the
		// resolver's no-evidence path is byte-identical to plain screen
		// detection, so a machine with no integration behaves exactly as before
		// at the cost of one stat. Both surfaces must get it or they would
		// disagree about the same pane, which is the parity bug this whole
		// extraction exists to prevent.
		c.lifecycle = defaultLifecycleSource()
	}
	if c.trackers == nil {
		c.trackers = &trackerStore{values: make(map[string]agentactivity.Tracker)}
	}
	if c.DoneTTL == 0 {
		c.DoneTTL = agentstatus.DefaultDoneTTL
	}
	return c
}

// SeedTrackers installs persisted activity as the collector's starting state.
// Entries already observed in this process win: a live reading is always
// better evidence than a restored one.
func (c Collector) SeedTrackers(seed map[string]agentactivity.Tracker) Collector {
	c = c.defaults()
	c.trackers.mu.Lock()
	defer c.trackers.mu.Unlock()
	for key, tracker := range seed {
		if _, exists := c.trackers.values[key]; !exists {
			c.trackers.values[key] = tracker
		}
	}
	return c
}

// TrackerSnapshot copies committed activity for persistence.
func (c Collector) TrackerSnapshot() map[string]agentactivity.Tracker {
	c = c.defaults()
	c.trackers.mu.Lock()
	defer c.trackers.mu.Unlock()
	values := make(map[string]agentactivity.Tracker, len(c.trackers.values))
	for key, tracker := range c.trackers.values {
		values[key] = tracker
	}
	return values
}

func (c Collector) WithDefaults() Collector { return c.defaults() }

// ForRefresh returns a collector sharing provider history with its parent while
// independently bounding pane captures for one refresh generation.
func (c Collector) ForRefresh(maxCaptures int, claims ...ShellClaims) Collector {
	c = c.defaults()
	if maxCaptures < 1 {
		maxCaptures = 1
	}
	c.captures = make(chan struct{}, maxCaptures)
	c.metrics = &RefreshMetrics{}
	c.trackerBase = c.trackers
	c.trackers.mu.Lock()
	localTrackers := make(map[string]agentactivity.Tracker, len(c.trackers.values))
	for key, tracker := range c.trackers.values {
		localTrackers[key] = tracker
	}
	c.trackers.mu.Unlock()
	c.trackers = &trackerStore{values: localTrackers}
	if len(claims) > 0 {
		c.reservedSessions = claims[0].Sessions
		c.shellOwners = claims[0].Owners
	}
	return c
}

// CommitTrackers promotes a completed refresh's generation-local activity
// state. Canceled/stale refreshes are simply never committed.
func (c Collector) CommitTrackers() {
	if c.trackerBase == nil {
		return
	}
	c.trackers.mu.Lock()
	values := make(map[string]agentactivity.Tracker, len(c.trackers.values))
	for key, tracker := range c.trackers.values {
		values[key] = tracker
	}
	c.trackers.mu.Unlock()
	c.trackerBase.mu.Lock()
	for key, tracker := range values {
		c.trackerBase.values[key] = tracker
	}
	c.trackerBase.mu.Unlock()
	if c.metrics != nil {
		c.metrics.trackerCommits.Add(1)
	}
}

func (c Collector) WithShellClaims(claims ShellClaims) Collector {
	c.reservedSessions = claims.Sessions
	c.shellOwners = claims.Owners
	return c
}

// HostPane overrides which pane ID counts as the pane hosting this process.
// The default comes from TMUX_PANE; tests set a fake one so the exclusion can
// be exercised against a scripted pane list.
func (c Collector) HostPane(id string) Collector {
	c.hostPaneID = id
	return c
}

func (c Collector) Metrics() MetricsSnapshot {
	if c.metrics == nil {
		return MetricsSnapshot{}
	}
	return MetricsSnapshot{ProjectOps: c.metrics.projectOps.Load(), Captures: c.metrics.captures.Load(), MaxCaptures: c.metrics.maxCaptures.Load(), TrackerCommits: c.metrics.trackerCommits.Load()}
}

// ServerPIDOf returns the tmux server pid a pane listing reported, or 0 when the
// listing carries none.
//
// Every pane in one listing comes from one server, so the first non-zero value
// is the answer. Callers use it to qualify a socket-only incarnation with the
// process behind it, which is the difference between an identity that can be
// written down and compared later and one that only names a file.
func ServerPIDOf(panes []Pane) int {
	for _, pane := range panes {
		if pane.ServerPID > 0 {
			return pane.ServerPID
		}
	}
	return 0
}

// ListPanes takes the single global tmux inventory used by an Overview refresh.
func (c Collector) ListPanes(ctx context.Context) ([]Pane, error) {
	c = c.defaults()
	out, err := c.Runner.Output(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_id}\t#{session_name}\t#{pane_current_path}\t#{pane_current_command}\t#{pane_title}\t#{pane_dead}\t#{pane_pid}\t#{pid}")
	if err != nil {
		message := strings.ToLower(string(out))
		if strings.Contains(message, "no server running") || strings.Contains(message, "no sessions") ||
			(strings.Contains(message, "error connecting to") && strings.Contains(message, "no such file")) {
			return nil, nil
		}
		return nil, err
	}
	var panes []Pane
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// The trailing fields are read defensively by index rather than by an
		// exact count, so a listing from an older format string — a fixture, or a
		// tmux too old to answer one of them — still yields the fields it does
		// have instead of being discarded whole.
		parts := strings.SplitN(line, "\t", 8)
		if len(parts) < 6 {
			continue
		}
		p := Pane{ID: parts[0], Session: parts[1], Path: filepath.Clean(parts[2]), Command: parts[3], Title: parts[4], Dead: parts[5] == "1"}
		if len(parts) >= 7 {
			p.PID, _ = strconv.Atoi(strings.TrimSpace(parts[6]))
		}
		if len(parts) >= 8 {
			p.ServerPID, _ = strconv.Atoi(strings.TrimSpace(parts[7]))
		}
		// The pane hosting this process never enters the inventory. Correlation
		// is by cwd, so without this a sidecar launched from a plain shell in
		// (or beside) a catalogued project — a mosh session, an outer tmux — is
		// the only live match for that project's worktree row, and the preview
		// bound to it asserts geometry on the window sidecar itself draws in.
		if c.hostPaneID != "" && p.ID == c.hostPaneID {
			continue
		}
		panes = append(panes, p)
	}
	return panes, nil
}

// CollectProject reads one Git inventory and already-existing Sidecar metadata.
func (c Collector) CollectProject(ctx context.Context, name, root string, allRoots []string, panes []Pane) ProjectResult {
	result := c.CollectProjectInventory(ctx, name, root)
	if result.Err != nil {
		return result
	}
	if c.reservedSessions == nil {
		c = c.ForRefresh(max(1, cap(c.captures)), BuildShellClaims([]ProjectResult{result}))
	}
	result = c.RefreshProjectStatus(ctx, result, allRoots, panes)
	// One-shot inventory callers receive only agent workspaces. Stateful
	// consumers such as Overview use RefreshProjectStatus directly and retain
	// hidden candidates so a later poll can notice an agent started in a shell,
	// and retain plain workspaces because the global browser lists them.
	result.Workspaces = slices.DeleteFunc(result.Workspaces, func(w Workspace) bool { return !w.HasAgent() })
	return result
}

// CollectProjectInventory reads one project's lightweight Git and existing
// Sidecar metadata without inspecting tmux or capturing panes. Callers can
// therefore fan this stage out with the same project bound as the rest of a
// refresh and paint each result before global shell collision resolution.
func (c Collector) CollectProjectInventory(ctx context.Context, name, root string) ProjectResult {
	c = c.defaults()
	if c.metrics != nil {
		c.metrics.projectOps.Add(1)
	}
	now := c.Now()
	result := ProjectResult{ProjectKey: canonical(root), ProjectName: name, ProjectRoot: canonical(root), ObservedAt: now}
	var shells []shellDefinition
	if projectState, ok := lookupProject(root); ok {
		shells = readShells(filepath.Join(projectState, "shells.json"))
	}
	// Durable shells are Sidecar state, not Git state: they must survive every
	// early return below. Dropping them when the Git inventory failed hid a
	// whole project from the global browser even though its shells were
	// recorded and live.
	appendShells := func() {
		for _, shell := range shells {
			workspace := Workspace{ProjectKey: result.ProjectKey, ProjectName: name, ProjectRoot: result.ProjectRoot, Kind: KindShell, Key: shell.TmuxName, Name: shell.DisplayName, Path: result.ProjectRoot, TmuxName: shell.TmuxName, Provider: shell.AgentType, Namespace: shell.Namespace, CreatedAt: shell.CreatedAt, ObservedAt: now}
			workspace.ID = workspace.ProjectKey + ":shell:" + workspace.Key
			workspace.Presentation = agentstatus.Resolve(agentstatus.Input{ProviderSupported: supported(shell.AgentType), Orphaned: true, CapturedAt: now, Now: now})
			result.Workspaces = append(result.Workspaces, workspace)
		}
	}
	info, err := os.Stat(root)
	if err != nil {
		result.Err = fmt.Errorf("configured project missing: %w", err)
		appendShells()
		return result
	}
	if !info.IsDir() {
		result.Err = fmt.Errorf("configured project is not a directory")
		appendShells()
		return result
	}
	out, err := c.Runner.Output(ctx, "git", "--no-optional-locks", "-C", root, "worktree", "list", "--porcelain")
	if err != nil {
		result.Err = fmt.Errorf("configured project is not a Git repository: %w", err)
		appendShells()
		return result
	}
	// Every Git worktree is catalogued, including the main worktree and
	// worktrees with no agent and no session. Sidecar's recorded agent/task
	// metadata upgrades a row when it exists; its absence demotes the row to a
	// plain workspace rather than hiding it. The Agents projection filters
	// these out again, so the board sees exactly what it saw before.
	for _, wt := range parseWorktrees(string(out)) {
		provider, taskID, displayName := "", "", ""
		if stateDir, ok := lookupWorktree(root, wt.Path); ok {
			if agentBytes, err := readRegularFile(filepath.Join(stateDir, "agent")); err == nil {
				provider = strings.TrimSpace(string(agentBytes))
			}
			if provider != "" {
				taskBytes, _ := readRegularFile(filepath.Join(stateDir, "task"))
				taskID = strings.TrimSpace(string(taskBytes))
			}
			if displayBytes, err := readRegularFile(filepath.Join(stateDir, "display-name")); err == nil {
				displayName = strings.TrimSpace(string(displayBytes))
			}
		}
		itemName := filepath.Base(wt.Path)
		if displayName != "" {
			itemName = displayName
		}
		workspace := Workspace{ProjectKey: result.ProjectKey, ProjectName: name, ProjectRoot: result.ProjectRoot, Kind: KindWorktree, Key: canonical(wt.Path), Name: itemName, Path: canonical(wt.Path), Branch: wt.Branch, TaskID: taskID, Provider: provider, IsMain: canonical(wt.Path) == result.ProjectKey, ObservedAt: now}
		workspace.IsBare, workspace.IsDetached = wt.Bare, wt.Detached
		workspace.IsLocked, workspace.IsPrunable = wt.Locked, wt.Prunable
		// A directory git has not yet marked prunable can still be gone; the
		// project surface answers the same way (repo_snapshot.go).
		_, statErr := os.Stat(wt.Path)
		workspace.IsMissing = wt.Prunable || os.IsNotExist(statErr)
		workspace.ID = workspace.ProjectKey + ":worktree:" + workspace.Key
		workspace.Plain = provider == ""
		if workspace.HasAgent() {
			workspace.Presentation = agentstatus.Resolve(agentstatus.Input{ProviderSupported: supported(provider), Orphaned: true, CapturedAt: now, Now: now})
		}
		result.Workspaces = append(result.Workspaces, workspace)
	}

	appendShells()
	return result
}

// RefreshProjectStatus reuses an immutable successful project inventory and
// refreshes only live tmux/provider evidence. It performs no Git or metadata
// reads, allowing visible polling to stay cheaper than explicit inventory
// refreshes.
func (c Collector) RefreshProjectStatus(ctx context.Context, previous ProjectResult, allRoots []string, panes []Pane) ProjectResult {
	c = c.defaults()
	now := c.Now()
	result := previous
	result.ObservedAt = now
	result.Workspaces = append([]Workspace(nil), previous.Workspaces...)
	// A failed inventory with no workspaces has nothing to refresh. One that
	// still carries durable shells does: the status pass reads only tmux
	// evidence, so a Git failure must not freeze those shells into a
	// permanently session-less presentation.
	if previous.Err != nil && len(previous.Workspaces) == 0 {
		return result
	}
	for i := range result.Workspaces {
		workspace := &result.Workspaces[i]
		workspace.ObservedAt = now
		var matches []Pane
		switch workspace.Kind {
		case KindWorktree:
			matches = resolveWorktreePanes(*workspace, panesForPath(workspace.Path, allRoots, panes, c.reservedSessions))
		case KindShell:
			if workspace.Namespace != "" && workspace.Namespace == tmuxenv.Namespace() {
				matches = panesForOwnedSession(workspace.TmuxName, workspace.ProjectRoot, allRoots, panes, c.shellOwners)
			}
		}
		c.observeContext(ctx, workspace, matches, now)
	}
	return result
}

func (c Collector) observe(workspace *Workspace, matches []Pane, now time.Time) {
	c.observeContext(context.Background(), workspace, matches, now)
}

func (c Collector) observeContext(ctx context.Context, workspace *Workspace, matches []Pane, now time.Time) {
	workspace.Live, workspace.Ambiguous = false, false
	switch {
	case len(matches) > 1:
		workspace.Ambiguous = true
	case len(matches) == 1:
		workspace.PaneID, workspace.TmuxName = matches[0].ID, matches[0].Session
		workspace.Live = !matches[0].Dead
	}
	// A worktree with no recorded agent is a plain workspace. It gets pane
	// correlation — that is what "live" means — but no capture and no
	// agentstatus value: fabricating one would put a fake semantic state on the
	// board and in the list.
	if workspace.Kind == KindWorktree && workspace.Plain {
		return
	}
	input := agentstatus.Input{ProviderSupported: supported(workspace.Provider), CapturedAt: now, Now: now, StaleAfter: time.Minute, DoneTTL: c.DoneTTL}
	if len(matches) == 0 {
		input.Orphaned = true
	} else if len(matches) > 1 {
		input.Ambiguous = true
	} else {
		pane := matches[0]
		workspace.PaneID, workspace.TmuxName = pane.ID, pane.Session
		if pane.Dead {
			input.Orphaned = true
		} else if output, _, err := c.capturePane(ctx, pane.ID, 80); err != nil {
			input.Err = true
		} else {
			select {
			case <-ctx.Done():
				input.Unavailable = true
				workspace.Presentation = agentstatus.Resolve(input)
				return
			default:
			}
			processIdentity := ""
			if pane.PID > 0 && agentactivity.NeedsProcessIdentity(pane.Command) {
				processIdentity = agentactivity.ResolveForegroundAgent(pane.PID)
			}
			ob := agentactivity.Observation{Agent: workspace.Provider, Screen: output, PaneTitle: pane.Title, CurrentCommand: pane.Command, ProcessIdentity: processIdentity, CapturedAt: now}
			if identified := agentactivity.Identify(ob); identified != "" {
				if identified == "shell" {
					// A live shell process is not the launch-preference agent.
					// Keeping manifest agentType here is how defaultAgentType=
					// cursor painted every zsh pane as Cursor.
					workspace.Provider, ob.Agent = "", ""
					input.ProviderSupported = false
				} else {
					workspace.Provider, ob.Agent = identified, identified
					input.ProviderSupported = supported(identified)
				}
			}
			c.trackers.mu.Lock()
			select {
			case <-ctx.Done():
				c.trackers.mu.Unlock()
				input.Unavailable = true
				workspace.Presentation = agentstatus.Resolve(input)
				return
			default:
			}
			if c.beforeTrackerApply != nil {
				c.beforeTrackerApply()
			}
			tracker := c.trackers.values[workspace.ID]
			// The same resolver the two project polling paths call, applied
			// before this surface's own tracker exactly as they do. This is the
			// site the M6 plan warns about changing alone: doing so would give
			// the global Sessions view deterministic state while the project
			// Workspace and its notifications stayed screen-only.
			tracker.Apply(agentresolve.Result(ob, agentresolve.PaneRef{PaneID: pane.ID, Session: workspace.TmuxName}, c.lifecycle, now), now)
			c.trackers.values[workspace.ID] = tracker
			c.trackers.mu.Unlock()
			input.Activity = tracker
		}
	}
	workspace.Presentation = agentstatus.Resolve(input)
}

func (c Collector) capturePane(ctx context.Context, paneID string, lines int) (string, tty.PaneState, error) {
	if c.captures == nil {
		return c.Capture(paneID, lines)
	}
	select {
	case c.captures <- struct{}{}:
		defer func() { <-c.captures }()
	case <-ctx.Done():
		return "", tty.PaneState{}, ctx.Err()
	}
	if c.metrics != nil {
		c.metrics.captures.Add(1)
		active := c.metrics.activeCaptures.Add(1)
		defer c.metrics.activeCaptures.Add(-1)
		for {
			previous := c.metrics.maxCaptures.Load()
			if active <= previous || c.metrics.maxCaptures.CompareAndSwap(previous, active) {
				break
			}
		}
	}
	return c.Capture(paneID, lines)
}

// gitWorktree carries git's porcelain record of one worktree, including the
// state markers that decide whether an action may run against it at all. The
// markers are not decoration: a locked or prunable worktree must be refused,
// and a surface that never collected them cannot refuse it (td-2af16d).
type gitWorktree struct {
	Path, Branch                     string
	Bare, Detached, Locked, Prunable bool
}

func parseWorktrees(text string) []gitWorktree {
	var result []gitWorktree
	var current *gitWorktree
	s := bufio.NewScanner(strings.NewReader(text))
	for s.Scan() {
		line := s.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			result = append(result, gitWorktree{Path: strings.TrimPrefix(line, "worktree ")})
			current = &result[len(result)-1]
		case current == nil:
		case strings.HasPrefix(line, "branch refs/heads/"):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "bare":
			current.Bare = true
		case line == "detached":
			current.Detached = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			// git appends an optional reason after the marker.
			current.Locked = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			current.Prunable = true
		}
	}
	return result
}

type shellDefinition struct {
	TmuxName    string `json:"tmuxName"`
	DisplayName string `json:"displayName"`
	AgentType   string `json:"agentType"`
	Namespace   string `json:"namespace"`
	// CreatedAt identifies which incarnation of a reused tmux name this row is.
	// An auto-close carries it so the removal can be refused if the entry was
	// replaced while the death was being confirmed (td-6a4100).
	CreatedAt time.Time `json:"createdAt"`
}

type shellFile struct {
	Shells []shellDefinition `json:"shells"`
}

func readShells(path string) []shellDefinition {
	data, err := readRegularFile(path)
	if err != nil {
		return nil
	}
	var file shellFile
	if json.Unmarshal(data, &file) != nil {
		return nil
	}
	return file.Shells
}

func readRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	return os.ReadFile(path)
}

func panesForOwnedSession(name, projectRoot string, roots []string, panes []Pane, owners ...map[string]string) []Pane {
	var out []Pane
	projectRoot = canonical(projectRoot)
	// Claims reserve sessions the last full inventory already knew about.
	// A session that is not in the map is not a collision — it is a shell
	// created since that inventory, and path ownership is the only evidence
	// this pass has. Treating "unclaimed" as "not ours" is what made a
	// newly created global shell look dead on the next live-only poll.
	if len(owners) > 0 && owners[0] != nil {
		if owner, claimed := owners[0][name]; claimed {
			if owner == "" || owner != projectRoot {
				return nil
			}
			for _, pane := range panes {
				if pane.Session == name {
					out = append(out, pane)
				}
			}
			return out
		}
	}
	for _, pane := range panes {
		if pane.Session == name && canonicalOwner(pane.Path, roots) == projectRoot {
			out = append(out, pane)
		}
	}
	return out
}

func canonicalOwner(path string, roots []string) string {
	owner := ""
	for _, root := range roots {
		root = canonical(root)
		if within(path, root) && len(root) > len(owner) {
			owner = root
		}
	}
	return owner
}

const (
	worktreeSessionPrefix  = "sidecar-ws-"
	termPanelSessionPrefix = "sidecar-tp-"
	editorSessionPrefix    = "sidecar-edit-"
	shellSessionPrefix     = "sidecar-sh-"
)

// resolveWorktreePanes picks the Output / type target for a worktree row.
// Sidecar chrome (term panels, inline editors) and project shells are not
// rivals. A live sidecar-ws-* session wins when it is the only such session;
// leftover unmanaged or extra worktree sessions stay Ambiguous.
func resolveWorktreePanes(workspace Workspace, matches []Pane) []Pane {
	remaining := make([]Pane, 0, len(matches))
	for _, pane := range matches {
		if worktreeChromeSession(pane.Session) {
			continue
		}
		remaining = append(remaining, pane)
	}
	if preferred := preferredWorktreePane(workspace, remaining); preferred != nil {
		return []Pane{*preferred}
	}
	return remaining
}

func worktreeChromeSession(session string) bool {
	return strings.HasPrefix(session, termPanelSessionPrefix) ||
		strings.HasPrefix(session, editorSessionPrefix) ||
		strings.HasPrefix(session, shellSessionPrefix)
}

func preferredWorktreePane(workspace Workspace, matches []Pane) *Pane {
	expected := workspace.TmuxName
	if !strings.HasPrefix(expected, worktreeSessionPrefix) {
		expected = ""
	}
	var live []Pane
	for i := range matches {
		pane := &matches[i]
		if !strings.HasPrefix(pane.Session, worktreeSessionPrefix) || pane.Dead {
			continue
		}
		if expected != "" && pane.Session == expected {
			return pane
		}
		live = append(live, *pane)
	}
	if len(live) == 1 {
		return &live[0]
	}
	return nil
}

func panesForPath(path string, roots []string, panes []Pane, ignoredSessions map[string]bool) []Pane {
	var out []Pane
	for _, pane := range panes {
		if ignoredSessions[pane.Session] {
			continue
		}
		owner := canonicalOwner(pane.Path, roots)
		// Linked worktrees commonly live beside the configured main checkout,
		// so no configured root owns their pane paths. Exact worktree matching
		// is still authoritative in that case. When a configured root does own
		// the pane, retain the longest-root guard so a parent project cannot
		// claim panes from a nested configured project.
		if within(pane.Path, path) && (owner == "" || within(path, owner)) {
			out = append(out, pane)
		}
	}
	return out
}

func within(path, root string) bool {
	path, root = canonical(path), canonical(root)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

func lookupProject(root string) (string, bool) {
	if dir, ok := projectdir.Lookup(root); ok {
		return dir, true
	}
	return projectdir.Lookup(canonical(root))
}

func lookupWorktree(root, path string) (string, bool) {
	if dir, ok := projectdir.LookupWorktree(root, path); ok {
		return dir, true
	}
	return projectdir.LookupWorktree(canonical(root), canonical(path))
}

func CanonicalPath(path string) string { return canonical(path) }

// CanonicalProjectPath resolves a configured checkout to its canonical main
// worktree when Git's linked-worktree metadata is available. Missing and
// non-Git paths retain their canonical configured identity so they can still
// produce an independent error result.
func CanonicalProjectPath(path string) string {
	root := canonical(path)
	gitEntry := filepath.Join(root, ".git")
	info, err := os.Stat(gitEntry)
	if err == nil && info.IsDir() {
		return root
	}
	data, err := readRegularFile(gitEntry)
	if err != nil {
		return root
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if gitDir == "" {
		return root
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	commonData, err := readRegularFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return root
	}
	common := strings.TrimSpace(string(commonData))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	common = canonical(common)
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return root
}

// ShellClaims records globally reserved agent-shell sessions and their unique
// owning project. An empty owner marks a collision that no project may claim.
type ShellClaims struct {
	Sessions map[string]bool
	Owners   map[string]string
}

func BuildShellClaims(results []ProjectResult) ShellClaims {
	claims := ShellClaims{Sessions: make(map[string]bool), Owners: make(map[string]string)}
	for _, result := range results {
		for _, workspace := range result.Workspaces {
			if workspace.Kind == KindShell && workspace.TmuxName != "" {
				claims.Sessions[workspace.TmuxName] = true
				if owner, exists := claims.Owners[workspace.TmuxName]; exists && owner != result.ProjectKey {
					claims.Owners[workspace.TmuxName] = ""
				} else if !exists {
					claims.Owners[workspace.TmuxName] = result.ProjectKey
				}
			}
		}
	}
	return claims
}

func supported(provider string) bool {
	return agentactivity.Supports(provider)
}
