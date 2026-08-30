package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

// The registry half of `sidecar host`: list, add, remove, set.
//
// Sidecar owns the host registry — the entries live in its own config.json and
// vanish with it — so per the ownership test in the design principles it owes a
// non-interactive path to everything the Remote Hosts page offers. These four
// verbs are that path, and they share their validation with the page rather
// than repeating it: `config.ValidateHost` is state-free precisely so both
// callers can adopt it unchanged.
//
// None of them connects to anything. Registering a machine is a configuration
// change; whether it answers is reported as health by the running Sidecar, and
// `sidecar host probe` is the way to ask that question from a terminal.

// Exit codes shared by the registry verbs. They reuse `shell forget`'s
// vocabulary: 3 is "the thing you named is not one of mine", 5 is "a value was
// rejected", so an agent that has learned one verb's codes has learned these.
const (
	hostExitNotFound = 3
	hostExitRejected = 5
)

// hostEntryJSON is one registered machine as --json reports it. Disabled is not
// omitempty: a caller reading this is deciding whether a host is switched off,
// and a missing key is a worse answer than false.
type hostEntryJSON struct {
	ID       string   `json:"id"`
	Target   string   `json:"target"`
	Binary   string   `json:"binary,omitempty"`
	Config   string   `json:"config,omitempty"`
	Env      []string `json:"env,omitempty"`
	Disabled bool     `json:"disabled"`
}

type hostListResult struct {
	// FeatureEnabled reports whether the remote-hosts feature flag is on. A
	// registered host connects to nothing while it is off, and a list that did
	// not say so would read as a working setup.
	FeatureEnabled bool            `json:"featureEnabled"`
	Hosts          []hostEntryJSON `json:"hosts"`
}

type hostMutationResult struct {
	Status string        `json:"status"`
	Host   hostEntryJSON `json:"host"`
}

const (
	hostStatusAdded   = "added"
	hostStatusRemoved = "removed"
	hostStatusUpdated = "updated"
)

func hostEntry(host config.HostConfig) hostEntryJSON {
	host = config.NormalizeHost(host)
	return hostEntryJSON{
		ID:       host.ID,
		Target:   host.Target,
		Binary:   host.Binary,
		Config:   host.Config,
		Env:      host.Env,
		Disabled: host.Disabled,
	}
}

// hostRegistryCommands are the three siblings `serve` and `probe` gained once
// the registry became something a user could edit from Configuration. They are
// built here rather than inline in hostCommand so the group's own doc stays
// about what the group is for.
func hostRegistryCommands() []*Command {
	listCmd := &Command{
		Name:    "list",
		Summary: "List the registered remote hosts",
		Usage:   "sidecar host list [--json]",
		Long: "List the machines registered in this Sidecar's configuration, with the\n" +
			"target each resolves through ssh_config and whether it is switched off.\n\n" +
			"This reads config.json. It connects to nothing: use `sidecar host probe`\n" +
			"to ask whether a machine actually answers.\n\n" +
			"Registered hosts are only observed while the sidecar_remote_hosts feature\n" +
			"flag is on; the output says so when it is off.",
		Flags: []Flag{
			{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		Args: ArgSpec{Min: 0, Max: 0},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "success"},
			{Code: 1, Summary: "the configuration could not be read"},
			{Code: 2, Summary: "usage error"},
		},
		Examples: []Example{
			{Command: "sidecar host list"},
			{Command: "sidecar host list --json"},
		},
		Agent: AgentDoc{
			Invocation: "sidecar host list --json",
			Summary:    "See which machines this Sidecar is registered to watch",
		},
		Run: runHostList,
	}

	addCmd := &Command{
		Name:    "add",
		Summary: "Register a remote host",
		Usage:   "sidecar host add <ssh-target> [--id NAME] [--binary PATH] [--config PATH] [--env KEY=VALUE]... [--disabled] [--json]",
		Long: "Register another machine running Sidecar, to be observed over SSH.\n\n" +
			"The target is whatever `ssh <target>` already resolves on this machine —\n" +
			"its keys, its ProxyJump, its agent. Sidecar adds no second place to\n" +
			"describe how to reach a host, so anything that works in ssh works here and\n" +
			"nothing that does not can be fixed from this command.\n\n" +
			"--id names the host in the UI and scopes its workspace rows; it defaults to\n" +
			"the target. --binary is for a machine whose login shell does not find\n" +
			"sidecar on PATH. --config observes a host against a config other than its\n" +
			"user default. --env is extra environment for the remote process, which is\n" +
			"how a proof host is pinned to its own tmux server and state tree\n" +
			"(TMUX_TMPDIR, XDG_STATE_HOME, SIDECAR_ISOLATED_STATE).\n\n" +
			"--disabled registers a machine without connecting to it, which is what a\n" +
			"host that is off this week wants: the entry keeps its settings.",
		Flags: []Flag{
			{Name: "--id", Arg: "NAME", Summary: "Local name for the host (defaults to the target)"},
			{Name: "--binary", Arg: "PATH", Summary: "Explicit sidecar path on the host"},
			{Name: "--config", Arg: "PATH", Summary: "-config path for the remote sidecar"},
			{Name: "--env", Arg: "KEY=VALUE", Summary: "Environment for the remote process (repeatable)"},
			{Name: "--disabled", Summary: "Register the host without connecting to it", Bool: true},
			{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		Args: ArgSpec{Min: 1, Max: 1, Description: "SSH target, as ssh_config resolves it"},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "registered"},
			{Code: 1, Summary: "the configuration could not be read or written"},
			{Code: 2, Summary: "usage error"},
			{Code: 5, Summary: "a value was rejected — an empty target, a name already registered, or a malformed --env"},
		},
		Examples: []Example{
			{Command: "sidecar host add marcusbook"},
			{Description: "Name it, and point at a sidecar the login shell cannot find",
				Command: "sidecar host add marcusbook --id book --binary /opt/homebrew/bin/sidecar"},
			{Description: "A proof host pinned to its own tmux server and state tree",
				Command: "sidecar host add proof-host --env TMUX_TMPDIR=/tmp/proof --env SIDECAR_ISOLATED_STATE=1"},
		},
		Agent: AgentDoc{
			Invocation: "sidecar host add <ssh-target> [--id NAME]",
			Summary:    "Register another machine's Sidecar to watch from here",
		},
		Mutates: true,
		Run:     runHostAdd,
	}

	removeCmd := &Command{
		Name:    "remove",
		Summary: "Unregister a remote host",
		Usage:   "sidecar host remove [--json] <id>",
		Long: "Drop a machine from this Sidecar's registry, by the name `sidecar host list`\n" +
			"shows.\n\n" +
			"Nothing on that machine is touched: the entry described how to watch it, not\n" +
			"what runs there. To stop connecting while keeping the settings, use\n" +
			"`sidecar host set <id> --disabled` instead.",
		Flags: []Flag{
			{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		Args: ArgSpec{Min: 1, Max: 1, Description: "The host's id, as `sidecar host list` shows it"},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "unregistered"},
			{Code: 1, Summary: "the configuration could not be read or written"},
			{Code: 2, Summary: "usage error"},
			{Code: 3, Summary: "no host is registered under that id"},
		},
		Examples: []Example{
			{Command: "sidecar host remove marcusbook"},
			{Command: "sidecar host remove --json book"},
		},
		Agent: AgentDoc{
			Invocation: "sidecar host remove <id>",
			Summary:    "Stop watching a machine and forget its settings",
		},
		Mutates: true,
		Run:     runHostRemove,
	}

	setCmd := &Command{
		Name:    "set",
		Summary: "Change a registered host's settings",
		Usage:   "sidecar host set <id> [--target T] [--id NEWID] [--binary PATH] [--config PATH] [--env KEY=VALUE]... [--enabled|--disabled] [--json]",
		Long: "Change one registered machine. Every field left unnamed is left alone.\n\n" +
			"--env replaces the whole environment list rather than appending to it, so\n" +
			"the entry after the command is exactly what the flags said; pass a single\n" +
			"empty --env \"\" to clear it. --binary and --config likewise clear when given\n" +
			"an empty value.\n\n" +
			"--disabled keeps the host registered but unconnected, which is what a\n" +
			"machine that is off this week wants; --enabled connects to it again.",
		Flags: []Flag{
			{Name: "--target", Arg: "T", Summary: "New ssh destination"},
			{Name: "--id", Arg: "NEWID", Summary: "Rename the host"},
			{Name: "--binary", Arg: "PATH", Summary: "Explicit sidecar path on the host"},
			{Name: "--config", Arg: "PATH", Summary: "-config path for the remote sidecar"},
			{Name: "--env", Arg: "KEY=VALUE", Summary: "Replace the remote environment (repeatable)"},
			{Name: "--enabled", Summary: "Connect to this host again", Bool: true},
			{Name: "--disabled", Summary: "Keep the host registered but unconnected", Bool: true},
			{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		Args: ArgSpec{Min: 1, Max: 1, Description: "The host's id, as `sidecar host list` shows it"},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "saved"},
			{Code: 1, Summary: "the configuration could not be read or written"},
			{Code: 2, Summary: "usage error"},
			{Code: 3, Summary: "no host is registered under that id"},
			{Code: 5, Summary: "a value was rejected — an empty target, a name already registered, or a malformed --env"},
		},
		Examples: []Example{
			{Command: "sidecar host set book --disabled"},
			{Command: "sidecar host set book --target marcusbook.local --enabled"},
			{Description: "Clear the pinned environment", Command: "sidecar host set proof --env \"\""},
		},
		Agent: AgentDoc{
			Invocation: "sidecar host set <id> [--enabled|--disabled]",
			Summary:    "Retarget a registered machine, or switch one off without losing it",
		},
		Mutates: true,
		Run:     runHostSet,
	}

	return []*Command{addCmd, listCmd, removeCmd, setCmd}
}

// hostRegistryFlags is one parsed invocation. Each optional value records
// whether it was given, because `set` distinguishes "leave this alone" from
// "set it to empty" and a bare string cannot.
type hostRegistryFlags struct {
	jsonOutput bool

	id, target, binary, remoteConfig string
	setID, setTarget                 bool
	setBinary, setRemoteConfig       bool

	env    []string
	setEnv bool

	disabled    bool
	setDisabled bool

	positional []string
}

// parseHostRegistryArgs reads the flags these four verbs share. It returns a
// non-negative code when the caller should stop, which is how `--help` prints
// usage and exits 0 without the verb running.
func parseHostRegistryArgs(env Env, args []string, help string, wantPositional int) (hostRegistryFlags, int) {
	var flags hostRegistryFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, inline, hasInline := arg, "", false
		if idx := strings.IndexByte(arg, '='); idx > 0 && strings.HasPrefix(arg, "-") {
			name, inline, hasInline = arg[:idx], arg[idx+1:], true
		}
		// value reads a flag's argument, from `--flag=value` or the next token.
		// An empty next token is a real value: `--env ""` is how the list is
		// cleared, so a blank cannot be treated as a missing argument.
		value := func(flag string) (string, bool) {
			if hasInline {
				return inline, true
			}
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "%s requires a value\n\n%s", flag, help)
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		// Only the flag spellings request usage. The bare word "help" is read as
		// a value, which is the same rule dispatch's asksForHelp follows for the
		// mutation gate: an ssh target or a host id called "help" is unlikely
		// but legal, and the two readings must not disagree about whether this
		// invocation is going to write.
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(env.Stdout, help)
			return flags, 0
		case name == "--json":
			flags.jsonOutput = true
		case name == "--id":
			got, ok := value("--id")
			if !ok {
				return flags, 2
			}
			flags.id, flags.setID = got, true
		case name == "--target":
			got, ok := value("--target")
			if !ok {
				return flags, 2
			}
			flags.target, flags.setTarget = got, true
		case name == "--binary":
			got, ok := value("--binary")
			if !ok {
				return flags, 2
			}
			flags.binary, flags.setBinary = got, true
		case name == "--config":
			got, ok := value("--config")
			if !ok {
				return flags, 2
			}
			flags.remoteConfig, flags.setRemoteConfig = got, true
		case name == "--env":
			got, ok := value("--env")
			if !ok {
				return flags, 2
			}
			flags.setEnv = true
			if strings.TrimSpace(got) != "" {
				flags.env = append(flags.env, got)
			}
		case name == "--disabled":
			flags.disabled, flags.setDisabled = true, true
		case name == "--enabled":
			flags.disabled, flags.setDisabled = false, true
		case strings.HasPrefix(arg, "-"):
			cliErrf(env.Stderr, "unknown flag %q\n\n%s", arg, help)
			return flags, 2
		default:
			flags.positional = append(flags.positional, arg)
		}
	}
	if len(flags.positional) != wantPositional {
		switch wantPositional {
		case 0:
			cliErrf(env.Stderr, "this command takes no positional arguments\n\n%s", help)
		default:
			cliErrf(env.Stderr, "requires exactly one argument\n\n%s", help)
		}
		return flags, 2
	}
	if wantPositional == 1 && strings.TrimSpace(flags.positional[0]) == "" {
		cliErrf(env.Stderr, "requires exactly one non-empty argument\n\n%s", help)
		return flags, 2
	}
	return flags, -1
}

func runHostList(env Env, args []string) int {
	help := RenderHelp(RootCommand().FindSubcommand("host").FindSubcommand("list"))
	flags, code := parseHostRegistryArgs(env, args, help, 0)
	if code >= 0 {
		return code
	}
	if flags.setID || flags.setTarget || flags.setBinary || flags.setRemoteConfig || flags.setEnv || flags.setDisabled {
		cliErrf(env.Stderr, "sidecar host list takes no settings flags\n\n%s", help)
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	list := append([]config.HostConfig(nil), cfg.Hosts.List...)
	// Registration order is not meaningful — there is no "first host" — so the
	// listing sorts by name, which is the order the Sessions browser groups
	// remote rows in (overview.hostOrder). Two surfaces listing the same
	// machines in different orders is a small lie that costs a user real time.
	sort.SliceStable(list, func(a, b int) bool {
		return config.HostIDFor(list[a]) < config.HostIDFor(list[b])
	})
	enabled := features.IsEnabled(features.SidecarRemoteHosts.Name)

	if flags.jsonOutput {
		entries := make([]hostEntryJSON, 0, len(list))
		for _, host := range list {
			entries = append(entries, hostEntry(host))
		}
		return writeHostJSON(env, hostListResult{FeatureEnabled: enabled, Hosts: entries})
	}

	if len(list) == 0 {
		_, _ = fmt.Fprintln(env.Stdout, "No remote hosts registered. Add one with: sidecar host add <ssh-target>")
		return 0
	}
	width := 0
	for _, host := range list {
		if n := len(config.HostIDFor(host)); n > width {
			width = n
		}
	}
	for _, host := range list {
		normalized := config.NormalizeHost(host)
		state := "enabled"
		if normalized.Disabled {
			state = "disabled"
		}
		line := fmt.Sprintf("%-*s  %s  %s", width, normalized.ID, normalized.Target, state)
		if normalized.Binary != "" {
			line += "  binary=" + normalized.Binary
		}
		if normalized.Config != "" {
			line += "  config=" + normalized.Config
		}
		for _, entry := range normalized.Env {
			line += "  " + entry
		}
		_, _ = fmt.Fprintln(env.Stdout, line)
	}
	if !enabled {
		// A registered host connects to nothing while the flag is off, and a
		// listing that stayed silent about that reads as a working setup.
		_, _ = fmt.Fprintf(env.Stdout, "\nRemote hosts are not being observed: the %s feature flag is off.\n", features.SidecarRemoteHosts.Name)
	}
	return 0
}

func runHostAdd(env Env, args []string) int {
	help := RenderHelp(RootCommand().FindSubcommand("host").FindSubcommand("add"))
	flags, code := parseHostRegistryArgs(env, args, help, 1)
	if code >= 0 {
		return code
	}
	if flags.setTarget {
		cliErrf(env.Stderr, "sidecar host add takes the target as its argument, not --target\n\n%s", help)
		return 2
	}

	host := config.HostConfig{
		ID:       flags.id,
		Target:   flags.positional[0],
		Binary:   flags.binary,
		Config:   flags.remoteConfig,
		Env:      flags.env,
		Disabled: flags.disabled,
	}
	// Validation runs against the configuration on disk, in AddHost, so the
	// answer is about the file this is about to write rather than a copy that
	// may have moved since. The shape checks that need no configuration at all
	// still run here first, so a malformed --env is refused by name.
	for _, entry := range host.Env {
		if message := config.ValidateHostEnv(entry); message != "" {
			cliErrf(env.Stderr, "%s\n", message)
			return hostExitRejected
		}
	}

	saved, err := config.AddHost(host)
	if err != nil {
		return hostWriteFailure(env, err)
	}
	if flags.jsonOutput {
		return writeHostJSON(env, hostMutationResult{Status: hostStatusAdded, Host: hostEntry(saved)})
	}
	_, _ = fmt.Fprintf(env.Stdout, "Registered host %q at %s.\n", saved.ID, saved.Target)
	if !features.IsEnabled(features.SidecarRemoteHosts.Name) {
		_, _ = fmt.Fprintf(env.Stdout, "It will not be observed until the %s feature flag is on.\n", features.SidecarRemoteHosts.Name)
	}
	return 0
}

func runHostRemove(env Env, args []string) int {
	help := RenderHelp(RootCommand().FindSubcommand("host").FindSubcommand("remove"))
	flags, code := parseHostRegistryArgs(env, args, help, 1)
	if code >= 0 {
		return code
	}
	if flags.setID || flags.setTarget || flags.setBinary || flags.setRemoteConfig || flags.setEnv || flags.setDisabled {
		cliErrf(env.Stderr, "sidecar host remove takes no settings flags\n\n%s", help)
		return 2
	}

	removed, err := config.RemoveHost(flags.positional[0])
	if err != nil {
		return hostWriteFailure(env, err)
	}
	if flags.jsonOutput {
		return writeHostJSON(env, hostMutationResult{Status: hostStatusRemoved, Host: hostEntry(removed)})
	}
	_, _ = fmt.Fprintf(env.Stdout, "Unregistered host %q.\n", config.HostIDFor(removed))
	return 0
}

func runHostSet(env Env, args []string) int {
	help := RenderHelp(RootCommand().FindSubcommand("host").FindSubcommand("set"))
	flags, code := parseHostRegistryArgs(env, args, help, 1)
	if code >= 0 {
		return code
	}
	if !flags.setID && !flags.setTarget && !flags.setBinary && !flags.setRemoteConfig && !flags.setEnv && !flags.setDisabled {
		cliErrf(env.Stderr, "sidecar host set needs at least one setting to change\n\n%s", help)
		return 2
	}
	for _, entry := range flags.env {
		if message := config.ValidateHostEnv(entry); message != "" {
			cliErrf(env.Stderr, "%s\n", message)
			return hostExitRejected
		}
	}

	saved, err := config.UpdateHost(flags.positional[0], func(host *config.HostConfig) {
		if flags.setID {
			host.ID = flags.id
		}
		if flags.setTarget {
			host.Target = flags.target
		}
		if flags.setBinary {
			host.Binary = flags.binary
		}
		if flags.setRemoteConfig {
			host.Config = flags.remoteConfig
		}
		if flags.setEnv {
			// A replacement, not an append: the entry after the command is
			// exactly what the flags said, which is the only form a script can
			// run twice and get the same host.
			host.Env = append([]string(nil), flags.env...)
		}
		if flags.setDisabled {
			host.Disabled = flags.disabled
		}
	})
	if err != nil {
		return hostWriteFailure(env, err)
	}
	if flags.jsonOutput {
		return writeHostJSON(env, hostMutationResult{Status: hostStatusUpdated, Host: hostEntry(saved)})
	}
	state := "enabled"
	if saved.Disabled {
		state = "disabled"
	}
	_, _ = fmt.Fprintf(env.Stdout, "Saved host %q at %s (%s).\n", saved.ID, saved.Target, state)
	return 0
}

// hostWriteFailure turns a mutation error into the right exit code. The three
// outcomes are genuinely different problems: a name nobody registered, a value
// the registry refuses, and a configuration that could not be read or written.
func hostWriteFailure(env Env, err error) int {
	cliErrln(env.Stderr, err)
	switch {
	case errors.Is(err, config.ErrHostNotFound):
		return hostExitNotFound
	case config.IsHostValueRejection(err):
		return hostExitRejected
	default:
		// Everything left is the file: it could not be read, parsed, or written.
		return 1
	}
}

func writeHostJSON(env Env, v any) int {
	encoder := json.NewEncoder(env.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}
