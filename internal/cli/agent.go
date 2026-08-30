package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/managedtarget"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/workspaceops"
)

var newAgentTerminal = func() agentcontrol.Terminal { return agentcontrol.NewLocalTerminal() }

func agentCommand() *Command {
	common := []Flag{{Name: "--project", Arg: "NAME", Summary: "Target project (slug, basename, or path)"}, {Name: "--shell", Arg: "NAME", Summary: "Resolve the project from a registered shell"}, {Name: "--json", Summary: "Write stable structured JSON", Bool: true}, {Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}}
	list := &Command{Name: "list", Summary: "List live managed agents", Usage: "sidecar agent list [--project NAME] [--json]", Flags: common, ExitCodes: agentExitCodes(), Examples: []Example{{Command: "sidecar agent list --json"}}, Agent: AgentDoc{Invocation: "sidecar agent list --json", Summary: "List live managed agents and their current status"}, Run: runAgentList}
	get := &Command{Name: "get", Summary: "Get one managed agent", Usage: "sidecar agent get [TARGET] [--project NAME] [--json]", Long: "TARGET is a managed tmux session name or unique display name. Inside a managed shell it may be omitted.", Flags: common, Args: ArgSpec{Min: 0, Max: 1}, ExitCodes: agentExitCodes(), Examples: []Example{{Command: "sidecar agent get reviewer --json"}}, Agent: AgentDoc{Invocation: "sidecar agent get [TARGET] --json", Summary: "Read one managed agent's provider and lifecycle state"}, Run: runAgentGet}
	startFlags := append([]Flag{}, common...)
	startFlags = append(startFlags, Flag{Name: "--kind", Arg: "KIND", Summary: "Catalog provider kind (required)"}, Flag{Name: "--timeout", Arg: "DURATION", Summary: "Bound the readiness wait (default 30s)"})
	start := &Command{Name: "start", Summary: "Start a provider in an idle managed shell and wait for readiness", Usage: "sidecar agent start [TARGET] --kind KIND [--timeout DURATION] [-- AGENT_ARG ...]", Long: "Refuses commands, editors, copy mode, agents, ambiguous panes, and replacement processes. Provider arguments remain structured until the final shell boundary.", Flags: startFlags, Args: ArgSpec{Min: 0, Max: -1}, ExitCodes: agentExitCodes(), Examples: []Example{{Command: "sidecar agent start reviewer --kind codex --timeout 30s"}}, Agent: AgentDoc{Invocation: "sidecar agent start [TARGET] --kind KIND", Summary: "Start a known provider in a shell and return only when it is ready"}, Mutates: true, Run: runAgentStart}
	return &Command{Name: "agent", Summary: "Inspect and start agents in Sidecar-managed shells", Usage: "sidecar agent <command>", Long: "Provider-aware control over shells Sidecar owns. The feature is discoverable while disabled; enable agent_control to run it.", Sub: []*Command{get, list, start}, Run: runAgentRoot}
}

func agentExitCodes() []ExitCode {
	return []ExitCode{{Code: 0, Summary: "success"}, {Code: 1, Summary: "transport, timeout, or internal failure"}, {Code: 2, Summary: "usage error or version skew"}, {Code: 3, Summary: "target is not registered"}, {Code: 5, Summary: "feature disabled or semantic/value refusal"}}
}

func runAgentRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	if sub := cmd.FindSubcommand(args[0]); sub != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown agent command %q\n\n%s", args[0], RenderHelp(cmd))
	return 2
}

type agentFlags struct {
	json           bool
	project, shell string
	positional     []string
}

func parseAgentCommon(env Env, args []string, help string) (agentFlags, int) {
	var f agentFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return f, 0
		case arg == "--json":
			f.json = true
		case arg == "--project" || strings.HasPrefix(arg, "--project="):
			v, n, ok := takeFlagArg(arg, args, i, "--project")
			if !ok || v == "" {
				cliErrf(env.Stderr, "--project requires a value\n\n%s", help)
				return f, 2
			}
			f.project = v
			i = n
		case arg == "--shell" || strings.HasPrefix(arg, "--shell="):
			v, n, ok := takeFlagArg(arg, args, i, "--shell")
			if !ok || v == "" {
				cliErrf(env.Stderr, "--shell requires a value\n\n%s", help)
				return f, 2
			}
			f.shell = v
			i = n
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
				return f, 2
			}
			f.positional = append(f.positional, arg)
		}
	}
	return f, -1
}

func requireAgentControl(env Env, jsonOutput bool) int {
	if enabled, ok := env.FeatureOverrides[features.AgentControl.Name]; ok && enabled {
		return -1
	}
	if enabled, ok := env.FeatureOverrides[features.AgentControl.Name]; ok && !enabled {
		return emitAgentError(env, jsonOutput, &agentcontrol.Error{Code: agentcontrol.ErrFeatureDisabled, Message: "agent control is disabled"})
	}
	cfg, err := config.Load()
	if err != nil {
		return emitAgentError(env, jsonOutput, err)
	}
	if !cfg.Features.Flags[features.AgentControl.Name] {
		return emitAgentError(env, jsonOutput, &agentcontrol.Error{Code: agentcontrol.ErrFeatureDisabled, Message: "agent control is disabled; enable the agent_control feature"})
	}
	return -1
}

func runAgentList(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("list")
	help := RenderHelp(cmd)
	f, code := parseAgentCommon(env, args, help)
	if code >= 0 {
		return code
	}
	if len(f.positional) > 0 {
		cliErrf(env.Stderr, "agent list takes no positional arguments\n\n%s", help)
		return 2
	}
	if code = requireAgentControl(env, f.json); code >= 0 {
		return code
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var projects []registeredProject
	if f.shell != "" || f.project != "" {
		dest, err := resolveCreateDestination(ctx, env.StateDir, f.shell, f.project, resolveProjectOnly)
		if err != nil {
			return emitAgentError(env, f.json, &agentcontrol.Error{Code: agentcontrol.ErrNotFound, Message: err.Error(), Err: err})
		}
		proj, err := registeredProjectForCreate(env.StateDir, dest)
		if err != nil {
			return emitAgentError(env, f.json, &agentcontrol.Error{Code: agentcontrol.ErrNotFound, Message: err.Error(), Err: err})
		}
		projects = []registeredProject{proj}
	}
	if len(projects) == 0 {
		var err error
		projects, err = loadRegisteredProjects(env.StateDir)
		if err != nil {
			return emitAgentError(env, f.json, err)
		}
	}
	targets, err := managedTargetCandidates(env, projects)
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	svc := agentcontrol.Service{Terminal: newAgentTerminal()}
	agents := make([]agentcontrol.Agent, 0, len(targets))
	for _, target := range targets {
		a, e := svc.Get(ctx, targetFromManaged(target))
		if e == nil && a.Agent.Kind != "" {
			agents = append(agents, a)
		}
	}
	if f.json {
		return writeAgentJSON(env, map[string]any{"agents": agents})
	}
	if len(agents) == 0 {
		_, _ = fmt.Fprintln(env.Stdout, "No live managed agents.")
		return 0
	}
	for _, a := range agents {
		_, _ = fmt.Fprintf(env.Stdout, "%-20s %-10s %s\n", a.Target.Name, a.Agent.Kind, a.Agent.Status)
	}
	return 0
}

func runAgentGet(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("get")
	help := RenderHelp(cmd)
	f, code := parseAgentCommon(env, args, help)
	if code >= 0 {
		return code
	}
	if len(f.positional) > 1 {
		cliErrf(env.Stderr, "agent get accepts at most one target\n\n%s", help)
		return 2
	}
	if code = requireAgentControl(env, f.json); code >= 0 {
		return code
	}
	target := ""
	if len(f.positional) == 1 {
		target = f.positional[0]
	} else {
		target = os.Getenv(shellstate.SessionEnv)
	}
	if target == "" {
		return emitAgentError(env, f.json, &agentcontrol.Error{Code: agentcontrol.ErrNotFound, Message: "target is required outside a managed shell"})
	}
	tgt, code := resolveAgentShellTarget(env, target, f.shell, f.project, len(f.positional) == 1 && f.shell == "" && f.project == "", f.json)
	if code != 0 {
		return code
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a, err := (agentcontrol.Service{Terminal: newAgentTerminal()}).Get(ctx, targetFromShell(tgt))
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitAgent(env, f.json, a)
}

func runAgentStart(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("start")
	help := RenderHelp(cmd)
	f := agentFlags{}
	kind := ""
	timeout := 30 * time.Second
	var extra []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			extra = append(extra, args[i+1:]...)
			break
		}
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			f.json = true
		case arg == "--kind" || strings.HasPrefix(arg, "--kind="):
			v, n, ok := takeFlagArg(arg, args, i, "--kind")
			if !ok || v == "" {
				cliErrf(env.Stderr, "--kind requires a value\n\n%s", help)
				return 2
			}
			kind = v
			i = n
		case arg == "--timeout" || strings.HasPrefix(arg, "--timeout="):
			v, n, ok := takeFlagArg(arg, args, i, "--timeout")
			if !ok {
				return 2
			}
			d, e := time.ParseDuration(v)
			if e != nil || d <= 0 {
				cliErrf(env.Stderr, "invalid --timeout %q\n\n%s", v, help)
				return 2
			}
			timeout = d
			i = n
		case arg == "--project" || strings.HasPrefix(arg, "--project="):
			v, n, ok := takeFlagArg(arg, args, i, "--project")
			if !ok {
				return 2
			}
			f.project = v
			i = n
		case arg == "--shell" || strings.HasPrefix(arg, "--shell="):
			v, n, ok := takeFlagArg(arg, args, i, "--shell")
			if !ok {
				return 2
			}
			f.shell = v
			i = n
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
				return 2
			}
			f.positional = append(f.positional, arg)
		}
	}
	if len(f.positional) > 1 || kind == "" {
		cliErrf(env.Stderr, "agent start requires --kind and at most one target\n\n%s", help)
		return 2
	}
	if code := requireAgentControl(env, f.json); code >= 0 {
		return code
	}
	target := ""
	if len(f.positional) == 1 {
		target = f.positional[0]
	} else {
		target = os.Getenv(shellstate.SessionEnv)
	}
	if target == "" {
		return emitAgentError(env, f.json, &agentcontrol.Error{Code: agentcontrol.ErrNotFound, Message: "target is required outside a managed shell"})
	}
	argv, err := agentcatalog.BuildLaunch(kind, extra, false)
	if err != nil {
		return emitAgentError(env, f.json, &agentcontrol.Error{Code: agentcontrol.ErrNotReady, Message: err.Error(), Err: err})
	}
	tgt, code := resolveAgentShellTarget(env, target, f.shell, f.project, len(f.positional) == 1 && f.shell == "" && f.project == "", f.json)
	if code != 0 {
		return code
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a, err := (agentcontrol.Service{Terminal: newAgentTerminal()}).Start(ctx, agentcontrol.StartRequest{Target: targetFromShell(tgt), Kind: kind, Argv: argv, Timeout: timeout})
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitAgent(env, f.json, a)
}

func resolveAgentShellTarget(env Env, target, shellFlag, projectFlag string, globalExplicit, jsonOutput bool) (shellTarget, int) {
	tgt, code, err := findShellTarget(env, target, shellFlag, projectFlag, globalExplicit, tmuxenv.Namespace())
	if err == nil {
		return tgt, 0
	}
	typed := &agentcontrol.Error{Code: agentcontrol.ErrTransport, Message: err.Error(), Err: err}
	if code == shellTargetUnregistered || code == exitInputRejected {
		typed.Code = agentcontrol.ErrNotFound
	}
	return shellTarget{}, emitAgentError(env, jsonOutput, typed)
}

func targetFromManaged(t managedtarget.Target) agentcontrol.Target {
	return agentcontrol.Target{Host: t.Host, Project: t.Project, Session: t.Session, Name: t.Name, Namespace: t.Namespace}
}
func targetFromShell(t shellTarget) agentcontrol.Target {
	return agentcontrol.Target{Host: "local", Project: t.Project.Key, Session: t.Session, Name: t.DisplayName, Namespace: t.Namespace}
}

func startCreatedAgent(ctx context.Context, proj registeredProject, session, display, workDir, kind string, skipPerms bool) (agentcontrol.Agent, error) {
	cfg := loadCreateConfig()
	argv, _, err := workspaceops.ResolveAgentLaunchArgv(workDir, kind, cfg.Plugins.Workspace.AgentStart, skipPerms)
	if err != nil {
		return agentcontrol.Agent{}, &agentcontrol.Error{Code: agentcontrol.ErrNotReady, Message: err.Error(), Err: err}
	}
	target := agentcontrol.Target{Host: "local", Project: proj.Key, Session: session, Name: display}
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	svc := agentcontrol.Service{Terminal: newAgentTerminal()}
	ready, err := svc.WaitShellReady(readyCtx, target, 30*time.Second)
	if err != nil {
		return agentcontrol.Agent{}, err
	}
	return svc.Start(readyCtx, agentcontrol.StartRequest{Target: ready.Target, Kind: kind, Argv: argv, Timeout: 30 * time.Second})
}
func emitAgent(env Env, jsonOutput bool, a agentcontrol.Agent) int {
	if jsonOutput {
		return writeAgentJSON(env, a)
	}
	_, e := fmt.Fprintf(env.Stdout, "%s  %s  %s\n", a.Target.Name, a.Agent.Kind, a.Agent.Status)
	if e != nil {
		return 1
	}
	return 0
}
func writeAgentJSON(env Env, v any) int {
	if err := json.NewEncoder(env.Stdout).Encode(v); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}
func emitAgentError(env Env, jsonOutput bool, err error) int {
	var typed *agentcontrol.Error
	if !agentcontrol.AsError(err, &typed) {
		typed = &agentcontrol.Error{Code: agentcontrol.ErrTransport, Message: err.Error(), Err: err}
	}
	if jsonOutput {
		_, _ = env.Stderr.Write(append(agentcontrol.MarshalError(typed), '\n'))
	} else {
		cliErrln(env.Stderr, typed.Error())
	}
	switch typed.Code {
	case agentcontrol.ErrNotFound:
		return 3
	case agentcontrol.ErrTransport, agentcontrol.ErrTimeout:
		return 1
	default:
		return 5
	}
}
