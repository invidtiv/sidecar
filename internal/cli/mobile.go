package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tty"
)

func mobileCommand() *Command {
	status := &Command{
		Name: "status", Summary: "Report the local mobile terminal protocol", Usage: "sidecar mobile status --json",
		Flags:     []Flag{{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true}, {Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}},
		ExitCodes: []ExitCode{{Code: 0, Summary: "success"}, {Code: 2, Summary: "usage error"}}, Run: runMobileStatus,
		Examples: []Example{{Command: "sidecar mobile status --json"}},
	}
	serve := &Command{
		Name: "serve", Summary: "Serve one bounded mobile terminal protocol stream", Usage: "sidecar mobile serve --stdio",
		Long:      "Read versioned JSONL requests from stdin and write JSONL responses and terminal frames to stdout. The service resolves only Sidecar-managed local sessions and refuses multi-pane layouts.",
		Flags:     []Flag{{Name: "--stdio", Summary: "Use stdin and stdout for the protocol", Bool: true}, {Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}},
		ExitCodes: []ExitCode{{Code: 0, Summary: "stream closed normally"}, {Code: 1, Summary: "service failed"}, {Code: 2, Summary: "usage error"}},
		Examples:  []Example{{Command: "sidecar mobile serve --stdio"}},
		Mutates:   true, Run: runMobileServe,
	}
	return &Command{Name: "mobile", Summary: "Serve the native mobile terminal client", Usage: "sidecar mobile <command>", Sub: []*Command{serve, status}, Run: runMobileRoot}
}

func runMobileRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("mobile")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	sub := cmd.FindSubcommand(args[0])
	if sub == nil || sub.Run == nil {
		cliErrf(env.Stderr, "unknown mobile command %q\n\n%s", args[0], RenderHelp(cmd))
		return 2
	}
	return sub.Run(env, args[1:])
}

func runMobileStatus(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("mobile").FindSubcommand("status")
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	if len(args) != 1 || args[0] != "--json" {
		cliErrf(env.Stderr, "--json is required\n\n%s", RenderHelp(cmd))
		return 2
	}
	host, _ := os.Hostname()
	result := struct {
		Version      int                      `json:"version"`
		HubID        string                   `json:"hub_id"`
		Capabilities mobileproto.Capabilities `json:"capabilities"`
	}{mobileproto.Version, host, mobileproto.DefaultCapabilities()}
	if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}

func runMobileServe(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("mobile").FindSubcommand("serve")
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	if len(args) != 1 || args[0] != "--stdio" {
		cliErrf(env.Stderr, "--stdio is required\n\n%s", RenderHelp(cmd))
		return 2
	}
	// CLI dispatch precedes main's tmux cleanup. A remote shell commonly
	// inherits both variables; allowing them through would address its hosting
	// server instead of the configured Sidecar namespace.
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	host, _ := os.Hostname()
	service, err := mobile.New(mobile.Config{
		Input: env.Stdin, Output: env.Stdout, HubID: host, OwnerHostID: "local:" + host,
		OwnerConfigGeneration: mobileConfigGeneration(), Resolver: mobileResolver(env),
	})
	if err == nil {
		err = service.Run(env.Ctx)
	}
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}

func mobileResolver(env Env) mobile.Resolver {
	return func(ctx context.Context, value string) (mobile.ResolvedTarget, error) {
		target, code, err := findShellTarget(env, strings.TrimSpace(value), "", "", true, tmuxenv.Namespace())
		if err != nil {
			kind := mobileproto.ErrorAmbiguous
			if code == shellTargetUnregistered {
				kind = mobileproto.ErrorNotFound
			}
			return mobile.ResolvedTarget{}, &mobile.ResolveError{Code: kind, Message: err.Error()}
		}
		if target.Kind != shellTargetKindShell || target.CreatedAt == "" {
			return mobile.ResolvedTarget{}, &mobile.ResolveError{Code: mobileproto.ErrorUnsupported, Message: "mobile M0 requires a managed shell with durable creation identity"}
		}
		identity, err := tty.InspectHeadlessTarget(ctx, target.Session)
		if err != nil {
			return mobile.ResolvedTarget{}, &mobile.ResolveError{Code: mobileproto.ErrorUnsupported, Message: err.Error()}
		}
		return mobile.ResolvedTarget{WorkspaceID: target.Project.Key, WorkspaceKind: target.Kind, Session: identity.Session,
			Pane: identity.Pane, DisplayName: target.DisplayName, ServerPID: identity.ServerPID, SessionID: identity.SessionID,
			SessionCreated: identity.SessionCreated, DurableSessionCreated: target.CreatedAt, Width: identity.Width, Height: identity.Height}, nil
	}
}

func mobileConfigGeneration() string {
	b, err := os.ReadFile(config.ConfigPath())
	if err != nil {
		b = []byte(config.ConfigPath())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}
