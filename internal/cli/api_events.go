package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

func apiEventsCommand() *Command {
	return &Command{Name: "events", Summary: "Stream Sessions and attention as JSONL", Usage: "sidecar api events --stdio [--sort MODE] [--search TEXT] [--host ID] [--provider ID] [--state STATE] [--show-idle-sessions true|false]",
		Long:      "Stream the running UI API's events over its Local socket. One JSON object per line, with hello first, then catalog, attention, terminal attachment changes and shutdown. Use SSH without a PTY for native clients. The API server must already be running; this command never starts a service or tmux.",
		Flags:     []Flag{{Name: "--stdio", Summary: "Write the event stream as JSONL until disconnect or shutdown", Bool: true}, {Name: "--sort", Arg: "MODE", Summary: "activity, project, recent or name"}, {Name: "--search", Arg: "TEXT", Summary: "Search Sessions"}, {Name: "--host", Arg: "ID", Summary: "Filter host (repeatable)"}, {Name: "--provider", Arg: "ID", Summary: "Filter provider (repeatable)"}, {Name: "--state", Arg: "STATE", Summary: "Filter state (repeatable)"}, {Name: "--show-idle-sessions", Arg: "BOOL", Summary: "Include No Session rows"}, {Name: "--help", Short: "-h", Bool: true, Summary: "Show this help"}},
		ExitCodes: []ExitCode{{Code: 0, Summary: "stream stopped normally"}, {Code: 1, Summary: "API unavailable or stream failed"}, {Code: 2, Summary: "usage error"}},
		Examples:  []Example{{Command: "sidecar api events --stdio"}, {Command: "sidecar api events --stdio --sort activity --show-idle-sessions false"}},
		Agent:     AgentDoc{Invocation: "sidecar api events --stdio", Summary: "Read live Sessions and attention without polling"}, Run: runAPIEvents}
}

func catalogQueryValues(q mobileproto.CatalogQuery) url.Values {
	v := url.Values{}
	if q.Sort != "" {
		v.Set("sort", q.Sort)
	}
	if q.Search != "" {
		v.Set("search", q.Search)
	}
	for _, h := range q.Hosts {
		v.Add("host", h)
	}
	for _, p := range q.Providers {
		v.Add("provider", p)
	}
	for _, s := range q.States {
		v.Add("state", s)
	}
	if q.ShowIdleSessions != nil {
		v.Set("show_idle_sessions", strconv.FormatBool(*q.ShowIdleSessions))
	}
	return v
}

func runAPIEvents(env Env, args []string) int {
	cmd := apiSubcommand("events")
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	stdio := false
	filters := []string{"--json"}
	for _, arg := range args {
		if arg == "--stdio" {
			stdio = true
		} else {
			filters = append(filters, arg)
		}
	}
	if !stdio {
		cliErrf(env.Stderr, "--stdio is required\n\n%s", RenderHelp(cmd))
		return 2
	}
	query, code := parseMobileCatalogArgs(env, filters, RenderHelp(cmd))
	if code != 0 {
		return code
	}
	if err := mobile.ValidateCatalogQuery(query); err != nil {
		cliErrln(env.Stderr, err)
		return 2
	}
	client, code := apiLocalClient(env)
	if client == nil {
		return code
	}
	base := env.Ctx
	if base == nil {
		base = context.Background()
	}
	ctx, stop := signal.NotifyContext(base, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	u := strings.Replace(client.URL("/api/v0/events"), "http:", "ws:", 1)
	if q := catalogQueryValues(query).Encode(); q != "" {
		u += "?" + q
	}
	conn, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: client.HTTPClient()})
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(mobileproto.MaxLineBytes)
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			status := websocket.CloseStatus(err)
			if ctx.Err() != nil || status == websocket.StatusNormalClosure || status == 4409 {
				return 0
			}
			cliErrln(env.Stderr, err)
			return 1
		}
		if kind != websocket.MessageText {
			cliErrln(env.Stderr, "events server sent a binary frame")
			return 1
		}
		if _, err = fmt.Fprintln(env.Stdout, string(data)); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
	}
}
