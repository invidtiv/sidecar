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

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tty"
)

// Run dispatches a non-interactive command. handled=false leaves legacy TUI
// flag parsing entirely untouched.
func Run(args []string, stdout, stderr io.Writer) (handled bool, exitCode int) {
	if len(args) == 0 || args[0] != "shell" {
		return false, 0
	}
	if len(args) == 1 || isHelp(args[1]) {
		if _, err := fmt.Fprint(stdout, shellHelp); err != nil {
			return true, 1
		}
		return true, 0
	}
	if args[1] != "rename" {
		cliErrf(stderr, "unknown shell command %q\n\n%s", args[1], shellHelp)
		return true, 2
	}
	return true, runShellRename(args[2:], stdout, stderr)
}

func runShellRename(args []string, stdout, stderr io.Writer) int {
	jsonOutput := false
	var positional []string
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			if _, err := fmt.Fprint(stdout, renameHelp); err != nil {
				return 1
			}
			return 0
		case "--json":
			jsonOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(stderr, "unknown option %q\n\n%s", arg, renameHelp)
				return 2
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		cliErrf(stderr, "shell rename requires exactly one quoted display name\n\n%s", renameHelp)
		return 2
	}
	name, err := shellstate.NormalizeName(positional[0])
	if err != nil {
		cliErrln(stderr, err)
		return 2
	}
	identity, err := currentShellIdentity(context.Background())
	if err != nil {
		cliErrln(stderr, err)
		return 1
	}
	result, err := shellstate.RenameCurrent(config.StateDir(), shellstate.RenameRequest{TmuxName: identity.session, Namespace: identity.socket, Name: name})
	if err != nil {
		cliErrln(stderr, err)
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
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			cliErrln(stderr, err)
			return 1
		}
	} else if result.Changed {
		if _, err := fmt.Fprintf(stdout, "Renamed current Sidecar shell %q to %q.\n", result.OldName, result.Name); err != nil {
			return 1
		}
	} else {
		if _, err := fmt.Fprintf(stdout, "Current Sidecar shell is already named %q.\n", result.Name); err != nil {
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

type shellIdentity struct{ session, socket string }

func currentShellIdentity(ctx context.Context) (shellIdentity, error) {
	if os.Getenv("TMUX") == "" {
		return shellIdentity{}, fmt.Errorf("not inside tmux; run this command from a Sidecar project shell")
	}
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return shellIdentity{}, fmt.Errorf("tmux did not identify the calling pane; run this command directly from a Sidecar project shell")
	}
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", pane, "#{session_name}\t#{socket_path}").Output()
	if err != nil {
		return shellIdentity{}, fmt.Errorf("identify current tmux shell: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return shellIdentity{}, fmt.Errorf("tmux returned an incomplete current-shell identity")
	}
	if !strings.HasPrefix(parts[0], "sidecar-sh-") {
		return shellIdentity{}, fmt.Errorf("current tmux session is not a Sidecar project shell")
	}
	socket := filepath.Clean(parts[1])
	if resolved, resolveErr := filepath.EvalSymlinks(socket); resolveErr == nil {
		socket = filepath.Clean(resolved)
	}
	return shellIdentity{session: parts[0], socket: socket}, nil
}

func isHelp(arg string) bool { return arg == "-h" || arg == "--help" || arg == "help" }

const shellHelp = `Usage: sidecar shell <command>

Manage the current Sidecar project shell.

Commands:
  rename    Rename the current shell's display name

Run "sidecar shell rename --help" for command details.
`

const renameHelp = `Usage: sidecar shell rename [--json] <display-name>

Rename only the Sidecar project shell containing this command. This changes
Sidecar's display name; it does not rename the tmux session.

The current display name is also published as $SIDECAR_SHELL_NAME. A name
like "Shell 3" is Sidecar's unset default.

Example:
  sidecar shell rename "shell rename implementation"

Options:
  --json    Write one structured result object to stdout
  -h, --help
            Show this help

Exit codes: 0 success, 1 identity or state failure, 2 usage or validation error.
`
