package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// shellDeleteResult is `sidecar shell delete --json`.
//
// Every field is stated rather than trimmed to the minimum, because this
// result crosses a host boundary: internal/hosts decides whether a decoded
// object IS this verb's answer, and a type with one field is a type almost any
// JSON object satisfies. See ValidRemoteResult below.
type shellDeleteResult struct {
	Shell   string `json:"shell"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status"`
	Deleted bool   `json:"deleted"`
}

// ValidRemoteResult states which fields make a decoded object this verb's
// answer (internal/hosts.ResultValidator).
//
// Not decoration. A host whose login profile writes JSON log lines to stdout
// once had `{"level":"info","msg":"loading nvm"}` accepted as a result: Go
// ignores unknown fields and tolerates missing ones, so the decode succeeded
// with a nil error and an all-zero value, and the surface rendered a blank
// confirmation over a mutation that really ran. The session and the status are
// what `shell delete` always writes and what a log line never carries together.
func (r shellDeleteResult) ValidRemoteResult() bool {
	return strings.TrimSpace(r.Shell) != "" && strings.TrimSpace(r.Status) != ""
}

const shellStatusDeleted = "deleted"

// runShellDelete closes a Sidecar-managed shell and forgets its record.
//
// This is the verb the Sessions browser's Delete runs on a host, and it is
// deliberately the same function that surface calls locally
// (workspaceops.DeleteManagedShell) rather than a second implementation of
// "close it and tombstone it". A remote delete and a local delete are then one
// behaviour observed from two places, which is the only way they cannot drift.
//
// The target is resolved through resolveShellTarget first, so a session name
// this project does not own is refused (exit 3) rather than killed: tmux
// resolves `-t <name>` against whatever answers to it, and a kill is the least
// recoverable thing this CLI does.
func runShellDelete(env Env, args []string) int {
	deleteCmd := RootCommand().FindSubcommand("shell").FindSubcommand("delete")
	help := RenderHelp(deleteCmd)

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
			// Everything after `--` is a value, not a flag. This verb takes no
			// positionals, so the terminator only changes which message a caller
			// gets — but the parsers of the shell verbs agreeing about what ends
			// flag parsing is worth more than the saved four lines, and
			// asksForHelp reads arguments the same way when it decides whether
			// the isolation gate arms.
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
		cliErrf(env.Stderr, "shell delete takes no positional arguments; name the session with --target\n\n%s", help)
		return 2
	}
	// --target is required, and there is deliberately no current-shell form.
	// `shell rename` has one because renaming the shell you are sitting in is
	// the thing agents do all day; deleting it would kill the tmux session
	// running this very command, so the result could never be read and a
	// mistyped invocation would take the caller with it.
	if target == "" {
		cliErrf(env.Stderr, "shell delete requires --target\n\n%s", help)
		return 2
	}

	tgt, code := resolveShellTarget(env, target, shellFlag, projectFlag, help)
	if code != 0 {
		return code
	}
	// A worktree resolves through the same --target vocabulary — `shell rename`
	// renames one — but deleting a worktree is a different operation with
	// different consequences: it removes a checkout and carries branch-cleanup
	// decisions this verb has no way to express. Refuse rather than reinterpret.
	// The Sessions browser refuses remote worktree delete for the same reason;
	// this is that refusal on the host side, so the two cannot disagree.
	if tgt.Kind != shellTargetKindShell {
		cliErrf(env.Stderr,
			"%q is a worktree session, and shell delete only removes managed shells; "+
				"delete the worktree from the project that owns it\n", tgt.Session)
		return exitInputRejected
	}
	// The ownership proof and the kill must land on the same tmux server, for
	// the reason `shell send` refuses across servers and more sharply: a record
	// proved present on socket A says nothing about what answers to that session
	// name on socket B, and this verb's mistake is a killed session rather than
	// a stray keystroke. `sidecar shell forget` is the record-only operation for
	// a shell whose server is elsewhere.
	if !sameTmuxServer(tgt.Namespace) {
		cliErrf(env.Stderr,
			"shell %q in project %q is recorded on tmux server %s, but this process talks to %s; "+
				"run `sidecar shell delete` from that server, or `sidecar shell forget` to drop the record alone\n",
			tgt.Session, tgt.Project.Key, tgt.Namespace, tmuxenv.Namespace())
		return shellTargetUnregistered
	}

	if err := workspaceops.DeleteManagedShell(tgt.Project.Path, tgt.Session, tgt.Namespace); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}

	if jsonOutput {
		result := shellDeleteResult{
			Shell:   tgt.Session,
			Name:    tgt.DisplayName,
			Status:  shellStatusDeleted,
			Deleted: true,
		}
		if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		return 0
	}
	if _, err := fmt.Fprintf(env.Stdout, "Deleted Sidecar shell %q (%s).\n", tgt.DisplayName, tgt.Session); err != nil {
		return 1
	}
	return 0
}
