package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentremote"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/hosts"
)

// newRemoteRunner is the transport seam. Production dials the host over the
// ssh ControlMaster; tests replace it and assert on the argv.
var newRemoteRunner = productionRemoteRunner

// productionRemoteRunner resolves a registered host and runs one verb on it.
//
// It builds a one-shot client rather than a Registry because a CLI process has
// no stream to be healthy: see hosts.OneShotClient. Reading the config here,
// once, is also what makes "no such host" a refusal with the registered names
// in it instead of a connection attempt to a name nobody registered.
func productionRemoteRunner(env Env, hostID string) (agentremote.Runner, int) {
	cfg, err := config.Load()
	if err != nil {
		return nil, emitAgentError(env, true, err)
	}
	if !features.IsEnabled(features.SidecarRemoteHosts.Name) {
		return nil, remoteRefusal(env, agentcontrol.ErrFeatureDisabled,
			fmt.Sprintf("remote hosts are disabled; enable the %s feature", features.SidecarRemoteHosts.Name))
	}
	registered := hosts.FromConfig(cfg)
	for _, host := range registered {
		if host.ID != hostID {
			continue
		}
		client := hosts.OneShotClient(host, hosts.ClientOptions{})
		return func(ctx context.Context, _ string, args []string, out any) error {
			return client.RunSidecar(ctx, args, out)
		}, -1
	}
	return nil, remoteRefusal(env, agentcontrol.ErrHostUnavailable, unknownHostMessage(hostID, registered))
}

func unknownHostMessage(hostID string, registered []hosts.Host) string {
	if len(registered) == 0 {
		return fmt.Sprintf("no host is registered as %q; register one with `sidecar host add`", hostID)
	}
	names := make([]string, 0, len(registered))
	for _, host := range registered {
		names = append(names, host.ID)
	}
	return fmt.Sprintf("no host is registered as %q; registered hosts are %s", hostID, strings.Join(names, ", "))
}

// remoteRefusal is used before flags are known to be JSON or not, so it always
// writes the machine envelope. Every caller here has already parsed --json;
// the helper takes it explicitly where that matters.
func remoteRefusal(env Env, code agentcontrol.ErrorCode, message string) int {
	return emitAgentError(env, true, &agentcontrol.Error{Code: code, Message: message})
}

// remoteClient builds the addressed host's client, or returns a CLI exit code.
func remoteClient(env Env, f agentFlags) (agentremote.Client, int) {
	runner, code := newRemoteRunner(env, f.host)
	if code >= 0 {
		return agentremote.Client{}, code
	}
	return agentremote.Client{HostID: f.host, Run: runner, Project: f.project}, -1
}

// remoteTarget applies the one target rule that differs from the local path: a
// remote verb needs an explicit target.
//
// The omitted-target rule resolves SIDECAR_SHELL, which names a shell on THIS
// machine. Sending it to another host would either miss entirely or — worse,
// and the reason this is a refusal rather than a best effort — hit a shell
// that happens to carry the same generated name on the other machine. Two
// Sidecars started in the same-named project produce the same session names by
// construction, so this is a realistic collision rather than a theoretical one.
func remoteTarget(env Env, f agentFlags, target string, explicit bool) (string, int) {
	if !explicit || strings.TrimSpace(target) == "" {
		return "", emitAgentError(env, f.json, &agentcontrol.Error{
			Code:    agentcontrol.ErrNotFound,
			Message: "--host requires an explicit target: the current shell is on this machine, not on " + f.host,
		})
	}
	return target, -1
}

// remoteContext returns the caller's context.
func remoteContext(env Env) context.Context {
	if env.Ctx != nil {
		return env.Ctx
	}
	return context.Background()
}

// ---------------------------------------------------------------------------
// Verb dispatch. Each of these is entered only when --host was given, and each
// returns a process exit code.
// ---------------------------------------------------------------------------

func runRemoteAgentList(env Env, f agentFlags) int {
	client, code := remoteClient(env, f)
	if code >= 0 {
		return code
	}
	agents, err := client.List(remoteContext(env))
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitAgentList(env, f.json, agents)
}

func runRemoteAgentGet(env Env, f agentFlags, target string, explicit bool) int {
	client, code := remoteClient(env, f)
	if code >= 0 {
		return code
	}
	session, code := remoteTarget(env, f, target, explicit)
	if code >= 0 {
		return code
	}
	agent, err := client.Get(remoteContext(env), session, f.includeSession)
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitAgent(env, f.json, agent)
}

func runRemoteAgentStart(env Env, f agentFlags, target, kind string, explicit bool, providerArgs []string) int {
	client, code := remoteClient(env, f)
	if code >= 0 {
		return code
	}
	session, code := remoteTarget(env, f, target, explicit)
	if code >= 0 {
		return code
	}
	agent, err := client.Start(remoteContext(env), session, kind, f.timeout, providerArgs)
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitAgent(env, f.json, agent)
}

func runRemoteAgentPrompt(env Env, f agentFlags, target, text string, explicit bool) int {
	client, code := remoteClient(env, f)
	if code >= 0 {
		return code
	}
	session, code := remoteTarget(env, f, target, explicit)
	if code >= 0 {
		return code
	}
	agent, err := client.Prompt(remoteContext(env), session, text, f.wait, f.until, f.timeout)
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitAgent(env, f.json, agent)
}

func runRemoteAgentWait(env Env, f agentFlags, target string, explicit bool) int {
	client, code := remoteClient(env, f)
	if code >= 0 {
		return code
	}
	session, code := remoteTarget(env, f, target, explicit)
	if code >= 0 {
		return code
	}
	agent, err := client.Wait(remoteContext(env), session, f.until, f.timeout)
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitAgent(env, f.json, agent)
}

func runRemoteAgentRead(env Env, f agentFlags, target string, explicit bool) int {
	client, code := remoteClient(env, f)
	if code >= 0 {
		return code
	}
	session, code := remoteTarget(env, f, target, explicit)
	if code >= 0 {
		return code
	}
	result, err := client.Read(remoteContext(env), session, f.source, f.lines, f.ansi)
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitReadResult(env, f.json, result)
}

func runRemoteAgentSendKeys(env Env, f agentFlags, target string, explicit bool, keys []string) int {
	client, code := remoteClient(env, f)
	if code >= 0 {
		return code
	}
	session, code := remoteTarget(env, f, target, explicit)
	if code >= 0 {
		return code
	}
	agent, err := client.SendKeys(remoteContext(env), session, keys)
	if err != nil {
		return emitAgentError(env, f.json, err)
	}
	return emitAgent(env, f.json, agent)
}

// ---------------------------------------------------------------------------
// session status / restore on a host.
// ---------------------------------------------------------------------------

// runRemoteSessionDocument relays the host's own plan or result document.
//
// Cold restore executes on the host. The viewer requests it and observes the
// answer; it never rebuilds another machine's plan locally, because the inputs
// a plan is built from — live tmux sessions, the server id, whether a working
// directory still exists, whether a provider binary is installed — are all
// facts about the host and none of them is knowable from here.
func runRemoteSessionDocument(env Env, hostID string, jsonOutput bool, build func(agentremote.Client) ([]string, error)) int {
	runner, code := newRemoteRunner(env, hostID)
	if code >= 0 {
		return code
	}
	client := agentremote.Client{HostID: hostID, Run: runner}
	args, err := build(client)
	if err != nil {
		return emitAgentError(env, jsonOutput, err)
	}
	var raw json.RawMessage
	if err := client.Run(remoteContext(env), hostID, args, &raw); err != nil {
		return emitAgentError(env, jsonOutput, agentremote.TranslateError(hostID, err))
	}
	return emitRemoteSessionDocument(env, hostID, jsonOutput, raw)
}

// emitRemoteSessionDocument prints the host's document, annotated with which
// host it describes.
//
// The annotation is not decoration. A restore plan names tmux sessions and
// working directories, and those read exactly like local ones; a plan pasted
// into an issue without its host is a plan nobody can act on.
func emitRemoteSessionDocument(env Env, hostID string, jsonOutput bool, raw json.RawMessage) int {
	if jsonOutput {
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			// Forward what the host said rather than replacing it with a
			// complaint; a caller can still read it.
			_, _ = fmt.Fprintln(env.Stdout, string(raw))
			return 0
		}
		document["host"] = hostID
		return writeAgentJSON(env, document)
	}
	_, _ = fmt.Fprintf(env.Stdout, "host: %s\n", hostID)
	_, _ = fmt.Fprintln(env.Stdout, string(raw))
	return 0
}
