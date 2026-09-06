package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/marcus/sidecar/internal/agentbroadcast"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/managedtarget"
	"github.com/marcus/sidecar/internal/shellstate"
)

const broadcastStdinCap = 64 * 1024

func agentBroadcastCommand() *Command {
	flags := []Flag{
		{Name: "--project", Arg: "NAME", Summary: "Scope to one project (slug, basename, or path; or a worktree it created, by path or basename)"},
		{Name: "--all", Summary: "Scope to every registered project on this machine", Bool: true},
		{Name: "--to", Arg: "TARGET", Summary: "Add an explicit recipient (repeatable); alone, this is the whole set"},
		{Name: "--exclude", Arg: "TARGET", Summary: "Remove a discovered recipient (repeatable)"},
		{Name: "--status", Arg: "STATUS", Summary: "Narrow discovery to these states (repeatable; default idle, done, working)"},
		{Name: "--include-self", Summary: "Do not drop the calling shell", Bool: true},
		{Name: "--raw", Summary: "Deliver the text exactly as given, without the envelope", Bool: true},
		{Name: "--dry-run", Summary: "Print the plan and send nothing", Bool: true},
		{Name: "--json", Summary: "Write stable structured JSON", Bool: true},
		{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
	}
	return &Command{
		Name:    "broadcast",
		Summary: "Send one prompt to every live agent in scope",
		Usage:   "sidecar agent broadcast TEXT [--project NAME | --all] [--to TARGET ...] [--exclude TARGET ...] [--status STATUS ...] [--include-self] [--raw] [--dry-run] [--json]",
		Long: "Recipients are the live agents in the caller's project, minus the caller.\n" +
			"Outside a managed shell, --project NAME or --all is required. --to TARGET\n" +
			"alone is the whole set; with a scope flag it adds. Each recipient still\n" +
			"has to pass the same promptable check agent prompt uses: a pane with no\n" +
			"identified provider is never a recipient, and nothing is started.\n\n" +
			"TEXT is a positional argument or - for stdin. Delivered text is prefixed\n" +
			"with [Sidecar broadcast from \"<shell>\" in <project>] unless --raw.\n\n" +
			"There is no --wait: use agent wait per target if the recipients need to\n" +
			"settle. Receipts, not acknowledgements — each row is submitted, skipped,\n" +
			"or unknown. --host is not accepted; remote hosts are not in this slice.\n\n" +
			"--dry-run prints the plan (would_send / skipped) and sends nothing, and\n" +
			"exits 0 even when the plan is empty.",
		Flags:     flags,
		Args:      ArgSpec{Min: 1, Max: 1, Description: "Message text, or - to read stdin"},
		ExitCodes: agentExitCodes(),
		Examples: []Example{
			{Command: `sidecar agent broadcast "Code freeze on main; hold pushes." --json`, Description: "the caller's project, minus the caller"},
			{Command: `sidecar agent broadcast "stop" --all --dry-run --json`, Description: "every live agent on this machine, send nothing"},
		},
		Agent:   AgentDoc{Invocation: "sidecar agent broadcast TEXT [--json]", Summary: "Send one prompt to every live agent in the project, or every live agent Sidecar can see"},
		Mutates: true,
		Run:     runAgentBroadcast,
	}
}

type broadcastFlags struct {
	json, all, includeSelf, raw, dryRun bool
	project                             string
	to, exclude                         []string
	status                              []agentcontrol.Status
	positional                          []string
}

func parseBroadcastArgs(env Env, args []string, help string) (broadcastFlags, int) {
	var f broadcastFlags
	usage := func(format string, a ...any) int {
		cliErrf(env.Stderr, format+"\n\n%s", append(a, help)...)
		return 2
	}
	value := func(arg, name string, i int) (string, int, bool) {
		v, n, ok := takeFlagArg(arg, args, i, name)
		if !ok || v == "" {
			return "", i, false
		}
		return v, n, true
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, _, _ := strings.Cut(arg, "=")
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return f, 0
		case arg == "--json":
			f.json = true
		case arg == "--all":
			f.all = true
		case arg == "--include-self":
			f.includeSelf = true
		case arg == "--raw":
			f.raw = true
		case arg == "--dry-run":
			f.dryRun = true
		case name == "--project":
			v, n, ok := value(arg, "--project", i)
			if !ok {
				return f, usage("--project requires a value")
			}
			f.project, i = v, n
		case name == "--host":
			return f, usage("agent broadcast does not accept --host; remote hosts are not in this slice")
		case name == "--to":
			v, n, ok := value(arg, "--to", i)
			if !ok {
				return f, usage("--to requires a value")
			}
			f.to = append(f.to, v)
			i = n
		case name == "--exclude":
			v, n, ok := value(arg, "--exclude", i)
			if !ok {
				return f, usage("--exclude requires a value")
			}
			f.exclude = append(f.exclude, v)
			i = n
		case name == "--status":
			v, n, ok := value(arg, "--status", i)
			if !ok {
				return f, usage("--status requires a value")
			}
			status, err := agentcontrol.ParseStatus(v)
			if err != nil {
				return f, usage("%v", err)
			}
			f.status = append(f.status, status)
			i = n
		default:
			if strings.HasPrefix(arg, "-") && arg != "-" {
				return f, usage("unknown option %q", arg)
			}
			f.positional = append(f.positional, arg)
		}
	}
	return f, -1
}

func runAgentBroadcast(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("broadcast")
	help := RenderHelp(cmd)
	f, code := parseBroadcastArgs(env, args, help)
	if code >= 0 {
		return code
	}
	if f.all && f.project != "" {
		cliErrf(env.Stderr, "--project and --all cannot be combined\n\n%s", help)
		return 2
	}
	if len(f.positional) != 1 {
		cliErrf(env.Stderr, "agent broadcast takes TEXT (or - for stdin)\n\n%s", help)
		return 2
	}
	text, err := broadcastText(env, f.positional[0])
	if err != nil {
		cliErrf(env.Stderr, "%v\n\n%s", err, help)
		return 2
	}
	if strings.TrimSpace(text) == "" {
		cliErrf(env.Stderr, "agent broadcast TEXT must not be empty\n\n%s", help)
		return 2
	}

	inShell := strings.TrimSpace(os.Getenv(shellstate.SessionEnv)) != ""
	if !inShell && !f.all && f.project == "" && len(f.to) == 0 {
		cliErrf(env.Stderr, "agent broadcast requires --project NAME or --all outside a managed shell\n\n%s", help)
		return 2
	}

	if code = requireAgentControl(env, f.json); code >= 0 {
		return code
	}

	req := agentbroadcast.PlanRequest{
		To:          f.to,
		Exclude:     f.exclude,
		Status:      f.status,
		IncludeSelf: f.includeSelf,
		Raw:         f.raw,
	}
	if f.all {
		req.ScopeKind = agentbroadcast.ScopeAll
	} else if f.project != "" {
		projects, scanCode, scanErr := scanProjects(env, "", f.project, false)
		if scanErr != nil {
			return emitBroadcastError(env, f.json, scanCode, scanErr)
		}
		if len(projects) == 0 {
			return emitAgentError(env, f.json, &agentcontrol.Error{Code: agentcontrol.ErrNotFound, Message: "no Sidecar project named " + f.project})
		}
		req.ScopeKind = agentbroadcast.ScopeProject
		req.Project = projects[0].Key
	}

	req.SenderSession = strings.TrimSpace(os.Getenv(shellstate.SessionEnv))
	if origin, ok := callerShellOrigin(env.StateDir); ok {
		req.SenderName = origin.DisplayName
		if req.SenderName == "" {
			req.SenderName = origin.TmuxName
		}
		req.SenderProject = origin.ProjectKey
	} else {
		// No identified calling shell: the envelope is from the user, not an
		// invented "cli" / "local" sender.
		req.FromUser = true
	}
	if req.ScopeKind == "" && len(req.To) == 0 {
		if req.SenderProject != "" {
			req.ScopeKind = agentbroadcast.ScopeProject
			req.Project = req.SenderProject
		} else {
			cliErrf(env.Stderr, "agent broadcast requires --project NAME or --all outside a managed shell\n\n%s", help)
			return 2
		}
	}

	svc := agentbroadcast.Service{
		Control:    agentcontrol.Service{Terminal: newAgentTerminal()},
		Candidates: broadcastCandidates(env),
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	plan, err := svc.Plan(ctx, req)
	if err != nil {
		return emitBroadcastError(env, f.json, 0, err)
	}
	if !f.dryRun && len(plan.Recipients) == 0 {
		return emitAgentError(env, f.json, &agentcontrol.Error{Code: "no_recipients", Message: "no live agents in scope"})
	}

	if f.dryRun {
		return emitBroadcastResult(env, f.json, broadcastResultFromPlan(plan, text), true)
	}
	result, err := svc.Send(ctx, plan, text)
	if err != nil {
		return emitBroadcastError(env, f.json, 0, err)
	}
	return emitBroadcastResult(env, f.json, result, false)
}

// broadcastCandidates is a CLI-local closure over scanProjects and
// managedTargetCandidates, the same universe agent list uses. Extracting those
// into a package the TUI can import is S2's job — they are bound to CLI types
// (registeredProject, matchProject, worktree claims) and the move is not
// mechanical.
func broadcastCandidates(env Env) func(context.Context, agentbroadcast.PlanRequest) ([]managedtarget.Target, error) {
	return func(_ context.Context, req agentbroadcast.PlanRequest) ([]managedtarget.Target, error) {
		projectFlag := ""
		globalExplicit := true
		if req.ScopeKind == agentbroadcast.ScopeProject && len(req.To) == 0 {
			projectFlag = req.Project
			globalExplicit = false
		}
		projects, _, err := scanProjects(env, "", projectFlag, globalExplicit)
		if err != nil {
			return nil, err
		}
		return managedTargetCandidates(env, projects)
	}
}

func broadcastText(env Env, positional string) (string, error) {
	if positional != "-" {
		return positional, nil
	}
	if env.Stdin == nil {
		return "", errors.New("agent broadcast - read no text from stdin")
	}
	data, err := io.ReadAll(io.LimitReader(env.Stdin, broadcastStdinCap+1))
	if err != nil {
		return "", fmt.Errorf("agent broadcast - could not read stdin: %w", err)
	}
	if len(data) > broadcastStdinCap {
		return "", fmt.Errorf("agent broadcast text is larger than the %d byte cap", broadcastStdinCap)
	}
	return strings.TrimSpace(string(data)), nil
}

func broadcastResultFromPlan(plan agentbroadcast.Plan, text string) agentbroadcast.Result {
	delivered := text
	if !plan.Raw {
		delivered = agentbroadcast.Envelope(broadcastEnvelopeSender(plan), text)
	}
	sum := agentbroadcast.Summary{ShellsWithoutAgent: plan.ShellsWithoutAgent}
	for _, row := range plan.Recipients {
		switch row.Outcome {
		case agentbroadcast.OutcomeWouldSend:
			// Dry-run does not submit; submitted stays 0.
		case agentbroadcast.OutcomeUnknown:
			sum.Unknown++
		default:
			sum.Skipped++
		}
	}
	return agentbroadcast.Result{
		Text:       delivered,
		Scope:      plan.Scope,
		Recipients: plan.Recipients,
		Summary:    sum,
	}
}

func broadcastEnvelopeSender(plan agentbroadcast.Plan) string {
	if plan.FromUser {
		return "the user"
	}
	return fmt.Sprintf("%q in %s", plan.SenderName, plan.SenderProject)
}

func emitBroadcastResult(env Env, jsonOutput bool, result agentbroadcast.Result, dryRun bool) int {
	if jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
	} else {
		for _, row := range result.Recipients {
			_, _ = fmt.Fprintln(env.Stdout, formatBroadcastRow(row))
		}
		_, _ = fmt.Fprintln(env.Stdout, formatBroadcastSummary(result, dryRun))
	}
	if dryRun {
		return 0
	}
	if result.Summary.Submitted > 0 {
		return 0
	}
	if result.Summary.Unknown > 0 {
		return 1
	}
	return 5
}

func formatBroadcastRow(row agentbroadcast.Recipient) string {
	name := row.Target.Name
	if name == "" {
		name = row.Target.Session
	}
	line := fmt.Sprintf("%-20s %-8s %-8s %s", name, row.Agent.Kind, row.Agent.Status, row.Outcome)
	if row.Reason != nil {
		line += "   " + row.Reason.Code
		if row.Reason.Message != "" {
			line += ": " + row.Reason.Message
		}
	}
	return line
}

func formatBroadcastSummary(result agentbroadcast.Result, dryRun bool) string {
	wouldSend := 0
	if dryRun {
		for _, row := range result.Recipients {
			if row.Outcome == agentbroadcast.OutcomeWouldSend {
				wouldSend++
			}
		}
	}
	var parts []string
	if dryRun {
		parts = append(parts, fmt.Sprintf("%d would_send", wouldSend))
	} else {
		parts = append(parts, fmt.Sprintf("%d submitted", result.Summary.Submitted))
	}
	parts = append(parts, fmt.Sprintf("%d skipped", result.Summary.Skipped))
	if result.Summary.Unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d unknown", result.Summary.Unknown))
	}
	line := strings.Join(parts, ", ") + "."
	if result.Summary.ShellsWithoutAgent > 0 {
		n := result.Summary.ShellsWithoutAgent
		noun := "shells"
		if n == 1 {
			noun = "shell"
		}
		line += fmt.Sprintf(" %d managed %s had no live agent.", n, noun)
	}
	return line
}

func emitBroadcastError(env Env, jsonOutput bool, scanCode int, err error) int {
	var typed *agentcontrol.Error
	if agentcontrol.AsError(err, &typed) {
		return emitAgentError(env, jsonOutput, err)
	}
	var me *managedtarget.Error
	if errors.As(err, &me) {
		code := agentcontrol.ErrNotFound
		if me.Kind == managedtarget.Ambiguous {
			code = agentcontrol.ErrTransport
		}
		return emitAgentError(env, jsonOutput, &agentcontrol.Error{Code: code, Message: err.Error(), Err: err})
	}
	if scanCode == 1 {
		return emitAgentError(env, jsonOutput, err)
	}
	return emitAgentError(env, jsonOutput, &agentcontrol.Error{Code: agentcontrol.ErrNotFound, Message: err.Error(), Err: err})
}
