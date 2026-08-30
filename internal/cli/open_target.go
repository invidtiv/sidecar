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

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uirequest"
)

type openDestination struct {
	Origin      uirequest.Origin
	DisplayName string
	Resolved    string
	// Project is the project this destination resolved to, when resolution
	// already found one. It is carried rather than re-derived because a
	// configured project that has never been opened here has no state directory
	// to be found by a second lookup — resolveProjectOnly deliberately did not
	// create one — and re-deriving would turn a resolved project back into
	// "unknown project".
	Project registeredProject
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

const unregisteredCreateProject = "no Sidecar project is registered for this directory; pass --project or run from a registered project"

// projectRegistration says whether resolving --project may CREATE the state
// directory for a project that is configured but has never been opened here.
//
// It exists because those two answers belong to different callers.
// `create worktree --project X` is about to write X's state and must have a
// directory to write into; `shell list --project X` is only asking what X owns,
// and answering that by materialising a directory makes a read leave something
// behind. Passed explicitly at every resolution site rather than inferred, so
// which callers can create is a thing you can read off the call sites.
type projectRegistration bool

const (
	// registerProject: a mutating verb, which needs somewhere to write.
	registerProject projectRegistration = true
	// resolveProjectOnly: a read. An unregistered project resolves with no
	// directory, which is the honest answer — Sidecar owns no state for it.
	resolveProjectOnly projectRegistration = false
)

// resolveCreateDestination is open's ladder plus cwd's already-registered
// project, including sibling worktrees. A Sidecar-owned session whose
// LookupOrigin misses (sidecar-ws-* is not in shells.json) continues to unique
// instance then cwd rather than failing. KindState read errors stay exit 1.
// Missing or ambiguous running instances are not fatal: a registered project
// is enough. Unknown projects stay a usage error and never initialize state.
func resolveCreateDestination(ctx context.Context, stateDir, shellFlag, projectFlag string, register projectRegistration) (openDestination, error) {
	if shellFlag != "" || projectFlag != "" {
		return resolveExplicitDestination(stateDir, shellFlag, projectFlag, register)
	}

	if identity, err := currentShellIdentity(ctx); err == nil {
		origin, err := shellstate.LookupOrigin(stateDir, shellstate.Identity{
			TmuxName:  identity.session,
			Namespace: identity.socket,
		})
		if err == nil {
			return destFromOrigin(origin, uirequest.ResolvedCurrentShell, origin.WorkDir), nil
		}
		if isShellStateError(err) {
			return openDestination{}, &destError{code: 1, msg: err.Error()}
		}
		// LookupOrigin miss for a sidecar-sh-/sidecar-ws- session: continue.
	}

	instances, err := uirequest.ListInstances(stateDir)
	if err != nil {
		return openDestination{}, &destError{code: 1, msg: err.Error()}
	}
	if len(instances) == 1 {
		return destFromUniqueInstance(stateDir, instances[0])
	}

	if cwdDest, cwdErr := resolveRegisteredCwdProject(stateDir); cwdErr == nil {
		return cwdDest, nil
	}
	return openDestination{}, &destError{code: 2, msg: unregisteredCreateProject}
}

func isShellStateError(err error) bool {
	var se *shellstate.Error
	return errors.As(err, &se) && se.Kind == shellstate.KindState
}

func createDestExitCode(err error) int {
	code := destExitCode(err)
	if code == 3 {
		return 2
	}
	return code
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
	proj, root, ok := uniqueProjectContaining(projects, cwd)
	if !ok {
		return openDestination{}, &destError{code: 2, msg: unregisteredCreateProject}
	}
	workDir := root
	if workDir == "" {
		workDir = proj.Path
	}
	return destFromProject(proj, workDir, uirequest.ResolvedProject), nil
}

func uniqueProjectContaining(projects []registeredProject, path string) (registeredProject, string, bool) {
	var best registeredProject
	bestRoot := ""
	bestLen := -1
	nBest := 0
	for _, p := range projects {
		root := containingRoot(path, p.roots())
		if root == "" {
			continue
		}
		n := len(canonicalOpenPath(root))
		if n > bestLen {
			best = p
			bestRoot = root
			bestLen = n
			nBest = 1
			continue
		}
		if n == bestLen {
			nBest++
		}
	}
	if nBest != 1 {
		return registeredProject{}, "", false
	}
	return best, bestRoot, true
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
		if p, _, ok := uniqueProjectContaining(projects, dest.Origin.WorkDir); ok {
			return p, nil
		}
	}
	// Neither lookup can find a project with no directory on disk, which is
	// exactly what a read-only resolution of a never-opened configured project
	// produces. It was already resolved; use it rather than losing it.
	if dest.Project.Path != "" {
		return dest.Project, nil
	}
	return registeredProject{}, &destError{code: 2, msg: unregisteredCreateProject}
}

func resolveOpenDestination(ctx context.Context, stateDir, shellFlag, projectFlag string, register projectRegistration) (openDestination, error) {
	if shellFlag != "" || projectFlag != "" {
		return resolveExplicitDestination(stateDir, shellFlag, projectFlag, register)
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

func resolveExplicitDestination(stateDir, shellFlag, projectFlag string, register projectRegistration) (openDestination, error) {
	projects, err := loadRegisteredProjects(stateDir)
	if err != nil {
		return openDestination{}, err
	}

	var proj registeredProject
	if projectFlag != "" {
		proj, err = matchProject(stateDir, projects, projectFlag, register)
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
		// A running instance's project key names a directory that already
		// exists, so nothing here can need creating.
		if dest, err := resolveExplicitDestination(stateDir, "", inst.ProjectKey, resolveProjectOnly); err == nil {
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
		Project:     proj,
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
		Project:  proj,
	}
}

// resolveSessionsDestination addresses the running instance's global Sessions
// surface. Optional row is a durable inventory ID first, then a display name,
// with the same ambiguity rules --shell already has.
func resolveSessionsDestination(ctx context.Context, stateDir, row string) (openDestination, error) {
	rowID, rowName := "", ""
	if strings.TrimSpace(row) != "" {
		id, name, err := matchSessionsRow(stateDir, row)
		if err != nil {
			return openDestination{}, err
		}
		rowID, rowName = id, name
	}
	dest, err := resolveOpenDestination(ctx, stateDir, "", "", resolveProjectOnly)
	if err != nil {
		return openDestination{}, err
	}
	dest.Origin.Sessions = true
	dest.Origin.TmuxSession = ""
	dest.Origin.Namespace = ""
	dest.Resolved = uirequest.ResolvedSessions
	dest.Origin.SessionsRow = rowID
	if rowName != "" {
		dest.DisplayName = rowName
	} else if dest.DisplayName == "" {
		dest.DisplayName = "Sessions"
	}
	return dest, nil
}

type sessionsRowHit struct {
	id, name, tmux string
	aliases        []string
}

func matchSessionsRow(stateDir, name string) (id, display string, err error) {
	if name == "" {
		return "", "", &destError{code: 2, msg: "--sessions requires a row when given an argument"}
	}
	projects, err := loadRegisteredProjects(stateDir)
	if err != nil {
		return "", "", err
	}
	rows := listSessionsRows(projects)
	matches := func(pred func(sessionsRowHit) bool) []sessionsRowHit {
		var hits []sessionsRowHit
		for _, row := range rows {
			if pred(row) {
				hits = append(hits, row)
			}
		}
		return hits
	}
	idHits := matches(func(row sessionsRowHit) bool { return sessionsRowMatchesID(row, name) })
	hits := idHits
	if len(hits) == 0 {
		hits = matches(func(row sessionsRowHit) bool { return row.name == name })
	}
	if len(hits) == 0 {
		hits = matches(func(row sessionsRowHit) bool { return row.tmux == name })
	}
	if len(hits) == 0 {
		// Durable inventory IDs the CLI has not enumerated (the main checkout
		// before we listed it, a git worktree Sidecar never registered) still
		// address a live catalog row. Usage-failing them made get→apply on the
		// surface ID get printed die with "unknown Sessions row". Send the
		// ID through and let the host accept or decline.
		if _, _, _, ok := sessionsInventoryID(name); ok {
			id := canonicalizeSessionsRowID(name)
			return id, sessionsRowDisplay(id), nil
		}
		return "", "", &destError{code: 2, msg: fmt.Sprintf("unknown Sessions row %q", name)}
	}
	if len(hits) > 1 {
		ids := make([]string, 0, len(hits))
		for _, h := range hits {
			ids = append(ids, h.id)
		}
		return "", "", &destError{
			code: 3,
			msg:  fmt.Sprintf("row %q matches more than one Sessions row (%s); pass --sessions with a durable id", name, strings.Join(ids, ", ")),
		}
	}
	return hits[0].id, hits[0].name, nil
}

func listSessionsRows(projects []registeredProject) []sessionsRowHit {
	var rows []sessionsRowHit
	for _, p := range projects {
		projectIDs := []string{p.Key}
		rootCanon := ""
		if p.Path != "" {
			rootCanon = canonicalOpenPath(p.Path)
			if rootCanon != "" && rootCanon != p.Key {
				projectIDs = append(projectIDs, rootCanon)
			}
		}
		for _, sh := range p.Shells {
			name := sh.DisplayName
			if name == "" {
				name = sh.TmuxName
			}
			aliases := make([]string, 0, len(projectIDs))
			for _, key := range projectIDs {
				aliases = append(aliases, key+":shell:"+sh.TmuxName)
			}
			id := aliases[len(aliases)-1]
			rows = append(rows, sessionsRowHit{id: id, name: name, tmux: sh.TmuxName, aliases: aliases})
		}
		seenWorktrees := map[string]bool{}
		addWorktree := func(path string) {
			if path == "" {
				return
			}
			canon := canonicalOpenPath(path)
			if seenWorktrees[canon] {
				return
			}
			seenWorktrees[canon] = true
			base := filepath.Base(canon)
			if base == "" {
				base = filepath.Base(path)
			}
			aliases := make([]string, 0, len(projectIDs)*2)
			for _, key := range projectIDs {
				aliases = append(aliases, key+":worktree:"+canon)
				if path != canon {
					aliases = append(aliases, key+":worktree:"+path)
				}
			}
			id := aliases[len(aliases)-1]
			if rootCanon != "" {
				id = rootCanon + ":worktree:" + canon
			}
			rows = append(rows, sessionsRowHit{id: id, name: base, aliases: aliases})
		}
		// The main checkout is a Sessions catalog row. Registered worktrees
		// under projects/<slug>/worktrees/ are extras; inventory always
		// includes canonical(root):worktree:canonical(root).
		addWorktree(p.Path)
		for _, path := range p.Worktrees {
			addWorktree(path)
		}
	}
	return rows
}

func sessionsRowMatchesID(row sessionsRowHit, name string) bool {
	if row.id == name {
		return true
	}
	for _, alias := range row.aliases {
		if alias == name {
			return true
		}
	}
	want := canonicalizeSessionsRowID(name)
	if row.id == want || canonicalizeSessionsRowID(row.id) == want {
		return true
	}
	for _, alias := range row.aliases {
		if alias == want || canonicalizeSessionsRowID(alias) == want {
			return true
		}
	}
	return false
}

// sessionsInventoryID splits a durable catalog ID (projectKey:shell:key or
// projectKey:worktree:path). ok is false for a display name.
func sessionsInventoryID(s string) (kind, project, key string, ok bool) {
	for _, k := range []string{"shell", "worktree"} {
		sep := ":" + k + ":"
		i := strings.Index(s, sep)
		if i <= 0 {
			continue
		}
		return k, s[:i], s[i+len(sep):], true
	}
	return "", "", "", false
}

func canonicalizeSessionsRowID(id string) string {
	kind, project, key, ok := sessionsInventoryID(id)
	if !ok {
		return id
	}
	if kind == "worktree" {
		if looksLikePath(project) {
			project = canonicalOpenPath(project)
		}
		if key != "" {
			key = canonicalOpenPath(key)
		}
	}
	return project + ":" + kind + ":" + key
}

func looksLikePath(s string) bool {
	return filepath.IsAbs(s) || strings.Contains(s, string(filepath.Separator))
}

func sessionsRowDisplay(id string) string {
	kind, _, key, ok := sessionsInventoryID(id)
	if !ok {
		return id
	}
	if kind == "worktree" {
		if base := filepath.Base(key); base != "" && base != "." {
			return base
		}
	}
	if key != "" {
		return key
	}
	return id
}

func matchProject(stateDir string, projects []registeredProject, name string, register projectRegistration) (registeredProject, error) {
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
		if proj, ok := configuredProjectFallback(stateDir, name, register); ok {
			return proj, nil
		}
		// 5, not 2. "unknown project" is a verdict on a value the caller
		// supplied, and a caller on another machine reads 2 as version skew and
		// tells its user to update Sidecar. That reading is especially wrong
		// here: `host serve` advertises every configured project without
		// checking the path still exists, so the commonest way to reach this
		// line is a stale entry in the host's own config.
		return registeredProject{}, &destError{code: exitInputRejected, msg: fmt.Sprintf("unknown project %q", name)}
	}
}

// configuredProjectFallback resolves --project against the projects the user
// CONFIGURED, and registers the one it finds.
//
// Two lists were being treated as one. `sidecar host serve` advertises
// config.projects.list, so a host's projects appear in another Sidecar's picker
// as soon as the host is reachable; --project resolved only
// $STATE/sidecar/projects/<slug>, which exists after a project has been OPENED
// on that machine. A configured-but-never-opened project was therefore listed,
// selectable, and refused by every mutation with `unknown project` — the first
// thing a new user of remote hosts hits, with no way to guess that the answer
// is "go and open it over there once".
//
// Registration goes through projectdir.ResolveWithBase, the same call the local
// first-open path makes, so there is one implementation of "what a registered
// project directory looks like" and one AssertIsolatedPath gate over it: an
// isolated run still refuses to create this directory in the real state tree.
//
// Only an unambiguous single match registers anything. A name matching two
// configured projects keeps the existing refusal rather than picking one and
// materialising state for it.
func configuredProjectFallback(stateDir, name string, register projectRegistration) (registeredProject, bool) {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return registeredProject{}, false
	}
	wantPath := canonicalOpenPath(name)
	var matched []string
	for _, entry := range cfg.Projects.List {
		raw := strings.TrimSpace(config.ExpandPath(entry.Path))
		if raw == "" {
			continue
		}
		canon := canonicalOpenPath(raw)
		// The same three spellings matchProject accepts, so a configured
		// project answers to exactly what a registered one answers to.
		if entry.Name != name && canon != wantPath &&
			filepath.Base(canon) != name && filepath.Base(filepath.Clean(raw)) != name {
			continue
		}
		matched = append(matched, canon)
	}
	if len(matched) != 1 {
		return registeredProject{}, false
	}
	root := matched[0]
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		// A configured path that is not there is not a project this machine can
		// act on, and inventing a state directory for it would only move the
		// failure later.
		return registeredProject{}, false
	}
	if register != registerProject {
		// A read resolves the project and creates nothing. There is no state
		// directory, and that is the answer rather than a problem: Sidecar owns
		// no shells and no worktrees for a project nobody has opened here, so
		// the empty Dir below correctly produces an empty manifest.
		return registeredProject{Key: filepath.Base(root), Path: root}, true
	}
	dir, err := projectdir.ResolveWithBase(stateDir, root)
	if err != nil {
		return registeredProject{}, false
	}
	return registeredProject{
		Key:       filepath.Base(dir),
		Path:      root,
		Dir:       dir,
		Worktrees: listRegisteredWorktrees(dir),
	}, true
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
		// A rejected value, like unknown project above.
		return registeredProject{}, shellstate.Definition{}, &destError{code: exitInputRejected, msg: fmt.Sprintf("unknown shell %q", name)}
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
