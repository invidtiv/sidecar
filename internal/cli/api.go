package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/marcus/sidecar/internal/apiservice"
	"github.com/marcus/sidecar/internal/buildinfo"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/uiapi"
)

const apiShutdownTimeout = 5 * time.Second

// apiTailnetIdentity is the Tailscale adapter seam; tests replace it.
var apiTailnetIdentity uiapi.TailnetIdentityFunc = uiapi.TailscaleStatusIdentity

// apiOpenBrowser opens a URL in the default browser; tests replace it.
var apiOpenBrowser = func(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("no browser opener for %s", runtime.GOOS)
	}
	return cmd.Run()
}

func apiCommand() *Command {
	help := Flag{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}
	jsonFlag := Flag{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true}
	serve := &Command{
		Name: "serve", Summary: "Serve the UI API on this machine", Usage: "sidecar api serve [--port N] [--ui DIR] [--fixtures DIR] [--tailnet] [--tailnet-mode direct|serve] [--tailnet-port N] [--json]",
		Long: "Run the UI API server in the foreground until interrupted. It listens on a Unix socket in the state directory (local agents and the CLI, no auth) and on 127.0.0.1 for browsers (paired with `sidecar api open` or `sidecar api pair`). --tailnet adds the Tailnet listener, trusting only allowed tailnet logins (config api.tailnetLogins, default the node owner). " +
			"In direct mode, the default (config api.tailnetMode or --tailnet-mode), Sidecar binds this node's tailnet addresses itself on api.tailnetHTTPSPort (default 7861), serves HTTPS with a `tailscale cert` certificate, and identifies each connection with `tailscale whois`: only an allowed login's untagged devices get in, connections from this machine's own tailnet address are refused, and Tailscale-User-Login headers are ignored. No `tailscale serve` route is needed; one that holds the same port is refused with the command that removes it. When Tailscale is down or its addresses change, it retries in the background and `sidecar api status` reports the state. " +
			"Serve mode (--tailnet-mode serve) instead creates a second Unix socket for `tailscale serve`, which vouches for the login with a header, and prints the `tailscale serve` command to run; there api.tailnetHTTPSPort is the public Serve port (default 443). Neither mode changes Tailscale configuration. --tailnet-port N (serve mode only) serves that listener on a dedicated loopback port instead, for a tailscaled that cannot open a 0600 user socket; any local process or OS user can reach that port and claim an allowed tailnet login, so use it only on a machine where every local user and process is already trusted. " +
			"It records itself in $STATE/api/endpoint.json and refuses to start while another server owns the same state tree. It never starts or stops tmux. Each terminal WebSocket is one mobile protocol v0 stream, served exactly as `sidecar mobile serve --stdio` serves stdin. --fixtures requires SIDECAR_ISOLATED_STATE=1 and temporary XDG_STATE_HOME and -config paths; it refuses real state/config paths, including symlink aliases. --json writes the endpoint object as one line once every listener is bound.",
		Flags: []Flag{{Name: "--port", Arg: "N", Summary: "Browser listener port on 127.0.0.1 (default 7861; 0 picks a free port)"},
			{Name: "--fixtures", Arg: "DIR", Summary: "Serve recorded Sessions/status and deterministic echo terminals without tmux"},
			{Name: "--ui", Arg: "DIR", Summary: "Serve a built UI from DIR (overrides config api.uiDir), with index.html as the fallback for app routes"},
			{Name: "--tailnet", Summary: "Also serve the Tailnet listener (direct mode unless api.tailnetMode says serve)", Bool: true},
			{Name: "--tailnet-mode", Arg: "MODE", Summary: "direct (bind the tailnet address, the default) or serve (a socket for tailscale serve); implies --tailnet"},
			{Name: "--tailnet-port", Arg: "N", Summary: "Serve mode: serve the tailnet listener on this loopback port instead of a Unix socket"},
			{Name: "--json", Summary: "Write the endpoint object as one JSON line once listening", Bool: true}, help},
		ExitCodes: []ExitCode{{Code: 0, Summary: "stopped normally"}, {Code: 1, Summary: "could not start, or a listener failed"}, {Code: 2, Summary: "usage error"}},
		Examples:  []Example{{Command: "sidecar api serve"}, {Command: "sidecar api serve --ui ~/code/sidecar-ui/apps/sidecar-ui/build"}, {Command: "sidecar api serve --tailnet"}, {Command: "sidecar api serve --tailnet-mode serve"}},
		Agent:     AgentDoc{Invocation: "sidecar api serve", Summary: "Serve Sessions and live terminals over HTTP and WebSocket for a web UI"},
		Mutates:   true, Run: runAPIServe,
	}
	open := &Command{
		Name: "open", Summary: "Pair this machine's browser and open the UI", Usage: "sidecar api open [--print] [--proxy] [--path P]",
		Long:      "Ask the running server for a single-use pairing link (valid for 60 seconds) and open it in the default browser. The code rides in the link's fragment, so it never appears in a request line; the pairing page registers a non-extractable WebCrypto key in that origin's IndexedDB and goes to --path. Only the public key persists on the server, with a 30-day sliding expiry and a 180-day absolute cap. The browser signs a fresh nonce after each restart to obtain a 15-minute memory-only bearer; legacy localStorage tokens are cleared. Pairing again leaves other tabs valid. --print writes the link instead of opening it. --proxy uses the running server's configured api.browserProxyOrigin so you can pair a remote browser through the HTTPS proxy.",
		Flags:     []Flag{{Name: "--print", Summary: "Print the pairing URL instead of opening a browser", Bool: true}, {Name: "--proxy", Summary: "Use the configured HTTPS browser proxy origin", Bool: true}, {Name: "--path", Arg: "P", Summary: "Path to land on after pairing (default /)"}, help},
		ExitCodes: []ExitCode{{Code: 0, Summary: "success"}, {Code: 1, Summary: "no server running or the server refused"}, {Code: 2, Summary: "usage error"}},
		Examples:  []Example{{Command: "sidecar api open"}, {Command: "sidecar api open --print"}},
		Run:       runAPIOpen,
	}
	pair := &Command{
		Name: "pair", Summary: "Manage paired origins and browser sessions", Usage: "sidecar api pair --origin URL [--scope SCOPE] [--scopes LIST] | --list | --revoke URL | --revoke-sessions [--origin URL] [--json]",
		Long: "Register another web origin (an app embedding Sidecar components) and print its bearer token, which is shown only once and stored only as a hash in $STATE/api/origins.json. Pairing an origin again rotates its token and closes the terminals and event streams the old token opened. --scope selects a paired origin scope, repeatable; --scopes accepts a comma-separated list. Both accept full, workspace:write, content:read and ui:control and may be combined. Full is the default. --list shows registrations without tokens; --revoke removes one. " +
			"--revoke-sessions signs out every browser paired with `sidecar api open` without restarting the server: their session tokens get 401 from then on and their open terminals close with 4401. With --origin it signs out only the browsers on that origin. Revocation persists across API restarts. Paired origins keep their tokens; --revoke URL also revokes browser sessions bound to that exact origin.",
		Flags: []Flag{{Name: "--origin", Arg: "URL", Summary: "Pair this origin (scheme://host[:port]); with --revoke-sessions, the origin to sign out"}, {Name: "--scope", Arg: "SCOPE", Summary: "Paired origin scope, repeatable (default full)"}, {Name: "--scopes", Arg: "LIST", Summary: "Comma-separated scopes (full, workspace:write, content:read or ui:control)"}, {Name: "--list", Summary: "List paired origins", Bool: true}, {Name: "--revoke", Arg: "URL", Summary: "Revoke a paired origin"},
			{Name: "--revoke-sessions", Summary: "Sign out browser sessions from `sidecar api open`", Bool: true}, jsonFlag, help},
		ExitCodes: []ExitCode{{Code: 0, Summary: "success"}, {Code: 1, Summary: "no server running or the server refused"}, {Code: 2, Summary: "usage error"}},
		Examples: []Example{{Command: "sidecar api pair --origin http://localhost:5173"}, {Command: "sidecar api pair --list --json"}, {Command: "sidecar api pair --revoke http://localhost:5173"},
			{Command: "sidecar api pair --revoke-sessions"}, {Command: "sidecar api pair --revoke-sessions --origin http://127.0.0.1:7861 --json"}},
		Mutates: true, Run: runAPIPair,
	}
	status := &Command{
		Name: "status", Summary: "Report the running UI API server", Usage: "sidecar api status [--json]",
		Long:      "Read the status route over the local socket: UI directory, listeners, connected clients, and open terminal attachments with whether each holds control. Exits 1 when no server is running.",
		Flags:     []Flag{jsonFlag, help},
		ExitCodes: []ExitCode{{Code: 0, Summary: "success"}, {Code: 1, Summary: "no server running"}, {Code: 2, Summary: "usage error"}},
		Examples:  []Example{{Command: "sidecar api status"}, {Command: "sidecar api status --json"}},
		Agent:     AgentDoc{Invocation: "sidecar api status --json", Summary: "See whether the UI API is up, who is connected, and who holds terminal control"},
		Run:       runAPIStatus,
	}
	return &Command{Name: "api", Summary: "Serve Sidecar's UI API for web and embedded clients", Usage: "sidecar api <command>",
		Long: "The UI API exposes Sessions and live terminals over HTTP and WebSocket so a web UI, an embedding app, or an agent can use them. The wire contract is docs/reference/ui-api.md.",
		Sub:  []*Command{apiEventsCommand(), open, pair, serve, apiServiceCommand(), status, apiSpecCommand()}, Run: runAPIRoot}
}

func runAPIRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("api")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	sub := cmd.FindSubcommand(args[0])
	if sub == nil || sub.Run == nil {
		cliErrf(env.Stderr, "unknown api command %q\n\n%s", args[0], RenderHelp(cmd))
		return 2
	}
	return sub.Run(env, args[1:])
}

// apiFlags is a small parser for the api verbs: boolean flags and
// value flags given as `--name value` or `--name=value`.
type apiFlags struct {
	bools    map[string]bool
	values   map[string]string
	repeated map[string][]string
}

func parseAPIFlags(args []string, boolNames, valueNames []string, repeatNames ...string) (apiFlags, error) {
	flags := apiFlags{bools: map[string]bool{}, values: map[string]string{}, repeated: map[string][]string{}}
	isBool := map[string]bool{}
	for _, name := range boolNames {
		isBool[name] = true
	}
	isValue := map[string]bool{}
	for _, name := range valueNames {
		isValue[name] = true
	}
	isRepeated := map[string]bool{}
	for _, name := range repeatNames {
		isRepeated[name] = true
		isValue[name] = true
	}
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		switch {
		case isBool[name] && !hasValue:
			flags.bools[name] = true
		case isValue[name]:
			if !hasValue {
				if i+1 >= len(args) {
					return flags, fmt.Errorf("%s requires a value", name)
				}
				i++
				value = args[i]
			}
			if isRepeated[name] {
				flags.repeated[name] = append(flags.repeated[name], value)
				continue
			}
			if _, seen := flags.values[name]; seen {
				return flags, fmt.Errorf("%s was given more than once", name)
			}
			flags.values[name] = value
		default:
			return flags, fmt.Errorf("unknown option %q", args[i])
		}
	}
	return flags, nil
}

func apiSubcommand(name string) *Command {
	return RootCommand().FindSubcommand("api").FindSubcommand(name)
}

func runAPIServe(env Env, args []string) int {
	cmd := apiSubcommand("serve")
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	flags, err := parseAPIFlags(args, []string{"--tailnet", "--json"}, []string{"--port", "--ui", "--tailnet-port", "--tailnet-mode", "--fixtures"})
	if err != nil {
		cliErrf(env.Stderr, "%v\n\n%s", err, RenderHelp(cmd))
		return 2
	}
	port := uiapi.DefaultPort
	if raw, ok := flags.values["--port"]; ok {
		if port, err = strconv.Atoi(raw); err != nil || port < 0 || port > 65535 {
			cliErrf(env.Stderr, "--port must be a number from 0 to 65535\n\n%s", RenderHelp(cmd))
			return 2
		}
	}
	tailnetPort := 0
	if raw, ok := flags.values["--tailnet-port"]; ok {
		if tailnetPort, err = strconv.Atoi(raw); err != nil || tailnetPort < 1 || tailnetPort > 65535 {
			cliErrf(env.Stderr, "--tailnet-port must be a number from 1 to 65535\n\n%s", RenderHelp(cmd))
			return 2
		}
	}
	if _, fixtures := flags.values["--fixtures"]; fixtures {
		if err := checkAPIFixtureIsolation(env.StateDir); err != nil {
			cliErrf(env.Stderr, "fixture isolation: %v\n", err)
			return 1
		}
	}
	var explicitMode uiapi.TailnetMode
	if raw, ok := flags.values["--tailnet-mode"]; ok {
		if explicitMode, err = uiapi.ParseTailnetMode(raw); err != nil {
			cliErrf(env.Stderr, "--tailnet-mode must be direct or serve\n\n%s", RenderHelp(cmd))
			return 2
		}
	}
	if explicitMode == uiapi.TailnetModeDirect && tailnetPort > 0 {
		cliErrf(env.Stderr, "--tailnet-port is the serve-mode loopback fallback; direct mode binds the tailnet address itself\n\n%s", RenderHelp(cmd))
		return 2
	}
	withTailnet := flags.bools["--tailnet"] || tailnetPort > 0 || explicitMode != ""

	// As for `mobile serve`: an inherited TMUX would address the hosting
	// server instead of the configured Sidecar namespace.
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	base := env.Ctx
	if base == nil {
		base = context.Background()
	}
	ctx, stop := signal.NotifyContext(base, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		cliErrf(env.Stderr, "load API config: %v; fix %s and retry\n", err, config.ConfigPath())
		return 1
	}
	var tailnet *uiapi.TailnetOptions
	if withTailnet {
		mode, modeErr := resolveTailnetMode(explicitMode, cfg.API.TailnetMode, tailnetPort)
		if modeErr != nil {
			cliErrln(env.Stderr, modeErr)
			return 1
		}
		tailnet, err = apiTailnetOptions(ctx, tailnetPort, mode)
		if err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
	}
	uiDir := cfg.API.UIDir
	if explicit, ok := flags.values["--ui"]; ok {
		uiDir = explicit
	}
	inherited, err := apiservice.Activate()
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	defer apiservice.CloseActivated(inherited)
	if inherited == nil {
		inherited = []apiservice.ActivatedListener{}
	}
	executable, err := apiExecutablePath()
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	changed, err := uiapi.WatchExecutable(ctx, executable, time.Second)
	if err != nil {
		cliErrf(env.Stderr, "watch executable: %v; reinstall Sidecar and retry\n", err)
		return 1
	}
	var backend uiapi.Backend
	var fixtureStatus *uiapi.Status
	if dir, fixtures := flags.values["--fixtures"]; fixtures {
		if dir == "" {
			cliErrln(env.Stderr, "--fixtures requires a directory")
			return 2
		}
		fixture, loadErr := uiapi.LoadFixtures(dir)
		if loadErr != nil {
			cliErrln(env.Stderr, loadErr)
			return 1
		}
		backend, fixtureStatus = fixture, &fixture.Status
	} else {
		live, loadErr := newMobileBackend(ctx, env)
		if loadErr != nil {
			cliErrln(env.Stderr, loadErr)
			return 1
		}
		defer live.Close()
		backend = live
	}
	server, err := uiapi.Start(uiapi.Options{StateDir: env.StateDir, Port: port, UIDir: uiDir, Tailnet: tailnet, BrowserProxyOrigin: cfg.API.BrowserProxyOrigin,
		Backend: backend, Version: buildinfo.Version(), FixtureStatus: fixtureStatus, Inherited: inherited, Logf: apiServeLogf(env.Stderr)})
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	endpoint := server.Endpoint()
	if flags.bools["--json"] {
		data, _ := json.Marshal(endpoint)
		_, _ = fmt.Fprintln(env.Stdout, string(data))
		if tailnet != nil && tailnet.Mode == uiapi.TailnetModeDirect {
			printTailnetDirect(server.TailnetStatus(), env.Stderr)
		} else if tailnet != nil {
			printTailnetHint(endpoint, tailnet, env.Stderr)
		}
	} else {
		_, _ = fmt.Fprintf(env.Stdout, "Sidecar UI API v%d serving (pid %d)\n  local    %s\n  browser  %s\n", uiapi.APIVersion, endpoint.PID, endpoint.UnixSocket, server.BrowserURL())
		if tailnet != nil && tailnet.Mode == uiapi.TailnetModeDirect {
			printTailnetDirect(server.TailnetStatus(), env.Stdout)
		} else if tailnet != nil {
			printTailnetHint(endpoint, tailnet, env.Stdout)
		}
		_, _ = fmt.Fprintln(env.Stdout, "Pair a browser with `sidecar api open`. Press Ctrl-C to stop.")
	}

	code := 0
	select {
	case <-ctx.Done():
	case <-changed:
		_, _ = fmt.Fprintf(env.Stderr, "Sidecar executable changed at %s; shutting down cleanly so the service manager can restart the API. tmux is unchanged.\n", executable)
	case err := <-server.Failed():
		cliErrln(env.Stderr, err)
		code = 1
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), apiShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		cliErrf(env.Stderr, "shutdown: %v\n", err)
		code = 1
	}
	return code
}

// resolveTailnetMode picks the Tailnet listener's mode: the flag, then
// api.tailnetMode, then serve when only the serve-mode --tailnet-port was
// given, and otherwise direct.
func resolveTailnetMode(explicit uiapi.TailnetMode, configured string, tailnetPort int) (uiapi.TailnetMode, error) {
	mode := explicit
	if mode == "" && configured != "" {
		parsed, err := uiapi.ParseTailnetMode(configured)
		if err != nil {
			return "", fmt.Errorf("api.tailnetMode must be \"direct\" or \"serve\", not %q; fix %s and retry", configured, config.ConfigPath())
		}
		mode = parsed
	}
	if mode == "" {
		mode = uiapi.TailnetModeDirect
		if tailnetPort > 0 {
			mode = uiapi.TailnetModeServe
		}
	}
	if mode == uiapi.TailnetModeDirect && tailnetPort > 0 {
		return "", errors.New("--tailnet-port is the serve-mode loopback fallback, but api.tailnetMode is direct; drop --tailnet-port or pass --tailnet-mode serve")
	}
	return mode, nil
}

// apiServeLogf writes server log lines (Tailnet listener state changes among
// them) to stderr, which the service manager keeps in its log.
func apiServeLogf(out io.Writer) func(string, ...any) {
	var mu sync.Mutex
	return func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = fmt.Fprintf(out, "sidecar api: "+format+"\n", args...)
	}
}

func apiTailnetOptions(ctx context.Context, port int, mode uiapi.TailnetMode) (*uiapi.TailnetOptions, error) {
	if mode == uiapi.TailnetModeDirect {
		// Direct mode discovers the node, and its owner when no logins are
		// configured, in the background, so Tailscale being down at start
		// delays only the Tailnet listener.
		cfg, err := config.Load()
		if err != nil {
			return nil, err
		}
		if cfg.API.TailnetHTTPSPort < 0 || cfg.API.TailnetHTTPSPort > 65535 {
			return nil, errors.New("api.tailnetHTTPSPort must be a number from 1 to 65535, or 0 for the default 7861")
		}
		return &uiapi.TailnetOptions{Mode: uiapi.TailnetModeDirect, Logins: append([]string(nil), cfg.API.TailnetLogins...), HTTPSPort: cfg.API.TailnetHTTPSPort}, nil
	}
	identityCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	identity, err := apiTailnetIdentity(identityCtx)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	logins := append([]string(nil), cfg.API.TailnetLogins...)
	if len(logins) == 0 && identity.OwnerLogin != "" {
		logins = []string{identity.OwnerLogin}
	}
	if len(logins) == 0 {
		return nil, errors.New("--tailnet found no owner login for this node; set api.tailnetLogins in the Sidecar config")
	}
	options := &uiapi.TailnetOptions{Mode: uiapi.TailnetModeServe, Host: identity.Host, Logins: logins, Port: port, HTTPSPort: cfg.API.TailnetHTTPSPort}
	if _, err := options.HTTPSURL(); err != nil {
		return nil, err
	}
	return options, nil
}

// tailnetPortWarning says what --tailnet-port gives up. The Tailnet listener
// trusts the Tailscale-User-Login header because only tailscaled can reach its
// 0600 socket; nothing stops another local process from sending that header
// to a loopback port.
const tailnetPortWarning = "Warning: any local process or OS user can reach this port and claim an allowed tailnet login, which drives your terminals. Use --tailnet-port only where every local user and process is already trusted."

// printTailnetDirect reports the direct-mode listener as Start left it: up,
// or why not yet and that it keeps retrying.
func printTailnetDirect(status *uiapi.TailnetStatus, out io.Writer) {
	if status == nil {
		return
	}
	if status.State == uiapi.TailnetStateListening {
		_, _ = fmt.Fprintf(out, "  tailnet  %s (direct on %s; logins %s)\n", status.Origin, strings.Join(status.Addresses, ", "), strings.Join(status.Logins, ", "))
		return
	}
	message := status.Message
	if message == "" {
		message = "still starting; check `sidecar api status`."
	}
	_, _ = fmt.Fprintf(out, "  tailnet  %s (direct): %s\n", status.State, message)
}

func printTailnetHint(endpoint uiapi.Endpoint, tailnet *uiapi.TailnetOptions, out io.Writer) {
	publicURL, _ := tailnet.HTTPSURL()
	httpsFlag := ""
	if tailnet.HTTPSPort != 0 && tailnet.HTTPSPort != 443 {
		httpsFlag = fmt.Sprintf(" --https=%d", tailnet.HTTPSPort)
	}
	if endpoint.TailnetTCP != "" {
		_, _ = fmt.Fprintf(out, "  tailnet  %s (%s; logins %s)\nExpose it on the tailnet with:\n  tailscale serve --bg%s http://%s\n%s\n",
			endpoint.TailnetTCP, publicURL, strings.Join(tailnet.Logins, ", "), httpsFlag, endpoint.TailnetTCP, tailnetPortWarning)
		return
	}
	_, _ = fmt.Fprintf(out, "  tailnet  %s (%s; logins %s)\nExpose it on the tailnet with:\n  tailscale serve --bg%s unix:%s\n"+
		"If tailscaled cannot open that 0600 socket (a sandboxed Tailscale build), restart with --tailnet-port N and run:\n  tailscale serve --bg%s http://127.0.0.1:N\n",
		endpoint.TailnetSocket, publicURL, strings.Join(tailnet.Logins, ", "), httpsFlag, endpoint.TailnetSocket, httpsFlag)
}

func apiLocalClient(env Env) (*uiapi.LocalClient, int) {
	client, err := uiapi.NewLocalClient(env.StateDir)
	if err != nil {
		cliErrln(env.Stderr, err)
		return nil, 1
	}
	return client, 0
}

func apiContext(env Env) (context.Context, context.CancelFunc) {
	base := env.Ctx
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(base, 30*time.Second)
}

func runAPIOpen(env Env, args []string) int {
	cmd := apiSubcommand("open")
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	flags, err := parseAPIFlags(args, []string{"--print", "--proxy"}, []string{"--path"})
	if err != nil {
		cliErrf(env.Stderr, "%v\n\n%s", err, RenderHelp(cmd))
		return 2
	}
	client, code := apiLocalClient(env)
	if client == nil {
		return code
	}
	ctx, cancel := apiContext(env)
	defer cancel()
	if flags.bools["--proxy"] && client.Endpoint.BrowserProxyOrigin == "" {
		cliErrln(env.Stderr, "The running server has no api.browserProxyOrigin; configure it and restart the API first.")
		return 1
	}
	pairing, err := client.PairingCode(ctx, flags.values["--path"])
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	if flags.bools["--proxy"] {
		localOrigin := "http://" + client.Endpoint.TCP
		if !strings.HasPrefix(pairing.URL, localOrigin+"/pair#") {
			cliErrln(env.Stderr, "The server returned an unexpected pairing URL.")
			return 1
		}
		pairing.URL = client.Endpoint.BrowserProxyOrigin + strings.TrimPrefix(pairing.URL, localOrigin)
	}
	if flags.bools["--print"] {
		_, _ = fmt.Fprintln(env.Stdout, pairing.URL)
		return 0
	}
	if err := apiOpenBrowser(pairing.URL); err != nil {
		cliErrf(env.Stderr, "could not open a browser (%v); open this link within 60 seconds:\n%s\n", err, pairing.URL)
		return 1
	}
	_, _ = fmt.Fprintf(env.Stdout, "Opened %s in your browser. The link works once, for 60 seconds.\n", strings.SplitN(pairing.URL, "/pair#", 2)[0])
	return 0
}

func runAPIPair(env Env, args []string) int {
	cmd := apiSubcommand("pair")
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	flags, err := parseAPIFlags(args, []string{"--list", "--json", "--revoke-sessions"}, []string{"--origin", "--revoke", "--scopes"}, "--scope")
	if err != nil {
		cliErrf(env.Stderr, "%v\n\n%s", err, RenderHelp(cmd))
		return 2
	}
	// With --revoke-sessions, --origin narrows the revocation instead of
	// pairing an origin.
	revokingSessions := flags.bools["--revoke-sessions"]
	_, pairing := flags.values["--origin"]
	pairing = pairing && !revokingSessions
	_, revoking := flags.values["--revoke"]
	modes := 0
	for _, on := range []bool{pairing, revoking, flags.bools["--list"], revokingSessions} {
		if on {
			modes++
		}
	}
	if len(flags.repeated["--scope"]) > 0 && !pairing {
		cliErrln(env.Stderr, "--scope requires origin pairing")
		return 2
	}
	if modes != 1 {
		cliErrf(env.Stderr, "give exactly one of --origin, --list, --revoke or --revoke-sessions\n\n%s", RenderHelp(cmd))
		return 2
	}
	if scopes, ok := flags.values["--scopes"]; ok && (!pairing || strings.TrimSpace(scopes) == "") {
		cliErrln(env.Stderr, "--scopes requires --origin pairing and a nonempty list")
		return 2
	}
	client, code := apiLocalClient(env)
	if client == nil {
		return code
	}
	ctx, cancel := apiContext(env)
	defer cancel()
	asJSON := flags.bools["--json"]
	switch {
	case revokingSessions:
		revocation, err := client.RevokeSessions(ctx, flags.values["--origin"])
		if err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		if asJSON {
			return writeCLIJSON(env, revocation)
		}
		scope := "every origin"
		if revocation.Origin != "" {
			scope = revocation.Origin
		}
		_, _ = fmt.Fprintf(env.Stdout, "Signed out %d browser session(s) on %s and closed %d terminal(s). Pair again with `sidecar api open`.\n",
			revocation.Revoked, scope, revocation.TerminalsClosed)
	case pairing:
		scopes := append([]string(nil), flags.repeated["--scope"]...)
		if raw, ok := flags.values["--scopes"]; ok {
			for _, scope := range strings.Split(raw, ",") {
				scopes = append(scopes, strings.TrimSpace(scope))
			}
		}
		registration, err := client.PairOrigin(ctx, flags.values["--origin"], scopes...)
		if err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		if asJSON {
			return writeCLIJSON(env, registration)
		}
		_, _ = fmt.Fprintf(env.Stdout, "Paired %s (scopes: %s)\nToken, shown only this once:\n%s\nSend it as `Authorization: Bearer <token>`; WebSockets use a ticket from POST /api/v0/ws-tickets.\n",
			registration.Origin, strings.Join(registration.Scopes, ", "), registration.Token)
	case revoking:
		revocation, err := client.RevokeOrigin(ctx, flags.values["--revoke"])
		if err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		if asJSON {
			return writeCLIJSON(env, revocation)
		}
		_, _ = fmt.Fprintf(env.Stdout, "Revoked %s\n", revocation.Origin)
	default:
		list, err := client.ListOrigins(ctx)
		if err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		if asJSON {
			return writeCLIJSON(env, list)
		}
		if len(list.Origins) == 0 {
			_, _ = fmt.Fprintln(env.Stdout, "No paired origins. Pair one with `sidecar api pair --origin URL`.")
			return 0
		}
		for _, origin := range list.Origins {
			_, _ = fmt.Fprintf(env.Stdout, "%s  %s  paired %s\n", origin.Origin, strings.Join(origin.Scopes, ","), origin.CreatedAt.Local().Format(time.RFC3339))
		}
	}
	return 0
}

func runAPIStatus(env Env, args []string) int {
	cmd := apiSubcommand("status")
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	flags, err := parseAPIFlags(args, []string{"--json"}, nil)
	if err != nil {
		cliErrf(env.Stderr, "%v\n\n%s", err, RenderHelp(cmd))
		return 2
	}
	client, code := apiLocalClient(env)
	if client == nil {
		return code
	}
	ctx, cancel := apiContext(env)
	defer cancel()
	if flags.bools["--json"] {
		// Pass the route's bytes through: the CLI and HTTP documents are one.
		var raw json.RawMessage
		if err := client.Do(ctx, "GET", "/api/v0/status", nil, &raw); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		_, _ = env.Stdout.Write(raw)
		return 0
	}
	status, err := client.Status(ctx)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(env.Stdout, "Sidecar UI API v%d, server %s, pid %d, up since %s\n", status.APIVersion, status.ServerVersion, status.PID, status.StartedAt.Local().Format(time.RFC3339))
	if status.UIDir != nil {
		printAPIUIDir(env, *status.UIDir)
	}
	for _, listener := range status.Listeners {
		line := fmt.Sprintf("  %-8s %s %s", listener.Name, listener.Network, listener.Address)
		if listener.Host != "" {
			line += " (" + listener.Host + ")"
		}
		_, _ = fmt.Fprintln(env.Stdout, line)
	}
	if tailnet := status.Tailnet; tailnet != nil {
		line := fmt.Sprintf("Tailnet (%s): %s", tailnet.Mode, tailnet.State)
		if tailnet.Origin != "" {
			line += " " + tailnet.Origin
		}
		if len(tailnet.Logins) > 0 {
			line += "; logins " + strings.Join(tailnet.Logins, ", ")
		}
		if tailnet.CertificateExpiresAt != nil {
			line += "; certificate until " + tailnet.CertificateExpiresAt.Local().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintln(env.Stdout, line)
		if tailnet.Message != "" {
			_, _ = fmt.Fprintln(env.Stdout, "  "+tailnet.Message)
		}
	}
	_, _ = fmt.Fprintf(env.Stdout, "%d connected client(s)\n", len(status.Clients))
	for _, client := range status.Clients {
		who := client.Origin
		if client.Login != "" {
			who = client.Login
		}
		_, _ = fmt.Fprintf(env.Stdout, "  %s %s via %s (%s) %s since %s\n", client.ID, client.Kind, client.Listener, client.Auth, who, client.Since.Local().Format(time.RFC3339))
	}
	for _, terminal := range status.Terminals {
		holder := "viewing"
		if terminal.Control {
			holder = "holds control"
		}
		name := terminal.DisplayName
		if name == "" {
			name = terminal.Session
		}
		_, _ = fmt.Fprintf(env.Stdout, "  %s attached to %s (%s %s): %s\n", terminal.ClientID, name, terminal.Session, terminal.Pane, holder)
	}
	return 0
}

func writeCLIJSON(env Env, value any) int {
	if err := json.NewEncoder(env.Stdout).Encode(value); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}
