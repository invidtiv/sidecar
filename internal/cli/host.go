package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/hostserve"
)

// hostCommand is the remote-host command group.
//
// `serve` is the host half: what a viewer spawns over ssh. `probe` is the
// smallest possible viewer, and it exists for two reasons — it is how the
// Phase 0 evidence was measured, and it is the tool a user reaches for when a
// host will not connect and the question is "what is actually coming back over
// that pipe?". Both reasons outlive the spike.
func hostCommand() *Command {
	serveCmd := &Command{
		Name:    "serve",
		Summary: "Stream this machine's Sidecar state as JSONL (spawned over SSH by a remote viewer)",
		Usage:   "sidecar host serve --stdio [--cycles N] [--project NAME=PATH]",
		Long: "Run the headless host agent: collect this machine's projects, shells,\n" +
			"worktrees and agent status on the ordinary Overview cadence, and stream a\n" +
			"versioned JSONL snapshot plus status transitions to stdout.\n\n" +
			"This is not a daemon. It is spawned per connection over an SSH stdio pipe\n" +
			"and exits when that pipe closes.\n\n" +
			"It has exactly one write, and it is the same one a local Sidecar makes: a\n" +
			"shell record whose tmux session is confirmed gone is reaped — tombstoned\n" +
			"through the flocked, conditional writer the Sessions browser uses, so\n" +
			"`sidecar shell restore` still brings it back. Without it a row for a shell\n" +
			"the user had already exited stayed on the viewer's screen until somebody\n" +
			"opened Sidecar on this machine. Nothing else is written: no geometry lease,\n" +
			"no pane resize, no mutating tmux command at all.\n\n" +
			"Nothing is bound to a network. SSH is the entire transport and the entire\n" +
			"trust boundary.",
		Flags: []Flag{
			{Name: "--stdio", Summary: "Serve on stdin/stdout (the only transport)", Bool: true},
			{Name: "--cycles", Arg: "N", Summary: "Exit after N collection cycles (0 = run until the pipe closes)"},
			{Name: "--project", Arg: "NAME=PATH", Summary: "Observe this project instead of the configured list (repeatable)"},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "stream ended cleanly"},
			{Code: 1, Summary: "serve failed"},
			{Code: 2, Summary: "usage error"},
		},
		Examples: []Example{
			{Description: "What a viewer runs over ssh", Command: "sidecar host serve --stdio"},
			{Description: "One cycle, for inspection", Command: "sidecar host serve --stdio --cycles 1"},
		},
		// Serve reaps, so it writes state outside this process and the
		// isolation gate must arm before the loop starts rather than at the
		// first tombstone. shellstate still fails closed underneath; this is
		// ordering, not a new guarantee — a proof run that forgot to move the
		// state tree should be refused before it has observed anything, not
		// after it has already tombstoned a record in the developer's real
		// manifest.
		//
		// The flags cannot disarm it. asksForHelp reads them the way this
		// command's own parser does — only -h/--help count, and a token after a
		// value-taking flag is that flag's value — so `--project help` and
		// `--cycles --help` are values, not requests for usage. That distinction
		// is the Phase C incident recorded in cli.go: `shell send --run help`
		// sailed past the gate and reached tmux.
		Mutates: true,
		Run:     runHostServe,
	}

	probeCmd := &Command{
		Name:    "probe",
		Summary: "Connect to a remote host's serve stream and report what came back",
		Usage:   "sidecar host probe <ssh-target> [--json] [--raw] [--cycles N] [--timeout D]",
		Long: "Spawn `sidecar host serve --stdio` on an SSH target and consume its stream.\n\n" +
			"Prints a health verdict naming the fix when something is wrong: unreachable,\n" +
			"no sidecar on the host, protocol too old on either end, no tmux, or a stream\n" +
			"that is not the protocol at all (a login-shell banner on stdout is the usual\n" +
			"cause). With --raw it passes the JSONL through untouched, which is the form\n" +
			"to capture when recording evidence.",
		Flags: []Flag{
			{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true},
			{Name: "--raw", Summary: "Pass the host's JSONL through verbatim", Bool: true},
			{Name: "--cycles", Arg: "N", Summary: "Stop after N snapshots (default 1)"},
			{Name: "--timeout", Arg: "D", Summary: "Give up after this long (default 30s)"},
			{Name: "--binary", Arg: "PATH", Summary: "Explicit sidecar path on the host"},
			{Name: "--remote-config", Arg: "PATH", Summary: "-config path for the remote sidecar"},
			{Name: "--env", Arg: "K=V", Summary: "Environment for the remote process (repeatable)"},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		Args: ArgSpec{Min: 1, Max: 1, Description: "SSH target, as ssh_config resolves it"},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "host answered and is compatible"},
			{Code: 1, Summary: "host unreachable, incompatible, or not serving the protocol"},
			{Code: 2, Summary: "usage error"},
		},
		Examples: []Example{
			{Command: "sidecar host probe marcusbook"},
			{Description: "Record a raw transcript", Command: "sidecar host probe marcusbook --raw --cycles 3"},
		},
		Run: runHostProbe,
	}

	// The registry verbs sort in with serve and probe rather than sitting under
	// a group of their own: "which machines do I watch" and "what does one of
	// them say" are the same subject, and a user who found `host probe` should
	// find `host add` in the same help.
	sub := append([]*Command{probeCmd, serveCmd}, hostRegistryCommands()...)
	sort.Slice(sub, func(a, b int) bool { return sub[a].Name < sub[b].Name })

	return &Command{
		Name:    "host",
		Summary: "Remote hosts: register them, and observe them over SSH",
		Usage:   "sidecar host <list|add|remove|set|probe|serve> [options]",
		Long: "Register and observe other machines running Sidecar.\n\n" +
			"`list`, `add`, `remove` and `set` edit this Sidecar's host registry — the\n" +
			"same entries the Remote Hosts page in Configuration shows, written through\n" +
			"the same validation. `probe` asks one machine what it is answering with;\n" +
			"`serve` is the half that runs on the remote host.",
		Sub: sub,
		// A group needs its own Run. Dispatch only looks at a top-level
		// command's Run and Launch, so a group without one is "handled" and
		// exits 0 having done nothing — which reads, over an ssh pipe, as a
		// host that connected and then said nothing at all.
		Run: runHostRoot,
	}
}

func runHostRoot(env Env, args []string) int {
	hostCmd := RootCommand().FindSubcommand("host")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(hostCmd))
		return 0
	}
	sub := hostCmd.FindSubcommand(args[0])
	if sub != nil && sub.Run != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown host command %q\n\n%s", args[0], RenderHelp(hostCmd))
	return 2
}

func runHostServe(env Env, args []string) int {
	help := RenderHelp(RootCommand().FindSubcommand("host").FindSubcommand("serve"))

	stdio := false
	cycles := 0
	var explicit []hostserve.Project

	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := arg, "", false
		if idx := strings.IndexByte(arg, '='); idx > 0 {
			name, value, hasValue = arg[:idx], arg[idx+1:], true
		}
		next := func(flag string) (string, bool) {
			if hasValue {
				return value, true
			}
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "%s requires a value\n\n%s", flag, help)
				return "", false
			}
			i++
			return args[i], true
		}
		switch name {
		case "-h", "--help":
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case "--stdio":
			stdio = true
		case "--cycles":
			raw, ok := next("--cycles")
			if !ok {
				return 2
			}
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 {
				cliErrf(env.Stderr, "--cycles requires a non-negative integer\n\n%s", help)
				return 2
			}
			cycles = parsed
		case "--project":
			raw, ok := next("--project")
			if !ok {
				return 2
			}
			label, path, found := strings.Cut(raw, "=")
			if !found || strings.TrimSpace(path) == "" {
				cliErrf(env.Stderr, "--project requires NAME=PATH\n\n%s", help)
				return 2
			}
			explicit = append(explicit, hostserve.Project{Name: label, Path: config.ExpandPath(path)})
		default:
			cliErrf(env.Stderr, "unknown flag %q\n\n%s", arg, help)
			return 2
		}
	}

	// --stdio is required rather than assumed. It is the only transport there
	// is, so making it explicit costs a viewer nothing and reserves the shape
	// of the command for a day when it is not.
	if !stdio {
		cliErrf(env.Stderr, "sidecar host serve requires --stdio\n\n%s", help)
		return 2
	}

	// Fail closed before reading a state tree.
	//
	// The plan requires that a proof run against a remote host can never touch
	// a real remote state tree. main() enforces that for TUI startup, but
	// cli.Run dispatches before main reaches the check, so every subcommand has
	// been exempt from it — serve most consequentially, because serve is the
	// one that gets spawned on someone else's machine. Checking here restores
	// the guarantee for this command; the wider gap is recorded as a finding
	// rather than fixed under a spike.
	if err := config.CheckStateIsolation(); err != nil {
		cliErrf(env.Stderr, "sidecar host serve: %v\n", err)
		return 1
	}

	projects := explicit
	if len(projects) == 0 {
		var err error
		projects, err = configuredProjects()
		if err != nil {
			// A host whose config will not parse must not present as a healthy
			// host with nothing configured. Those are opposite problems and
			// only one of them is the user's to fix.
			_ = hostproto.NewEncoder(env.Stdout).Encode(hostproto.Message{
				Kind: hostproto.KindError,
				Error: &hostproto.Error{
					Code:    hostproto.ErrNoConfig,
					Message: fmt.Sprintf("cannot read the remote Sidecar config: %v", err),
					Fatal:   true,
				},
			})
			return 1
		}
	}

	// SIGPIPE is the normal way this process ends: the viewer disconnected.
	// Go turns a write to a closed stdout into an error rather than a signal,
	// so the loop returns on its own; the signal handling here is for SIGTERM
	// and SIGINT, which is how an ssh session teardown reaches the child.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()

	err := hostserve.Serve(ctx, hostserve.Options{
		Out:      env.Stdout,
		Projects: projects,
		Cycles:   cycles,
	})
	if err != nil {
		// The failure goes to stderr, not into the JSONL stream: a viewer that
		// can no longer read stdout cannot read a protocol error on it either,
		// and ssh delivers the host's stderr separately.
		_, _ = fmt.Fprintf(env.Stderr, "sidecar host serve: %v\n", err)
		return 1
	}
	return 0
}

// configuredProjects reads the same projects.list the local Overview reads.
// Host discovery is not a new mechanism: whatever the remote machine's own
// Sidecar shows is exactly what it serves.
func configuredProjects() ([]hostserve.Project, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	projects := make([]hostserve.Project, 0, len(cfg.Projects.List))
	for _, project := range cfg.Projects.List {
		path := config.ExpandPath(project.Path)
		if strings.TrimSpace(path) == "" {
			continue
		}
		name := project.Name
		if name == "" {
			name = filepath.Base(path)
		}
		projects = append(projects, hostserve.Project{Name: name, Path: path})
	}
	return projects, nil
}

// probeResult is the structured verdict --json emits.
type probeResult struct {
	Target       string           `json:"target"`
	OK           bool             `json:"ok"`
	State        string           `json:"state"`
	Detail       string           `json:"detail,omitempty"`
	Hello        *hostproto.Hello `json:"hello,omitempty"`
	Snapshots    int              `json:"snapshots"`
	Events       int              `json:"events"`
	Items        int              `json:"items"`
	HelloLatency string           `json:"helloLatency,omitempty"`
	FirstData    string           `json:"firstSnapshotLatency,omitempty"`
	Bytes        int64            `json:"bytes"`
}

// Probe row states. Each names a distinct fix, which is the requirement the
// plan puts on host health: "unreachable / no sidecar / protocol too old / no
// tmux / stale each render a distinct row state naming the fix."
const (
	probeStateOK          = "ok"
	probeStateUnreachable = "unreachable"
	probeStateNoSidecar   = "no-sidecar"
	probeStateProtocol    = "protocol-mismatch"
	probeStateNotProtocol = "not-protocol"
	probeStateNoTmux      = "no-tmux"
	probeStateTimeout     = "timeout"
)

func runHostProbe(env Env, args []string) int {
	help := RenderHelp(RootCommand().FindSubcommand("host").FindSubcommand("probe"))

	jsonOutput, raw := false, false
	cycles := 1
	timeout := 30 * time.Second
	binary, remoteConfig := "", ""
	var remoteEnv []string
	var target string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := arg, "", false
		if idx := strings.IndexByte(arg, '='); idx > 0 && strings.HasPrefix(arg, "-") {
			name, value, hasValue = arg[:idx], arg[idx+1:], true
		}
		next := func(flag string) (string, bool) {
			if hasValue {
				return value, true
			}
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "%s requires a value\n\n%s", flag, help)
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case name == "-h" || name == "--help":
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case name == "--json":
			jsonOutput = true
		case name == "--raw":
			raw = true
		case name == "--cycles":
			got, ok := next("--cycles")
			if !ok {
				return 2
			}
			parsed, err := strconv.Atoi(got)
			if err != nil || parsed < 1 {
				cliErrf(env.Stderr, "--cycles requires a positive integer\n\n%s", help)
				return 2
			}
			cycles = parsed
		case name == "--timeout":
			got, ok := next("--timeout")
			if !ok {
				return 2
			}
			parsed, err := time.ParseDuration(got)
			if err != nil || parsed <= 0 {
				cliErrf(env.Stderr, "--timeout requires a positive duration (e.g. 30s)\n\n%s", help)
				return 2
			}
			timeout = parsed
		case name == "--binary":
			got, ok := next("--binary")
			if !ok {
				return 2
			}
			binary = got
		case name == "--remote-config":
			got, ok := next("--remote-config")
			if !ok {
				return 2
			}
			remoteConfig = got
		case name == "--env":
			got, ok := next("--env")
			if !ok {
				return 2
			}
			remoteEnv = append(remoteEnv, got)
		case strings.HasPrefix(arg, "-"):
			cliErrf(env.Stderr, "unknown flag %q\n\n%s", arg, help)
			return 2
		default:
			if target != "" {
				cliErrf(env.Stderr, "probe takes one ssh target\n\n%s", help)
				return 2
			}
			target = arg
		}
	}
	if target == "" {
		cliErrf(env.Stderr, "probe requires an ssh target\n\n%s", help)
		return 2
	}

	result := probeHost(env, hosts.Host{
		ID:           target,
		Target:       target,
		RemoteBinary: binary,
		RemoteConfig: remoteConfig,
		Env:          remoteEnv,
	}, cycles, timeout, raw)

	if jsonOutput {
		encoder := json.NewEncoder(env.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(result)
	} else if !raw {
		renderProbe(env, result)
	}
	if result.OK {
		return 0
	}
	return 1
}

func renderProbe(env Env, result probeResult) {
	if result.OK {
		hello := result.Hello
		_, _ = fmt.Fprintf(env.Stdout, "%s: ok — sidecar %s, proto %d, %s/%s\n",
			result.Target, hello.Version, hello.Proto, hello.OS, hello.Arch)
		tmux := "tmux missing"
		switch {
		case hello.TmuxPresent && hello.ServerRunning:
			tmux = hello.TmuxVersion + ", server running"
		case hello.TmuxPresent:
			tmux = hello.TmuxVersion + ", no server running"
		}
		_, _ = fmt.Fprintf(env.Stdout, "  %s; %d project(s); %d workspace(s)\n", tmux, hello.Projects, result.Items)
		_, _ = fmt.Fprintf(env.Stdout, "  hello in %s, first snapshot in %s, %d bytes\n",
			result.HelloLatency, result.FirstData, result.Bytes)
		if !hello.Capabilities.ProcessIdentity {
			_, _ = fmt.Fprintf(env.Stdout, "  note: no argv0 process identity on this host — a shared-runtime pane's agent is a guess\n")
		}
		if hello.Capabilities.IsolatedState {
			_, _ = fmt.Fprintf(env.Stdout, "  isolated state: %s\n", hello.Capabilities.StateDir)
		}
		return
	}
	_, _ = fmt.Fprintf(env.Stderr, "%s: %s\n", result.Target, result.State)
	if result.Detail != "" {
		_, _ = fmt.Fprintf(env.Stderr, "  %s\n", result.Detail)
	}
	_, _ = fmt.Fprintf(env.Stderr, "  %s\n", probeFix(result.State))
}

// probeFix names what to do about a state. A health row that does not say how
// to fix it is a row the user has to go and research.
func probeFix(state string) string {
	switch state {
	case probeStateUnreachable:
		return "fix: check the host is up and `ssh <target>` succeeds from this machine"
	case probeStateNoSidecar:
		return "fix: install sidecar on the host, or pass --binary with its absolute path"
	case probeStateProtocol:
		return "fix: update sidecar on whichever end is older"
	case probeStateNoTmux:
		return "fix: install tmux on the host"
	case probeStateNotProtocol:
		return "fix: the host's login shell prints to stdout; move that output to stderr or guard it with a non-interactive check"
	case probeStateTimeout:
		return "fix: the host connected but sent nothing in time; check that sidecar there is not blocked on a prompt"
	default:
		return ""
	}
}

func probeHost(env Env, host hosts.Host, cycles int, timeout time.Duration, raw bool) probeResult {
	result := probeResult{Target: host.Target, State: probeStateUnreachable}

	dir, err := os.MkdirTemp("", "sidecar-host-probe-")
	if err != nil {
		result.Detail = err.Error()
		return result
	}
	defer func() { _ = os.RemoveAll(dir) }()

	transport, err := hosts.NewTransport(host, dir)
	if err != nil {
		result.Detail = err.Error()
		return result
	}
	defer func() { _ = transport.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := transport.Command(ctx, transport.ServeCommand())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		result.Detail = err.Error()
		return result
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Start(); err != nil {
		result.Detail = err.Error()
		return result
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	counting := &countingReader{inner: stdout}
	var source = counting
	reader := teeIf(raw, source, env.Stdout)
	decoder := hostproto.NewDecoder(reader)

	for {
		msg, err := decoder.Next()
		if err != nil {
			result.Bytes = counting.count
			if result.Hello == nil {
				result.State, result.Detail = classifyProbeFailure(ctx, err, stderr.String(), counting.count)
				return result
			}
			// Data already arrived, so the stream simply ended. That is only a
			// failure if we never got what we came for.
			result.OK = result.Snapshots > 0
			if result.OK {
				result.State = probeStateOK
			} else {
				result.State, result.Detail = probeStateTimeout, strings.TrimSpace(stderr.String())
			}
			return result
		}
		switch msg.Kind {
		case hostproto.KindHello:
			result.Hello = msg.Hello
			result.HelloLatency = time.Since(start).Round(time.Millisecond).String()
			if !hostproto.Compatible(msg.Proto) {
				result.State = probeStateProtocol
				result.Detail = hostproto.IncompatibleMessage(host.ID, msg.Proto)
				result.Bytes = counting.count
				return result
			}
			if !msg.Hello.TmuxPresent {
				result.State = probeStateNoTmux
				result.Detail = "the host has sidecar but no tmux, so it has no sessions to observe"
				result.Bytes = counting.count
				return result
			}
		case hostproto.KindSnapshot:
			if result.Snapshots == 0 {
				result.FirstData = time.Since(start).Round(time.Millisecond).String()
			}
			result.Snapshots++
			result.Items = 0
			for _, project := range msg.Snapshot.Projects {
				result.Items += len(project.Items)
			}
			if result.Snapshots >= cycles {
				result.OK, result.State = true, probeStateOK
				result.Bytes = counting.count
				return result
			}
		case hostproto.KindEvent:
			result.Events++
		case hostproto.KindError:
			if msg.Error != nil && msg.Error.Fatal {
				result.OK = false
				result.State, result.Detail = msg.Error.Code, msg.Error.Message
				result.Bytes = counting.count
				return result
			}
		}
	}
}

// classifyProbeFailure turns a read failure into a named row state. The
// distinctions here are the ones a user can act on, and each was observed
// during the Phase 0 spike rather than imagined: a non-login ssh shell really
// does report a Homebrew-installed sidecar as "command not found", and a
// remote profile that echoes really does put a banner on the same pipe as the
// protocol.
func classifyProbeFailure(ctx context.Context, err error, stderr string, read int64) (string, string) {
	detail := strings.TrimSpace(stderr)
	lowered := strings.ToLower(detail)
	switch {
	// Anchored on the binary name. A bare "no such file or directory" also
	// comes from unrelated remote failures, and telling someone to install
	// sidecar when sidecar is running fine is a worse answer than not guessing.
	case strings.Contains(lowered, "command not found"),
		strings.Contains(lowered, "sidecar: no such file"),
		strings.Contains(lowered, "not found") && strings.Contains(lowered, "sidecar"):
		return probeStateNoSidecar, detail
	case strings.Contains(lowered, "permission denied"),
		strings.Contains(lowered, "could not resolve hostname"),
		strings.Contains(lowered, "connection refused"),
		strings.Contains(lowered, "connection timed out"),
		strings.Contains(lowered, "operation timed out"),
		strings.Contains(lowered, "host key verification failed"),
		strings.Contains(lowered, "no route to host"):
		return probeStateUnreachable, detail
	case strings.Contains(err.Error(), "unparseable line"):
		return probeStateNotProtocol, err.Error()
	case ctx.Err() != nil && read == 0:
		// Nothing ever arrived. ssh to a blackholed address produces no stderr
		// before the deadline, so a bare context timeout here means the
		// connection never came up — which is "unreachable", not "slow". Only
		// a host that sent some bytes and then stalled is a real timeout.
		return probeStateUnreachable, "no response from the host before the deadline"
	case ctx.Err() != nil:
		return probeStateTimeout, detail
	case detail != "":
		return probeStateUnreachable, detail
	default:
		return probeStateUnreachable, err.Error()
	}
}

type countingReader struct {
	inner interface{ Read([]byte) (int, error) }
	count int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	r.count += int64(n)
	return n, err
}

func teeIf(enabled bool, source *countingReader, sink interface{ Write([]byte) (int, error) }) interface {
	Read([]byte) (int, error)
} {
	if !enabled {
		return source
	}
	return &teeReader{source: source, sink: sink}
}

type teeReader struct {
	source *countingReader
	sink   interface{ Write([]byte) (int, error) }
}

func (r *teeReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if n > 0 {
		_, _ = r.sink.Write(p[:n])
	}
	return n, err
}
