package managedtarget

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/workspaceops"
)

const (
	KindShell    = "shell"
	KindWorktree = "worktree"
)

// Project is the registered-project facts the candidate scan needs. Callers
// that already loaded the registry (the CLI) pass this; List loads it from
// stateDir.
type Project struct {
	Key, Path, Dir string
	Worktrees      []string
}

// List is every managed shell and worktree in stateDir, or only projectKey
// when that is set. Empty projectKey means every registered project. This is
// the universe agent list and agent broadcast share.
func List(ctx context.Context, stateDir, projectKey string) ([]Target, error) {
	projects, err := LoadProjects(stateDir)
	if err != nil {
		return nil, err
	}
	if projectKey != "" {
		var matched []Project
		for _, p := range projects {
			if p.Key == projectKey {
				matched = append(matched, p)
			}
		}
		projects = matched
	}
	return Candidates(ctx, stateDir, projects)
}

// LoadProjects reads every registered project under stateDir/projects.
func LoadProjects(stateDir string) ([]Project, error) {
	entries, err := os.ReadDir(filepath.Join(stateDir, "projects"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var projects []Project
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(stateDir, "projects", entry.Name())
		p := Project{Key: entry.Name(), Dir: dir}
		if data, err := os.ReadFile(filepath.Join(dir, "meta.json")); err == nil {
			var meta struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(data, &meta); err == nil {
				p.Path = meta.Path
			}
		}
		p.Worktrees = listRegisteredWorktrees(dir)
		projects = append(projects, p)
	}
	return projects, nil
}

func listRegisteredWorktrees(projectDir string) []string {
	entries, err := os.ReadDir(filepath.Join(projectDir, "worktrees"))
	if err != nil {
		return nil
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(projectDir, "worktrees", entry.Name(), "meta.json"))
		if err != nil {
			continue
		}
		var meta struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(data, &meta); err == nil && meta.Path != "" {
			paths = append(paths, meta.Path)
		}
	}
	return paths
}

func projectManifestPath(proj Project) string {
	if proj.Dir == "" {
		return ""
	}
	return filepath.Join(proj.Dir, "shells.json")
}

// Candidates is every shell and worktree session the given projects own, each
// worktree root listed exactly once.
//
// Shell records belong to the project whose manifest holds them. Worktree
// roots do not, because several registered projects can see the same
// directory. Each root therefore has one owner, chosen by how strongly a
// project claims it: created worktree, then the project's own checkout, then
// the first project that merely discovered it through Git.
func Candidates(ctx context.Context, stateDir string, projects []Project) ([]Target, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var candidates []Target
	type rootClaim struct {
		proj     Project
		manifest string
		tier     int
	}
	const (
		tierCreated = iota
		tierCheckout
		tierDiscovered
	)
	claims := map[string]rootClaim{}
	var roots []string
	claim := func(root string, proj Project, manifest string, tier int) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		root = canonicalPath(root)
		current, ok := claims[root]
		if !ok {
			claims[root] = rootClaim{proj: proj, manifest: manifest, tier: tier}
			roots = append(roots, root)
			return
		}
		if tier < current.tier {
			claims[root] = rootClaim{proj: proj, manifest: manifest, tier: tier}
		}
	}
	for _, proj := range projects {
		if ctx.Err() != nil {
			break
		}
		manifest := projectManifestPath(proj)
		defs, err := shellstate.ListAtPath(manifest)
		if err != nil {
			return nil, err
		}
		for _, def := range defs {
			workDir := def.WorkDir
			if workDir == "" {
				workDir = proj.Path
			}
			candidates = append(candidates, Target{
				Host: "local", Project: proj.Key, ProjectRoot: proj.Path, Kind: KindShell,
				Session: def.TmuxName, Name: def.DisplayName, Namespace: def.Namespace,
				WorkDir: workDir, ManifestPath: manifest, Priority: 0,
			})
		}
		if strings.TrimSpace(proj.Path) == "" {
			continue
		}
		for _, root := range proj.Worktrees {
			claim(root, proj, manifest, tierCreated)
		}
		claim(proj.Path, proj, manifest, tierCheckout)
		for _, root := range discoveredWorktreeRoots(ctx, proj) {
			claim(root, proj, manifest, tierDiscovered)
		}
	}
	for _, root := range roots {
		c := claims[root]
		priority := 1
		if c.tier == tierDiscovered {
			priority = 2
		}
		name, _ := workspaceops.LookupWorktreeDisplayName(stateDir, c.proj.Path, root)
		candidates = append(candidates, Target{
			Host: "local", Project: c.proj.Key, ProjectRoot: c.proj.Path, Kind: KindWorktree,
			Session: workspaceops.WorktreeSessionName(root, ""), Name: name, Namespace: tmuxenv.Namespace(),
			WorkDir: root, WorktreeRoot: root, ManifestPath: c.manifest, Priority: priority,
		})
	}
	return candidates, nil
}

func discoveredWorktreeRoots(ctx context.Context, proj Project) []string {
	if proj.Path == "" {
		return nil
	}
	states, err := workspaceops.ListWorktreeStates(ctx, proj.Path)
	if err != nil {
		return nil
	}
	var discovered []string
	for _, state := range states {
		if state.Bare || state.Path == "" {
			continue
		}
		discovered = append(discovered, state.Path)
	}
	return discovered
}

func canonicalPath(path string) string {
	path = filepath.Clean(path)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = filepath.Clean(resolved)
	}
	return path
}
