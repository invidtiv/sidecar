// Package cli provides Sidecar's non-interactive command dispatch boundary.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// Run dispatches a non-interactive command. handled=false leaves legacy TUI
// flag parsing entirely untouched.
func Run(args []string, stdout, stderr io.Writer) (handled bool, exitCode int) {
	if len(args) == 0 {
		return false, 0
	}

	env := defaultEnv(stdout, stderr)

	if args[0] == "-h" || args[0] == "--help" {
		return true, runHelpCommand(env, args[1:])
	}
	if args[0] == "help" {
		return true, runHelpCommand(env, args[1:])
	}
	// Spelled as a flag because that is how an agent probes an unfamiliar
	// binary; "sidecar agents" answers the same way for the same reason.
	if args[0] == "--agents" || args[0] == "agents" {
		_, _ = fmt.Fprint(env.Stdout, RenderAgents(RootCommand()))
		return true, 0
	}

	root := RootCommand()
	cmd := root.FindSubcommand(args[0])
	if cmd == nil {
		return false, 0
	}

	if cmd.Run != nil {
		return true, cmd.Run(env, args[1:])
	}

	return true, 0
}

func runShellName(env Env, args []string) int {
	nameCmd := RootCommand().FindSubcommand("shell").FindSubcommand("name")
	nameHelp := RenderHelp(nameCmd)
	jsonOutput := false
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			if _, err := fmt.Fprint(env.Stdout, nameHelp); err != nil {
				return 1
			}
			return 0
		case "--json":
			jsonOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, nameHelp)
				return 2
			}
			cliErrf(env.Stderr, "shell name takes no positional arguments\n\n%s", nameHelp)
			return 2
		}
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	identity, err := currentShellIdentity(ctx)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	result, err := lookupCurrentShellName(ctx, env.StateDir, identity)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	if jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		return 0
	}
	if _, err := fmt.Fprintln(env.Stdout, result.Name); err != nil {
		return 1
	}
	return 0
}

func runShellRename(env Env, args []string) int {
	renameCmd := RootCommand().FindSubcommand("shell").FindSubcommand("rename")
	renameHelp := RenderHelp(renameCmd)
	jsonOutput := false
	var positional []string
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			if _, err := fmt.Fprint(env.Stdout, renameHelp); err != nil {
				return 1
			}
			return 0
		case "--json":
			jsonOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, renameHelp)
				return 2
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		cliErrf(env.Stderr, "shell rename requires exactly one quoted display name\n\n%s", renameHelp)
		return 2
	}
	name, err := shellstate.NormalizeName(positional[0])
	if err != nil {
		cliErrln(env.Stderr, err)
		return 2
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	identity, err := currentShellIdentity(ctx)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	result, err := renameCurrentShell(ctx, env.StateDir, identity, name)
	if err != nil {
		cliErrln(env.Stderr, err)
		if shellstate.IsValidation(err) {
			return 2
		}
		return 1
	}
	// Refresh the environment cue for anything started in this shell later.
	// The calling process keeps the value it inherited; the manifest, not the
	// environment, is the authority.
	_ = tty.SetSessionEnv(identity.session, shellstate.NameEnv, result.Name)
	if jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
	} else if result.Changed {
		if _, err := fmt.Fprintf(env.Stdout, "Renamed current Sidecar shell %q to %q.\n", result.OldName, result.Name); err != nil {
			return 1
		}
	} else {
		if _, err := fmt.Fprintf(env.Stdout, "Current Sidecar shell is already named %q.\n", result.Name); err != nil {
			return 1
		}
	}
	return 0
}

// cliErrf / cliErrln write usage and failure messages best-effort: the caller
// already has a more specific exit code, and a broken stderr should not replace it.
func cliErrf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func cliErrln(w io.Writer, a ...any) {
	_, _ = fmt.Fprintln(w, a...)
}

type shellIdentity struct{ session, socket, path string }

func currentShellIdentity(ctx context.Context) (shellIdentity, error) {
	if os.Getenv("TMUX") == "" {
		return shellIdentity{}, fmt.Errorf("not inside tmux; run this command from a Sidecar project shell")
	}
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return shellIdentity{}, fmt.Errorf("tmux did not identify the calling pane; run this command directly from a Sidecar project shell")
	}
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", pane, "#{session_name}\t#{socket_path}\t#{pane_current_path}").Output()
	if err != nil {
		return shellIdentity{}, fmt.Errorf("identify current tmux shell: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
		return shellIdentity{}, fmt.Errorf("tmux returned an incomplete current-shell identity")
	}
	if !strings.HasPrefix(parts[0], "sidecar-sh-") && !strings.HasPrefix(parts[0], "sidecar-ws-") {
		return shellIdentity{}, fmt.Errorf("current tmux session is not a Sidecar project shell")
	}
	socket := filepath.Clean(parts[1])
	if resolved, resolveErr := filepath.EvalSymlinks(socket); resolveErr == nil {
		socket = filepath.Clean(resolved)
	}
	path := ""
	if len(parts) == 3 {
		path = filepath.Clean(parts[2])
	}
	return shellIdentity{session: parts[0], socket: socket, path: path}, nil
}

func lookupCurrentShellName(ctx context.Context, stateDir string, identity shellIdentity) (shellstate.LookupResult, error) {
	if strings.HasPrefix(identity.session, "sidecar-sh-") {
		return shellstate.LookupCurrent(stateDir, shellstate.Identity{TmuxName: identity.session, Namespace: identity.socket})
	}
	projectRoot, worktreeRoot, err := currentManagedWorktree(ctx, stateDir, identity)
	if err != nil {
		return shellstate.LookupResult{}, err
	}
	name, err := workspaceops.LookupWorktreeDisplayName(stateDir, projectRoot, worktreeRoot)
	if err != nil {
		return shellstate.LookupResult{}, err
	}
	return shellstate.LookupResult{Shell: identity.session, Name: name}, nil
}

func renameCurrentShell(ctx context.Context, stateDir string, identity shellIdentity, name string) (shellstate.RenameResult, error) {
	if strings.HasPrefix(identity.session, "sidecar-sh-") {
		return shellstate.RenameCurrent(stateDir, shellstate.RenameRequest{TmuxName: identity.session, Namespace: identity.socket, Name: name})
	}
	projectRoot, worktreeRoot, err := currentManagedWorktree(ctx, stateDir, identity)
	if err != nil {
		return shellstate.RenameResult{}, err
	}
	result, err := workspaceops.RenameWorktreeDisplayName(ctx, stateDir, projectRoot, worktreeRoot, name)
	if err != nil {
		return shellstate.RenameResult{}, err
	}
	if result.Changed {
		// Persistence is authoritative. The request is a best-effort repaint cue
		// for already-running Sidecar instances; a restart reads the same value.
		_, _ = uirequest.WriteRequest(stateDir, uirequest.Request{
			Action: uirequest.ActionRenameWorktree,
			Origin: uirequest.Origin{
				TmuxSession: identity.session,
				Namespace:   identity.socket,
				WorkDir:     worktreeRoot,
				PID:         os.Getpid(),
			},
			Target: uirequest.Target{Kind: uirequest.TargetKindWorktree, Value: result.Name},
		})
	}
	return shellstate.RenameResult{Shell: identity.session, OldName: result.OldName, Name: result.Name, Changed: result.Changed}, nil
}

func currentManagedWorktree(ctx context.Context, stateDir string, identity shellIdentity) (projectRoot, worktreeRoot string, err error) {
	if identity.path == "" {
		return "", "", fmt.Errorf("tmux did not identify the managed worktree path")
	}
	worktreeRoot = workspaceops.WorktreeRoot(ctx, identity.path)
	if worktreeRoot == "" {
		return "", "", fmt.Errorf("current Sidecar worktree session is not inside a Git worktree")
	}
	if want := workspaceops.WorktreeSessionName(worktreeRoot, ""); identity.session != want {
		return "", "", fmt.Errorf("current tmux session does not match its Sidecar worktree identity")
	}
	projectRoot = workspaceops.MainWorktreePath(ctx, worktreeRoot)
	if projectRoot == "" {
		return "", "", fmt.Errorf("cannot resolve the owning Sidecar project")
	}
	if _, lookupErr := workspaceops.LookupWorktreeDisplayName(stateDir, projectRoot, worktreeRoot); lookupErr != nil {
		return "", "", lookupErr
	}
	return projectRoot, worktreeRoot, nil
}

func isHelp(arg string) bool { return arg == "-h" || arg == "--help" || arg == "help" }
