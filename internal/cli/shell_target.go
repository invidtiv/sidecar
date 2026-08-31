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
		if typed, ok := err.(*managedtarget.Error); ok && typed.Kind == managedtarget.NotFound {
			return shellTarget{}, shellTargetUnregistered, err
		}
		return shellTarget{}, 1, err
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
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var projects []registeredProject
	if dest, err := resolveCreateDestination(ctx, env.StateDir, shellFlag, projectFlag, resolveProjectOnly); !globalExplicit && err == nil {
		if proj, projectErr := registeredProjectForCreate(env.StateDir, dest); projectErr == nil {
			projects = []registeredProject{proj}
		}
	} else if shellFlag != "" || projectFlag != "" {
		return &shellTargetScan{code: createDestExitCode(err), err: err}
	}
	if len(projects) == 0 {
		var err error
		projects, err = loadRegisteredProjects(env.StateDir)
		if err != nil {
			return &shellTargetScan{code: 1, err: err}
		}
	}
	candidates, err := managedTargetCandidates(env, projects)
	if err != nil {
		return &shellTargetScan{code: 1, err: err}
	}
	return &shellTargetScan{projects: projects, candidates: candidates}
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

func managedTargetCandidates(env Env, projects []registeredProject) ([]managedtarget.Target, error) {
	var candidates []managedtarget.Target
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
		registered, discovered := worktreeRootsForTarget(env, proj)
		seen := map[string]bool{}
		addRoots := func(roots []string, priority int) {
			for _, root := range roots {
				root = canonicalOpenPath(root)
				if root == "" || seen[root] {
					continue
				}
				seen[root] = true
				name, _ := workspaceops.LookupWorktreeDisplayName(env.StateDir, proj.Path, root)
				candidates = append(candidates, managedtarget.Target{Host: "local", Project: proj.Key, ProjectRoot: proj.Path, Kind: shellTargetKindWorktree, Session: workspaceops.WorktreeSessionName(root, ""), Name: name, Namespace: tmuxenv.Namespace(), WorkDir: root, WorktreeRoot: root, ManifestPath: manifest, Priority: priority})
			}
		}
		addRoots(registered, 1)
		addRoots(discovered, 2)
	}
	return candidates, nil
}

// worktreeRootsForTarget is every worktree of this project a row can be shown
// for, split into the ones Sidecar registered and the ones only Git knows.
//
// proj.Worktrees is the set Sidecar CREATED (<projectDir>/worktrees/*). The
// rows come from Git's own `worktree list`, a superset that also holds a
// worktree the user made by hand — and locally RenameWorktreeDisplayName
// creates the state directory on demand, so renaming one of those works. Scoped
// to the created set, a remote rename of a hand-made worktree exited 3 while
// the identical local rename succeeded, for a row the user could see either way.
//
// The two come back separately because a session name is derived from a
// basename and therefore collides across directories; the caller resolves the
// registered tier first. Git is asked only when the name matched no shell
// record, so the ordinary `--target sidecar-sh-…` path still spawns nothing,
// and a repository Git cannot read simply has no discovered tier.
func worktreeRootsForTarget(env Env, proj registeredProject) (registered, discovered []string) {
	registered = append([]string{proj.Path}, proj.Worktrees...)
	if proj.Path == "" {
		return registered, nil
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	states, err := workspaceops.ListWorktreeStates(ctx, proj.Path)
	if err != nil {
		return registered, nil
	}
	for _, state := range states {
		if state.Bare || state.Path == "" {
			continue
		}
		discovered = append(discovered, state.Path)
	}
	return registered, discovered
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
