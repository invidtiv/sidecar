package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/workspaceops"
	"github.com/marcus/sidecar/internal/workspacewire"
)

func runShellStart(env Env, args []string) int { return runRecordedSessionStart(env, args, "shell") }
func runWorktreeStart(env Env, args []string) int {
	return runRecordedSessionStart(env, args, "worktree")
}

func runRecordedSessionStart(env Env, args []string, kind string) int {
	help := RenderHelp(RootCommand().FindSubcommand(kind).FindSubcommand("start"))
	flags, code := parseShellRecordArgs(args, help, env, 1)
	if code >= 0 {
		return code
	}
	project, _, code := resolveShellRecordsProject(env, flags, help, registerProject)
	if code != 0 {
		return code
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	svc := workspaceops.Service{}
	var result workspaceops.SessionStartResult
	var err error
	if kind == "shell" {
		result, err = svc.StartShell(ctx, project.Path, flags.positional[0], tmuxenv.Namespace())
	} else {
		result, err = svc.StartWorktree(ctx, env.StateDir, project.Path, flags.positional[0])
	}
	if err != nil {
		exit := 4
		if errors.Is(err, workspaceops.ErrSessionStartNotFound) {
			exit = 3
		}
		return emitWorktreeDeleteError(env, flags.jsonOutput, "refused", err.Error(), exit)
	}
	status := "started"
	if result.AlreadyRunning {
		status = "already_running"
	}
	doc := workspacewire.SessionStarted{Shell: workspacewire.ShellInfo{Session: result.Session, DisplayName: result.DisplayName, WorkDir: result.WorkDir}, Project: project.Key, Status: status}
	if flags.jsonOutput {
		return writeJSON(env, doc)
	}
	_, err = fmt.Fprintf(env.Stdout, "Terminal %q: %s.\n", result.DisplayName, status)
	if err != nil {
		return 1
	}
	return 0
}

func recordedSessionStartCommand(kind string, run func(Env, []string) int) *Command {
	target, description := "SESSION", "Exact managed shell identity; restores its terminal while retaining the record"
	if kind == "worktree" {
		target, description = "PATH", "Absolute existing linked worktree path; starts its session without recreating the checkout"
	}
	return &Command{
		Name:    "start",
		Summary: "Start an inactive " + kind + " terminal",
		Usage:   "sidecar " + kind + " start " + target + " [--project NAME] [--json]",
		Long:    description + ".\nA running terminal is returned unchanged. No previous agent conversation is resumed.\n",
		Flags: []Flag{
			{Name: "--project", Arg: "NAME", Summary: "Owning project key or path"},
			{Name: "--shell", Arg: "NAME", Summary: "Resolve the owning project from a registered shell"},
			{Name: "--json", Summary: "Write a structured session result", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		Args: ArgSpec{Min: 1, Max: 1, Description: target},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "started or already running"},
			{Code: 1, Summary: "project or state resolution failed"},
			{Code: 2, Summary: "usage error"},
			{Code: 3, Summary: "no matching record or worktree"},
			{Code: 4, Summary: "start refused or failed"},
			{Code: 5, Summary: "unknown project or shell"},
		},
		Examples: []Example{{Command: "sidecar " + kind + " start " + target + " --project sidecar --json", Description: "Start the selected terminal without creating a new identity"}},
		Agent:    AgentDoc{Invocation: "sidecar " + kind + " start " + target + " --project NAME --json", Summary: "Start a selected inactive " + kind + " terminal"},
		Mutates:  true,
		Run:      run,
	}
}
