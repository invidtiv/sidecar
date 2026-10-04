package contentservice

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	kindShell    = "shell"
	kindWorktree = "worktree"
)

// Workspace is a durable identity re-resolved to its authoritative root.
type Workspace struct {
	ID   string
	Kind string
	Key  string
	Root string
}

// parseWorkspaceID splits an unscoped durable id (projectKey:shell:key or
// projectKey:worktree:path). The same split the Sessions catalog uses.
func parseWorkspaceID(id string) (kind, projectKey, key string, ok bool) {
	if err := validateLocator(id, "workspace"); err != nil {
		return "", "", "", false
	}
	for _, k := range []string{kindShell, kindWorktree} {
		sep := ":" + k + ":"
		i := strings.Index(id, sep)
		if i <= 0 {
			continue
		}
		return k, id[:i], id[i+len(sep):], true
	}
	return "", "", "", false
}

// LookupWorkspace re-resolves a durable workspace id to its authoritative root.
//
// Exported because `sidecar repo` is scoped by the same identity. Which root a
// viewer is reading is the one fact both verb families must agree on, so there
// is one resolver rather than one per family: a second implementation is how a
// bound surface starts reading a different directory than its neighbour.
func (s *Service) LookupWorkspace(ctx context.Context, workspaceID string) (Workspace, error) {
	return s.lookupWorkspace(ctx, workspaceID)
}

func (s *Service) lookupWorkspace(ctx context.Context, workspaceID string) (Workspace, error) {
	if err := ctx.Err(); err != nil {
		return Workspace{}, err
	}
	kind, projectKey, key, ok := parseWorkspaceID(workspaceID)
	if !ok {
		return Workspace{}, Rejected("unknown workspace %q", workspaceID)
	}
	projects, err := s.projects()
	if err != nil {
		return Workspace{}, err
	}
	project, ok := findProject(projects, projectKey)
	if !ok {
		return Workspace{}, Rejected("unconfigured project for workspace %q", workspaceID)
	}
	root := canonical(config.ExpandPath(project.Path))
	if root == "" {
		return Workspace{}, Rejected("unconfigured project for workspace %q", workspaceID)
	}

	switch kind {
	case kindShell:
		shells, err := s.listShells(root)
		if err != nil {
			return Workspace{}, Internal("list shells", err)
		}
		var match *shellstate.Definition
		for i := range shells {
			if shells[i].TmuxName == key {
				if match != nil {
					return Workspace{}, Rejected("shell %q has ambiguous durable ownership", key)
				}
				match = &shells[i]
			}
		}
		if match == nil {
			return Workspace{}, Rejected("workspace %q no longer owns this shell", workspaceID)
		}
		paths := []string{root}
		if match.WorkDir != "" {
			if worktrees, err := s.listWorktrees(ctx, root); err == nil {
				paths = append(paths, worktrees...)
			}
		}
		return Workspace{ID: workspaceID, Kind: kindShell, Key: key, Root: workspaceinventory.OwningWorkspacePath(match.WorkDir, root, paths)}, nil
	case kindWorktree:
		paths, err := s.listWorktrees(ctx, root)
		if err != nil {
			return Workspace{}, Rejected("workspace %q no longer owns this worktree", workspaceID)
		}
		want := canonical(key)
		for _, p := range paths {
			if canonical(p) == want {
				return Workspace{ID: workspaceID, Kind: kindWorktree, Key: key, Root: want}, nil
			}
		}
		return Workspace{}, Rejected("workspace %q no longer owns this worktree", workspaceID)
	default:
		return Workspace{}, UnknownKind(kind)
	}
}

func findProject(list []config.ProjectConfig, projectKey string) (config.ProjectConfig, bool) {
	want := canonical(projectKey)
	for _, project := range list {
		if canonical(config.ExpandPath(project.Path)) == want {
			return project, true
		}
	}
	return config.ProjectConfig{}, false
}

func (s *Service) projects() ([]config.ProjectConfig, error) {
	load := s.LoadConfig
	if load == nil {
		load = config.Load
	}
	cfg, err := load()
	if err != nil {
		return nil, Internal("load config", err)
	}
	if cfg == nil {
		return nil, Rejected("unconfigured project")
	}
	return cfg.Projects.List, nil
}

func (s *Service) listShells(projectRoot string) ([]shellstate.Definition, error) {
	if s.ListShells != nil {
		return s.ListShells(projectRoot)
	}
	// Inventory IDs are canonical, but the registry retains the configured
	// spelling. Read every equivalent registration, including legacy aliases.
	var shells []shellstate.Definition
	for _, project := range projectdir.LookupEquivalent(projectRoot) {
		defs, err := shellstate.ListAtPath(filepath.Join(project.Dir, "shells.json"))
		if err != nil {
			return nil, err
		}
		shells = append(shells, defs...)
	}
	return shells, nil
}

func (s *Service) listWorktrees(ctx context.Context, projectRoot string) ([]string, error) {
	git := s.Git
	if git == nil {
		git = defaultGit
	}
	out, err := git(ctx, projectRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktreePaths(string(out)), nil
}

func parseWorktreePaths(text string) []string {
	var paths []string
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			rest = strings.TrimSpace(rest)
			if rest != "" {
				paths = append(paths, rest)
			}
		}
	}
	return paths
}
