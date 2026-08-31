package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/managedtarget"
	"github.com/marcus/sidecar/internal/shellstate"
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
	promptFlags := append([]Flag{}, common...)
	promptFlags = append(promptFlags,
		Flag{Name: "--wait", Summary: "Submit and wait for the agent to settle under one pinned target", Bool: true},
		Flag{Name: "--until", Arg: "STATUS", Summary: "Repeatable settled state: idle, done, blocked, or working (default idle, done, blocked)"},
		Flag{Name: "--timeout", Arg: "DURATION", Summary: "Required with --wait; there is no implicit timeout"})
	prompt := &Command{
		Name:    "prompt",
		Summary: "Send a prompt to a managed agent, optionally waiting for it to settle",
		Usage:   "sidecar agent prompt [TARGET] TEXT [--wait] [--until STATUS]... [--timeout DURATION] [--json]",
		Long: "With two positional arguments the first is the target and the second is the prompt.\n" +
			"With one, the prompt goes to the shell named by SIDECAR_SHELL — unless that one\n" +
			"argument names a managed target, which is read as a missing prompt rather than as\n" +
			"a prompt that happens to be a target's name. Empty text is a usage error too.\n\n" +
			"Nothing is written to a target that is blocked, unidentified, stale, dead, or\n" +
			"occupied by a replacement process. The text goes through the same ordered,\n" +
			"bracketed-paste-aware path the embedded terminal uses, and the submission key is\n" +
			"sent separately, so a headless prompt delivers exactly what typing it would.\n\n" +
			"A prompt sent from idle or done must produce an observed lifecycle change within " + agentcontrol.PromptStallWindow.String() + "\n" +
			"or the command reports agent_prompt_stalled. A prompt sent to an agent that is\n" +
			"already working makes no claim about which turn is which: completion of the turn\n" +
			"already in flight may satisfy --wait.",
		Flags:     promptFlags,
		Args:      ArgSpec{Min: 1, Max: 2},
		ExitCodes: agentExitCodes(),
		Examples: []Example{
			{Command: `sidecar agent prompt reviewer "Review the current diff and report only actionable findings." --wait --timeout 2m`},
			{Command: `sidecar agent prompt "Summarise what changed." --json`, Description: "the shell you are running in"},
		},
		Agent:   AgentDoc{Invocation: "sidecar agent prompt [TARGET] TEXT [--wait --timeout DURATION]", Summary: "Send a prompt to a managed agent and optionally wait for it to settle"},
		Mutates: true,
		Run:     runAgentPrompt,
	}

	waitFlags := append([]Flag{}, common...)
	waitFlags = append(waitFlags,
		Flag{Name: "--until", Arg: "STATUS", Summary: "Repeatable settled state: idle, done, blocked, or working (default idle, done, blocked)"},
		Flag{Name: "--timeout", Arg: "DURATION", Summary: "Required; there is no implicit timeout"})
	wait := &Command{
		Name:    "wait",
		Summary: "Wait for a managed agent to reach a settled state",
		Usage:   "sidecar agent wait [TARGET] [--until STATUS]... --timeout DURATION [--json]",
		Long: "Observes the target without writing to it. The target stays pinned to the same\n" +
			"tmux session, pane, pane process, server, and provider for the whole wait: a\n" +
			"replacement occupant is reported as agent_replaced rather than satisfying it.",
		Flags:     waitFlags,
		Args:      ArgSpec{Min: 0, Max: 1},
		ExitCodes: agentExitCodes(),
		Examples: []Example{
			{Command: "sidecar agent wait reviewer --timeout 5m --json"},
			{Command: "sidecar agent wait reviewer --until done --timeout 5m", Description: "blocked no longer settles the wait"},
		},
		Agent: AgentDoc{Invocation: "sidecar agent wait [TARGET] --timeout DURATION", Summary: "Wait for a managed agent to reach idle, done, or blocked"},
		Run:   runAgentWait,
	}

	readFlags := append([]Flag{}, common...)
	readFlags = append(readFlags,
		Flag{Name: "--source", Arg: "SOURCE", Summary: "visible, recent, recent-unwrapped, detection, or transcript (default visible)"},
		Flag{Name: "--lines", Arg: "N", Summary: "Bound the result to the last N lines"},
		Flag{Name: "--ansi", Summary: "Preserve styling where the source has it", Bool: true})
	read := &Command{
		Name:    "read",
		Summary: "Read a managed agent's output without touching it",
		Usage:   "sidecar agent read [TARGET] [--source SOURCE] [--lines N] [--ansi] [--json]",
		Long: "Every source is a passive snapshot. Reads never scroll, resize, or otherwise\n" +
			"manipulate the agent's own screen.\n\n" +
			"  visible           the current screen\n" +
			"  recent            the screen plus recent scrollback\n" +
			"  recent-unwrapped  recent, with soft-wrapped lines joined back together\n" +
			"  detection         the exact slice the lifecycle detector read\n" +
			"  transcript        the provider's own conversation, once an exact session\n" +
			"                    binding exists; otherwise transcript_unavailable. It is\n" +
			"                    never guessed from the newest session in the same directory.",
		Flags:     readFlags,
		Args:      ArgSpec{Min: 0, Max: 1},
		ExitCodes: agentExitCodes(),
		Examples: []Example{
			{Command: "sidecar agent read reviewer --source recent-unwrapped --lines 120"},
			{Command: "sidecar agent read reviewer --source detection --json", Description: "the evidence behind the status"},
		},
		Agent: AgentDoc{Invocation: "sidecar agent read [TARGET] [--source SOURCE] [--lines N]", Summary: "Read a managed agent's terminal output passively"},
		Run:   runAgentRead,
	}

	sendKeys := &Command{
		Name:    "send-keys",
		Summary: "Send validated logical keys to a managed agent's UI",
		Usage:   "sidecar agent send-keys [TARGET] KEY [KEY ...] [--json]",
		Long: "With two or more positional arguments the first is the target and the rest are\n" +
			"keys. With exactly one, the key goes to the shell named by SIDECAR_SHELL.\n\n" +
			"Keys are named, not typed: enter, esc, tab, space, backspace, delete, insert,\n" +
			"the arrows, home, end, pageup, pagedown, f1-f12, ctrl+<letter>, ctrl+space,\n" +
			"alt+<key>, shift+tab, shift+enter, shift+<arrow>, and any single character.\n" +
			"The whole list is validated before any of it is written, so a typo sends\n" +
			"nothing at all.\n\n" +
			"This is for answering an agent's UI, not for typing at it: prompt text belongs\n" +
			"to sidecar agent prompt. When a wait returns blocked the sequence is read the\n" +
			"screen, decide, then send keys. Sidecar never answers an approval for you.",
		Flags:     common,
		Args:      ArgSpec{Min: 1, Max: -1},
		ExitCodes: agentExitCodes(),
		Examples: []Example{
			{Command: "sidecar agent send-keys reviewer down enter"},
			{Command: "sidecar agent send-keys reviewer esc", Description: "dismiss a picker"},
		},
		Agent:   AgentDoc{Invocation: "sidecar agent send-keys [TARGET] KEY [KEY ...]", Summary: "Answer a blocked agent's UI with validated logical keys"},
		Mutates: true,
		Run:     runAgentSendKeys,
	}

	// The lifecycle reporting surface. These are deliberately not gated behind
	// agent_control: that flag governs *driving* an agent, while these only
	// record what a provider says about itself, and a pane whose integration is
	// installed should keep reporting whether or not the operator has opted in
	// to agent control.
	lcReport, lcEnd, lcRelease, lcExplain := lifecycleCommands()

	// Sub is rendered in slice order by both RenderHelp and the generated CLI
	// doc, so it is kept alphabetical and TestCLIDocDrift enforces the result.
	sub := []*Command{lcEnd, lcExplain, get, integrationCommand(), list, prompt, read, lcRelease, lcReport, sendKeys, start, wait}
	return &Command{Name: "agent", Summary: "Inspect, start, and coordinate agents in Sidecar-managed shells", Usage: "sidecar agent <command>", Long: "Provider-aware control over shells Sidecar owns. The feature is discoverable while disabled; enable agent_control to run it.\n\nThe safe sequence is: create the layout separately with sidecar create shell, start the provider with agent start, prompt and wait, read before you send keys, and never close a target you did not create.\n\nThe report, end, release, and explain commands are a separate surface: they record and inspect the lifecycle events a provider's own integration reports, and they are not gated behind agent_control.", Sub: sub, Run: runAgentRoot}
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
	wait           bool
	ansi           bool
	lines          int
	timeout        time.Duration
	until          []agentcontrol.Status
	source         agentcontrol.ReadSource
}

// agentOpt is the option set one agent subcommand accepts. Options are declared
// per command rather than parsed permissively so `--wait` on a read, or
// `--source` on a prompt, is a usage error instead of a silent no-op.
type agentOpt uint8

const (
	optTimeout agentOpt = 1 << iota
	optUntil
	optWait
	optSource
	optLines
	optANSI
)

func (a agentOpt) has(opt agentOpt) bool { return a&opt != 0 }

func parseAgentCommon(env Env, args []string, help string) (agentFlags, int) {
	return parseAgentArgs(env, args, help, 0)
}

func parseAgentArgs(env Env, args []string, help string, allowed agentOpt) (agentFlags, int) {
	var f agentFlags
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
		case name == "--project":
			v, n, ok := value(arg, "--project", i)
			if !ok {
				return f, usage("--project requires a value")
			}
			f.project, i = v, n
		case name == "--shell":
			v, n, ok := value(arg, "--shell", i)
			if !ok {
				return f, usage("--shell requires a value")
			}
			f.shell, i = v, n
		case name == "--wait" && allowed.has(optWait):
			f.wait = true
		case name == "--ansi" && allowed.has(optANSI):
			f.ansi = true
		case name == "--timeout" && allowed.has(optTimeout):
			v, n, ok := value(arg, "--timeout", i)
			if !ok {
				return f, usage("--timeout requires a value")
			}
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return f, usage("invalid --timeout %q", v)
			}
			f.timeout, i = d, n
		case name == "--until" && allowed.has(optUntil):
			v, n, ok := value(arg, "--until", i)
			if !ok {
				return f, usage("--until requires a value")
			}
			status, err := agentcontrol.ParseStatus(v)
			if err != nil {
				return f, usage("%v", err)
			}
			f.until, i = append(f.until, status), n
		case name == "--source" && allowed.has(optSource):
			v, n, ok := value(arg, "--source", i)
			if !ok {
				return f, usage("--source requires a value")
			}
			source, err := agentcontrol.ParseReadSource(v)
			if err != nil {
				return f, usage("%v", err)
			}
			f.source, i = source, n
		case name == "--lines" && allowed.has(optLines):
			v, n, ok := value(arg, "--lines", i)
			if !ok {
				return f, usage("--lines requires a value")
			}
			lines, err := strconv.Atoi(v)
			if err != nil || lines <= 0 {
				return f, usage("invalid --lines %q", v)
			}
			f.lines, i = lines, n
		default:
			if strings.HasPrefix(arg, "-") {
				return f, usage("unknown option %q", arg)
			}
			f.positional = append(f.positional, arg)
		}
	}
	return f, -1
}

// currentShellTarget is the omitted-target rule: inside a managed shell the
// command addresses that shell. Outside one there is nothing to fall back to —
// deliberately not the user's focused TUI row, which would make the same
// command mean different things depending on where somebody's cursor is.
func currentShellTarget() string { return os.Getenv(shellstate.SessionEnv) }

// splitAgentTarget applies the positional rule the agent commands share: the
// leading positional is the target only when the caller supplied more than the
// command's own arguments.
func splitAgentTarget(positional []string, wantArgs int) (target string, rest []string, explicit bool) {
	if len(positional) > wantArgs {
		return positional[0], positional[1:], true
	}
	return currentShellTarget(), positional, false
}

// resolveAgentTarget turns the shared flags into a pinned target.
//
// lookup may be nil. Passing one lets a command that has already scanned for
// managed targets — `agent prompt`, checking whether its lone positional names
// one — reuse that scan instead of walking every project's worktrees again.
func resolveAgentTarget(env Env, lookup *shellTargetLookup, target string, f agentFlags, explicit bool) (agentcontrol.Target, int) {
	if target == "" {
		return agentcontrol.Target{}, emitAgentError(env, f.json, &agentcontrol.Error{Code: agentcontrol.ErrNotFound, Message: "target is required outside a managed shell"})
	}
	tgt, code := resolveAgentShellTarget(env, lookup, target, f.shell, f.project, explicit && f.shell == "" && f.project == "", f.json)
	if code != 0 {
		return agentcontrol.Target{}, code
	}
	return targetFromShell(tgt), 0
}

// agentService builds the service and the cleanup its terminal needs. The
// control-mode pool a wait opens has to be released before the process exits,
// and a one-shot read must not pay for one at all.
func agentService(env Env) (agentcontrol.Service, func(), context.Context) {
	terminal := newAgentTerminal()
	release := func() {
		if closer, ok := terminal.(interface{ Close() }); ok {
			closer.Close()
		}
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return agentcontrol.Service{Terminal: terminal}, release, ctx
}

func runAgentPrompt(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("prompt")
	help := RenderHelp(cmd)
	f, code := parseAgentArgs(env, args, help, optWait|optUntil|optTimeout)
	if code >= 0 {
		return code
	}
	if len(f.positional) == 0 || len(f.positional) > 2 {
		cliErrf(env.Stderr, "agent prompt takes [TARGET] TEXT\n\n%s", help)
		return 2
	}
	if f.wait && f.timeout <= 0 {
		cliErrf(env.Stderr, "--wait requires --timeout; there is no implicit timeout\n\n%s", help)
		return 2
	}
	if !f.wait && (f.timeout > 0 || len(f.until) > 0) {
		cliErrf(env.Stderr, "--timeout and --until apply to --wait\n\n%s", help)
		return 2
	}
	// One positional is the prompt text, sent to this shell. But if that one
	// word names a managed target, the caller meant `agent prompt TARGET TEXT`
	// and left the text off — and carrying on would type the target's own name
	// into the caller's own shell, which is both useless and unasked for. Say
	// what is missing instead, and name the way out: someone whose prompt
	// genuinely is one word that collides with a shell name needs to be told
	// how to send it, not merely that they cannot.
	//
	// The scan is memoized because the happy path runs the identical lookup a
	// few lines below through resolveAgentShellTarget, and it walks
	// `git worktree list` per registered project. Paying for that twice on
	// every one-positional prompt is a cost with nothing to show for it.
	var guard shellTargetLookup
	if len(f.positional) == 1 {
		if _, _, err := guard.find(env, f.positional[0], f.shell, f.project, f.shell == "" && f.project == ""); err == nil {
			cliErrf(env.Stderr,
				"agent prompt %s: the prompt text is missing\n\nIf %s really is the prompt, name the target explicitly: sidecar agent prompt %s %q\n\n%s",
				f.positional[0], f.positional[0], f.positional[0], f.positional[0], help)
			return 2
		}
	}
	// Empty text is a usage mistake like every other one here, answered before
	// a target is resolved. The service refuses it too, but that refusal is an
	// operational code — a caller cannot tell "I built the command line wrong"
	// from "the agent would not take it" if both leave by the same exit.
	if strings.TrimSpace(f.positional[len(f.positional)-1]) == "" {
		cliErrf(env.Stderr, "agent prompt TEXT must not be empty\n\n%s", help)
		return 2
	}
	if code = requireAgentControl(env, f.json); code >= 0 {
		return code
	}
	name, rest, explicit := splitAgentTarget(f.positional, 1)
	target, code := resolveAgentTarget(env, &guard, name, f, explicit)
	if code != 0 {
		return code
	}
	svc, release, ctx := agentService(env)
	defer release()
	agent, err := svc.Prompt(ctx, agentcontrol.PromptRequest{Target: target, Text: rest[0], Wait: f.wait, Until: f.until, Timeout: f.timeout})
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitAgent(env, f.json, agent)
}

func runAgentWait(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("wait")
	help := RenderHelp(cmd)
	f, code := parseAgentArgs(env, args, help, optUntil|optTimeout)
	if code >= 0 {
		return code
	}
	if len(f.positional) > 1 {
		cliErrf(env.Stderr, "agent wait accepts at most one target\n\n%s", help)
		return 2
	}
	if f.timeout <= 0 {
		cliErrf(env.Stderr, "agent wait requires --timeout; there is no implicit timeout\n\n%s", help)
		return 2
	}
	if code = requireAgentControl(env, f.json); code >= 0 {
		return code
	}
	name, _, explicit := splitAgentTarget(f.positional, 0)
	target, code := resolveAgentTarget(env, nil, name, f, explicit)
	if code != 0 {
		return code
	}
	svc, release, ctx := agentService(env)
	defer release()
	agent, err := svc.Wait(ctx, agentcontrol.WaitRequest{Target: target, Until: f.until, Timeout: f.timeout})
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitAgent(env, f.json, agent)
}

func runAgentRead(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("read")
	help := RenderHelp(cmd)
	f, code := parseAgentArgs(env, args, help, optSource|optLines|optANSI)
	if code >= 0 {
		return code
	}
	if len(f.positional) > 1 {
		cliErrf(env.Stderr, "agent read accepts at most one target\n\n%s", help)
		return 2
	}
	if code = requireAgentControl(env, f.json); code >= 0 {
		return code
	}
	name, _, explicit := splitAgentTarget(f.positional, 0)
	target, code := resolveAgentTarget(env, nil, name, f, explicit)
	if code != 0 {
		return code
	}
	svc, release, ctx := agentService(env)
	defer release()
	result, err := svc.Read(ctx, agentcontrol.ReadRequest{Target: target, Source: f.source, Lines: f.lines, ANSI: f.ansi})
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	if f.json {
		return writeAgentJSON(env, result)
	}
	for _, message := range result.Messages {
		_, _ = fmt.Fprintf(env.Stdout, "%s: %s\n", message.Role, message.Text)
	}
	if result.Text != "" {
		_, _ = fmt.Fprint(env.Stdout, result.Text)
		if !strings.HasSuffix(result.Text, "\n") {
			_, _ = fmt.Fprintln(env.Stdout)
		}
	}
	return 0
}

func runAgentSendKeys(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("send-keys")
	help := RenderHelp(cmd)
	f, code := parseAgentArgs(env, args, help, 0)
	if code >= 0 {
		return code
	}
	if len(f.positional) == 0 {
		cliErrf(env.Stderr, "agent send-keys takes [TARGET] KEY [KEY ...]\n\n%s", help)
		return 2
	}
	name, keys, explicit := splitAgentTarget(f.positional, 1)
	// Validating here, before the feature gate and before any target
	// resolution, keeps a mistyped key a usage error rather than a refusal that
	// looks like the agent's fault.
	if err := agentcontrol.ValidateKeys(keys); err != nil {
		cliErrf(env.Stderr, "%v\n\n%s", err, help)
		return 2
	}
	if code = requireAgentControl(env, f.json); code >= 0 {
		return code
	}
	target, code := resolveAgentTarget(env, nil, name, f, explicit)
	if code != 0 {
		return code
	}
	svc, release, ctx := agentService(env)
	defer release()
	agent, err := svc.SendKeys(ctx, agentcontrol.KeysRequest{Target: target, Keys: keys})
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitAgent(env, f.json, agent)
}

// agentControlEnabled answers whether this run may drive the provider-aware
// agent commands, without deciding what to do about the answer.
//
// Separate from requireAgentControl because the two callers want different
// things from the same fact: `sidecar agent …` is the feature and refuses
// without it, while `create shell --agent` has work to do either way — it
// records the agent family whether or not it can also start it.
func agentControlEnabled(env Env) (bool, error) {
	if enabled, ok := env.FeatureOverrides[features.AgentControl.Name]; ok {
		return enabled, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return false, err
	}
	return cfg.Features.Flags[features.AgentControl.Name], nil
}

func requireAgentControl(env Env, jsonOutput bool) int {
	if enabled, ok := env.FeatureOverrides[features.AgentControl.Name]; ok && enabled {
		return -1
	}
	if enabled, ok := env.FeatureOverrides[features.AgentControl.Name]; ok && !enabled {
		return emitAgentError(env, jsonOutput, &agentcontrol.Error{Code: agentcontrol.ErrFeatureDisabled, Message: "agent control is disabled"})
	}
	enabled, err := agentControlEnabled(env)
	if err != nil {
		return emitAgentError(env, jsonOutput, err)
	}
	if !enabled {
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
	tgt, code := resolveAgentShellTarget(env, nil, target, f.shell, f.project, len(f.positional) == 1 && f.shell == "" && f.project == "", f.json)
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
	tgt, code := resolveAgentShellTarget(env, nil, target, f.shell, f.project, len(f.positional) == 1 && f.shell == "" && f.project == "", f.json)
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

func resolveAgentShellTarget(env Env, lookup *shellTargetLookup, target, shellFlag, projectFlag string, globalExplicit, jsonOutput bool) (shellTarget, int) {
	if lookup == nil {
		lookup = &shellTargetLookup{}
	}
	tgt, code, err := lookup.find(env, target, shellFlag, projectFlag, globalExplicit)
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
