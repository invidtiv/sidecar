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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
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
	Dead                              bool
}

type Workspace struct {
	ID, ProjectKey, ProjectName, ProjectRoot string
	Kind                                     Kind
	Key, Name, Path, Branch, TaskID          string
	TmuxName, PaneID, Provider, Namespace    string
	Presentation                             agentstatus.Presentation
	ObservedAt                               time.Time
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
			if shell.TmuxName == workspace.Key && shell.TmuxName == workspace.TmuxName && shell.AgentType != "" {
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

type Collector struct {
	Runner           Runner
	Capture          func(string, int) (string, error)
	Now              func() time.Time
	trackers         *trackerStore
	captures         chan struct{}
	metrics          *RefreshMetrics
	reservedSessions map[string]bool
	shellOwners      map[string]string
	generation       uint64
}

// RefreshMetrics contains privacy-safe operation and concurrency counters for
// an in-memory refresh. It never includes project paths, pane IDs, or output.
type RefreshMetrics struct {
	projectOps     atomic.Int64
	captures       atomic.Int64
	activeCaptures atomic.Int64
	maxCaptures    atomic.Int64
}

type MetricsSnapshot struct {
	ProjectOps, Captures, MaxCaptures int64
}

type trackerStore struct {
	mu         sync.Mutex
	values     map[string]agentactivity.Tracker
	generation atomic.Uint64
}

func (c Collector) defaults() Collector {
	if c.Runner == nil {
		c.Runner = ExecRunner{}
	}
	if c.Capture == nil {
		c.Capture = tty.CapturePaneOutput
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.trackers == nil {
		c.trackers = &trackerStore{values: make(map[string]agentactivity.Tracker)}
	}
	return c
}

func (c Collector) WithDefaults() Collector { return c.defaults() }

// InvalidateObservations prevents any in-flight collector from applying
// provider evidence after its owning Overview generation has been canceled.
func (c Collector) InvalidateObservations() {
	c = c.defaults()
	c.trackers.generation.Add(1)
}

// ForRefresh returns a collector sharing provider history with its parent while
// independently bounding pane captures for one refresh generation.
func (c Collector) ForRefresh(maxCaptures int, claims ...ShellClaims) Collector {
	c = c.defaults()
	if maxCaptures < 1 {
		maxCaptures = 1
	}
	c.captures = make(chan struct{}, maxCaptures)
	c.metrics = &RefreshMetrics{}
	c.generation = c.trackers.generation.Load()
	if len(claims) > 0 {
		c.reservedSessions = claims[0].Sessions
		c.shellOwners = claims[0].Owners
	}
	return c
}

func (c Collector) WithShellClaims(claims ShellClaims) Collector {
	c.reservedSessions = claims.Sessions
	c.shellOwners = claims.Owners
	return c
}

func (c Collector) Metrics() MetricsSnapshot {
	if c.metrics == nil {
		return MetricsSnapshot{}
	}
	return MetricsSnapshot{ProjectOps: c.metrics.projectOps.Load(), Captures: c.metrics.captures.Load(), MaxCaptures: c.metrics.maxCaptures.Load()}
}

// ListPanes takes the single global tmux inventory used by an Overview refresh.
func (c Collector) ListPanes(ctx context.Context) ([]Pane, error) {
	c = c.defaults()
	out, err := c.Runner.Output(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_id}\t#{session_name}\t#{pane_current_path}\t#{pane_current_command}\t#{pane_title}\t#{pane_dead}")
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
		parts := strings.SplitN(line, "\t", 6)
		if len(parts) != 6 {
			continue
		}
		panes = append(panes, Pane{ID: parts[0], Session: parts[1], Path: filepath.Clean(parts[2]), Command: parts[3], Title: parts[4], Dead: parts[5] == "1"})
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
	return c.RefreshProjectStatus(ctx, result, allRoots, panes)
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
	info, err := os.Stat(root)
	if err != nil {
		result.Err = fmt.Errorf("configured project missing: %w", err)
		return result
	}
	if !info.IsDir() {
		result.Err = fmt.Errorf("configured project is not a directory")
		return result
	}
	out, err := c.Runner.Output(ctx, "git", "--no-optional-locks", "-C", root, "worktree", "list", "--porcelain")
	if err != nil {
		result.Err = fmt.Errorf("configured project is not a Git repository: %w", err)
		return result
	}
	for _, wt := range parseWorktrees(string(out)) {
		stateDir, ok := lookupWorktree(root, wt.Path)
		if !ok {
			continue
		}
		agentBytes, err := readRegularFile(filepath.Join(stateDir, "agent"))
		if err != nil || strings.TrimSpace(string(agentBytes)) == "" {
			continue
		}
		provider := strings.TrimSpace(string(agentBytes))
		taskBytes, _ := readRegularFile(filepath.Join(stateDir, "task"))
		workspace := Workspace{ProjectKey: result.ProjectKey, ProjectName: name, ProjectRoot: result.ProjectRoot, Kind: KindWorktree, Key: canonical(wt.Path), Name: filepath.Base(wt.Path), Path: canonical(wt.Path), Branch: wt.Branch, TaskID: strings.TrimSpace(string(taskBytes)), Provider: provider, ObservedAt: now}
		workspace.ID = workspace.ProjectKey + ":worktree:" + workspace.Key
		workspace.Presentation = agentstatus.Resolve(agentstatus.Input{ProviderSupported: supported(provider), Orphaned: true, CapturedAt: now, Now: now})
		result.Workspaces = append(result.Workspaces, workspace)
	}

	if len(shells) > 0 {
		for _, shell := range shells {
			if shell.AgentType == "" {
				continue
			}
			workspace := Workspace{ProjectKey: result.ProjectKey, ProjectName: name, ProjectRoot: result.ProjectRoot, Kind: KindShell, Key: shell.TmuxName, Name: shell.DisplayName, Path: result.ProjectRoot, TmuxName: shell.TmuxName, Provider: shell.AgentType, Namespace: shell.Namespace, ObservedAt: now}
			workspace.ID = workspace.ProjectKey + ":shell:" + workspace.Key
			workspace.Presentation = agentstatus.Resolve(agentstatus.Input{ProviderSupported: supported(shell.AgentType), Orphaned: true, CapturedAt: now, Now: now})
			result.Workspaces = append(result.Workspaces, workspace)
		}
	}
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
	if previous.Err != nil {
		return result
	}
	for i := range result.Workspaces {
		workspace := &result.Workspaces[i]
		workspace.ObservedAt = now
		var matches []Pane
		switch workspace.Kind {
		case KindWorktree:
			matches = panesForPath(workspace.Path, allRoots, panes, c.reservedSessions)
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
	input := agentstatus.Input{ProviderSupported: supported(workspace.Provider), CapturedAt: now, Now: now, StaleAfter: time.Minute}
	if len(matches) == 0 {
		input.Orphaned = true
	} else if len(matches) > 1 {
		input.Ambiguous = true
	} else {
		pane := matches[0]
		workspace.PaneID, workspace.TmuxName = pane.ID, pane.Session
		if pane.Dead {
			input.Orphaned = true
		} else if output, err := c.capturePane(ctx, pane.ID, 80); err != nil {
			input.Err = true
		} else {
			select {
			case <-ctx.Done():
				input.Unavailable = true
				workspace.Presentation = agentstatus.Resolve(input)
				return
			default:
			}
			ob := agentactivity.Observation{Agent: workspace.Provider, Screen: output, PaneTitle: pane.Title, CurrentCommand: pane.Command, CapturedAt: now}
			if identified := agentactivity.Identify(ob); identified != "" && identified != "shell" {
				workspace.Provider, ob.Agent = identified, identified
				input.ProviderSupported = supported(identified)
			}
			c.trackers.mu.Lock()
			if c.generation != c.trackers.generation.Load() {
				c.trackers.mu.Unlock()
				input.Unavailable = true
				workspace.Presentation = agentstatus.Resolve(input)
				return
			}
			select {
			case <-ctx.Done():
				c.trackers.mu.Unlock()
				input.Unavailable = true
				workspace.Presentation = agentstatus.Resolve(input)
				return
			default:
			}
			tracker := c.trackers.values[workspace.ID]
			tracker.Apply(agentactivity.Detect(ob), now)
			c.trackers.values[workspace.ID] = tracker
			c.trackers.mu.Unlock()
			input.Activity = tracker
		}
	}
	workspace.Presentation = agentstatus.Resolve(input)
}

func (c Collector) capturePane(ctx context.Context, paneID string, lines int) (string, error) {
	if c.captures == nil {
		return c.Capture(paneID, lines)
	}
	select {
	case c.captures <- struct{}{}:
		defer func() { <-c.captures }()
	case <-ctx.Done():
		return "", ctx.Err()
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

type gitWorktree struct{ Path, Branch string }

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
		case current != nil && strings.HasPrefix(line, "branch refs/heads/"):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	return result
}

type shellDefinition struct {
	TmuxName    string `json:"tmuxName"`
	DisplayName string `json:"displayName"`
	AgentType   string `json:"agentType"`
	Namespace   string `json:"namespace"`
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
	if len(owners) > 0 && owners[0] != nil {
		owner, claimed := owners[0][name]
		if !claimed || owner == "" || owner != canonical(projectRoot) {
			return nil
		}
		for _, pane := range panes {
			if pane.Session == name {
				out = append(out, pane)
			}
		}
		return out
	}
	for _, pane := range panes {
		if pane.Session == name && canonicalOwner(pane.Path, roots) == canonical(projectRoot) {
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
			if workspace.Kind == KindShell && workspace.Provider != "" && workspace.TmuxName != "" {
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
