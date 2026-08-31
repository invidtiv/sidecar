package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/sessionrestore"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// The cold-restore command group.
//
// `status` and `restore --dry-run` are read-only and work with no TUI running,
// which is the property that makes a restore reviewable: the plan can be read,
// argued with, and diffed before anything is created. `restore` then executes
// exactly the plan that was printed, through the same planner and executor the
// automatic startup restore uses.
//
// The one asymmetry worth naming is --yes. Under the default ask policy nothing
// resumes a conversation without a human saying so, and a CLI invocation has no
// TUI to ask through. Rather than silently downgrading to shells-only or
// silently resuming, an ask-policy resume requested without --yes is refused
// with an exit code and a message naming the flag.

const (
	// sessionExitPlanRefused reports that the request could not be carried out
	// as asked — a missing confirmation, an unusable policy value, or a target
	// that resolves to no managed shell.
	sessionExitPlanRefused = exitInputRejected
)

func sessionCommand() *Command {
	jsonFlag := Flag{Name: "--json", Summary: "Write the stable structured document to stdout", Bool: true}
	helpFlag := Flag{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}

	status := &Command{
		Name:    "status",
		Summary: "Report what a cold restore would do, without doing it",
		Usage:   "sidecar session status [--json]",
		Long: "Reads Sidecar's managed shell records and the current tmux inventory and prints the ordered restore plan.\n\n" +
			"Every managed shell is named as reattach, recreate-shell, resume-agent, manual, skip, or refuse, with the reason " +
			"and whether performing it would run an agent process. This command is read-only: it creates nothing, starts nothing, " +
			"and does not require a running Sidecar.",
		Flags:     []Flag{jsonFlag, helpFlag},
		Args:      ArgSpec{Min: 0, Max: 0},
		ExitCodes: sessionExitCodes(),
		Examples: []Example{
			{Description: "See whether the last tmux server was replaced and what is restorable", Command: "sidecar session status"},
			{Description: "Read the plan as JSON", Command: "sidecar session status --json"},
		},
		Agent: AgentDoc{
			Invocation: "sidecar session status --json",
			Summary:    "Read the cold-restore plan for this machine without changing anything",
		},
		Run: runSessionStatus,
	}

	restore := &Command{
		Name:    "restore",
		Summary: "Recreate managed shells, and optionally resume their exact conversations",
		Usage:   "sidecar session restore [--dry-run] [--shell TARGET] [--agents] [--yes] [--json]",
		Long: "Executes the plan `session status` prints.\n\n" +
			"Shells are recreated under their own tmux session names and existing working directories; no --run command, dev server, " +
			"or test watcher is ever replayed. A missing working directory is a refusal, never a fallback to another directory, and a " +
			"tmux session name held by something else is a refusal too — Sidecar never closes a live session to take its name.\n\n" +
			"Conversations are resumed only with --agents, only from an exact reference an official integration reported, and only when " +
			"the policy allows it. Under the default ask policy a non-interactive resume additionally requires --yes.\n\n" +
			"The tmux session name is the idempotency key, so running this twice does not produce two shells or two agents, and a run " +
			"interrupted at any point converges when it is run again. Nothing here ever deletes a shell record: a failure is reported " +
			"and left retryable.",
		Flags: []Flag{
			{Name: "--dry-run", Summary: "Print the plan and exit without creating or starting anything", Bool: true},
			{Name: "--shell", Arg: "TARGET", Summary: "Restore only this shell, by tmux session name or display name"},
			{Name: "--agents", Summary: "Also resume eligible exact agent conversations", Bool: true},
			{Name: "--yes", Summary: "Confirm agent resumes non-interactively when the policy is ask", Bool: true},
			jsonFlag,
			helpFlag,
		},
		Args:      ArgSpec{Min: 0, Max: 0},
		ExitCodes: sessionExitCodes(),
		Mutates:   true,
		Examples: []Example{
			{Description: "Recreate eligible shells, no agents", Command: "sidecar session restore"},
			{Description: "See exactly what would happen first", Command: "sidecar session restore --agents --dry-run"},
			{Description: "Recreate one shell and resume its conversation", Command: "sidecar session restore --shell reviewer --agents --yes"},
		},
		Agent: AgentDoc{
			Invocation: "sidecar session restore --dry-run --json",
			Summary:    "Rebuild shells lost to a tmux restart; --dry-run first, --agents to resume conversations",
		},
		Run: runSessionRestore,
	}

	policy := &Command{
		Name:    "policy",
		Summary: "Read or set one shell's cold-restore policy",
		Usage:   "sidecar session policy [TARGET] [--shell|--resume|--never|--inherit] [--json]",
		Long: "With no policy flag, reports the effective policy for the target shell.\n\n" +
			"  --inherit  follow the machine default (plugins.workspace.sessionRestore)\n" +
			"  --shell    recreate the terminal, but never resume its agent\n" +
			"  --resume   recreate the terminal and resume its exact conversation\n" +
			"  --never    leave this shell out of restore entirely\n\n" +
			"This is per shell, so a long-running server, a disposable helper, and a sensitive agent session can differ without " +
			"changing the machine default. Omitting TARGET inside a managed shell targets that shell.",
		Flags: []Flag{
			{Name: "--inherit", Summary: "Follow the machine default", Bool: true},
			{Name: "--shell", Summary: "Recreate the shell, never resume the agent", Bool: true},
			{Name: "--resume", Summary: "Recreate the shell and resume the exact conversation", Bool: true},
			{Name: "--never", Summary: "Never restore this shell", Bool: true},
			{Name: "--project", Arg: "NAME", Summary: "Target project (slug, basename, or path)"},
			jsonFlag,
			helpFlag,
		},
		Args:      ArgSpec{Min: 0, Max: 1, Description: "TARGET is a tmux session name or a unique display name"},
		ExitCodes: sessionExitCodes(),
		Mutates:   true,
		Examples: []Example{
			{Description: "Read this shell's policy", Command: "sidecar session policy"},
			{Description: "Never resume this agent automatically", Command: "sidecar session policy reviewer --shell"},
			{Description: "Always resume this one", Command: "sidecar session policy reviewer --resume"},
		},
		Agent: AgentDoc{
			Invocation: "sidecar session policy TARGET --shell",
			Summary:    "Decide per shell whether a cold restore recreates it and resumes its agent",
		},
		Run: runSessionPolicy,
	}

	return &Command{
		Name:    "session",
		Summary: "Inspect and perform cold restore of managed shells after a tmux restart",
		Usage:   "sidecar session <command>",
		Long: "A tmux server restart destroys every managed shell's processes; tmux exposes no way to hand a live PTY to a replacement " +
			"server, so Sidecar reconstructs rather than preserves. These commands read what is reconstructible, do the reconstruction, " +
			"and decide per shell how far it should go.\n\n" +
			"Shell records are never deleted by a restore, a restore failure, or a tmux server death. A shell that cannot be restored " +
			"stays as a visible row with a reason.",
		Sub: []*Command{policy, restore, status},
		Run: runSessionRoot,
	}
}

// writeSessionJSONError writes a structured refusal to stderr, keeping stdout
// reserved for the document a caller is parsing.
func writeSessionJSONError(env Env, code, message string) int {
	enc := json.NewEncoder(env.Stderr)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
	switch code {
	case "confirmation_required":
		return sessionExitPlanRefused
	default:
		return 1
	}
}

func sessionExitCodes() []ExitCode {
	return []ExitCode{
		{Code: 0, Summary: "Success"},
		{Code: 1, Summary: "Reading state or talking to tmux failed"},
		{Code: 2, Summary: "Usage error"},
		{Code: sessionExitPlanRefused, Summary: "The request was refused: confirmation is required, the policy value is unknown, or the target is not a managed shell"},
	}
}

func runSessionRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("session")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	if sub := cmd.FindSubcommand(args[0]); sub != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown session command %q\n\n%s", args[0], RenderHelp(cmd))
	return 2
}

// sessionConfig reads the machine's restore defaults.
func sessionConfig() sessionrestore.Config {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return sessionrestore.DefaultConfig()
	}
	restore := cfg.Plugins.Workspace.SessionRestore
	mode, err := sessionrestore.ParseResumeMode(restore.ResumeAgents)
	if err != nil {
		mode = sessionrestore.ResumeAsk
	}
	return sessionrestore.Config{RecreateShells: restore.RecreateShells, ResumeAgents: mode}
}

func sessionCollector(env Env) sessionrestore.Collector {
	return sessionrestore.Collector{StateDir: env.StateDir, Namespace: tmuxenv.Namespace()}
}

func sessionContext(env Env) context.Context {
	if env.Ctx != nil {
		return env.Ctx
	}
	return context.Background()
}

// planDocument is the stable JSON shape both status and restore emit.
type planDocument struct {
	ServerChanged bool                  `json:"serverChanged"`
	CurrentServer string                `json:"currentServer,omitempty"`
	PriorServers  []string              `json:"priorServers,omitempty"`
	ResumePolicy  string                `json:"resumePolicy"`
	RecreateShell bool                  `json:"recreateShells"`
	Steps         []sessionrestore.Step `json:"steps"`
}

func newPlanDocument(plan sessionrestore.Plan, cfg sessionrestore.Config) planDocument {
	return planDocument{
		ServerChanged: plan.ServerChanged,
		CurrentServer: plan.CurrentServer,
		PriorServers:  plan.PriorServers,
		ResumePolicy:  string(cfg.ResumeAgents),
		RecreateShell: cfg.RecreateShells,
		Steps:         plan.Steps,
	}
}

func runSessionStatus(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("session").FindSubcommand("status")
	help := RenderHelp(cmd)
	jsonOutput := false
	for _, arg := range args {
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		default:
			cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
			return 2
		}
	}

	cfg := sessionConfig()
	// status reports what the configured policy would do, so it asks for agents
	// without confirming them: the point is to disclose that a resume is
	// possible, not to authorize one.
	plan, code := buildSessionPlan(env, cfg, sessionrestore.Request{Agents: true}, jsonOutput)
	if code != 0 {
		return code
	}
	if jsonOutput {
		return writeJSON(env, newPlanDocument(plan, cfg))
	}
	writeSessionPlanHuman(env, plan, cfg, false)
	return 0
}

func buildSessionPlan(env Env, cfg sessionrestore.Config, req sessionrestore.Request, jsonOutput bool) (sessionrestore.Plan, int) {
	in, err := sessionCollector(env).Collect(sessionContext(env), cfg, req)
	if err != nil {
		if jsonOutput {
			return sessionrestore.Plan{}, writeSessionJSONError(env, "transport_failed", err.Error())
		}
		cliErrf(env.Stderr, "read restore state: %v", err)
		return sessionrestore.Plan{}, 1
	}
	return sessionrestore.Build(in), 0
}

func runSessionRestore(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("session").FindSubcommand("restore")
	help := RenderHelp(cmd)
	usage := func(format string, a ...any) int {
		cliErrf(env.Stderr, format+"\n\n%s", append(a, help)...)
		return 2
	}

	var (
		jsonOutput, dryRun, agents, yes bool
		shellTargetName                 string
	)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, _, _ := strings.Cut(arg, "=")
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--dry-run":
			dryRun = true
		case arg == "--agents":
			agents = true
		case arg == "--yes":
			yes = true
		case name == "--shell":
			value, next, ok := takeFlagArg(arg, args, i, "--shell")
			if !ok || strings.TrimSpace(value) == "" {
				return usage("--shell requires a value")
			}
			shellTargetName, i = value, next
		default:
			return usage("unknown option %q", arg)
		}
	}

	cfg := sessionConfig()
	req := sessionrestore.Request{OnlyShell: shellTargetName, Agents: agents, Confirmed: yes}
	plan, code := buildSessionPlan(env, cfg, req, jsonOutput)
	if code != 0 {
		return code
	}

	// A resume that only a confirmation is holding back, with no confirmation
	// available, is refused rather than quietly downgraded. Silently doing less
	// than asked is how a user ends up believing a conversation was resumed.
	if agents && !yes && !dryRun && len(plan.PendingConfirmation()) > 0 {
		if jsonOutput {
			return writeSessionJSONError(env, "confirmation_required",
				"the resumeAgents policy is ask and there is no TUI to confirm through; pass --yes to authorize these resumes")
		}
		cliErrf(env.Stderr,
			"%d conversation(s) need confirmation before they are resumed (resumeAgents is %q).\nRe-run with --yes to authorize them, or --dry-run to see the plan.",
			len(plan.PendingConfirmation()), cfg.ResumeAgents)
		return sessionExitPlanRefused
	}

	if dryRun {
		if jsonOutput {
			return writeJSON(env, newPlanDocument(plan, cfg))
		}
		writeSessionPlanHuman(env, plan, cfg, true)
		return 0
	}

	result := sessionrestore.Execute(sessionContext(env), plan, sessionRestoreDeps(env))
	if jsonOutput {
		return writeJSON(env, newResultDocument(result, cfg))
	}
	writeSessionResultHuman(env, result)
	if len(result.Failed()) > 0 {
		return 1
	}
	return 0
}

// sessionRestoreDeps binds the executor to the real machine.
func sessionRestoreDeps(env Env) sessionrestore.Deps {
	namespace := tmuxenv.Namespace()
	collector := sessionCollector(env)
	svc := agentcontrol.Service{Terminal: newAgentTerminal()}

	return sessionrestore.Deps{
		Live: func(session string) sessionrestore.LiveState {
			ctx := sessionContext(env)
			if !workspaceops.SessionExists(session) {
				return sessionrestore.LiveAbsent
			}
			if collector.ManagedSessionOrDefault(ctx, session) {
				return sessionrestore.LiveManaged
			}
			return sessionrestore.LiveForeign
		},
		CurrentServer: collector.ServerIDOrDefault,
		CreateShell: func(_ context.Context, step sessionrestore.Step) error {
			// CreateShell, not CreateManagedShell: the manifest record already
			// exists and is the thing being restored. Creating the record again
			// would fail, and creating it with a fresh CreatedAt would discard
			// the identity every other fence in the system is keyed on.
			_, err := workspaceops.CreateShell(workspaceops.ShellSpec{
				WorkDir:     step.WorkDir,
				SessionName: step.Session,
				DisplayName: step.Name,
			})
			return err
		},
		ResumePlanFor: func(step sessionrestore.Step) (agentsession.ResumePlan, error) {
			return sessionrestore.ResumePlanFor(step, namespace)
		},
		ResumeAgent: func(ctx context.Context, step sessionrestore.Step, plan agentsession.ResumePlan) error {
			target := agentcontrol.Target{
				Host:      "local",
				Project:   step.Project,
				Session:   step.Session,
				Name:      step.Name,
				Namespace: namespace,
			}
			ready, err := svc.WaitShellReady(ctx, target, 30*time.Second)
			if err != nil {
				return err
			}
			_, err = svc.StartResume(ctx, agentcontrol.ResumeRequest{
				Target:  ready.Target,
				Plan:    plan,
				Timeout: 60 * time.Second,
			})
			return err
		},
	}
}

type outcomeDocument struct {
	Project           string `json:"project"`
	Session           string `json:"session"`
	Name              string `json:"name,omitempty"`
	Status            string `json:"status"`
	Reason            string `json:"reason"`
	Detail            string `json:"detail"`
	ExternalExecution bool   `json:"externalExecution"`
}

type resultDocument struct {
	ResumePolicy string            `json:"resumePolicy"`
	Outcomes     []outcomeDocument `json:"outcomes"`
	Failed       int               `json:"failed"`
}

func newResultDocument(result sessionrestore.Result, cfg sessionrestore.Config) resultDocument {
	doc := resultDocument{ResumePolicy: string(cfg.ResumeAgents), Failed: len(result.Failed())}
	for _, o := range result.Outcomes {
		doc.Outcomes = append(doc.Outcomes, outcomeDocument{
			Project:           o.Step.Project,
			Session:           o.Step.Session,
			Name:              o.Step.Name,
			Status:            string(o.Status),
			Reason:            string(o.Reason),
			Detail:            o.Detail,
			ExternalExecution: o.Status == sessionrestore.StatusResumed,
		})
	}
	return doc
}

func writeSessionPlanHuman(env Env, plan sessionrestore.Plan, cfg sessionrestore.Config, dryRun bool) {
	out := env.Stdout
	switch {
	case plan.CurrentServer == "":
		_, _ = fmt.Fprintln(out, "tmux: no server is running")
	case plan.ServerChanged:
		_, _ = fmt.Fprintf(out, "tmux server %s (replaced; shells below were last alive in %s)\n",
			plan.CurrentServer, strings.Join(plan.PriorServers, ", "))
	default:
		_, _ = fmt.Fprintf(out, "tmux server %s (unchanged)\n", plan.CurrentServer)
	}
	if len(plan.Steps) == 0 {
		_, _ = fmt.Fprintln(out, "no managed shell records were found")
		return
	}

	_, _ = fmt.Fprintf(out, "recreateShells=%v resumeAgents=%s\n\n", cfg.RecreateShells, cfg.ResumeAgents)
	width := 0
	for _, s := range plan.Steps {
		if n := len(string(s.Action)); n > width {
			width = n
		}
	}
	for _, s := range plan.Steps {
		label := s.Name
		if label == "" {
			label = s.Session
		}
		_, _ = fmt.Fprintf(out, "  %-*s  %-24s  %s\n", width, s.Action, label, s.Detail)
		if s.Agent != nil && s.Agent.Reason != s.Reason {
			_, _ = fmt.Fprintf(out, "  %-*s  %-24s  agent %s: %s\n", width, "", "", s.Agent.Kind, agentReasonSentence(*s.Agent))
		}
	}

	_, _ = fmt.Fprintln(out)
	shells, resumes := 0, 0
	for _, s := range plan.Steps {
		if s.Action == sessionrestore.ActionRecreateShell || s.Action == sessionrestore.ActionResumeAgent {
			shells++
		}
		if s.Agent != nil && s.Agent.Resume {
			resumes++
		}
	}
	if dryRun {
		_, _ = fmt.Fprintln(out, "dry run: nothing was created, started, or written")
	}
	_, _ = fmt.Fprintf(out, "%d shell(s) would be recreated; %d conversation(s) would be resumed\n", shells, resumes)
	if pending := plan.PendingConfirmation(); len(pending) > 0 {
		_, _ = fmt.Fprintf(out, "%d conversation(s) await confirmation; add --agents --yes to authorize them\n", len(pending))
	}
}

func agentReasonSentence(a sessionrestore.AgentStep) string {
	switch a.Reason {
	case sessionrestore.ReasonNeedsConfirmation:
		return "can be resumed, needs confirmation"
	case sessionrestore.ReasonDuplicateRef:
		return "same conversation is claimed by " + a.ConflictWith + "; this one restores as a plain shell"
	case sessionrestore.ReasonProviderNoResume:
		return "this provider has no native resume"
	case sessionrestore.ReasonProviderUnavailable:
		return "the provider binary is not installed here"
	case sessionrestore.ReasonUnreportedRef:
		return "no official integration vouched for its session reference"
	case sessionrestore.ReasonNoSessionRef:
		return "no conversation is bound to this shell"
	case sessionrestore.ReasonResumeOff:
		return "resume is disabled by policy"
	case sessionrestore.ReasonAgentsNotRequested:
		return "not requested; pass --agents"
	case sessionrestore.ReasonPolicyResume:
		return "will be resumed"
	default:
		return string(a.Reason)
	}
}

func writeSessionResultHuman(env Env, result sessionrestore.Result) {
	out := env.Stdout
	for _, o := range result.Outcomes {
		if o.Status == sessionrestore.StatusSkipped {
			continue
		}
		label := o.Step.Name
		if label == "" {
			label = o.Step.Session
		}
		_, _ = fmt.Fprintf(out, "  %-11s  %-24s  %s\n", o.Status, label, o.Detail)
	}
	counts := result.Counts()
	keys := make([]string, 0, len(counts))
	for status := range counts {
		keys = append(keys, string(status))
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", key, counts[sessionrestore.Status(key)]))
	}
	_, _ = fmt.Fprintf(out, "\n%s\n", strings.Join(parts, ", "))
	if failed := result.Failed(); len(failed) > 0 {
		_, _ = fmt.Fprintf(out, "%d step(s) failed and can be retried; no shell record was removed\n", len(failed))
	}
}

func runSessionPolicy(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("session").FindSubcommand("policy")
	help := RenderHelp(cmd)
	usage := func(format string, a ...any) int {
		cliErrf(env.Stderr, format+"\n\n%s", append(a, help)...)
		return 2
	}

	var (
		jsonOutput  bool
		projectFlag string
		positional  []string
		chosen      agentsession.Policy
		chosenFlag  string
	)
	setPolicy := func(p agentsession.Policy, flag string) int {
		if chosenFlag != "" && chosenFlag != flag {
			return usage("%s and %s cannot both be given", chosenFlag, flag)
		}
		chosen, chosenFlag = p, flag
		return 0
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, _, _ := strings.Cut(arg, "=")
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--inherit":
			if code := setPolicy(agentsession.PolicyInherit, arg); code != 0 {
				return code
			}
		case arg == "--shell":
			if code := setPolicy(agentsession.PolicyShell, arg); code != 0 {
				return code
			}
		case arg == "--resume":
			if code := setPolicy(agentsession.PolicyResume, arg); code != 0 {
				return code
			}
		case arg == "--never":
			if code := setPolicy(agentsession.PolicyNever, arg); code != 0 {
				return code
			}
		case name == "--project":
			value, next, ok := takeFlagArg(arg, args, i, "--project")
			if !ok || strings.TrimSpace(value) == "" {
				return usage("--project requires a value")
			}
			projectFlag, i = value, next
		default:
			if strings.HasPrefix(arg, "-") {
				return usage("unknown option %q", arg)
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) > 1 {
		return usage("expected at most one TARGET")
	}
	target := ""
	if len(positional) == 1 {
		target = positional[0]
	}

	resolved, code := resolveShellTarget(env, target, "", projectFlag, help)
	if code != 0 {
		return code
	}
	id := shellstate.Identity{TmuxName: resolved.Session, Namespace: resolved.Namespace}

	effective := agentsession.PolicyInherit
	if chosenFlag == "" {
		defs, err := shellstate.ListAtPath(resolved.ManifestPath)
		if err != nil {
			cliErrf(env.Stderr, "read shell records: %v", err)
			return 1
		}
		for _, def := range defs {
			if def.TmuxName == resolved.Session {
				effective = shellstate.RestorePolicyOf(def)
				break
			}
		}
	} else {
		set, err := shellstate.SetRestorePolicyAtPath(resolved.ManifestPath, id, chosen)
		if err != nil {
			if shellstate.IsNotFound(err) {
				cliErrf(env.Stderr, "%v", err)
				return sessionExitPlanRefused
			}
			cliErrf(env.Stderr, "set restore policy: %v", err)
			return 1
		}
		effective = set
	}

	if jsonOutput {
		return writeJSON(env, map[string]any{
			"session": resolved.Session,
			"name":    resolved.DisplayName,
			"project": resolved.Project.Key,
			"policy":  string(effective),
			"default": string(sessionConfig().ResumeAgents),
		})
	}
	label := resolved.DisplayName
	if label == "" {
		label = resolved.Session
	}
	if chosenFlag == "" {
		_, _ = fmt.Fprintf(env.Stdout, "%s: restore policy %s\n", label, effective)
	} else {
		_, _ = fmt.Fprintf(env.Stdout, "%s: restore policy set to %s\n", label, effective)
	}
	return 0
}
