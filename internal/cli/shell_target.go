package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/managedtarget"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceops"
)

const (
	shellTargetKindShell    = "shell"
	shellTargetKindWorktree = "worktree"
)

// shellTargetUnregistered is the exit code for a --target that names no
// session this project owns. It is deliberately distinct from 1: a caller
// driving these verbs from another machine has to tell "you addressed
// something Sidecar does not own" apart from "the state tree failed to read".
const shellTargetUnregistered = 3

// shellSendRunner is the tmux runner `sidecar shell send` sends keys through.
// It is a variable so a test can prove the registration guard refuses before
// any key reaches tmux at all.
var shellSendRunner workspaceops.TmuxRunner = workspaceops.ExecTmuxRunner{}

// shellTarget is a tmux session that has been proved to belong to the resolved
// Sidecar project — either a record in its shells.json or a registered
// worktree whose agent session name matches.
//
// Nothing may send keys to, or rename, a session that did not come out of this
// function. tmux resolves `-t <name>` against whatever session happens to
// answer to that name, so a bare name match is not identity: this is the
// boundary that turns a caller-supplied string into a session Sidecar owns.
type shellTarget struct {
	Kind         string
	Session      string
	DisplayName  string
	Namespace    string
	WorkDir      string
	WorktreeRoot string
	Project      registeredProject
	ManifestPath string
}

func resolveShellTarget(env Env, target, shellFlag, projectFlag, help string) (shellTarget, int) {
	return resolveShellTargetMode(env, target, shellFlag, projectFlag, help, false)
}

func resolveShellTargetMode(env Env, target, shellFlag, projectFlag, help string, globalExplicit bool) (shellTarget, int) {
	resolved, code, err := findShellTarget(env, target, shellFlag, projectFlag, globalExplicit, "")
	if err == nil {
		return resolved, 0
	}
	if code == 2 {
		cliErrf(env.Stderr, "--target requires a tmux session name\n\n%s", help)
		return shellTarget{}, code
	}
	if code == shellTargetUnregistered {
		cliErrf(env.Stderr, "no registered Sidecar shell or worktree session named %q; run `sidecar shell list --json` to see what Sidecar owns\n", strings.TrimSpace(target))
		return shellTarget{}, code
	}
	cliErrln(env.Stderr, err)
	return shellTarget{}, code
}

// findShellTarget is the shared non-rendering resolver. The shell commands
// wrap it with their established human errors; agent commands wrap the same
// result in their stable JSON error envelope.
//
// A caller that resolves more than one value under the same flags should build
// a [shellTargetLookup] and go through it instead, so the candidate scan
// happens once.
func findShellTarget(env Env, target, shellFlag, projectFlag string, globalExplicit bool, namespace string) (shellTarget, int, error) {
	var lookup shellTargetLookup
	return lookup.resolve(env, target, shellFlag, projectFlag, globalExplicit, namespace)
}

// shellTargetLookup memoizes the expensive half of resolving a shell target.
//
// Building the candidate list loads every registered project's manifest and
// runs `git worktree list` once per project. Matching a *value* against that
// list is a string comparison. Those are different costs, and a command that
// asks two questions under the same flags — as `agent prompt` does, once to
// decide whether a lone positional is a target and once to resolve the target
// it will actually write to — should pay the first only once.
//
// The zero value is ready, and a lookup is scoped to one command invocation:
// nothing here is a process-lifetime cache, because shells appear and disappear
// while Sidecar runs and a stale candidate list would resolve to a session that
// is gone.
type shellTargetLookup struct {
	scans map[string]*shellTargetScan
	// caller memoizes the project the calling shell belongs to; the empty
	// string after resolution means "none", and resolved says the lookup ran.
	caller         string
	callerResolved bool
}

// callerProject is the registered project the calling Sidecar session belongs
// to, or "" outside one. It is memoized because a prompt asks twice.
//
// SIDECAR_SHELL is asked first: it needs no tmux subprocess and survives a
// harness that dropped the TMUX variables. A worktree session (sidecar-ws-…)
// exports no SIDECAR_SHELL, though, and it is exactly the context
// `create worktree --agent` puts an agent in — so when the variable is
// absent the session tmux reports is resolved the way the current-shell verbs
// resolve it: a shell record by name, or a registered worktree to the project
// whose checkout Git says is its main worktree.
func (l *shellTargetLookup) callerProject(env Env) string {
	if l.callerResolved {
		return l.caller
	}
	l.callerResolved = true
	if origin, ok := callerShellOrigin(env.StateDir); ok {
		l.caller = origin.ProjectKey
		return l.caller
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	identity, err := currentShellIdentity(ctx)
	if err != nil {
		return ""
	}
	if strings.HasPrefix(identity.session, "sidecar-sh-") {
		if origin, err := shellstate.LookupOrigin(env.StateDir, shellstate.Identity{TmuxName: identity.session, Namespace: identity.socket}); err == nil {
			l.caller = origin.ProjectKey
		}
		return l.caller
	}
	projectRoot, _, err := currentManagedWorktree(ctx, env.StateDir, identity)
	if err != nil {
		return ""
	}
	projects, err := loadRegisteredProjects(env.StateDir)
	if err != nil {
		return ""
	}
	want := canonicalOpenPath(projectRoot)
	for _, p := range projects {
		if p.Path != "" && canonicalOpenPath(p.Path) == want {
			l.caller = p.Key
			break
		}
	}
	return l.caller
}

// callerShellOrigin resolves the managed shell this process was started in,
// by the name Sidecar exported into it, on the tmux server this process talks
// to. It is the state-file half of currentShellIdentity: no tmux subprocess,
// and no dependence on TMUX_PANE surviving whatever spawned us.
func callerShellOrigin(stateDir string) (shellstate.OriginInfo, bool) {
	name := strings.TrimSpace(os.Getenv(shellstate.SessionEnv))
	if name == "" {
		return shellstate.OriginInfo{}, false
	}
	origin, err := shellstate.LookupOrigin(stateDir, shellstate.Identity{TmuxName: name, Namespace: tmuxenv.Namespace()})
	if err != nil {
		return shellstate.OriginInfo{}, false
	}
	return origin, true
}

type shellTargetScan struct {
	projects   []registeredProject
	candidates []managedtarget.Target
	code       int
	err        error
}

// resolve answers one target value, reusing the scan for these flags.
func (l *shellTargetLookup) resolve(env Env, target, shellFlag, projectFlag string, globalExplicit bool, namespace string) (shellTarget, int, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return shellTarget{}, 2, fmt.Errorf("target is required")
	}
	scan := l.scan(env, shellFlag, projectFlag, globalExplicit)
	if scan.err != nil {
		return shellTarget{}, scan.code, scan.err
	}

	byProject := make(map[string]registeredProject, len(scan.projects))
	for _, proj := range scan.projects {
		byProject[proj.Key] = proj
	}
	resolved, err := managedtarget.Resolve(scan.candidates, managedtarget.Query{Host: "local", Namespace: namespace, Value: target})
	if err != nil {
		typed, ok := err.(*managedtarget.Error)
		if ok && typed.Kind == managedtarget.NotFound {
			return shellTarget{}, shellTargetUnregistered, err
		}
		if ok && typed.Kind == managedtarget.Ambiguous && shellFlag == "" && projectFlag == "" {
			// The caller's own project breaks a tie a global search cannot.
			// An agent driving a sibling worktree from its managed shell has
			// already said which Sidecar it means — SIDECAR_SHELL names it —
			// and being told to pass --shell with that same value is the
			// friction td-c906c1 records. Only ambiguity is narrowed: a value
			// that resolved uniquely elsewhere still resolves there, so a
			// shell can keep addressing another project by name.
			if project := l.callerProject(env); project != "" {
				if narrowed, narrowErr := managedtarget.Resolve(scan.candidates, managedtarget.Query{Host: "local", Project: project, Namespace: namespace, Value: target}); narrowErr == nil {
					resolved, err = narrowed, nil
				}
			}
		}
		if err != nil {
			return shellTarget{}, 1, err
		}
	}
	proj := byProject[resolved.Project]
	return shellTarget{Kind: resolved.Kind, Session: resolved.Session, DisplayName: resolved.Name, Namespace: resolved.Namespace, WorkDir: resolved.WorkDir, WorktreeRoot: resolved.WorktreeRoot, Project: proj, ManifestPath: resolved.ManifestPath}, 0, nil
}

// find is resolve with the namespace this process's own shell belongs to,
// which is what every agent command wants.
func (l *shellTargetLookup) find(env Env, target, shellFlag, projectFlag string, globalExplicit bool) (shellTarget, int, error) {
	return l.resolve(env, target, shellFlag, projectFlag, globalExplicit, tmuxenv.Namespace())
}

// scan builds, or reuses, the candidate list for one set of flags.
func (l *shellTargetLookup) scan(env Env, shellFlag, projectFlag string, globalExplicit bool) *shellTargetScan {
	key := shellFlag + "\x00" + projectFlag + "\x00" + strconv.FormatBool(globalExplicit)
	if s, ok := l.scans[key]; ok {
		return s
	}
	s := buildShellTargetScan(env, shellFlag, projectFlag, globalExplicit)
	if l.scans == nil {
		l.scans = map[string]*shellTargetScan{}
	}
	l.scans[key] = s
	return s
}

func buildShellTargetScan(env Env, shellFlag, projectFlag string, globalExplicit bool) *shellTargetScan {
	projects, code, err := scanProjects(env, shellFlag, projectFlag, globalExplicit)
	if err != nil {
		return &shellTargetScan{code: code, err: err}
	}
	candidates, err := managedTargetCandidates(env, projects)
	if err != nil {
		return &shellTargetScan{code: 1, err: err}
	}
	return &shellTargetScan{projects: projects, candidates: candidates}
}

// scanProjects is the set of projects a read-only lookup searches.
//
// An explicit --project or --shell is matched against the registry directly
// rather than through resolveExplicitDestination, because that function
// answers a different question. It resolves where a UI request should LAND,
// and so refuses a project that several running instances are showing — the
// request would have to pick one. A lookup only needs the project's manifest,
// which is the same file however many instances have it open; refusing
// `agent get X --project sidecar` because two Sidecars show sidecar sent a
// caller after --shell for a verb that never touches an instance.
//
// With no flags and globalExplicit unset, the caller's own context (its
// managed shell, the unique instance, or the registered project holding the
// working directory) scopes the search; failing that, or with globalExplicit,
// every registered project is searched.
func scanProjects(env Env, shellFlag, projectFlag string, globalExplicit bool) ([]registeredProject, int, error) {
	if shellFlag != "" || projectFlag != "" {
		projects, err := loadRegisteredProjects(env.StateDir)
		if err != nil {
			return nil, 1, err
		}
		if shellFlag != "" {
			search := projects
			if projectFlag != "" {
				proj, err := matchProject(env.StateDir, projects, projectFlag, resolveProjectOnly)
				if err != nil {
					return nil, createDestExitCode(err), err
				}
				search = []registeredProject{proj}
			}
			proj, _, err := matchShell(search, shellFlag, projectFlag == "")
			if err != nil {
				return nil, createDestExitCode(err), err
			}
			return []registeredProject{proj}, 0, nil
		}
		proj, err := matchProject(env.StateDir, projects, projectFlag, resolveProjectOnly)
		if err != nil {
			return nil, createDestExitCode(err), err
		}
		return []registeredProject{proj}, 0, nil
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if !globalExplicit {
		if dest, err := resolveCreateDestination(ctx, env.StateDir, "", "", resolveProjectOnly); err == nil {
			if proj, projectErr := registeredProjectForCreate(env.StateDir, dest); projectErr == nil {
				return []registeredProject{proj}, 0, nil
			}
		}
	}
	projects, err := loadRegisteredProjects(env.StateDir)
	if err != nil {
		return nil, 1, err
	}
	return projects, 0, nil
}

// projectManifestPath is where one registered project keeps its shell manifest.
// It is stated once so a caller that needs the path — the candidate scan, and
// the global session dedup check — cannot disagree about where it is.
func projectManifestPath(proj registeredProject) string {
	if proj.Dir == "" {
		return ""
	}
	return filepath.Join(proj.Dir, "shells.json")
}

// managedTargetCandidates is every shell and worktree session the given
// projects own, each worktree root listed exactly once.
//
// Shell records are simple: a shells.json row belongs to the project whose
// manifest holds it. Worktree roots are not, because several registered
// projects can see the same directory. A worktree Sidecar created under
// project A is also, in Git's inventory, a sibling of every other checkout of
// that repository — so a project registered from another worktree of the same
// repo (a `sidecar-2`, a `sidecar-pane-parity` someone opened Sidecar in) or
// from a subdirectory of it (`.claude`) rediscovers it. Emitting the root once
// per project that can see it is how `agent list` came to report one pane six
// times and how an explicit target became "ambiguous across 3 Sidecar
// sessions" (td-ebd72c, td-c906c1).
//
// Each root therefore has one owner, chosen by how strongly a project claims
// it: the project that registered it as a created worktree, then the project
// whose own checkout it is, then the first project that merely discovered it
// through Git. A project with no checkout path owns no worktree at all — its
// empty path used to canonicalize to the working directory, which claimed
// whatever repository the caller happened to be standing in. The same order
// decides which project a working directory belongs to (uniqueProjectContaining),
// so a rename resolved from inside a worktree writes the display name the
// global listing reads back.
func managedTargetCandidates(env Env, projects []registeredProject) ([]managedtarget.Target, error) {
	var candidates []managedtarget.Target
	type rootClaim struct {
		proj     registeredProject
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
	claim := func(root string, proj registeredProject, manifest string, tier int) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		root = canonicalOpenPath(root)
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
			candidates = append(candidates, managedtarget.Target{Host: "local", Project: proj.Key, ProjectRoot: proj.Path, Kind: shellTargetKindShell, Session: def.TmuxName, Name: def.DisplayName, Namespace: def.Namespace, WorkDir: workDir, ManifestPath: manifest, Priority: 0})
		}
		if strings.TrimSpace(proj.Path) == "" {
			continue
		}
		for _, root := range proj.Worktrees {
			claim(root, proj, manifest, tierCreated)
		}
		claim(proj.Path, proj, manifest, tierCheckout)
		for _, root := range discoveredWorktreeRoots(env, proj) {
			claim(root, proj, manifest, tierDiscovered)
		}
	}
	for _, root := range roots {
		c := claims[root]
		priority := 1
		if c.tier == tierDiscovered {
			priority = 2
		}
		name, _ := workspaceops.LookupWorktreeDisplayName(env.StateDir, c.proj.Path, root)
		candidates = append(candidates, managedtarget.Target{Host: "local", Project: c.proj.Key, ProjectRoot: c.proj.Path, Kind: shellTargetKindWorktree, Session: workspaceops.WorktreeSessionName(root, ""), Name: name, Namespace: tmuxenv.Namespace(), WorkDir: root, WorktreeRoot: root, ManifestPath: c.manifest, Priority: priority})
	}
	return candidates, nil
}

// discoveredWorktreeRoots is every worktree Git lists for this project's
// checkout, whether or not Sidecar registered it.
//
// proj.Worktrees is the set Sidecar CREATED (<projectDir>/worktrees/*). Git's
// own `worktree list` is a superset that also holds a worktree the user made
// by hand — and locally RenameWorktreeDisplayName creates the state directory
// on demand, so renaming one of those works. Scoped to the created set, a
// remote rename of a hand-made worktree exited 3 while the identical local
// rename succeeded, for a row the user could see either way.
//
// The caller ranks these below the registered tiers, because a session name
// is derived from a basename and collides across directories, and because a
// repository's inventory is visible from every one of its checkouts. A
// repository Git cannot read simply has no discovered tier.
func discoveredWorktreeRoots(env Env, proj registeredProject) []string {
	if proj.Path == "" {
		return nil
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
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

// sameTmuxServer reports whether a recorded namespace names the tmux server
// this process's tmux children will actually reach.
//
// tmuxenv.Namespace() is the socket path, and it is the whole identity of "the
// server whose sessions this process can see". A record proved present on one
// socket says nothing about what answers to that session name on another, which
// is why send compares them: proving ownership against server A and then typing
// into server B is worse than refusing.
//
// An empty namespace is not a mismatch. A worktree agent session carries no
// recorded socket, so there is nothing to compare and nothing is claimed.
func sameTmuxServer(namespace string) bool {
	if strings.TrimSpace(namespace) == "" {
		return true
	}
	return canonicalSocketPath(namespace) == canonicalSocketPath(tmuxenv.Namespace())
}

// canonicalSocketPath resolves the directory holding a tmux socket so two
// spellings of one server compare equal. The socket file itself is not
// resolved: it need not exist, and a server that is not running must still be
// recognised as the same server.
func canonicalSocketPath(path string) string {
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	dir, base := filepath.Split(cleaned)
	resolved, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return cleaned
	}
	return filepath.Join(resolved, base)
}

// runShellRenameTarget renames a shell or worktree the caller is not sitting
// in. It resolves the project explicitly and then dispatches exactly as the
// current-shell path does: a shells.json record through
// shellstate.RenameAtPath, a registered worktree through
// workspaceops.RenameWorktreeDisplayName.
func runShellRenameTarget(env Env, args []string) int {
	renameCmd := RootCommand().FindSubcommand("shell").FindSubcommand("rename")
	help := RenderHelp(renameCmd)

	jsonOutput := false
	target, shellFlag, projectFlag := "", "", ""
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			if _, err := fmt.Fprint(env.Stdout, help); err != nil {
				return 1
			}
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--target" || strings.HasPrefix(arg, "--target="):
			val, next, ok := takeFlagArg(arg, args, i, "--target")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--target requires a tmux session name\n\n%s", help)
				return 2
			}
			target = val
			i = next
		case arg == "--shell" || strings.HasPrefix(arg, "--shell="):
			val, next, ok := takeFlagArg(arg, args, i, "--shell")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--shell requires a shell name\n\n%s", help)
				return 2
			}
			shellFlag = val
			i = next
		case arg == "--project" || strings.HasPrefix(arg, "--project="):
			val, next, ok := takeFlagArg(arg, args, i, "--project")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--project requires a project name\n\n%s", help)
				return 2
			}
			projectFlag = val
			i = next
		case arg == "--":
			// Everything after `--` is a value, not a flag. NormalizeName
			// accepts a leading dash, so "-wip" is a legal display name here
			// and legal in the TUI; without this the only spelling that
			// reached the parser came back as `unknown option "-wip"`.
			positional = append(positional, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
				return 2
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		cliErrf(env.Stderr, "shell rename requires exactly one quoted display name\n\n%s", help)
		return 2
	}
	name, err := shellstate.NormalizeName(positional[0])
	if err != nil {
		cliErrln(env.Stderr, err)
		return exitInputRejected
	}

	tgt, code := resolveShellTarget(env, target, shellFlag, projectFlag, help)
	if code != 0 {
		return code
	}

	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	var result shellstate.RenameResult
	if tgt.Kind == shellTargetKindShell {
		result, err = shellstate.RenameAtPath(tgt.ManifestPath, shellstate.RenameRequest{
			TmuxName:  tgt.Session,
			Namespace: tgt.Namespace,
			Name:      name,
		})
	} else {
		var renamed workspaceops.WorktreeDisplayNameResult
		renamed, err = workspaceops.RenameWorktreeDisplayName(ctx, env.StateDir, tgt.Project.Path, tgt.WorktreeRoot, name)
		result = shellstate.RenameResult{Shell: tgt.Session, OldName: renamed.OldName, Name: renamed.Name, Changed: renamed.Changed}
	}
	if err != nil {
		cliErrln(env.Stderr, err)
		return renameTargetExitCode(err)
	}

	// Refresh the environment cue so anything started in that session later
	// reads the new name. The manifest, not the environment, is authoritative;
	// a session that is not running simply has nothing to update.
	//
	// Skipped when the record belongs to another tmux server: SetSessionEnv
	// resolves -t against THIS process's socket, so the one thing it could
	// still find there is an unrelated session with a colliding name. Renaming
	// the record is correct across servers; writing a variable into a stranger
	// is not.
	if sameTmuxServer(tgt.Namespace) {
		_ = tty.SetSessionEnv(tgt.Session, shellstate.NameEnv, result.Name)
	}

	if result.Changed {
		writeRenameTargetRepaint(env, tgt, result.Name)
	}

	if jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		return 0
	}
	if result.Changed {
		if _, err := fmt.Fprintf(env.Stdout, "Renamed Sidecar %s %q to %q (%s).\n", tgt.Kind, result.OldName, result.Name, tgt.Session); err != nil {
			return 1
		}
		return 0
	}
	if _, err := fmt.Fprintf(env.Stdout, "Sidecar %s %s is already named %q.\n", tgt.Kind, tgt.Session, result.Name); err != nil {
		return 1
	}
	return 0
}

// renameTargetExitCode maps the guards RenameAtPath already enforces onto the
// command's documented codes: a refused name is the caller's to fix (5), a
// manifest that cannot answer which record it means is not (1).
//
// 5 rather than 2 because "another shell is already named Demo" is a fact about
// the value, not about the command. A caller on another machine reads 2 as
// "these two Sidecars disagree about this verb" and tells its user to upgrade;
// what that user actually has to do is pick a different name.
func renameTargetExitCode(err error) int {
	if shellstate.IsValidation(err) {
		return exitInputRejected
	}
	if shellstate.IsNotFound(err) {
		return shellTargetUnregistered
	}
	return 1
}

// writeRenameTargetRepaint is a best-effort cue for already-running Sidecar
// instances, exactly as the current-shell path writes. Persistence has already
// happened; a restart reads the same value either way.
func writeRenameTargetRepaint(env Env, tgt shellTarget, name string) {
	origin := uirequest.Origin{
		TmuxSession: tgt.Session,
		Namespace:   tgt.Namespace,
		ProjectKey:  tgt.Project.Key,
		WorkDir:     tgt.WorkDir,
		PID:         os.Getpid(),
	}
	action := uirequest.ActionRenameShell
	target := uirequest.Target{Kind: uirequest.TargetKindShell, Value: name}
	if tgt.Kind == shellTargetKindWorktree {
		action = uirequest.ActionRenameWorktree
		target = uirequest.Target{Kind: uirequest.TargetKindWorktree, Value: name}
		origin.WorkDir = tgt.WorktreeRoot
	}
	_, _ = uirequest.WriteRequest(env.StateDir, uirequest.Request{Action: action, Origin: origin, Target: target})
}

type shellSendResult struct {
	Shell   string `json:"shell"`
	Name    string `json:"name,omitempty"`
	Kind    string `json:"kind"`
	Mode    string `json:"mode"`
	Command string `json:"command"`
	Sent    bool   `json:"sent"`
}

// runShellSend starts a command in a Sidecar session the caller is not sitting
// in. The target is resolved through resolveShellTarget first and refused if
// this project does not own it — workspaceops.StartAgentInShell has no such
// guard, and sending keys into whatever session answers to a matching name is
// exactly the failure this verb exists to avoid.
func runShellSend(env Env, args []string) int {
	sendCmd := RootCommand().FindSubcommand("shell").FindSubcommand("send")
	help := RenderHelp(sendCmd)

	jsonOutput := false
	target, shellFlag, projectFlag := "", "", ""
	runCommand, typeCommand := "", ""
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			if _, err := fmt.Fprint(env.Stdout, help); err != nil {
				return 1
			}
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--target" || strings.HasPrefix(arg, "--target="):
			val, next, ok := takeFlagArg(arg, args, i, "--target")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--target requires a tmux session name\n\n%s", help)
				return 2
			}
			target = val
			i = next
		case arg == "--run" || strings.HasPrefix(arg, "--run="):
			val, next, ok := takeFlagArg(arg, args, i, "--run")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--run requires a command\n\n%s", help)
				return 2
			}
			runCommand = val
			i = next
		case arg == "--type" || strings.HasPrefix(arg, "--type="):
			val, next, ok := takeFlagArg(arg, args, i, "--type")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--type requires a command\n\n%s", help)
				return 2
			}
			typeCommand = val
			i = next
		case arg == "--shell" || strings.HasPrefix(arg, "--shell="):
			val, next, ok := takeFlagArg(arg, args, i, "--shell")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--shell requires a shell name\n\n%s", help)
				return 2
			}
			shellFlag = val
			i = next
		case arg == "--project" || strings.HasPrefix(arg, "--project="):
			val, next, ok := takeFlagArg(arg, args, i, "--project")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--project requires a project name\n\n%s", help)
				return 2
			}
			projectFlag = val
			i = next
		case arg == "--":
			// Everything after `--` is a value, not a flag. This verb takes no
			// positionals, so the terminator only changes which message a
			// caller gets — "takes no positional arguments" rather than
			// "unknown option" — but the two parsers agreeing about what ends
			// flag parsing is worth more than the saved four lines.
			positional = append(positional, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
				return 2
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 0 {
		cliErrf(env.Stderr, "shell send takes no positional arguments; pass the command to --run or --type\n\n%s", help)
		return 2
	}
	if runCommand != "" && typeCommand != "" {
		cliErrf(env.Stderr, "--run and --type are mutually exclusive\n\n%s", help)
		return 2
	}
	if runCommand == "" && typeCommand == "" {
		cliErrf(env.Stderr, "shell send requires --run or --type\n\n%s", help)
		return 2
	}
	if target == "" {
		cliErrf(env.Stderr, "shell send requires --target\n\n%s", help)
		return 2
	}

	tgt, code := resolveShellTarget(env, target, shellFlag, projectFlag, help)
	if code != 0 {
		return code
	}
	// The ownership proof and the keystrokes must land on the same tmux server.
	// resolveShellTarget proves a record exists on the socket the record names;
	// the runner below resolves `-t <session>` on the socket THIS process uses.
	// Where those differ, a matching session name on this server is a different
	// session belonging to someone else, and typing into it is the failure this
	// verb exists to make impossible. Refuse rather than guess.
	if !sameTmuxServer(tgt.Namespace) {
		cliErrf(env.Stderr,
			"shell %q in project %q is recorded on tmux server %s, but this process talks to %s; "+
				"run `sidecar shell send` from that server so the keys cannot reach a different session\n",
			tgt.Session, tgt.Project.Key, tgt.Namespace, tmuxenv.Namespace())
		return shellTargetUnregistered
	}

	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	mode, command := "run", runCommand
	err := error(nil)
	if runCommand != "" {
		err = workspaceops.StartAgentInShellWithRunner(ctx, tgt.Session, runCommand, shellSendRunner)
	} else {
		mode, command = "type", typeCommand
		err = workspaceops.TypeInShellWithRunner(ctx, tgt.Session, typeCommand, shellSendRunner)
	}
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}

	if jsonOutput {
		result := shellSendResult{
			Shell:   tgt.Session,
			Name:    tgt.DisplayName,
			Kind:    tgt.Kind,
			Mode:    mode,
			Command: command,
			Sent:    true,
		}
		if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		return 0
	}
	verb := "Ran"
	if mode == "type" {
		verb = "Typed"
	}
	if _, err := fmt.Fprintf(env.Stdout, "%s %q in %s.\n", verb, command, tgt.Session); err != nil {
		return 1
	}
	return 0
}
