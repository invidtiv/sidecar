package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentactivity/manifests"
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

// agentExplainExitCodes is explain's own table rather than the shared lifecycle
// one, because explain stores nothing: "the report could not be stored" can
// never be why it failed, and reusing that summary told a caller with an
// unreadable --file to go looking at a store the command never opens.
//
// So exit 1 is narrowed here to a true internal failure, and the two things a
// caller actually gets wrong -- a file that is not there and an agent kind
// Sidecar has no manifest for -- take exitInputRejected, which is what the rest
// of the CLI uses for "the command parsed and a value inside it was refused"
// and what lifecycleExitFor already returns for every non-store error.
func agentExplainExitCodes() []ExitCode {
	return []ExitCode{
		{Code: 0, Summary: "success, or no-op outside a Sidecar-managed shell"},
		{Code: 1, Summary: "internal failure: the explanation could not be produced or written"},
		{Code: 2, Summary: "usage error"},
		{Code: 5, Summary: "invalid context, or a rejected value: an unreadable --file, an unknown --agent"},
	}
}

func lifecycleCommands() (report, end, release, explain, manifests *Command) {
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
			"With --file it runs the screen lane alone over a saved capture: no tmux, no lifecycle store, no running agent. It does read the local override directory, so two people reproducing one fixture can reach different verdicts if one of them has an override for that agent; the `manifest` line of the output says which file answered. That is how a wrong badge is reproduced from a fixture, and how a new fixture is minted.\n\n" +
			"Detection manifests can be tuned locally: a file at ~/.config/sidecar/agent-detection/<file>.toml replaces the vendored Herdr manifest for that agent, where <file> is the vendored file's own base name (github-copilot.toml for Copilot, antigravity.toml for Antigravity). It replaces the Sidecar overlay too rather than layering over it, so a rule Sidecar rewrote upstream is not rewritten under an override. An override that cannot be parsed, that declares a different agent, or that needs a newer engine is ignored and the vendored manifest is used; either way explain prints a warning line saying what was found and why.\n\n" +
			"Every diagnostic fact the Configuration surface shows is available here, so a pane that is not being driven by its integration always has an actionable reason rather than silence.\n\n" +
			"This command is read-only. It never locks, compacts, repairs, or creates the lifecycle log.",
		Flags: []Flag{
			{Name: "--current", Summary: "Explain the pane this command is running in (the default)", Bool: true},
			{Name: "--shell", Arg: "TARGET", Summary: "Explain a managed shell by name"},
			{Name: "--file", Arg: "PATH", Summary: "Explain a saved capture offline, with no tmux and no lifecycle store (a local override for the agent is still read)"},
			{Name: "--agent", Arg: "KIND", Summary: "Which agent's manifest to evaluate --file against (required with --file)"},
			{Name: "--title", Arg: "TEXT", Summary: "Pane title for --file when the capture carries no header"},
			{Name: "--rows", Arg: "N", Summary: "Pane height for --file; the detection read window. Must be positive; defaults to the fixture header, else 24"},
			{Name: "--print-window", Summary: "With --file, print the detection read window instead of a verdict", Bool: true},
			{Name: "--json", Summary: "Write stable structured JSON", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		ExitCodes: agentExplainExitCodes(),
		Examples: []Example{
			{Command: "sidecar agent explain --current --json"},
			{Command: "sidecar agent explain --file internal/agentactivity/testdata/claude/blocked.txt --agent claude --json"},
		},
		Agent: AgentDoc{Invocation: "sidecar agent explain [--current | --shell TARGET | --file PATH --agent KIND] --json", Summary: "See why a pane is in the state it is, why hooks are or are not driving it, and which manifest rule the screen lane matched"},
		Run:   runAgentExplain,
	}

	manifests = &Command{
		Name:    "manifests",
		Summary: "List every detection manifest, its version, and which source is active",
		Usage:   "sidecar agent manifests [--refresh | --clear-cache] [--json]",
		Long: "Prints the table `explain` reports for one agent, for every agent Sidecar vendors a manifest for: which of the three sources is active, the version that source carries, the version vendored into this binary, the version in the runtime fetch cache, whether the Sidecar overlay was merged in, and any file that was found and refused.\n\n" +
			"Precedence is a local override in ~/.config/sidecar/agent-detection, then the newer of the runtime fetch cache and the vendored manifest, with the Sidecar overlay merged onto whichever upstream file won.\n\n" +
			"The runtime fetch is off unless `detection.remoteManifests` in ~/.config/sidecar/config.json is set to \"herdr.dev\" or to a catalog index URL. When it is on, Sidecar checks at most once a day, after the first frame, and a check that fails is reported here rather than shown to the user.\n\n" +
			"Off means off: with the setting off, nothing fetches and no cached manifest is loaded, so every agent runs the vendored file again. A cache left over from when it was on is still listed in the REMOTE column, marked as not in use, because \"you have a fetched file and it is not the one running\" is what this table exists to say. `--clear-cache` deletes it.\n\n" +
			"Without a flag this command is read-only: it never fetches, and it never writes the cache or its status file. `--refresh` and `--clear-cache` are the two forms that change something, and each of them prints the table afterwards.",
		Flags: []Flag{
			{Name: "--refresh", Summary: "Check the catalog now, ignoring the once-a-day gate (requires detection.remoteManifests to be on)", Bool: true},
			{Name: "--clear-cache", Summary: "Delete every cached manifest and the fetch status file, then print the table", Bool: true},
			{Name: "--json", Summary: "Write stable structured JSON", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "success"},
			{Code: 1, Summary: "the vendored manifest tree could not be read, or --refresh or --clear-cache failed"},
			{Code: 2, Summary: "usage error, including --refresh with detection.remoteManifests off"},
		},
		Examples: []Example{
			{Command: "sidecar agent manifests"},
			{Command: "sidecar agent manifests --json"},
			{Command: "sidecar agent manifests --refresh"},
			{Command: "sidecar agent manifests --clear-cache"},
		},
		Agent: AgentDoc{Invocation: "sidecar agent manifests --json", Summary: "See every detection manifest's active source and version, and whether a runtime fetch is ahead of the vendored tree"},
		Run:   runAgentManifests,
	}
	return report, end, release, explain, manifests
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
			if err != nil || rows < 1 {
				// Zero is refused rather than accepted and ignored. It used to
				// parse and then lose to the fixture header or the 24-row
				// default, so `--rows 0` silently meant something other than
				// what it says -- the worst available answer for a flag whose
				// whole job is to pin the read window.
				return f, usage("--rows must be a positive integer; omit it for the fixture header, else the 24-row fallback")
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
	attachScreenExplain(&dec.Explanation, ob)

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
	attachScreenExplain(&dec.Explanation, ob)

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

// attachScreenExplain fills in the screen lane's own record: which manifest the
// engine loaded and from where, which rule matched, and any warning about a
// local override that was found and refused.
//
// The resolver has already run the screen lane, through Evaluate, which builds
// no record because the polling surfaces would throw one away 200ms later.
// explain is a diagnostic and can afford the second pass. It is the same
// observation through the same compiled manifest, so the two cannot reach
// different verdicts. The record is nil when the process gate refused before any
// rule ran, which is what "screenExplain is absent" means to a reader.
//
// Open, deliberately: the record belongs in agentresolve, which already runs
// this lane and throws the record away. Producing it there and letting explain
// read it would remove the second evaluation and the standing risk that a
// diagnostic holds a private answer beside the shared one. It is duplicated here
// for now because moving it widens an override fix into a resolver change. If
// agentresolve ever populates Explanation.ScreenExplain itself, delete this
// function rather than keeping both.
func attachScreenExplain(e *agentlifecycle.Explanation, ob agentactivity.Observation) {
	e.ScreenExplain = agentactivity.ExplainManifest(ob)
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
	if e.ScreenExplain == nil {
		return
	}
	// Which manifest authored that screen state, and from where. A user who put
	// a file in ~/.config/sidecar/agent-detection has to be able to see here
	// whether it is the one running, and if not, why not.
	_, _ = fmt.Fprintf(env.Stdout, "manifest    %s\n", e.ScreenExplain.ManifestSource)
	// The same rule as `explain --file`: printed only once a runtime fetch has
	// cached something, because otherwise it is the vendored version restated.
	if e.ScreenExplain.CachedRemoteVersion != "" {
		_, _ = fmt.Fprintf(env.Stdout, "versions    vendored=%s remote=%s active=%s (%s)\n",
			dashIfEmpty(e.ScreenExplain.VendoredVersion), e.ScreenExplain.CachedRemoteVersion,
			dashIfEmpty(e.ScreenExplain.ManifestVersion), dashIfEmpty(e.ScreenExplain.ActiveSource))
	}
	if rule := e.ScreenExplain.MatchedRule; rule != nil {
		_, _ = fmt.Fprintf(env.Stdout, "rule        %s (region=%s priority=%d)\n",
			rule.ID, rule.Region, rule.Priority)
	}
	if e.ScreenExplain.Warning != "" {
		_, _ = fmt.Fprintf(env.Stdout, "warning     %s\n", e.ScreenExplain.Warning)
	}
}

// `sidecar agent manifests` is the whole-corpus form of the three lines
// `explain` prints about one agent's manifest. It exists because the question a
// runtime fetch creates -- "am I ahead of the vendored tree, and where?" -- is
// per agent, and answering it by running `explain --file` twenty-one times with
// a fixture for each is not an answer anyone would get.
//
// It never fetches. A verb that both reports and mutates would make the report
// impossible to trust as a description of the state a running Sidecar is in;
// the fetch belongs to the app, after the first frame, at most once a day.

// manifestsResult is the JSON contract of `sidecar agent manifests`.
//
// It carries everything the text form does and two things the text form has no
// room for: the full per-agent fetch status from the last check, and the raw
// setting value alongside the URL it resolved to. Agents read this, not the
// table.
type manifestsResult struct {
	SchemaVersion int `json:"schemaVersion"`
	// RemoteManifests is the configured value, verbatim, and CatalogURL is what
	// it resolved to, empty when fetching is off. A value that resolved to
	// neither is reported in SettingError, because the config loader has
	// already replaced it with the default and the log line it wrote is not
	// somewhere a user will look.
	RemoteManifests string `json:"remoteManifests"`
	CatalogURL      string `json:"catalogUrl,omitempty"`
	SettingError    string `json:"settingError,omitempty"`
	// CacheIgnored is set when the fetch cache holds files and the setting is
	// off, so nothing in it is loaded. The versions still appear per agent,
	// marked here rather than silently dropped: a cache that is present and
	// unused is the one state a user cannot deduce from the rest of the table.
	CacheIgnored string `json:"cacheIgnored,omitempty"`
	// FetchStatusError says the status file itself could not be read or parsed.
	// Without it an unreadable status file reports as "never checked", which is
	// the one answer that makes a broken fetch look like one nobody configured.
	FetchStatusError string `json:"fetchStatusError,omitempty"`
	// Refreshed and Cleared record what --refresh and --clear-cache did, and are
	// absent for the ordinary read-only run.
	Refreshed *manifestsRefresh `json:"refreshed,omitempty"`
	Cleared   []string          `json:"cleared,omitempty"`
	// CacheDir is where a fetched manifest is cached, and OverrideDir is where
	// a local override is read from. Both are reported whether or not anything
	// is in them, because "where do I put the file" is the other question this
	// command gets asked.
	CacheDir    string                 `json:"cacheDir,omitempty"`
	OverrideDir string                 `json:"overrideDir,omitempty"`
	Fetch       manifests.FetchStatus  `json:"fetch"`
	Agents      []manifestAgentSummary `json:"agents"`
}

// manifestsRefresh is what `--refresh` did: the agents whose cache moved, or
// the reason the check did nothing.
type manifestsRefresh struct {
	Skipped bool     `json:"skipped"`
	Reason  string   `json:"reason,omitempty"`
	Updated []string `json:"updated,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// manifestAgentSummary is one row of the table.
type manifestAgentSummary struct {
	Agent string `json:"agent"`
	// ManifestID is the id the active manifest declares, which differs from
	// Agent for the two agents whose file name and Herdr label disagree
	// (antigravity.toml declares "agy", github-copilot.toml declares
	// "copilot"). Agent is the key every path here is built from.
	ManifestID string `json:"manifestId,omitempty"`
	// ActiveSource is "bundled", "remote", or "local override".
	ActiveSource string `json:"activeSource"`
	// ActiveVersion is the version of the file that answered; VendoredVersion
	// is always this binary's copy; CachedRemoteVersion is the fetch cache's,
	// or "" when nothing usable is cached.
	ActiveVersion       string `json:"activeVersion,omitempty"`
	VendoredVersion     string `json:"vendoredVersion,omitempty"`
	CachedRemoteVersion string `json:"cachedRemoteVersion,omitempty"`
	OverlayApplied      bool   `json:"overlayApplied"`
	// Path is the file a local override or a cached remote manifest was read
	// from, empty for a bundled source.
	Path string `json:"path,omitempty"`
	// Warning is any file that was found and refused, and why. Never fatal.
	Warning string `json:"warning,omitempty"`
	// Error is set when the agent has no usable manifest at all, which can only
	// mean the vendored file failed to load.
	Error string `json:"error,omitempty"`
	// Fetch is the last check's result for this agent, absent when there has
	// never been one.
	Fetch *manifests.AgentFetchStatus `json:"fetch,omitempty"`
}

func runAgentManifests(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("manifests")
	help := RenderHelp(cmd)
	jsonOutput := false
	refresh := false
	clearCache := false
	for _, arg := range args {
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--refresh":
			refresh = true
		case arg == "--clear-cache":
			clearCache = true
		default:
			cliErrf(env.Stderr, "unknown flag %q\n\n%s", arg, help)
			return 2
		}
	}
	if refresh && clearCache {
		cliErrf(env.Stderr, "--refresh and --clear-cache ask for opposite things; run them one at a time\n\n%s", help)
		return 2
	}

	agents, err := manifests.Agents()
	if err != nil {
		// The vendored tree is embedded in the binary, so this is a build
		// problem rather than anything the caller did: exit 1.
		cliErrln(env.Stderr, err.Error())
		return 1
	}

	res := manifestsResult{
		SchemaVersion:   agentlifecycle.SchemaVersion,
		RemoteManifests: config.RemoteManifestsOff,
		CacheDir:        manifests.RemoteDir(),
		OverrideDir:     manifests.OverrideDir(),
		Agents:          make([]manifestAgentSummary, 0, len(agents)),
	}
	// A config that cannot be read is not a reason to refuse the table: every
	// other column is answerable without it, and "detection.remoteManifests is
	// off" is the safe thing to report about a config nobody could load.
	detection := config.DetectionConfig{RemoteManifests: config.RemoteManifestsOff}
	if cfg, cfgErr := config.Load(); cfgErr == nil && cfg != nil {
		detection = cfg.Detection
		res.RemoteManifests = cfg.Detection.RemoteManifests
		if res.RemoteManifests == "" {
			res.RemoteManifests = config.RemoteManifestsOff
		}
		if url, urlErr := cfg.Detection.RemoteCatalogURL(); urlErr != nil {
			// Reachable because the loader keeps the value the user wrote rather
			// than replacing it with the default. This line, and not a log entry
			// nobody reads, is how someone finds out their setting did nothing.
			res.SettingError = urlErr.Error()
		} else {
			res.CatalogURL = url
		}
	}

	// The two forms that change something, before the table is built, so the
	// table describes the state they left behind.
	if clearCache {
		removed, clearErr := manifests.ClearCache()
		if clearErr != nil {
			cliErrln(env.Stderr, clearErr.Error())
			return 1
		}
		res.Cleared = removed
		manifests.Invalidate(agents...)
	}
	if refresh {
		if !detection.RemoteManifestsEnabled() {
			cliErrf(env.Stderr,
				"--refresh needs detection.remoteManifests set to %q or a catalog index URL in %s; it is %q\n\n%s",
				config.RemoteManifestsHerdrDev, config.ConfigPath(), res.RemoteManifests, help)
			return 2
		}
		result, fetchErr := manifests.FetchFromConfig(context.Background(), detection,
			manifests.FetchOptions{Force: true})
		res.Refreshed = &manifestsRefresh{
			Skipped: result.Skipped,
			Reason:  result.Reason,
			Updated: result.Updated,
		}
		if fetchErr != nil {
			// A failed check is reported and the table is still printed: what a
			// check learned about each agent is in the status file, and refusing
			// to print it is refusing to answer the question that was asked.
			res.Refreshed.Error = fetchErr.Error()
		}
	}

	res.Fetch = manifests.LoadFetchStatus()
	res.FetchStatusError = res.Fetch.ReadError

	cacheHolds := false
	for _, agent := range agents {
		row := manifestAgentSummary{Agent: agent}
		compiled, source, loadErr := manifests.Load(agent)
		row.ActiveSource = string(source.Kind)
		if row.ActiveSource == "" {
			row.ActiveSource = string(manifests.KindBundled)
		}
		row.ActiveVersion = source.Version
		row.VendoredVersion = source.VendoredVersion
		row.CachedRemoteVersion = source.CachedRemoteVersion
		row.OverlayApplied = source.OverlayApplied
		row.Path = source.Path
		row.Warning = source.Diagnostic
		if loadErr != nil {
			row.Error = loadErr.Error()
		} else if compiled != nil && compiled.Manifest != nil {
			row.ManifestID = compiled.Manifest.ID
		}
		// With the setting off the loader ignores the cache entirely, so the
		// version has to come from the file itself. The row still says the
		// active source is the vendored one; this is the column that tells the
		// user there is something on disk that --clear-cache would remove.
		if !detection.RemoteManifestsEnabled() {
			if cached := manifests.CachedRemote(agent); cached.Path != "" {
				cacheHolds = true
				row.CachedRemoteVersion = cached.Version
			}
		}
		if status, ok := res.Fetch.Agents[agent]; ok {
			row.Fetch = &status
		}
		res.Agents = append(res.Agents, row)
	}
	if cacheHolds {
		res.CacheIgnored = fmt.Sprintf(
			"detection.remoteManifests is %q, so no cached manifest is loaded; the versions below are files on disk that are not in use",
			res.RemoteManifests)
	}

	if jsonOutput {
		return encodeStdout(env, res)
	}
	writeManifestsText(env, res)
	return 0
}

func writeManifestsText(env Env, res manifestsResult) {
	_, _ = fmt.Fprintf(env.Stdout, "remote manifests  %s\n", res.RemoteManifests)
	if res.SettingError != "" {
		_, _ = fmt.Fprintf(env.Stdout, "setting refused   %s\n", res.SettingError)
	}
	if res.CatalogURL != "" {
		_, _ = fmt.Fprintf(env.Stdout, "catalog           %s\n", res.CatalogURL)
	}
	if res.CacheDir != "" {
		_, _ = fmt.Fprintf(env.Stdout, "cache             %s\n", res.CacheDir)
	}
	if res.OverrideDir != "" {
		_, _ = fmt.Fprintf(env.Stdout, "overrides         %s\n", res.OverrideDir)
	}
	if res.CacheIgnored != "" {
		_, _ = fmt.Fprintf(env.Stdout, "cache ignored     %s\n", res.CacheIgnored)
	}
	if res.FetchStatusError != "" {
		_, _ = fmt.Fprintf(env.Stdout, "status unreadable %s\n", res.FetchStatusError)
	}
	if res.Fetch.LastCheckUnix > 0 {
		_, _ = fmt.Fprintf(env.Stdout, "last check        %s (%s)\n",
			time.Unix(res.Fetch.LastCheckUnix, 0).Format(time.RFC3339), res.Fetch.LastResult)
		if res.Fetch.LastError != "" {
			_, _ = fmt.Fprintf(env.Stdout, "last error        %s\n", res.Fetch.LastError)
		}
	} else if res.CatalogURL != "" && res.FetchStatusError == "" {
		_, _ = fmt.Fprintln(env.Stdout, "last check        never")
	}
	for _, row := range res.Fetch.SkippedRows {
		_, _ = fmt.Fprintf(env.Stdout, "skipped row       %s\n", row)
	}
	if res.Cleared != nil {
		_, _ = fmt.Fprintf(env.Stdout, "cleared           %d file(s) from the fetch cache\n", len(res.Cleared))
	}
	if r := res.Refreshed; r != nil {
		switch {
		case r.Error != "":
			_, _ = fmt.Fprintf(env.Stdout, "refresh           failed: %s\n", r.Error)
		case r.Skipped:
			_, _ = fmt.Fprintf(env.Stdout, "refresh           skipped: %s\n", r.Reason)
		case len(r.Updated) > 0:
			_, _ = fmt.Fprintf(env.Stdout, "refresh           updated %s\n", strings.Join(r.Updated, ", "))
		default:
			_, _ = fmt.Fprintln(env.Stdout, "refresh           everything was already current")
		}
	}
	_, _ = fmt.Fprintln(env.Stdout)

	// A fixed-width table rather than tabwriter, because this output is read by
	// eye and by `awk`, and a column that moves when one version string grows
	// is worse for both than a column that is always in the same place.
	_, _ = fmt.Fprintf(env.Stdout, "%-18s %-14s %-14s %-14s %-14s %s\n",
		"AGENT", "ACTIVE", "VERSION", "VENDORED", "REMOTE", "OVERLAY")
	for _, row := range res.Agents {
		_, _ = fmt.Fprintf(env.Stdout, "%-18s %-14s %-14s %-14s %-14s %s\n",
			row.Agent, row.ActiveSource,
			dashIfEmpty(row.ActiveVersion), dashIfEmpty(row.VendoredVersion),
			dashIfEmpty(row.CachedRemoteVersion), yesNo(row.OverlayApplied))
	}

	// Warnings go under the table rather than in it: they are sentences, and a
	// sentence in a column turns the table into something no width fits.
	for _, row := range res.Agents {
		if row.Error != "" {
			_, _ = fmt.Fprintf(env.Stdout, "\nerror   %s: %s\n", row.Agent, row.Error)
		}
		if row.Warning != "" {
			_, _ = fmt.Fprintf(env.Stdout, "\nwarning %s: %s\n", row.Agent, row.Warning)
		}
		if row.Fetch != nil && row.Fetch.LastError != "" {
			_, _ = fmt.Fprintf(env.Stdout, "\nfetch   %s: %s (%s)\n",
				row.Agent, row.Fetch.LastError, row.Fetch.LastResult)
		}
	}
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
