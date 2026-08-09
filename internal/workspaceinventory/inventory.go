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
	TmuxName, PaneID, Provider               string
	Presentation                             agentstatus.Presentation
	ObservedAt                               time.Time
}

type ProjectResult struct {
	ProjectKey, ProjectName, ProjectRoot string
	Workspaces                           []Workspace
	ObservedAt                           time.Time
	Err                                  error
}

type Runner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type Collector struct {
	Runner   Runner
	Capture  func(string, int) (string, error)
	Now      func() time.Time
	trackers *trackerStore
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

// ListPanes takes the single global tmux inventory used by an Overview refresh.
func (c Collector) ListPanes(ctx context.Context) ([]Pane, error) {
	c = c.defaults()
	out, err := c.Runner.Output(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_id}\t#{session_name}\t#{pane_current_path}\t#{pane_current_command}\t#{pane_title}\t#{pane_dead}")
	if err != nil {
		message := strings.ToLower(string(out))
		if strings.Contains(message, "no server running") || strings.Contains(message, "no sessions") {
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
	c = c.defaults()
	now := c.Now()
	result := ProjectResult{ProjectKey: canonical(root), ProjectName: name, ProjectRoot: canonical(root), ObservedAt: now}
	var shells []shellDefinition
	if projectState, ok := projectdir.Lookup(root); ok {
		shells = readShells(filepath.Join(projectState, "shells.json"))
	}
	shellSessions := make(map[string]bool, len(shells))
	for _, shell := range shells {
		shellSessions[shell.TmuxName] = true
	}
	out, err := c.Runner.Output(ctx, "git", "--no-optional-locks", "-C", root, "worktree", "list", "--porcelain")
	if err != nil {
		result.Err = fmt.Errorf("git worktree inventory: %w", err)
		return result
	}
	for _, wt := range parseWorktrees(string(out)) {
		stateDir, ok := projectdir.LookupWorktree(root, wt.Path)
		if !ok {
			continue
		}
		agentBytes, err := os.ReadFile(filepath.Join(stateDir, "agent"))
		if err != nil || strings.TrimSpace(string(agentBytes)) == "" {
			continue
		}
		provider := strings.TrimSpace(string(agentBytes))
		taskBytes, _ := os.ReadFile(filepath.Join(stateDir, "task"))
		workspace := Workspace{ProjectKey: result.ProjectKey, ProjectName: name, ProjectRoot: result.ProjectRoot, Kind: KindWorktree, Key: canonical(wt.Path), Name: filepath.Base(wt.Path), Path: canonical(wt.Path), Branch: wt.Branch, TaskID: strings.TrimSpace(string(taskBytes)), Provider: provider, ObservedAt: now}
		workspace.ID = workspace.ProjectKey + ":worktree:" + workspace.Key
		c.observe(&workspace, panesForPath(workspace.Path, allRoots, panes, shellSessions), now)
		result.Workspaces = append(result.Workspaces, workspace)
	}

	if len(shells) > 0 {
		for _, shell := range shells {
			if shell.AgentType == "" {
				continue
			}
			workspace := Workspace{ProjectKey: result.ProjectKey, ProjectName: name, ProjectRoot: result.ProjectRoot, Kind: KindShell, Key: shell.TmuxName, Name: shell.DisplayName, Path: result.ProjectRoot, TmuxName: shell.TmuxName, Provider: shell.AgentType, ObservedAt: now}
			workspace.ID = workspace.ProjectKey + ":shell:" + workspace.Key
			matches := []Pane(nil)
			if shell.Namespace != "" && shell.Namespace == tmuxenv.Namespace() {
				matches = panesForSession(shell.TmuxName, panes)
			}
			c.observe(&workspace, matches, now)
			result.Workspaces = append(result.Workspaces, workspace)
		}
	}
	return result
}

func (c Collector) observe(workspace *Workspace, matches []Pane, now time.Time) {
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
		} else if output, err := c.Capture(pane.ID, 80); err != nil {
			input.Err = true
		} else {
			ob := agentactivity.Observation{Agent: workspace.Provider, Screen: output, PaneTitle: pane.Title, CurrentCommand: pane.Command, CapturedAt: now}
			if identified := agentactivity.Identify(ob); identified != "" && identified != "shell" {
				workspace.Provider, ob.Agent = identified, identified
				input.ProviderSupported = supported(identified)
			}
			c.trackers.mu.Lock()
			tracker := c.trackers.values[workspace.ID]
			tracker.Apply(agentactivity.Detect(ob), now)
			c.trackers.values[workspace.ID] = tracker
			c.trackers.mu.Unlock()
			input.Activity = tracker
		}
	}
	workspace.Presentation = agentstatus.Resolve(input)
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var file shellFile
	if json.Unmarshal(data, &file) != nil {
		return nil
	}
	return file.Shells
}

func panesForSession(name string, panes []Pane) []Pane {
	var out []Pane
	for _, pane := range panes {
		if pane.Session == name {
			out = append(out, pane)
		}
	}
	return out
}

func panesForPath(path string, roots []string, panes []Pane, ignoredSessions map[string]bool) []Pane {
	var out []Pane
	for _, pane := range panes {
		if ignoredSessions[pane.Session] {
			continue
		}
		owner := ""
		for _, root := range roots {
			root = canonical(root)
			if within(pane.Path, root) && len(root) > len(owner) {
				owner = root
			}
		}
		if within(pane.Path, path) && within(path, owner) {
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

func CanonicalPath(path string) string { return canonical(path) }

func supported(provider string) bool {
	return agentactivity.Supports(provider)
}
