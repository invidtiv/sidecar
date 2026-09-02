package contentservice

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
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
		for _, sh := range shells {
			if sh.TmuxName == key {
				return Workspace{ID: workspaceID, Kind: kindShell, Key: key, Root: root}, nil
			}
		}
		return Workspace{}, Rejected("workspace %q no longer owns this shell", workspaceID)
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
	dir, ok := projectdir.Lookup(projectRoot)
	if !ok {
		dir, ok = projectdir.Lookup(canonical(projectRoot))
	}
	if !ok {
		return nil, nil
	}
	return shellstate.ListAtPath(filepath.Join(dir, "shells.json"))
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
