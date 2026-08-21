package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uirequest"
)

type openDestination struct {
	Origin      uirequest.Origin
	DisplayName string
	Resolved    string
}

type destError struct {
	code int
	msg  string
}

func (e *destError) Error() string { return e.msg }

type registeredProject struct {
	Key       string
	Path      string
	Dir       string
	Shells    []shellstate.Definition
	Worktrees []string
}

// resolveCreateDestination is open's ladder plus cwd's already-registered
// project. Missing or ambiguous running instances are not fatal for create:
// a registered project is enough to write shells.json. Unknown projects stay
// a usage error and never initialize project state. Exit 3 is not used for a
// missing instance.
func resolveCreateDestination(ctx context.Context, stateDir, shellFlag, projectFlag string) (openDestination, error) {
	dest, err := resolveOpenDestination(ctx, stateDir, shellFlag, projectFlag)
	if err == nil {
		return dest, nil
	}
	if shellFlag != "" || projectFlag != "" {
		return dest, err
	}
	if destExitCode(err) != 3 {
		return dest, err
	}
	if cwdDest, cwdErr := resolveRegisteredCwdProject(stateDir); cwdErr == nil {
		return cwdDest, nil
	}
	return openDestination{}, &destError{
		code: 2,
		msg:  "no Sidecar project is registered for this directory; pass --project or run from a registered project",
	}
}

func resolveRegisteredCwdProject(stateDir string) (openDestination, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return openDestination{}, &destError{code: 2, msg: "cannot resolve working directory: " + err.Error()}
	}
	projects, err := loadRegisteredProjects(stateDir)
	if err != nil {
		return openDestination{}, err
	}
	var best registeredProject
	bestLen := -1
	nBest := 0
	for _, p := range projects {
		if p.Path == "" || !pathInside(cwd, p.Path) {
			continue
		}
		n := len(canonicalOpenPath(p.Path))
		if n > bestLen {
			best = p
			bestLen = n
			nBest = 1
			continue
		}
		if n == bestLen {
			nBest++
		}
	}
	if nBest != 1 {
		return openDestination{}, &destError{
			code: 2,
			msg:  "no Sidecar project is registered for this directory; pass --project or run from a registered project",
		}
	}
	return destFromProject(best, best.Path, uirequest.ResolvedProject), nil
}

func registeredProjectForCreate(stateDir string, dest openDestination) (registeredProject, error) {
	projects, err := loadRegisteredProjects(stateDir)
	if err != nil {
		return registeredProject{}, err
	}
	if dest.Origin.ProjectKey != "" {
		for _, p := range projects {
			if p.Key == dest.Origin.ProjectKey {
				return p, nil
			}
		}
	}
	if dest.Origin.WorkDir != "" {
		want := canonicalOpenPath(dest.Origin.WorkDir)
		var hits []registeredProject
		for _, p := range projects {
			if p.Path != "" && canonicalOpenPath(p.Path) == want {
				hits = append(hits, p)
			}
		}
		if len(hits) == 1 {
			return hits[0], nil
		}
	}
	return registeredProject{}, &destError{
		code: 2,
		msg:  "no Sidecar project is registered for this directory; pass --project or run from a registered project",
	}
}

func resolveOpenDestination(ctx context.Context, stateDir, shellFlag, projectFlag string) (openDestination, error) {
	if shellFlag != "" || projectFlag != "" {
		return resolveExplicitDestination(stateDir, shellFlag, projectFlag)
	}

	if identity, err := currentShellIdentity(ctx); err == nil {
		origin, err := shellstate.LookupOrigin(stateDir, shellstate.Identity{
			TmuxName:  identity.session,
			Namespace: identity.socket,
		})
		if err != nil {
			return openDestination{}, &destError{code: 3, msg: err.Error()}
		}
		return destFromOrigin(origin, uirequest.ResolvedCurrentShell, origin.WorkDir), nil
	}

	instances, err := uirequest.ListInstances(stateDir)
	if err != nil {
		return openDestination{}, &destError{code: 1, msg: err.Error()}
	}
	switch len(instances) {
	case 0:
		return openDestination{}, &destError{code: 3, msg: "no Sidecar instance is running"}
	case 1:
		return destFromUniqueInstance(stateDir, instances[0])
	default:
		return openDestination{}, &destError{code: 3, msg: formatAmbiguousInstances(stateDir, instances)}
	}
}

func resolveExplicitDestination(stateDir, shellFlag, projectFlag string) (openDestination, error) {
	projects, err := loadRegisteredProjects(stateDir)
	if err != nil {
		return openDestination{}, err
	}

	var proj registeredProject
	if projectFlag != "" {
		proj, err = matchProject(projects, projectFlag)
		if err != nil {
			return openDestination{}, err
		}
	}

	if shellFlag != "" {
		search := projects
		if projectFlag != "" {
			search = []registeredProject{proj}
		}
		hitProj, shell, err := matchShell(search, shellFlag, projectFlag == "")
		if err != nil {
			return openDestination{}, err
		}
		workDir := resolveTargetWorkDir(hitProj, "")
		if workDir == "" && shell.WorkDir != "" {
			workDir = shell.WorkDir
		}
		return destFromShell(hitProj, shell, workDir, uirequest.ResolvedShell), nil
	}

	if err := refuseDuplicateProjectInstances(stateDir, proj.Key); err != nil {
		return openDestination{}, err
	}
	workDir := resolveTargetWorkDir(proj, "")
	return destFromProject(proj, workDir, uirequest.ResolvedProject), nil
}

func destFromUniqueInstance(stateDir string, inst uirequest.Instance) (openDestination, error) {
	if inst.ProjectKey != "" {
		if dest, err := resolveExplicitDestination(stateDir, "", inst.ProjectKey); err == nil {
			dest.Resolved = uirequest.ResolvedInstance
			return dest, nil
		}
	}
	workDir := inst.WorkDir
	return openDestination{
		Origin: uirequest.Origin{
			ProjectKey: inst.ProjectKey,
			WorkDir:    workDir,
			PID:        os.Getpid(),
		},
		Resolved: uirequest.ResolvedInstance,
	}, nil
}

func destFromOrigin(origin shellstate.OriginInfo, resolved, workDir string) openDestination {
	if workDir == "" {
		workDir = origin.WorkDir
	}
	return openDestination{
		Origin: uirequest.Origin{
			TmuxSession: origin.TmuxName,
			Namespace:   origin.Namespace,
			ProjectKey:  origin.ProjectKey,
			WorkDir:     workDir,
			PID:         os.Getpid(),
		},
		DisplayName: origin.DisplayName,
		Resolved:    resolved,
	}
}

func destFromShell(proj registeredProject, shell shellstate.Definition, workDir, resolved string) openDestination {
	return openDestination{
		Origin: uirequest.Origin{
			TmuxSession: shell.TmuxName,
			Namespace:   shell.Namespace,
			ProjectKey:  proj.Key,
			WorkDir:     workDir,
			PID:         os.Getpid(),
		},
		DisplayName: shell.DisplayName,
		Resolved:    resolved,
	}
}

func destFromProject(proj registeredProject, workDir, resolved string) openDestination {
	return openDestination{
		Origin: uirequest.Origin{
			ProjectKey: proj.Key,
			WorkDir:    workDir,
			PID:        os.Getpid(),
		},
		Resolved: resolved,
	}
}

func matchProject(projects []registeredProject, name string) (registeredProject, error) {
	if name == "" {
		return registeredProject{}, &destError{code: 2, msg: "--project requires a project name"}
	}
	wantPath := canonicalOpenPath(name)
	var slug, pathHits, base []registeredProject
	for _, p := range projects {
		if p.Key == name {
			slug = append(slug, p)
		}
		if p.Path != "" && canonicalOpenPath(p.Path) == wantPath {
			pathHits = append(pathHits, p)
		}
		if p.Path != "" && (filepath.Base(p.Path) == name || filepath.Base(canonicalOpenPath(p.Path)) == name) {
			base = append(base, p)
		}
	}
	switch {
	case len(slug) == 1:
		return slug[0], nil
	case len(pathHits) == 1:
		return pathHits[0], nil
	case len(base) == 1:
		return base[0], nil
	case len(base) > 1:
		var keys []string
		for _, p := range base {
			keys = append(keys, p.Key)
		}
		return registeredProject{}, &destError{
			code: 3,
			msg:  fmt.Sprintf("project %q matches more than one Sidecar project (%s); pass --project with a slug", name, strings.Join(keys, ", ")),
		}
	default:
		return registeredProject{}, &destError{code: 2, msg: fmt.Sprintf("unknown project %q", name)}
	}
}

func matchShell(projects []registeredProject, name string, refuseMultiProject bool) (registeredProject, shellstate.Definition, error) {
	if name == "" {
		return registeredProject{}, shellstate.Definition{}, &destError{code: 2, msg: "--shell requires a shell name"}
	}
	type hit struct {
		proj  registeredProject
		shell shellstate.Definition
	}
	var display, tmux []hit
	for _, p := range projects {
		for _, sh := range p.Shells {
			if sh.DisplayName == name {
				display = append(display, hit{p, sh})
			}
			if sh.TmuxName == name {
				tmux = append(tmux, hit{p, sh})
			}
		}
	}
	hits := display
	if len(hits) == 0 {
		hits = tmux
	}
	if len(hits) == 0 {
		return registeredProject{}, shellstate.Definition{}, &destError{code: 2, msg: fmt.Sprintf("unknown shell %q", name)}
	}
	if len(hits) > 1 && refuseMultiProject {
		var keys []string
		seen := map[string]bool{}
		for _, h := range hits {
			if !seen[h.proj.Key] {
				seen[h.proj.Key] = true
				keys = append(keys, h.proj.Key)
			}
		}
		return registeredProject{}, shellstate.Definition{}, &destError{
			code: 3,
			msg:  fmt.Sprintf("shell %q matches more than one project (%s); pass --project to pick one", name, strings.Join(keys, ", ")),
		}
	}
	if len(hits) > 1 {
		return registeredProject{}, shellstate.Definition{}, &destError{
			code: 3,
			msg:  fmt.Sprintf("shell %q is ambiguous in this project", name),
		}
	}
	return hits[0].proj, hits[0].shell, nil
}

func loadRegisteredProjects(stateDir string) ([]registeredProject, error) {
	entries, err := os.ReadDir(filepath.Join(stateDir, "projects"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &destError{code: 1, msg: "read registered Sidecar projects: " + err.Error()}
	}
	var projects []registeredProject
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(stateDir, "projects", entry.Name())
		p := registeredProject{Key: entry.Name(), Dir: dir}
		if data, err := os.ReadFile(filepath.Join(dir, "meta.json")); err == nil {
			var meta struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(data, &meta); err == nil {
				p.Path = meta.Path
			}
		}
		if data, err := os.ReadFile(filepath.Join(dir, "shells.json")); err == nil {
			var m struct {
				Shells []shellstate.Definition `json:"shells"`
			}
			if err := json.Unmarshal(data, &m); err == nil {
				p.Shells = m.Shells
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

func (p registeredProject) roots() []string {
	roots := make([]string, 0, 2+len(p.Worktrees)+len(p.Shells))
	if p.Path != "" {
		roots = append(roots, p.Path)
	}
	roots = append(roots, p.Worktrees...)
	for _, sh := range p.Shells {
		if sh.WorkDir != "" {
			roots = append(roots, sh.WorkDir)
		}
	}
	return roots
}

func resolveTargetWorkDir(proj registeredProject, raw string) string {
	roots := proj.roots()
	cwd, cwdErr := os.Getwd()
	if cwdErr == nil {
		if root := containingRoot(cwd, roots); root != "" {
			return root
		}
	}

	filePath := stripLineSuffix(raw)
	if filePath != "" && cwdErr == nil && !filepath.IsAbs(filePath) {
		if root := containingRoot(filepath.Join(cwd, filePath), roots); root != "" {
			return root
		}
	}
	if filePath != "" {
		abs := filePath
		if !filepath.IsAbs(filePath) && cwdErr == nil {
			abs = filepath.Join(cwd, filePath)
		}
		if filepath.IsAbs(abs) {
			if root := containingRoot(abs, roots); root != "" {
				return root
			}
		}
	}
	if proj.Path != "" {
		return proj.Path
	}
	return ""
}

func refuseDuplicateProjectInstances(stateDir, projectKey string) error {
	if projectKey == "" {
		return nil
	}
	instances, err := uirequest.ListInstances(stateDir)
	if err != nil || len(instances) < 2 {
		return nil
	}
	n := 0
	for _, inst := range instances {
		if inst.ProjectKey == projectKey {
			n++
		}
	}
	if n > 1 {
		return &destError{
			code: 3,
			msg:  fmt.Sprintf("several Sidecar instances are showing project %q; pass --shell to pick one", projectKey),
		}
	}
	return nil
}

func containingRoot(path string, roots []string) string {
	best := ""
	bestLen := -1
	for _, root := range roots {
		if root == "" || !pathInside(path, root) {
			continue
		}
		n := len(canonicalOpenPath(root))
		if n > bestLen {
			best = root
			bestLen = n
		}
	}
	return best
}

func pathInside(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(canonicalOpenPath(root), canonicalOpenPath(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func canonicalOpenPath(path string) string {
	path = filepath.Clean(path)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = filepath.Clean(resolved)
	}
	return path
}

func stripLineSuffix(raw string) string {
	raw = strings.TrimSpace(raw)
	if colonIdx := strings.LastIndex(raw, ":"); colonIdx > 0 && colonIdx < len(raw)-1 {
		if n, err := strconv.Atoi(raw[colonIdx+1:]); err == nil && n > 0 {
			return raw[:colonIdx]
		}
	}
	return raw
}

func formatAmbiguousInstances(stateDir string, instances []uirequest.Instance) string {
	projects, _ := loadRegisteredProjects(stateDir)
	byKey := map[string]registeredProject{}
	for _, p := range projects {
		byKey[p.Key] = p
	}

	var b strings.Builder
	b.WriteString("several Sidecar instances are running; pass --project to pick one:")
	for _, inst := range instances {
		key := inst.ProjectKey
		if key == "" {
			key = inst.Project
		}
		if key == "" {
			key = inst.WorkDir
		}
		b.WriteString("\n  --project ")
		b.WriteString(key)
		if proj, ok := byKey[inst.ProjectKey]; ok {
			if name := uniqueShellDisplay(proj); name != "" {
				b.WriteString(" --shell ")
				b.WriteString(strconv.Quote(name))
			}
		}
	}
	return b.String()
}

func uniqueShellDisplay(proj registeredProject) string {
	var names []string
	seen := map[string]bool{}
	for _, sh := range proj.Shells {
		if sh.DisplayName == "" || seen[sh.DisplayName] {
			continue
		}
		seen[sh.DisplayName] = true
		names = append(names, sh.DisplayName)
	}
	if len(names) == 1 {
		return names[0]
	}
	return ""
}

func resolveTargetWorkDirForDest(stateDir string, dest openDestination, raw string) string {
	if dest.Origin.ProjectKey == "" {
		return dest.Origin.WorkDir
	}
	projects, err := loadRegisteredProjects(stateDir)
	if err != nil {
		return dest.Origin.WorkDir
	}
	for _, p := range projects {
		if p.Key != dest.Origin.ProjectKey {
			continue
		}
		if workDir := resolveTargetWorkDir(p, raw); workDir != "" {
			return workDir
		}
		break
	}
	return dest.Origin.WorkDir
}

func destExitCode(err error) int {
	var de *destError
	if errors.As(err, &de) {
		return de.code
	}
	if err != nil {
		return 1
	}
	return 0
}
