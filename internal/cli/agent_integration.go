package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/agentintegration"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// `sidecar agent integration ...` — the non-interactive half of integration
// management.
//
// Every command here is a thin projection of one call into
// [agentintegration.Service]. Nothing in this file decides what an install
// does, what makes a status current, or when a mutation is refused; if it did,
// the Configuration surface and the CLI would be two implementations that agree
// only until someone changes one of them.
//
// The mutating commands share one rendering for their plan, used identically by
// `--dry-run` and by the real run. That is the mechanism behind the parity rule
// rather than a promise about it: a preview cannot describe operations the
// mutation does not perform, because the same plan value produces both.

func integrationExitCodes() []ExitCode {
	return []ExitCode{
		{Code: 0, Summary: "success, including a no-op when nothing needed changing"},
		{Code: 1, Summary: "the change was attempted and failed part-way"},
		{Code: 2, Summary: "usage error"},
		{Code: 5, Summary: "refused: unknown or unsupported provider, wrong verb for the current state, or an unsafe path"},
	}
}

// integrationPrivacy is repeated in every command's long help because a hook
// that reports on an agent's activity is exactly the kind of thing a user
// should not have to go looking for the privacy behavior of.
const integrationPrivacy = "\n\nAn installed integration reports lifecycle facts only: lanes, a terminal outcome, a bounded reason code, a sequence, and an opaque session digest. It never sends prompt text, response text, tool arguments or results, file paths, environment values, or credentials, and it cannot notify, play a sound, or choose delivery policy."

func integrationCommand() *Command {
	jsonFlag := Flag{Name: "--json", Summary: "Write stable structured JSON", Bool: true}
	helpFlag := Flag{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}
	dryRun := Flag{Name: "--dry-run", Summary: "Print the exact ordered file operations and change nothing", Bool: true}

	list := &Command{
		Name:      "list",
		Summary:   "List every agent integration Sidecar knows about",
		Usage:     "sidecar agent integration list [--json]",
		Long:      "One line per provider: whether its CLI is installed, whether Sidecar's integration is installed and current, and the authority tier that integration can actually exercise.\n\nA provider Sidecar has recorded evidence for but ships no asset for is listed as unsupported rather than omitted, so \"not yet\" is distinguishable from \"never heard of it\"." + integrationPrivacy,
		Flags:     []Flag{jsonFlag, helpFlag},
		ExitCodes: integrationExitCodes(),
		Examples:  []Example{{Command: "sidecar agent integration list --json"}},
		Agent:     AgentDoc{Invocation: "sidecar agent integration list --json", Summary: "See which agent lifecycle integrations exist and which are installed"},
		Run:       runIntegrationList,
	}

	status := &Command{
		Name:      "status",
		Summary:   "Report one provider's integration state in full",
		Usage:     "sidecar agent integration status [PROVIDER] [--json]",
		Long:      "Reports the installed and bundled asset versions, the provider CLI version and whether it falls inside the range Sidecar has proved, the authority tier and any demotion reason, every path inspected with what was found in it, the known gaps recorded for the source, and the actions that would be accepted right now.\n\nStatus is decided by inspecting the installed files: the bytes on disk are hashed against the bundled asset, so a modified, truncated, or hand-edited asset reports needs-repair rather than current.\n\nWith no PROVIDER, every provider is reported." + integrationPrivacy,
		Flags:     []Flag{jsonFlag, helpFlag},
		Args:      ArgSpec{Min: 0, Max: 1, Description: "Provider name, e.g. opencode"},
		ExitCodes: integrationExitCodes(),
		Examples: []Example{
			{Command: "sidecar agent integration status opencode --json"},
			{Command: "sidecar agent integration status", Description: "every provider"},
		},
		Agent: AgentDoc{Invocation: "sidecar agent integration status [PROVIDER] --json", Summary: "Inspect an integration's installed files, version, and authority tier"},
		Run:   runIntegrationStatus,
	}

	mutating := func(name, summary, long string, examples []Example, agent AgentDoc) *Command {
		return &Command{
			Name:      name,
			Summary:   summary,
			Usage:     "sidecar agent integration " + name + " PROVIDER [--dry-run] [--json]",
			Long:      long + "\n\nThe exact ordered file operations are printed, each with the state of its path before and after and whether Sidecar owns it. --dry-run prints that same plan and changes nothing.\n\nSidecar only ever writes, replaces, or removes a file carrying its own integration marker. A file that merely has the name Sidecar would have chosen is refused and left exactly as it is." + integrationPrivacy,
			Flags:     []Flag{dryRun, jsonFlag, helpFlag},
			Args:      ArgSpec{Min: 1, Max: 1, Description: "Provider name, e.g. opencode"},
			ExitCodes: integrationExitCodes(),
			Examples:  examples,
			Agent:     agent,
			Mutates:   true,
			Run:       func(env Env, args []string) int { return runIntegrationMutation(env, args, name) },
		}
	}

	install := mutating("install",
		"Install a provider's Sidecar lifecycle integration",
		"Writes the bundled integration asset into the provider's user-level configuration directory. Nothing is installed into a repository, and no existing user configuration is rewritten from a template.\n\nInstalling when the current version is already installed is a no-op. Installing over an older or a damaged installation is refused, naming update or repair instead: the verb should mean what the user believes the situation to be.",
		[]Example{
			{Command: "sidecar agent integration install opencode --dry-run", Description: "see the exact files first"},
			{Command: "sidecar agent integration install opencode --json"},
		},
		AgentDoc{Invocation: "sidecar agent integration install PROVIDER [--dry-run]", Summary: "Install an agent's lifecycle integration after previewing the exact files"})

	update := mutating("update",
		"Update an installed integration to the bundled version",
		"Replaces an older installed asset with the version this Sidecar build ships, keeping a recoverable copy of what it replaced.\n\nRefused when nothing is installed, and when the installation is damaged rather than merely old.",
		[]Example{{Command: "sidecar agent integration update opencode"}},
		AgentDoc{Invocation: "sidecar agent integration update PROVIDER [--dry-run]", Summary: "Bring an installed agent integration up to the bundled version"})

	repair := mutating("repair",
		"Repair a damaged or duplicated installation",
		"Restores the bundled asset over one that has been modified or truncated, and removes a duplicate copy Sidecar owns in a second directory the provider also loads.\n\nIt cannot repair a file Sidecar does not own, and says so rather than deleting it.",
		[]Example{{Command: "sidecar agent integration repair opencode --json"}},
		AgentDoc{Invocation: "sidecar agent integration repair PROVIDER [--dry-run]", Summary: "Fix a modified, duplicated, or damaged agent integration"})

	uninstall := mutating("uninstall",
		"Remove a Sidecar-owned integration and nothing else",
		"Removes the asset Sidecar installed, any duplicate copy Sidecar owns, and the backup Sidecar kept. The provider's own configuration and every unrelated plugin are left untouched, and the plugin directory is removed only when removing Sidecar's files empties it.\n\nUninstalling when nothing is installed is a no-op, so a cleanup script can run unconditionally. It works with the provider CLI already gone.",
		[]Example{{Command: "sidecar agent integration uninstall opencode --dry-run"}},
		AgentDoc{Invocation: "sidecar agent integration uninstall PROVIDER [--dry-run]", Summary: "Remove exactly the integration files Sidecar installed"})

	return &Command{
		Name:    "integration",
		Summary: "Inspect and manage agent lifecycle integrations",
		Usage:   "sidecar agent integration <command>",
		Long: "An integration is a small Sidecar-owned file installed beside a supported agent, which reports that agent's own lifecycle events so Sidecar does not have to infer them from its screen.\n\nInstallation is always explicit, always previewable, and always reversible. Sidecar shows the exact user-level paths it would change before changing them, writes atomically, keeps a recoverable backup of anything it replaces, and removes only what it installed." +
			"\n\nThe same application service answers Configuration → Agents → Integrations, so every fact and action there has an equivalent here." + integrationPrivacy,
		Sub: []*Command{install, list, repair, status, uninstall, update},
		Run: runIntegrationRoot,
	}
}

func runIntegrationRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("integration")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	if sub := cmd.FindSubcommand(args[0]); sub != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown integration command %q\n\n%s", args[0], RenderHelp(cmd))
	return 2
}

// integrationFlags is the whole option set these commands accept. It is parsed
// here rather than through parseAgentArgs because none of the agent-control
// options — target, project, timeout, until — mean anything to an installer,
// and accepting them silently would be a worse surface than refusing them.
type integrationFlags struct {
	json     bool
	dryRun   bool
	provider string
}

func parseIntegrationFlags(env Env, args []string, help string, maxArgs int, allowDryRun bool) (integrationFlags, int) {
	var f integrationFlags
	usage := func(format string, a ...any) int {
		cliErrf(env.Stderr, format+"\n\n%s", append(a, help)...)
		return 2
	}
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return f, 0
		case arg == "--json":
			f.json = true
		case arg == "--dry-run":
			if !allowDryRun {
				return f, usage("--dry-run applies to install, update, repair, and uninstall")
			}
			f.dryRun = true
		case strings.HasPrefix(arg, "-"):
			return f, usage("unknown flag %q", arg)
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) > maxArgs {
		if maxArgs == 0 {
			return f, usage("this command takes no arguments, got %q", positional[0])
		}
		return f, usage("this command takes at most one provider, got %d arguments", len(positional))
	}
	if len(positional) == 1 {
		f.provider = strings.TrimSpace(positional[0])
		if f.provider == "" {
			return f, usage("PROVIDER must not be empty")
		}
	}
	return f, -1
}

func integrationHelp(name string) *Command {
	return RootCommand().FindSubcommand("agent").FindSubcommand("integration").FindSubcommand(name)
}

// integrationService is the seam tests replace so no test run inspects, let
// alone writes, a real provider configuration directory.
var integrationService = func(env Env) agentintegration.Service {
	if env.IntegrationService != nil {
		return *env.IntegrationService
	}
	return agentintegration.NewService()
}

// integrationErrorPayload is the JSON error contract for this surface. It
// carries the frozen refusal code so a caller can branch without reading prose.
type integrationErrorPayload struct {
	SchemaVersion int                              `json:"schemaVersion"`
	Code          agentintegration.RefusalCode     `json:"code"`
	Message       string                           `json:"message"`
	Path          string                           `json:"path,omitempty"`
	Status        agentlifecycle.IntegrationStatus `json:"status,omitempty"`
}

func emitIntegrationError(env Env, jsonOutput bool, err error, status agentlifecycle.IntegrationStatus) int {
	r, ok := agentintegration.AsRefusal(err)
	if !ok {
		// Not a refusal: the change was attempted and something underneath
		// failed. That is a different exit code because it is a different
		// problem — the caller's request was fine.
		if jsonOutput {
			_ = json.NewEncoder(env.Stderr).Encode(integrationErrorPayload{
				SchemaVersion: agentintegration.InstallSchemaVersion,
				Message:       err.Error(),
			})
		} else {
			cliErrln(env.Stderr, err.Error())
		}
		return 1
	}
	if jsonOutput {
		_ = json.NewEncoder(env.Stderr).Encode(integrationErrorPayload{
			SchemaVersion: agentintegration.InstallSchemaVersion,
			Code:          r.Code,
			Message:       r.Message,
			Path:          r.Path,
			Status:        status,
		})
	} else {
		cliErrln(env.Stderr, r.Message)
	}
	return exitInputRejected
}

// integrationListPayload is the JSON contract of `integration list` and of
// `integration status` with no provider.
type integrationListPayload struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Integrations  []agentintegration.Status `json:"integrations"`
}

func runIntegrationList(env Env, args []string) int {
	help := RenderHelp(integrationHelp("list"))
	f, code := parseIntegrationFlags(env, args, help, 0, false)
	if code >= 0 {
		return code
	}
	all := integrationService(env).List()
	if f.json {
		return encodeStdout(env, integrationListPayload{SchemaVersion: agentintegration.InstallSchemaVersion, Integrations: all})
	}
	writeIntegrationTable(env, all)
	return 0
}

func writeIntegrationTable(env Env, all []agentintegration.Status) {
	width := len("PROVIDER")
	for _, st := range all {
		if len(st.Provider) > width {
			width = len(st.Provider)
		}
	}
	_, _ = fmt.Fprintf(env.Stdout, "%-*s  %-16s  %-8s  %-16s  %s\n", width, "PROVIDER", "STATUS", "VERSION", "TIER", "SOURCE")
	for _, st := range all {
		version := st.InstalledVersion
		if version == "" {
			version = "-"
		}
		_, _ = fmt.Fprintf(env.Stdout, "%-*s  %-16s  %-8s  %-16s  %s\n", width, st.Provider, st.Status, version, st.EffectiveTier, st.Source)
	}
}

func runIntegrationStatus(env Env, args []string) int {
	help := RenderHelp(integrationHelp("status"))
	f, code := parseIntegrationFlags(env, args, help, 1, false)
	if code >= 0 {
		return code
	}
	svc := integrationService(env)

	if f.provider == "" {
		all := svc.List()
		if f.json {
			return encodeStdout(env, integrationListPayload{SchemaVersion: agentintegration.InstallSchemaVersion, Integrations: all})
		}
		for i, st := range all {
			if i > 0 {
				_, _ = fmt.Fprintln(env.Stdout)
			}
			writeIntegrationStatusText(env, st)
		}
		return 0
	}

	st, err := svc.Status(f.provider)
	if err != nil {
		return emitIntegrationError(env, f.json, err, "")
	}
	if f.json {
		return encodeStdout(env, st)
	}
	writeIntegrationStatusText(env, st)
	return 0
}

func writeIntegrationStatusText(env Env, st agentintegration.Status) {
	line := func(label, value string) {
		if value == "" {
			return
		}
		_, _ = fmt.Fprintf(env.Stdout, "%-14s %s\n", label, value)
	}
	line("provider", st.Provider)
	line("source", st.Source)
	line("status", string(st.Status))
	line("tier", string(st.EffectiveTier))
	line("tier reason", string(st.TierReason))
	line("bundled", st.BundledVersion)
	line("installed", st.InstalledVersion)
	if st.ProviderPath != "" {
		version := st.ProviderVersion
		if version == "" {
			version = "unknown version"
		}
		inRange := "outside the proved range"
		if st.ProviderInTestedRange {
			inRange = "inside the proved range"
		}
		line("provider cli", fmt.Sprintf("%s (%s, %s)", st.ProviderPath, version, inRange))
	} else {
		line("provider cli", "not found on PATH")
	}
	line("message", st.Message)

	for i, file := range st.Files {
		if i == 0 {
			_, _ = fmt.Fprintln(env.Stdout, "paths")
		}
		_, _ = fmt.Fprintf(env.Stdout, "  %s\n", describeFileState(file))
	}
	for i, gap := range st.KnownGaps {
		if i == 0 {
			_, _ = fmt.Fprintln(env.Stdout, "known gaps")
		}
		_, _ = fmt.Fprintf(env.Stdout, "  %s\n", gap)
	}
	if len(st.Offered) > 0 {
		names := make([]string, 0, len(st.Offered))
		for _, a := range st.Offered {
			names = append(names, string(a))
		}
		line("offered", strings.Join(names, ", "))
	}
}

// describeFileState renders one path's state on a single line. It always says
// whether Sidecar owns what it found, because that is what decides whether a
// mutation will touch it.
func describeFileState(f agentintegration.FileState) string {
	var b strings.Builder
	b.WriteString(f.Path)
	switch {
	case !f.Exists:
		b.WriteString("  absent")
	case f.Owned:
		b.WriteString("  sidecar-owned version " + f.Version)
	default:
		b.WriteString("  " + f.Kind + ", not Sidecar's")
	}
	if f.Mode != "" && f.Exists {
		b.WriteString(" " + f.Mode)
	}
	if f.UnsafeDetail != "" {
		b.WriteString("  (" + f.UnsafeDetail + ")")
	}
	return b.String()
}

func runIntegrationMutation(env Env, args []string, name string) int {
	help := RenderHelp(integrationHelp(name))
	f, code := parseIntegrationFlags(env, args, help, 1, true)
	if code >= 0 {
		return code
	}
	if f.provider == "" {
		cliErrf(env.Stderr, "sidecar agent integration %s requires a provider\n\n%s", name, help)
		return 2
	}
	if !agentintegration.IsAction(name) {
		cliErrf(env.Stderr, "unknown integration action %q\n\n%s", name, help)
		return 2
	}
	act := agentintegration.Action(name)
	svc := integrationService(env)

	// Refusals are answered against the current status so a JSON caller can see
	// the state that produced the refusal without a second command.
	var current agentlifecycle.IntegrationStatus
	if st, err := svc.Status(f.provider); err == nil {
		current = st.Status
	}

	var plan agentintegration.Plan
	var err error
	if f.dryRun {
		plan, err = svc.Plan(f.provider, act)
	} else {
		plan, err = svc.Apply(f.provider, act)
	}
	if err != nil {
		return emitIntegrationError(env, f.json, err, current)
	}
	if f.json {
		return encodeStdout(env, plan)
	}
	writePlanText(env, plan)
	return 0
}

// writePlanText renders a plan.
//
// Only the first line differs between a preview and a mutation. Everything
// below it is produced from the plan alone, so `--dry-run` output and the
// output of the run that follows it are the same bytes — which is the point:
// an agent that reads a preview, decides, and then runs the mutation should not
// have to reconcile two descriptions of one change.
func writePlanText(env Env, p agentintegration.Plan) {
	switch {
	case p.Unchanged && p.DryRun:
		_, _ = fmt.Fprintf(env.Stdout, "%s %s: nothing to do (%s)\n", p.Provider, p.Action, p.StatusBefore)
		return
	case p.Unchanged:
		_, _ = fmt.Fprintf(env.Stdout, "%s %s: unchanged (%s)\n", p.Provider, p.Action, p.StatusBefore)
		return
	case p.DryRun:
		_, _ = fmt.Fprintf(env.Stdout, "%s %s: %d file operations, nothing was changed\n", p.Provider, p.Action, len(p.Ops))
	default:
		_, _ = fmt.Fprintf(env.Stdout, "%s %s: %d file operations applied\n", p.Provider, p.Action, len(p.Ops))
	}
	writePlanBody(env, p)
}

func writePlanBody(env Env, p agentintegration.Plan) {
	_, _ = fmt.Fprintf(env.Stdout, "status  %s -> %s\n", p.StatusBefore, p.StatusAfter)
	for i, op := range p.Ops {
		_, _ = fmt.Fprintf(env.Stdout, "\n%d. %s %s\n", i+1, op.Kind, op.Path)
		if op.From != "" {
			_, _ = fmt.Fprintf(env.Stdout, "   from     %s\n", op.From)
		}
		if op.Mode != "" {
			_, _ = fmt.Fprintf(env.Stdout, "   mode     %s\n", op.Mode)
		}
		if op.Bytes > 0 {
			_, _ = fmt.Fprintf(env.Stdout, "   content  %d bytes, sha256 %s\n", op.Bytes, op.Checksum)
		}
		_, _ = fmt.Fprintf(env.Stdout, "   why      %s\n", op.Note)
		_, _ = fmt.Fprintf(env.Stdout, "   before   %s\n", describeOwnership(op.Before))
		_, _ = fmt.Fprintf(env.Stdout, "   after    %s\n", describeOwnership(op.After))
	}
}

// describeOwnership is the before/after ownership status the plan requires to
// be visible. "absent" and "sidecar-owned" are the only two states a mutation
// ever leaves behind; anything else means the plan refused instead.
func describeOwnership(f agentintegration.FileState) string {
	switch {
	case !f.Exists:
		return "absent"
	case f.Kind == "dir":
		return "directory " + f.Mode
	case f.Owned:
		return "sidecar-owned version " + f.Version + " " + f.Mode
	default:
		return "not Sidecar's (" + f.Kind + ") " + f.Mode
	}
}
