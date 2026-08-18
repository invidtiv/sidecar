package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceprovider"
)

// `sidecar terminal-links` is the protocol/admin surface for terminal resource
// providers. It is not a replacement for a provider's own CLI: it answers "is
// this instance configured, resolvable, and speaking the protocol", using the
// exact base environment, working directory, and timeouts the TUI will use.
//
// It dispatches from cli.Run, which runs before flag parsing and before any
// TUI, tmux, state, or log setup. Nothing in this file may reach for any of
// those.

type terminalLinksOptions struct {
	json       bool
	configPath string
	describe   bool
	resolve    string
}

func runTerminalLinksRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("terminal-links")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	sub := cmd.FindSubcommand(args[0])
	if sub != nil && sub.Run != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown terminal-links command %q\n\n%s", args[0], RenderHelp(cmd))
	return 2
}

// parseTerminalLinksArgs handles the flags both subcommands share.
func parseTerminalLinksArgs(env Env, help string, args []string, allowResolve bool) (terminalLinksOptions, []string, int, bool) {
	var opts terminalLinksOptions
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(env.Stdout, help)
			return opts, nil, 0, false
		case arg == "--json":
			opts.json = true
		case arg == "--describe":
			opts.describe = true
		case arg == "--config":
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "--config needs a path\n\n%s", help)
				return opts, nil, 2, false
			}
			i++
			opts.configPath = args[i]
		case strings.HasPrefix(arg, "--config="):
			opts.configPath = strings.TrimPrefix(arg, "--config=")
		case arg == "--resolve":
			if !allowResolve {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
				return opts, nil, 2, false
			}
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "--resolve needs a locator\n\n%s", help)
				return opts, nil, 2, false
			}
			i++
			opts.resolve = args[i]
		case allowResolve && strings.HasPrefix(arg, "--resolve="):
			opts.resolve = strings.TrimPrefix(arg, "--resolve=")
		case strings.HasPrefix(arg, "-"):
			cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
			return opts, nil, 2, false
		default:
			positional = append(positional, arg)
		}
	}
	return opts, positional, 0, true
}

// loadTerminalResources reads only the configuration file. It creates no state
// directory, opens no log, and starts no process.
func loadTerminalResources(path string) (config.TerminalResourcesConfig, string, error) {
	if path == "" {
		path = config.ConfigPath()
	}
	cfg, err := config.LoadFrom(path)
	if err != nil {
		return config.TerminalResourcesConfig{}, path, err
	}
	return cfg.TerminalResources, path, nil
}

// providerReport is the JSON shape both subcommands share. It carries variable
// names and presence, never a value.
type providerReport struct {
	Instance        string   `json:"instance"`
	Enabled         bool     `json:"enabled"`
	State           string   `json:"state"`
	Command         []string `json:"command"`
	CommandPath     string   `json:"commandPath,omitempty"`
	CommandResolved bool     `json:"commandResolved"`
	CommandError    string   `json:"commandError,omitempty"`
	PassEnv         []string `json:"passEnv,omitempty"`
	PassEnvMissing  []string `json:"passEnvMissing,omitempty"`
	Timeout         string   `json:"timeout"`

	Describe *describeReport `json:"describe,omitempty"`
	Resolve  *resolveReport  `json:"resolve,omitempty"`
}

type describeReport struct {
	OK           bool                       `json:"ok"`
	Outcome      string                     `json:"outcome"`
	DurationMs   int64                      `json:"durationMs"`
	Provider     *resourceprovider.Info     `json:"provider,omitempty"`
	Matchers     []resourceprovider.Matcher `json:"matchers,omitempty"`
	MatcherCount int                        `json:"matcherCount"`
	Error        *errorReport               `json:"error,omitempty"`
}

type resolveReport struct {
	OK         bool            `json:"ok"`
	Outcome    string          `json:"outcome"`
	DurationMs int64           `json:"durationMs"`
	Matcher    string          `json:"matcher,omitempty"`
	Locator    string          `json:"locator"`
	Resource   *documentReport `json:"resource,omitempty"`
	Error      *errorReport    `json:"error,omitempty"`
}

type errorReport struct {
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable"`
	SetupHint string `json:"setupHint,omitempty"`
}

type documentReport struct {
	Identity  string        `json:"identity"`
	Title     string        `json:"title"`
	Subtitle  string        `json:"subtitle,omitempty"`
	Status    *statusReport `json:"status,omitempty"`
	Fields    []fieldReport `json:"fields,omitempty"`
	Body      *bodyReport   `json:"body,omitempty"`
	SourceURL string        `json:"sourceUrl,omitempty"`
	UpdatedAt string        `json:"updatedAt,omitempty"`
	FreshFor  string        `json:"freshFor,omitempty"`
}

type statusReport struct {
	Label string `json:"label"`
	Tone  string `json:"tone"`
}

type fieldReport struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type bodyReport struct {
	Format string `json:"format"`
	Text   string `json:"text"`
}

type listReport struct {
	Protocol   string           `json:"protocol"`
	ConfigPath string           `json:"configPath"`
	Providers  []providerReport `json:"providers"`
}

func runTerminalLinksList(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("terminal-links").FindSubcommand("list")
	help := RenderHelp(cmd)
	opts, positional, code, cont := parseTerminalLinksArgs(env, help, args, false)
	if !cont {
		return code
	}
	if len(positional) > 0 {
		cliErrf(env.Stderr, "terminal-links list takes no positional arguments\n\n%s", help)
		return 2
	}

	section, path, err := loadTerminalResources(opts.configPath)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}

	report := listReport{Protocol: resource.Protocol, ConfigPath: path, Providers: []providerReport{}}
	for _, p := range section.Providers {
		report.Providers = append(report.Providers, inspectProvider(p))
	}

	if opts.describe {
		for i, p := range section.Providers {
			if !p.Enabled || !report.Providers[i].CommandResolved {
				continue
			}
			d := describeInstance(env.Ctx, p)
			report.Providers[i].Describe = d
			report.Providers[i].State = stateFromDescribe(p, d)
		}
	}

	if opts.json {
		return writeJSON(env, report)
	}
	return writeListText(env, report)
}

func runTerminalLinksCheck(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("terminal-links").FindSubcommand("check")
	help := RenderHelp(cmd)
	opts, positional, code, cont := parseTerminalLinksArgs(env, help, args, true)
	if !cont {
		return code
	}
	if len(positional) != 1 {
		cliErrf(env.Stderr, "terminal-links check takes exactly one provider instance id\n\n%s", help)
		return 2
	}
	instance := positional[0]

	section, _, err := loadTerminalResources(opts.configPath)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}

	var found *config.TerminalResourceProviderConfig
	for i := range section.Providers {
		if section.Providers[i].ID == instance {
			found = &section.Providers[i]
			break
		}
	}
	if found == nil {
		cliErrf(env.Stderr, "no terminal resource provider named %q is configured\n", instance)
		return 3
	}

	report := inspectProvider(*found)
	failed := !report.CommandResolved

	if found.Enabled && report.CommandResolved {
		report.Describe = describeInstance(env.Ctx, *found)
		report.State = stateFromDescribe(*found, report.Describe)
		if !report.Describe.OK {
			failed = true
		}
		// --resolve is a separate, explicit flag because it can perform network
		// access and print private resource data. It is never implied.
		if opts.resolve != "" {
			report.Resolve = resolveInstance(env.Ctx, *found, report.Describe, opts.resolve)
			if !report.Resolve.OK {
				failed = true
			}
		}
	} else if opts.resolve != "" {
		cliErrf(env.Stderr, "provider %q cannot resolve: %s\n", instance, report.State)
		failed = true
	}

	if opts.json {
		if code := writeJSON(env, report); code != 0 {
			return code
		}
	} else if code := writeCheckText(env, report); code != 0 {
		return code
	}
	if failed {
		return 1
	}
	return 0
}

// inspectProvider does the configuration and command-resolution half of a
// check. It resolves argv[0] through PATH but never runs it.
func inspectProvider(p config.TerminalResourceProviderConfig) providerReport {
	report := providerReport{
		Instance: p.ID,
		Enabled:  p.Enabled,
		Command:  p.Command,
		PassEnv:  p.PassEnv,
		Timeout:  p.Timeout.String(),
		State:    string(resourceprovider.StateUnchecked),
	}
	if !p.Enabled {
		report.State = string(resourceprovider.StateDisabled)
	}
	if len(p.Command) > 0 {
		path, err := exec.LookPath(p.Command[0])
		if err != nil {
			report.CommandError = "not found on PATH or not executable"
			if !p.Enabled {
				report.State = string(resourceprovider.StateDisabled)
			} else {
				report.State = string(resourceprovider.StateIncompatible)
			}
		} else {
			report.CommandPath = path
			report.CommandResolved = true
		}
	}
	// Presence only. A passEnv value is never printed, logged, or rendered.
	for _, name := range p.PassEnv {
		if _, ok := os.LookupEnv(name); !ok {
			report.PassEnvMissing = append(report.PassEnvMissing, name)
		}
	}
	return report
}

func describeInstance(ctx context.Context, p config.TerminalResourceProviderConfig) *describeReport {
	if ctx == nil {
		ctx = context.Background()
	}
	provider, err := newCheckProvider(p)
	if err != nil {
		return &describeReport{Outcome: "invalid-request", Error: toErrorReport(resource.Errorf(resource.CodeInvalidConfig, "%s", err))}
	}
	started := time.Now()
	desc, err := provider.Describe(ctx)
	out := &describeReport{
		Outcome:    resourceprovider.OutcomeCode(err),
		DurationMs: time.Since(started).Milliseconds(),
	}
	if err != nil {
		out.Error = toErrorReport(resourceprovider.AsResourceError(err))
		return out
	}
	out.OK = true
	info := desc.Info
	out.Provider = &info
	out.Matchers = desc.Matchers
	out.MatcherCount = len(desc.Matchers)
	return out
}

func resolveInstance(ctx context.Context, p config.TerminalResourceProviderConfig, describe *describeReport, locator string) *resolveReport {
	if ctx == nil {
		ctx = context.Background()
	}
	out := &resolveReport{Locator: locator}
	if describe == nil || !describe.OK {
		out.Outcome = "invalid-request"
		out.Error = toErrorReport(resource.Errorf(resource.CodeInvalidConfig, "describe did not succeed, so no matcher is known"))
		return out
	}
	matcher, ok := matcherFor(describe.Matchers, locator)
	if !ok {
		out.Outcome = "invalid-request"
		out.Error = toErrorReport(resource.Errorf(resource.CodeNotFound, "no declared matcher recognizes that locator"))
		return out
	}
	out.Matcher = matcher

	provider, err := newCheckProvider(p)
	if err != nil {
		out.Outcome = "invalid-request"
		out.Error = toErrorReport(resource.Errorf(resource.CodeInvalidConfig, "%s", err))
		return out
	}
	started := time.Now()
	doc, err := provider.Resolve(ctx, resource.Reference{Instance: p.ID, Matcher: matcher, Locator: locator})
	out.DurationMs = time.Since(started).Milliseconds()
	out.Outcome = resourceprovider.OutcomeCode(err)
	if err != nil {
		out.Error = toErrorReport(resourceprovider.AsResourceError(err))
		return out
	}
	out.OK = true
	out.Resource = toDocumentReport(doc)
	return out
}

// matcherFor picks the declared matcher whose whole match is the locator. The
// TUI gets this from the scanner; a headless check has to reproduce it, and the
// same whole-match rule applies.
func matcherFor(matchers []resourceprovider.Matcher, locator string) (string, bool) {
	for _, m := range matchers {
		re, err := regexp.Compile(m.Pattern)
		if err != nil {
			continue
		}
		if re.FindString(locator) == locator {
			return m.ID, true
		}
	}
	return "", false
}

func newCheckProvider(p config.TerminalResourceProviderConfig) (*resourceprovider.CommandProvider, error) {
	return resourceprovider.NewCommandProvider(resourceprovider.CommandConfig{
		Instance:       p.ID,
		Argv:           p.Command,
		Dir:            checkWorkingDir(),
		PassEnv:        p.PassEnv,
		HostEnv:        os.Environ(),
		ResolveTimeout: p.Timeout,
		Host:           resourceprovider.HostInfo{Name: "sidecar", Version: resourceprovider.HostVersion},
	})
}

// checkWorkingDir is the same neutral config directory the TUI uses, so a check
// and a click launch the provider identically. It is read, never created: the
// CLI must not bring a state or config tree into existence.
func checkWorkingDir() string {
	path := config.ConfigPath()
	if path == "" {
		return os.TempDir()
	}
	dir := dirOf(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return os.TempDir()
	}
	return dir
}

func stateFromDescribe(p config.TerminalResourceProviderConfig, d *describeReport) string {
	if !p.Enabled {
		return string(resourceprovider.StateDisabled)
	}
	if d == nil {
		return string(resourceprovider.StateUnchecked)
	}
	if d.OK {
		return string(resourceprovider.StateReady)
	}
	switch d.Outcome {
	case "protocol", "invalid-describe", "spawn", "invalid_config":
		return string(resourceprovider.StateIncompatible)
	default:
		return string(resourceprovider.StateTemporarilyFailed)
	}
}

func toErrorReport(e *resource.Error) *errorReport {
	if e == nil {
		return nil
	}
	return &errorReport{Code: string(e.Code), Message: e.Message, Retryable: e.Retryable, SetupHint: e.SetupHint}
}

func toDocumentReport(doc resource.Document) *documentReport {
	out := &documentReport{
		Identity:  doc.Identity,
		Title:     doc.Title,
		Subtitle:  doc.Subtitle,
		SourceURL: doc.SourceURL,
	}
	if doc.Status != nil {
		out.Status = &statusReport{Label: doc.Status.Label, Tone: string(doc.Status.Tone)}
	}
	for _, f := range doc.Fields {
		out.Fields = append(out.Fields, fieldReport{Label: f.Label, Value: f.Value})
	}
	if doc.Body != nil {
		out.Body = &bodyReport{Format: string(doc.Body.Format), Text: doc.Body.Text}
	}
	if !doc.UpdatedAt.IsZero() {
		out.UpdatedAt = doc.UpdatedAt.Format(time.RFC3339)
	}
	if doc.FreshFor > 0 {
		out.FreshFor = doc.FreshFor.String()
	}
	return out
}

func writeJSON(env Env, value any) int {
	enc := json.NewEncoder(env.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}

func writeListText(env Env, report listReport) int {
	if len(report.Providers) == 0 {
		_, _ = fmt.Fprintf(env.Stdout, "No terminal resource providers are configured in %s.\n", report.ConfigPath)
		_, _ = fmt.Fprintln(env.Stdout, "Add them under \"terminalResources\".\"providers\". Enabling a provider trusts that executable with your OS privileges.")
		return 0
	}
	_, _ = fmt.Fprintf(env.Stdout, "%d terminal resource provider(s) in %s\n", len(report.Providers), report.ConfigPath)
	for _, p := range report.Providers {
		_, _ = fmt.Fprintln(env.Stdout)
		writeProviderText(env, p)
	}
	return 0
}

func writeCheckText(env Env, report providerReport) int {
	writeProviderText(env, report)
	return 0
}

func writeProviderText(env Env, p providerReport) {
	enabled := "enabled"
	if !p.Enabled {
		enabled = "disabled"
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s  [%s, %s]\n", p.Instance, enabled, p.State)
	_, _ = fmt.Fprintf(env.Stdout, "  command   %s\n", strings.Join(p.Command, " "))
	if p.CommandResolved {
		_, _ = fmt.Fprintf(env.Stdout, "  resolves  %s\n", p.CommandPath)
	} else {
		_, _ = fmt.Fprintf(env.Stdout, "  resolves  no — %s\n", p.CommandError)
	}
	_, _ = fmt.Fprintf(env.Stdout, "  timeout   %s\n", p.Timeout)
	if len(p.PassEnv) > 0 {
		// Names only, and presence only. A value never reaches this output.
		_, _ = fmt.Fprintf(env.Stdout, "  passEnv   %s\n", strings.Join(p.PassEnv, ", "))
		if len(p.PassEnvMissing) > 0 {
			_, _ = fmt.Fprintf(env.Stdout, "            unset in this environment: %s\n", strings.Join(p.PassEnvMissing, ", "))
		}
	}

	if d := p.Describe; d != nil {
		if d.OK {
			name := d.Provider.Name
			if name == "" {
				name = d.Provider.Kind
			}
			_, _ = fmt.Fprintf(env.Stdout, "  describe  ok in %dms — %s %s, %d matcher(s)\n", d.DurationMs, name, d.Provider.Version, d.MatcherCount)
			for _, m := range d.Matchers {
				_, _ = fmt.Fprintf(env.Stdout, "            %s (priority %d)  %s\n", m.ID, m.Priority, m.Pattern)
			}
			if d.Provider.DocsURL != "" {
				_, _ = fmt.Fprintf(env.Stdout, "            docs %s\n", d.Provider.DocsURL)
			}
		} else {
			_, _ = fmt.Fprintf(env.Stdout, "  describe  failed in %dms — %s\n", d.DurationMs, d.Outcome)
			writeErrorText(env, "            ", d.Error)
		}
	}

	if r := p.Resolve; r != nil {
		if r.OK {
			_, _ = fmt.Fprintf(env.Stdout, "  resolve   ok in %dms via matcher %s\n", r.DurationMs, r.Matcher)
			doc := r.Resource
			_, _ = fmt.Fprintf(env.Stdout, "            %s  %s\n", doc.Identity, doc.Title)
			if doc.Subtitle != "" {
				_, _ = fmt.Fprintf(env.Stdout, "            %s\n", doc.Subtitle)
			}
			if doc.Status != nil {
				_, _ = fmt.Fprintf(env.Stdout, "            status %s (%s)\n", doc.Status.Label, doc.Status.Tone)
			}
			for _, f := range doc.Fields {
				_, _ = fmt.Fprintf(env.Stdout, "            %-16s %s\n", f.Label, f.Value)
			}
			if doc.SourceURL != "" {
				_, _ = fmt.Fprintf(env.Stdout, "            source %s\n", doc.SourceURL)
			}
		} else {
			_, _ = fmt.Fprintf(env.Stdout, "  resolve   failed in %dms — %s\n", r.DurationMs, r.Outcome)
			writeErrorText(env, "            ", r.Error)
		}
	}
}

func writeErrorText(env Env, indent string, e *errorReport) {
	if e == nil {
		return
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s%s: %s\n", indent, e.Code, e.Message)
	if e.SetupHint != "" {
		// Copyable text only. Sidecar never executes a setup hint.
		_, _ = fmt.Fprintf(env.Stdout, "%ssetup hint (not executed): %s\n", indent, e.SetupHint)
	}
}

func dirOf(path string) string {
	if i := strings.LastIndexByte(path, os.PathSeparator); i > 0 {
		return path[:i]
	}
	return "."
}
