package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/marcus/sidecar/internal/shellstate"
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
	target = strings.TrimSpace(target)
	if target == "" {
		cliErrf(env.Stderr, "--target requires a tmux session name\n\n%s", help)
		return shellTarget{}, 2
	}
	proj, manifest, code := resolveShellRecordsProject(env, shellRecordFlags{shellFlag: shellFlag, projectFlag: projectFlag}, help)
	if code != 0 {
		return shellTarget{}, code
	}

	defs, err := shellstate.ListAtPath(manifest)
	if err != nil {
		cliErrln(env.Stderr, err)
		return shellTarget{}, 1
	}
	var shells []shellstate.Definition
	for _, def := range defs {
		if def.TmuxName == target {
			shells = append(shells, def)
		}
	}
	if len(shells) > 1 {
		cliErrf(env.Stderr, "shell %q appears more than once in project %q; refusing an ambiguous target\n", target, proj.Key)
		return shellTarget{}, 1
	}
	if len(shells) == 1 {
		workDir := shells[0].WorkDir
		if workDir == "" {
			workDir = proj.Path
		}
		return shellTarget{
			Kind:         shellTargetKindShell,
			Session:      target,
			DisplayName:  shells[0].DisplayName,
			Namespace:    shells[0].Namespace,
			WorkDir:      workDir,
			Project:      proj,
			ManifestPath: manifest,
		}, 0
	}

	var roots []string
	seen := map[string]bool{}
	for _, path := range append([]string{proj.Path}, proj.Worktrees...) {
		if path == "" {
			continue
		}
		canon := canonicalOpenPath(path)
		if seen[canon] {
			continue
		}
		seen[canon] = true
		if workspaceops.WorktreeSessionName(path, "") == target {
			roots = append(roots, path)
		}
	}
	if len(roots) > 1 {
		cliErrf(env.Stderr, "worktree session %q matches more than one registered worktree in project %q; refusing an ambiguous target\n", target, proj.Key)
		return shellTarget{}, 1
	}
	if len(roots) == 1 {
		name, nameErr := workspaceops.LookupWorktreeDisplayName(env.StateDir, proj.Path, roots[0])
		if nameErr != nil {
			name = ""
		}
		return shellTarget{
			Kind:         shellTargetKindWorktree,
			Session:      target,
			DisplayName:  name,
			WorkDir:      roots[0],
			WorktreeRoot: roots[0],
			Project:      proj,
			ManifestPath: manifest,
		}, 0
	}

	cliErrf(env.Stderr, "no registered Sidecar shell or worktree session named %q in project %q; run `sidecar shell list --json` to see what this project owns\n", target, proj.Key)
	return shellTarget{}, shellTargetUnregistered
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
		return 2
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
	_ = tty.SetSessionEnv(tgt.Session, shellstate.NameEnv, result.Name)

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
// command's documented codes: a refused name is the caller's to fix (2), a
// manifest that cannot answer which record it means is not (1).
func renameTargetExitCode(err error) int {
	if shellstate.IsValidation(err) {
		return 2
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
