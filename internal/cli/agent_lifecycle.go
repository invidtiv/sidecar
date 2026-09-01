package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentintegration"
	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/agentlifecycle/lifecycleenv"
	"github.com/marcus/sidecar/internal/agentlifecycle/lifecyclestore"
	"github.com/marcus/sidecar/internal/agentresolve"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxformat"
	"github.com/marcus/sidecar/internal/tty"
)

// The lifecycle report surface: `sidecar agent report`, `end`, `release`, and
// `explain`.
//
// The first three are hook surfaces, and they obey one rule the rest of the CLI
// does not: they fail open. A provider hook runs in the agent's critical path,
// so a reporting failure has to be diagnostic and must never change what the
// provider does. Concretely, that means every one of these exits 0 and prints
// nothing when it is not inside a Sidecar-managed shell, and a store failure is
// reported without ever being allowed to look like the provider's own error.
//
// `explain` is the opposite: it is for a human or an agent asking why a pane is
// in the state it is, so it is loud, read-only, and never writes or repairs
// anything.

// lifecycleFlags is parsed by a private loop rather than through
// parseAgentArgs, because these commands share almost none of the agent-control
// option set — `--source` in particular already means a transcript source
// there, and reusing it would make one flag name mean two things.
type lifecycleFlags struct {
	json bool

	state         string
	outcome       string
	source        string
	sourceVersion string
	provider      string
	seq           uint64
	seqSet        bool
	session       string
	reason        string
	detail        string

	current bool
	shell   string

	// file, agent and title serve `explain --file`: an offline run of the
	// screen lane over a saved capture, with no tmux and no lifecycle store.
	file  string
	agent string
	title string
	rows  int
	// printWindow asks --file for the detection read window rather than a
	// verdict: the exact text the engine evaluated.
	printWindow bool
}

func agentLifecycleExitCodes() []ExitCode {
	return []ExitCode{
		{Code: 0, Summary: "success, or no-op outside a Sidecar-managed shell"},
		{Code: 1, Summary: "the report could not be stored"},
		{Code: 2, Summary: "usage error"},
		{Code: 5, Summary: "invalid context, stale sequence, or run mismatch"},
	}
}

func lifecycleCommands() (report, end, release, explain *Command) {
	common := []Flag{
		{Name: "--source", Arg: "SOURCE", Summary: "Integration source identifier (required)"},
		{Name: "--source-version", Arg: "VERSION", Summary: "Installed integration asset version"},
		{Name: "--provider", Arg: "PROVIDER", Summary: "Catalog agent kind (required)"},
		{Name: "--seq", Arg: "N", Summary: "Strictly increasing sequence within this run. Omit it to have the store assign the next one, which is what a per-event hook process should do"},
		{Name: "--session-id", Arg: "ID", Summary: "Provider session identifier; only a salted digest is retained"},
		{Name: "--reason", Arg: "CODE", Summary: "Bounded reason code from the frozen allowlist"},
		{Name: "--detail", Arg: "TEXT", Summary: "Short sanitized diagnostic; never prompt, response, or tool content"},
		{Name: "--json", Summary: "Write stable structured JSON", Bool: true},
		{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
	}

	hookLong := "\nThis is a hook surface and it fails open: outside a Sidecar-managed shell it " +
		"exits 0 and prints nothing, and no failure here ever changes the provider's own " +
		"behavior or output.\n\nIdentity is derived by Sidecar from the managed-shell " +
		"environment, live tmux, and this process's ancestry. Host, tmux server, pane, and " +
		"provider process cannot be selected through flags, so a hook can only ever report " +
		"about the pane it is running in.\n\nNothing is stored beyond lanes, outcomes, " +
		"bounded reason codes, sequences, timestamps, and opaque identity. Prompt text, " +
		"response text, tool arguments and results, and credentials are never recorded."

	reportFlags := append([]Flag{{Name: "--state", Arg: "LANE", Summary: "working, blocked, or idle (required)"}}, common...)
	report = &Command{
		Name:      "report",
		Summary:   "Report a lifecycle lane for the current agent run",
		Usage:     "sidecar agent report --state working|blocked|idle --source SOURCE --provider PROVIDER --seq N [--session-id ID] [--reason CODE] [--json]",
		Long:      "Records what a provider's own lifecycle event observed. A report is evidence, not a verdict: whether it authors the pane's state depends on the source's proved capability tier, the report's freshness, and whether every identity field still matches the live pane." + hookLong,
		Flags:     reportFlags,
		ExitCodes: agentLifecycleExitCodes(),
		Examples: []Example{
			{Command: "sidecar agent report --state working --source sidecar.opencode.plugin --provider opencode --seq 1 --reason turn_start"},
		},
		Agent:   AgentDoc{Invocation: "sidecar agent report --state LANE --source SOURCE --provider PROVIDER --seq N", Summary: "Record a provider lifecycle lane for the pane you are running in"},
		Mutates: true,
		Run:     runAgentReport,
	}

	endFlags := append([]Flag{{Name: "--outcome", Arg: "OUTCOME", Summary: "completed, cancelled, failed, or unknown (required)"}}, common...)
	end = &Command{
		Name:      "end",
		Summary:   "Report that the current agent run ended",
		Usage:     "sidecar agent end --outcome completed|cancelled|failed|unknown --source SOURCE --provider PROVIDER --seq N [--session-id ID] [--reason CODE] [--json]",
		Long:      "Records a terminal outcome and clears lifecycle authority. The outcome is not a fourth lane: a finished run's lane is idle, and the outcome is separate evidence the status projection may use for health. Process liveness still confirms the run really ended before any surface calls the pane orphaned or failed." + hookLong,
		Flags:     endFlags,
		ExitCodes: agentLifecycleExitCodes(),
		Examples: []Example{
			{Command: "sidecar agent end --outcome cancelled --source sidecar.opencode.plugin --provider opencode --seq 9 --reason cancelled"},
		},
		Agent:   AgentDoc{Invocation: "sidecar agent end --outcome OUTCOME --source SOURCE --provider PROVIDER --seq N", Summary: "Record that an agent run ended, with its terminal outcome"},
		Mutates: true,
		Run:     runAgentEnd,
	}

	release = &Command{
		Name:      "release",
		Summary:   "Surrender lifecycle authority for the current agent run",
		Usage:     "sidecar agent release --source SOURCE --provider PROVIDER --seq N [--session-id ID] [--reason CODE] [--json]",
		Long:      "Gives up authority without claiming an outcome, for an integration that is being uninstalled or disabled, or that has detected it can no longer observe the run truthfully. The pane returns to ordinary screen and process detection immediately rather than holding its last reported lane." + hookLong,
		Flags:     common,
		ExitCodes: agentLifecycleExitCodes(),
		Examples: []Example{
			{Command: "sidecar agent release --source sidecar.opencode.plugin --provider opencode --seq 10 --reason integration_removed"},
		},
		Agent:   AgentDoc{Invocation: "sidecar agent release --source SOURCE --provider PROVIDER --seq N", Summary: "Give up lifecycle authority so the pane returns to screen detection"},
		Mutates: true,
		Run:     runAgentRelease,
	}

	explain = &Command{
		Name:    "explain",
		Summary: "Explain which evidence authored a pane's lifecycle state",
		Usage:   "sidecar agent explain [--current | --shell TARGET | --file PATH --agent KIND] [--json]",
		Long: "Reports the effective state, which evidence authored it, the source's exercisable tier, the last valid report, and — when lifecycle evidence did not win — exactly why not.\n\n" +
			"With --file it runs the screen lane alone over a saved capture: no tmux, no lifecycle store, no running agent. That is how a wrong badge is reproduced from a fixture, and how a new fixture is minted.\n\n" +
			"Every diagnostic fact the Configuration surface shows is available here, so a pane that is not being driven by its integration always has an actionable reason rather than silence.\n\n" +
			"This command is read-only. It never locks, compacts, repairs, or creates the lifecycle log.",
		Flags: []Flag{
			{Name: "--current", Summary: "Explain the pane this command is running in (the default)", Bool: true},
			{Name: "--shell", Arg: "TARGET", Summary: "Explain a managed shell by name"},
			{Name: "--file", Arg: "PATH", Summary: "Explain a saved capture offline, with no tmux and no lifecycle store"},
			{Name: "--agent", Arg: "KIND", Summary: "Which agent's manifest to evaluate --file against (required with --file)"},
			{Name: "--title", Arg: "TEXT", Summary: "Pane title for --file when the capture carries no header"},
			{Name: "--rows", Arg: "N", Summary: "Pane height for --file; the detection read window. Defaults to the fixture header, else 24"},
			{Name: "--print-window", Summary: "With --file, print the detection read window instead of a verdict", Bool: true},
			{Name: "--json", Summary: "Write stable structured JSON", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		ExitCodes: agentLifecycleExitCodes(),
		Examples: []Example{
			{Command: "sidecar agent explain --current --json"},
			{Command: "sidecar agent explain --file internal/agentactivity/testdata/claude/blocked.txt --agent claude --json"},
		},
		Agent: AgentDoc{Invocation: "sidecar agent explain [--current | --shell TARGET | --file PATH --agent KIND] --json", Summary: "See why a pane is in the state it is, why hooks are or are not driving it, and which manifest rule the screen lane matched"},
		Run:   runAgentExplain,
	}
	return report, end, release, explain
}

func parseLifecycleFlags(env Env, args []string, help string, kind agentlifecycle.Kind) (lifecycleFlags, int) {
	var f lifecycleFlags
	usage := func(format string, a ...any) int {
		cliErrf(env.Stderr, format+"\n\n%s", append(a, help)...)
		return 2
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return f, 0
		case arg == "--json":
			f.json = true
		case arg == "--current":
			f.current = true
		case strings.HasPrefix(arg, "--state"):
			v, n, ok := takeFlagArg(arg, args, i, "--state")
			if !ok {
				return f, usage("--state requires a value")
			}
			f.state, i = v, n
		case strings.HasPrefix(arg, "--outcome"):
			v, n, ok := takeFlagArg(arg, args, i, "--outcome")
			if !ok {
				return f, usage("--outcome requires a value")
			}
			f.outcome, i = v, n
		case strings.HasPrefix(arg, "--source-version"):
			// Matched before --source: they share a prefix, and the more
			// specific flag has to win or --source-version would parse as
			// --source with a stray value.
			v, n, ok := takeFlagArg(arg, args, i, "--source-version")
			if !ok {
				return f, usage("--source-version requires a value")
			}
			f.sourceVersion, i = v, n
		case strings.HasPrefix(arg, "--source"):
			v, n, ok := takeFlagArg(arg, args, i, "--source")
			if !ok {
				return f, usage("--source requires a value")
			}
			f.source, i = v, n
		case strings.HasPrefix(arg, "--provider"):
			v, n, ok := takeFlagArg(arg, args, i, "--provider")
			if !ok {
				return f, usage("--provider requires a value")
			}
			f.provider, i = v, n
		case strings.HasPrefix(arg, "--session-id"):
			v, n, ok := takeFlagArg(arg, args, i, "--session-id")
			if !ok {
				return f, usage("--session-id requires a value")
			}
			f.session, i = v, n
		case strings.HasPrefix(arg, "--reason"):
			v, n, ok := takeFlagArg(arg, args, i, "--reason")
			if !ok {
				return f, usage("--reason requires a value")
			}
			f.reason, i = v, n
		case strings.HasPrefix(arg, "--detail"):
			v, n, ok := takeFlagArg(arg, args, i, "--detail")
			if !ok {
				return f, usage("--detail requires a value")
			}
			f.detail, i = v, n
		case strings.HasPrefix(arg, "--shell"):
			v, n, ok := takeFlagArg(arg, args, i, "--shell")
			if !ok {
				return f, usage("--shell requires a value")
			}
			f.shell, i = v, n
		case arg == "--print-window":
			f.printWindow = true
		case strings.HasPrefix(arg, "--file"):
			v, n, ok := takeFlagArg(arg, args, i, "--file")
			if !ok {
				return f, usage("--file requires a value")
			}
			f.file, i = v, n
		case strings.HasPrefix(arg, "--agent"):
			v, n, ok := takeFlagArg(arg, args, i, "--agent")
			if !ok {
				return f, usage("--agent requires a value")
			}
			f.agent, i = v, n
		case strings.HasPrefix(arg, "--title"):
			v, n, ok := takeFlagArg(arg, args, i, "--title")
			if !ok {
				return f, usage("--title requires a value")
			}
			f.title, i = v, n
		case strings.HasPrefix(arg, "--rows"):
			v, n, ok := takeFlagArg(arg, args, i, "--rows")
			if !ok {
				return f, usage("--rows requires a value")
			}
			rows, err := strconv.Atoi(v)
			if err != nil || rows < 0 {
				return f, usage("--rows must be a non-negative integer, got %q", v)
			}
			f.rows, i = rows, n
		case strings.HasPrefix(arg, "--seq"):
			v, n, ok := takeFlagArg(arg, args, i, "--seq")
			if !ok {
				return f, usage("--seq requires a value")
			}
			seq, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return f, usage("--seq must be a non-negative integer, got %q", v)
			}
			f.seq, f.seqSet, i = seq, true, n
		case strings.HasPrefix(arg, "-"):
			return f, usage("unknown flag %q", arg)
		default:
			return f, usage("unexpected argument %q", arg)
		}
	}

	if kind == "" {
		return f, -1
	}

	// Shape validation before anything is derived or opened, so a mistyped
	// command line is a usage error rather than something that looks like a
	// lifecycle refusal.
	if f.source == "" {
		return f, usage("--source is required")
	}
	if f.provider == "" {
		return f, usage("--provider is required")
	}
	// --seq is deliberately optional. Requiring it assumed the reporter is one
	// long-lived process that can hold a counter, which is true of a plugin and
	// false of a hook: a provider that runs each hook as its own short-lived
	// process has nothing to count with, and every process guessing produces
	// collisions the store correctly rejects by dropping a report. Omitting it
	// asks the store to assign the next sequence under the lock it already
	// takes, which is the only place the read and the write are atomic.
	switch kind {
	case agentlifecycle.KindState:
		if f.state == "" {
			return f, usage("--state is required")
		}
		if !agentlifecycle.IsReportState(agentactivity.State(f.state)) {
			return f, usage("--state must be working, blocked, or idle, got %q", f.state)
		}
		if f.outcome != "" {
			return f, usage("--outcome belongs to sidecar agent end, not report")
		}
	case agentlifecycle.KindEnd:
		if f.outcome == "" {
			return f, usage("--outcome is required")
		}
		if !validOutcomeFlag(f.outcome) {
			return f, usage("--outcome must be completed, cancelled, failed, or unknown, got %q", f.outcome)
		}
		if f.state != "" {
			return f, usage("--state belongs to sidecar agent report, not end")
		}
	case agentlifecycle.KindRelease:
		if f.state != "" || f.outcome != "" {
			return f, usage("release asserts neither a state nor an outcome")
		}
	}
	if f.reason != "" && !validReasonFlag(f.reason) {
		return f, usage("--reason %q is not in the frozen allowlist; see sidecar agent report --help", f.reason)
	}
	return f, -1
}

func validOutcomeFlag(s string) bool {
	for _, v := range agentlifecycle.Outcomes() {
		if string(v) == s {
			return true
		}
	}
	return false
}

func validReasonFlag(s string) bool {
	for _, v := range agentlifecycle.Reasons() {
		if string(v) == s {
			return true
		}
	}
	return false
}

func runAgentReport(env Env, args []string) int {
	return runLifecycleWrite(env, args, "report", agentlifecycle.KindState)
}

func runAgentEnd(env Env, args []string) int {
	return runLifecycleWrite(env, args, "end", agentlifecycle.KindEnd)
}

func runAgentRelease(env Env, args []string) int {
	return runLifecycleWrite(env, args, "release", agentlifecycle.KindRelease)
}

// lifecycleResult is the JSON contract of a successful report, end, or release.
type lifecycleResult struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Accepted      bool                      `json:"accepted"`
	Acceptance    agentlifecycle.Acceptance `json:"acceptance,omitempty"`
	Managed       bool                      `json:"managed"`
	Kind          agentlifecycle.Kind       `json:"kind,omitempty"`
	Identity      *agentlifecycle.Identity  `json:"identity,omitempty"`
	Sequence      uint64                    `json:"sequence,omitempty"`
	// Note is why nothing was recorded, for the no-op case.
	Note string `json:"note,omitempty"`
}

// lifecycleError is the JSON error contract, written to stderr.
type lifecycleError struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Code          agentlifecycle.ErrorCode `json:"code"`
	Message       string                   `json:"message"`
}

func runLifecycleWrite(env Env, args []string, name string, kind agentlifecycle.Kind) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand(name)
	help := RenderHelp(cmd)
	f, code := parseLifecycleFlags(env, args, help, kind)
	if code >= 0 {
		return code
	}

	stateDir := env.StateDir
	if stateDir == "" {
		stateDir = config.StateDir()
	}

	ctx, err := lifecycleenv.Resolve(stateDir)
	if err != nil {
		return emitLifecycleError(env, f.json, agentlifecycle.ErrInvalidContext, err)
	}
	if !ctx.Managed {
		// The ordinary quiet no-op. A hook fires for every provider event
		// whether or not the user is running inside Sidecar, and saying
		// anything here would put noise in the agent's own output.
		return emitLifecycleResult(env, f.json, lifecycleResult{
			SchemaVersion: agentlifecycle.SchemaVersion,
			Managed:       false,
			Note:          "not inside a Sidecar-managed shell; nothing was recorded",
		})
	}

	// --provider is the one identity field that comes from a flag rather than
	// from Sidecar's own derivation, so it is the one that can be wrong. A hook
	// entry lives in a settings file, and a provider that reads another
	// provider's settings file for compatibility will run it and pass along
	// whatever --provider that file was written with. Checked against the pane's
	// actual occupant, this becomes a claim Sidecar verified rather than one it
	// copied into an arbitration input.
	//
	// It maps to ErrInvalidContext because that is exactly what this is: the
	// claimed context does not match the live one. No shipped lifecycle adapter
	// can reach this today — only OpenCode ships one, and it is not cross-read by
	// anything — so this is a gate placed ahead of the Claude adapter that
	// td-b87b39 holds, not a fix for a live mis-binding.
	if err := lifecycleenv.VerifyReportedKind(ctx.PanePID, f.provider); err != nil {
		return emitLifecycleError(env, f.json, agentlifecycle.ErrInvalidContext, err)
	}

	rec := agentlifecycle.Report{
		SchemaVersion: agentlifecycle.SchemaVersion,
		ID:            reportID(),
		Kind:          kind,
		Identity:      ctx.IdentityFor(f.provider, f.session),
		Source:        f.source,
		SourceVersion: f.sourceVersion,
		Sequence:      f.seq,
		ObservedAt:    time.Now(),
		Reason:        agentlifecycle.ReasonCode(f.reason),
		Detail:        f.detail,
	}
	switch kind {
	case agentlifecycle.KindState:
		rec.State = agentactivity.State(f.state)
	case agentlifecycle.KindEnd:
		rec.Outcome = agentlifecycle.Outcome(f.outcome)
	}

	store, err := lifecyclestore.Open(stateDir)
	if err != nil {
		return emitLifecycleError(env, f.json, agentlifecycle.ErrStoreFailed, err)
	}

	var acc agentlifecycle.Acceptance
	switch {
	case !f.seqSet:
		// The store assigns. Release goes through the same path rather than
		// store.Release, whose only job is to check the kind -- already checked
		// above -- because two ways of appending a release is how the two would
		// eventually disagree.
		rec, acc, err = store.AppendNext(rec)
	case kind == agentlifecycle.KindRelease:
		acc, err = store.Release(rec)
	default:
		acc, err = store.Append(rec)
	}
	if err != nil {
		return emitLifecycleError(env, f.json, lifecycleErrorCode(err), err)
	}

	identity := rec.Identity
	return emitLifecycleResult(env, f.json, lifecycleResult{
		SchemaVersion: agentlifecycle.SchemaVersion,
		Accepted:      true,
		Acceptance:    acc,
		Managed:       true,
		Kind:          kind,
		Identity:      &identity,
		Sequence:      rec.Sequence,
	})
}

// lifecycleErrorCode maps a store error onto the frozen wire vocabulary, so a
// caller debugging a silent integration can tell "your sequence went backwards"
// from "you are not inside a Sidecar shell" without reading prose.
func lifecycleErrorCode(err error) agentlifecycle.ErrorCode {
	switch {
	case errors.Is(err, lifecyclestore.ErrStaleSequence):
		return agentlifecycle.ErrStaleSequence
	case errors.Is(err, lifecyclestore.ErrPriorRun):
		return agentlifecycle.ErrRunMismatch
	case errors.Is(err, agentlifecycle.ErrValidation):
		return agentlifecycle.ErrInvalidReport
	default:
		return agentlifecycle.ErrStoreFailed
	}
}

func lifecycleExitFor(code agentlifecycle.ErrorCode) int {
	if code == agentlifecycle.ErrStoreFailed {
		return 1
	}
	return exitInputRejected
}

func emitLifecycleResult(env Env, jsonOutput bool, res lifecycleResult) int {
	if jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(res); err != nil {
			cliErrln(env.Stderr, err.Error())
			return 1
		}
		return 0
	}
	// Success is silent in human mode. These run once per provider event, and
	// a line of chatter per event in the agent's own terminal would be a
	// regression the integration caused.
	return 0
}

func emitLifecycleError(env Env, jsonOutput bool, code agentlifecycle.ErrorCode, err error) int {
	if jsonOutput {
		enc := json.NewEncoder(env.Stderr)
		_ = enc.Encode(lifecycleError{
			SchemaVersion: agentlifecycle.SchemaVersion,
			Code:          code,
			Message:       err.Error(),
		})
	} else {
		cliErrln(env.Stderr, err.Error())
	}
	return lifecycleExitFor(code)
}

func reportID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.Itoa(os.Getpid())
}

// explainResult is the JSON contract of `sidecar agent explain`.
//
// It embeds the shared Explanation rather than restating its fields, so the CLI
// and the Configuration surface cannot drift into two different answers about
// why a pane is in the state it is.
type explainResult struct {
	SchemaVersion int                         `json:"schemaVersion"`
	Managed       bool                        `json:"managed"`
	Explanation   *agentlifecycle.Explanation `json:"explanation,omitempty"`
	Note          string                      `json:"note,omitempty"`
}

func runAgentExplain(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("explain")
	help := RenderHelp(cmd)
	f, code := parseLifecycleFlags(env, args, help, "")
	if code >= 0 {
		return code
	}
	if f.current && f.shell != "" {
		cliErrf(env.Stderr, "--current and --shell name different panes; pass one\n\n%s", help)
		return 2
	}
	if f.file != "" {
		if f.current || f.shell != "" {
			cliErrf(env.Stderr, "--file explains a saved capture, not a live pane; drop --current and --shell\n\n%s", help)
			return 2
		}
		return explainFile(env, f, help)
	}
	if f.agent != "" || f.title != "" || f.rows != 0 || f.printWindow {
		cliErrf(env.Stderr, "--agent, --title, --rows and --print-window are only valid with --file\n\n%s", help)
		return 2
	}

	stateDir := env.StateDir
	if stateDir == "" {
		stateDir = config.StateDir()
	}

	if f.shell != "" {
		return explainManagedShell(env, f, stateDir)
	}

	ctx, err := lifecycleenv.Resolve(stateDir)
	if err != nil {
		return emitLifecycleError(env, f.json, agentlifecycle.ErrInvalidContext, err)
	}
	if !ctx.Managed {
		res := explainResult{
			SchemaVersion: agentlifecycle.SchemaVersion,
			Managed:       false,
			Note:          "not inside a Sidecar-managed shell; lifecycle reporting does not apply here",
		}
		if f.json {
			return encodeStdout(env, res)
		}
		_, _ = fmt.Fprintln(env.Stdout, res.Note)
		return 0
	}

	// explain answers through exactly the same source and resolver the polling
	// surfaces use.
	//
	// An earlier version built its own Input by hand: it hardcoded
	// StatusNotInstalled, never populated a capability, passed an unknown
	// screen, and rebuilt the live run identity from *this* process's ancestry
	// — which is the explain command's own ancestry, so it could never match a
	// record written by a hook. TierFor(StatusNotInstalled) then returned
	// screen-fallback unconditionally and the resolver returned before
	// populating a single report field. The command reported "no integration"
	// for a pane with a fresh authoritative report, which is worse than not
	// having the command: it actively told the user the opposite of the truth.
	//
	// Sharing the source is what stops that recurring. If explain and the
	// surfaces ever disagree about a pane now, it is a bug in one shared answer
	// rather than a second answer nobody was maintaining.
	src := agentintegration.NewStoreSource(stateDir)

	// The screen half is really captured. explain is an on-demand diagnostic,
	// not a polling path, so one capture is affordable and an invented
	// "unknown" screen would misrepresent the arbitration it is describing.
	screen, paneTitle, command, paneHeight := capturePaneForExplain(ctx.PaneID)
	ob := agentactivity.Observation{
		Screen:         screen,
		PaneTitle:      paneTitle,
		CurrentCommand: command,
		PaneHeight:     paneHeight,
		CapturedAt:     time.Now(),
	}
	ob.Agent = agentactivity.Identify(ob)

	// The identity is supplied so that a pane with no integration still reports
	// which host, server, and pane it is talking about. Without it the
	// no-evidence explanation carried a pane id and nothing else, which reads
	// as a failure to determine the server rather than as there being no report
	// to check one against. It is used only on that path; when a report does
	// apply, arbitration builds the live identity itself.
	dec := agentresolve.Resolve(ob, agentresolve.PaneRef{
		PaneID:  ctx.PaneID,
		Session: ctx.Session,
		Identity: agentlifecycle.Identity{
			Host:              ctx.Host,
			ServerIncarnation: ctx.ServerIncarnation,
			PaneID:            ctx.PaneID,
			ProcessGeneration: ctx.ProcessGeneration,
		},
	}, src, time.Now())

	if f.json {
		return encodeStdout(env, explainResult{
			SchemaVersion: agentlifecycle.SchemaVersion,
			Managed:       true,
			Explanation:   &dec.Explanation,
		})
	}
	writeExplanationText(env, dec.Explanation)
	return 0
}

// explainManagedShell answers about a shell this process is not running in.
//
// This is the path `--current` cannot cover, and the reason it matters is
// specific: an agent's pane is normally occupied by the agent's own TUI, so
// there is no ordinary way to run a command inside it. Without this, the only
// way to ask why a pane's integration was or was not driving its state was to
// take the pane over, which changes the thing being diagnosed.
//
// Identity comes from the managed-shell inventory and live tmux, never from
// this process's environment or from a stored record. Concretely: the target
// resolves to a registered session, tmux is asked which pane that session holds
// right now, and the host and server incarnation are read live. A record that
// claims that pane is then checked against those observations by the ordinary
// resolver, exactly as it would be on a polling surface.
func explainManagedShell(env Env, f lifecycleFlags, stateDir string) int {
	tgt, code, err := findShellTarget(env, f.shell, "", "", false, tmuxenv.Namespace())
	if err != nil {
		if code == shellTargetUnregistered {
			return emitLifecycleError(env, f.json, agentlifecycle.ErrInvalidContext,
				fmt.Errorf("no registered Sidecar shell named %q; run `sidecar shell list --json` to see what Sidecar owns", f.shell))
		}
		return emitLifecycleError(env, f.json, agentlifecycle.ErrInvalidContext, err)
	}

	// The shell's own tmux namespace, not this process's. A managed shell can
	// live on a socket other than the one explain happens to be running under,
	// and a bare tmux call would then answer confidently about a pane on the
	// wrong server.
	identity := agentintegration.PaneIdentity(tgt.Namespace, tgt.Session)
	if identity.PaneID == "" {
		// The shell is registered but its tmux session is not live. That is a
		// real and common state — a shell whose server has gone away — and it
		// is a different answer from "no integration", so it says so.
		res := explainResult{
			SchemaVersion: agentlifecycle.SchemaVersion,
			Managed:       true,
			Note:          "shell " + tgt.DisplayName + " is registered but has no live tmux pane; there is nothing to explain until it is running",
		}
		if f.json {
			return encodeStdout(env, res)
		}
		_, _ = fmt.Fprintln(env.Stdout, res.Note)
		return 0
	}

	src := agentintegration.NewStoreSourceOn(stateDir, tgt.Namespace)
	screen, paneTitle, command, paneHeight := capturePaneForExplainOn(tgt.Namespace, identity.PaneID)
	ob := agentactivity.Observation{
		Screen:         screen,
		PaneTitle:      paneTitle,
		CurrentCommand: command,
		PaneHeight:     paneHeight,
		CapturedAt:     time.Now(),
	}
	ob.Agent = agentactivity.Identify(ob)

	dec := agentresolve.Resolve(ob, agentresolve.PaneRef{
		PaneID:   identity.PaneID,
		Session:  tgt.Session,
		Identity: identity,
	}, src, time.Now())

	if f.json {
		return encodeStdout(env, explainResult{
			SchemaVersion: agentlifecycle.SchemaVersion,
			Managed:       true,
			Explanation:   &dec.Explanation,
		})
	}
	writeExplanationText(env, dec.Explanation)
	return 0
}

// capturePaneForExplain reads the pane's visible screen and metadata so the
// screen half of the arbitration is real rather than assumed.
//
// Every failure degrades to empty values, which the detectors read as "no
// opinion". A diagnostic command that could not capture a screen should still
// report everything else it knows rather than fail outright.
func capturePaneForExplain(paneID string) (screen, paneTitle, command string, paneHeight int) {
	return capturePaneForExplainOn("", paneID)
}

// capturePaneForExplainOn is capturePaneForExplain against an explicit tmux
// socket, for a pane in a namespace this process is not running in.
func capturePaneForExplainOn(namespace, paneID string) (screen, paneTitle, command string, paneHeight int) {
	if paneID == "" {
		return "", "", "", 0
	}
	screen, err := tty.CapturePaneOutput(paneID, 0)
	if err != nil {
		screen = ""
	}
	// pane_height rides along because it is the manifest engine's read window,
	// and asking for it here costs nothing: it is one more field on a
	// display-message this command already runs.
	args := []string{"display-message", "-p", "-t", paneID, tmuxformat.Fields("pane_title", "pane_current_command", "pane_height")}
	if namespace != "" {
		args = append([]string{"-S", namespace}, args...)
	}
	out, err := exec.Command("tmux", args...).Output()
	if err == nil {
		fields := tmuxformat.Split(strings.TrimRight(string(out), "\n"))
		if len(fields) == 3 {
			paneTitle, command = fields[0], fields[1]
			paneHeight, _ = strconv.Atoi(strings.TrimSpace(fields[2]))
		}
	}
	return screen, paneTitle, command, paneHeight
}

func encodeStdout(env Env, v any) int {
	if err := json.NewEncoder(env.Stdout).Encode(v); err != nil {
		cliErrln(env.Stderr, err.Error())
		return 1
	}
	return 0
}

func writeExplanationText(env Env, e agentlifecycle.Explanation) {
	_, _ = fmt.Fprintf(env.Stdout, "state       %s\n", e.State)
	_, _ = fmt.Fprintf(env.Stdout, "authority   %s\n", e.Authority)
	_, _ = fmt.Fprintf(env.Stdout, "tier        %s\n", e.Tier)
	_, _ = fmt.Fprintf(env.Stdout, "freshness   %s\n", e.Freshness)
	if e.FallbackReason != "" {
		_, _ = fmt.Fprintf(env.Stdout, "fallback    %s\n", e.FallbackReason)
	}
	if e.TierReason != "" {
		_, _ = fmt.Fprintf(env.Stdout, "tier reason %s\n", e.TierReason)
	}
	_, _ = fmt.Fprintf(env.Stdout, "pane        %s on server %s\n", e.Identity.PaneID, e.Identity.ServerIncarnation)
	if e.Identity.Provider != "" {
		_, _ = fmt.Fprintf(env.Stdout, "provider    %s\n", e.Identity.Provider)
	}
	if e.ReportState != "" || e.ReportKind != "" {
		_, _ = fmt.Fprintf(env.Stdout, "last report %s %s seq %d", e.ReportKind, e.ReportState, e.ReportSequence)
		if e.ReportAge != "" {
			_, _ = fmt.Fprintf(env.Stdout, " age %s of %s", e.ReportAge, e.FreshnessWindow)
		}
		_, _ = fmt.Fprintln(env.Stdout)
	}
	_, _ = fmt.Fprintf(env.Stdout, "screen      %s\n", e.ScreenState)
}
