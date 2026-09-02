package cli

// The loopback proof for the M5 remote adapter.
//
// WHAT THIS HARNESS IS, EXACTLY
//
// There is no second machine and no sshd. `fakeSSHExecutingTheRenderedCommand`
// installs scripts/loopback-ssh.sh as `ssh` ahead of the real one on PATH; it
// ignores every -o option and the target, takes the LAST argument — which is
// the remote word internal/hosts rendered, `$SHELL -l -c '<quoted command>'` —
// and runs it locally by substituting a concrete shell for $SHELL and letting
// /bin/sh re-parse the quoting. So what is genuinely exercised is:
//
//   - hosts.Transport's argv rendering and its allow-list shell quoting, unwound
//     by a real shell rather than compared as a string;
//   - a real process spawn of a real `sidecar` binary built from this worktree,
//     against a state tree and tmux server that are not the viewer's;
//   - real exit codes through hosts.classifyRun;
//   - the banner-tolerant stdout decode and the stderr error-envelope recovery,
//     because the fake ssh writes a login banner to BOTH pipes before every
//     invocation;
//   - internal/cli/agent_remote.go's dispatch, and the exit code it produces.
//
// What it does NOT exercise, and what no claim here should be read as covering:
// sshd, authentication, ControlMaster multiplexing, network latency, partial
// writes, or a connection that drops mid-verb. Those need a real host, and the
// only real-ssh path in this repo is internal/tty's TestRemoteControlSpike,
// which is skipped unless SIDECAR_SPIKE_HOST is set.
//
// The "provider" is a fixture binary named `codex` (internal/agentremote/
// testdata/fakecodex). It paints the chrome the real codex detector rules
// already match; no paid provider runs.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentlifecycle/lifecyclestore"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/testenv"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type loopbackHost struct {
	t   *testing.T
	bin string

	hostHome     string
	hostState    string
	hostStateDir string
	hostConfig   string
	hostWork     string
	hostTmuxDir  string
	hostSocket   string
	hostEnv      []string // KEY=VALUE, as a hosts.Host registration carries them

	viewerState  string
	viewerConfig string

	agentSession string
	plainSession string
}

// runResult is one invocation of a verb, either path.
type runResult struct {
	stdout string
	stderr string
	code   int
}

func newLoopbackHost(t *testing.T) *loopbackHost {
	t.Helper()
	requireTmuxBinary(t)

	root := t.TempDir()
	h := &loopbackHost{t: t}

	h.bin = buildBinary(t, filepath.Join(root, "bin", "sidecar"), "../../cmd/sidecar")
	// The fixture provider must be named `codex` on disk: tmux reports a pane's
	// foreground process by name, and that name is what agentactivity.Identify
	// resolves a provider identity from.
	fakeBinDir := filepath.Join(root, "provider")
	buildBinary(t, filepath.Join(fakeBinDir, "codex"), "../agentremote/testdata/fakecodex")

	// PATH as it was before the fake ssh is installed, so the host's own
	// environment carries the real tmux and git.
	basePath := os.Getenv("PATH")
	hostPath := fakeBinDir + string(os.PathListSeparator) + basePath

	// --- the "remote" machine -------------------------------------------------
	//
	// The host tree is NOT a t.TempDir. Its $HOME belongs to interactive shells
	// running in tmux panes, and those write history on the way out — after the
	// server is killed and while t.TempDir's own removal is walking the tree, so
	// a t.TempDir here fails the run with "directory not empty" whenever the
	// race goes the wrong way. Removal is best-effort for the same reason.
	hostRoot, err := os.MkdirTemp("", "lbhost")
	if err != nil {
		t.Fatalf("host tree: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(hostRoot) })
	h.hostHome = mkdir(t, filepath.Join(hostRoot, "home"))
	h.hostState = mkdir(t, filepath.Join(hostRoot, "state"))
	h.hostStateDir = filepath.Join(h.hostState, "sidecar")
	h.hostConfig = filepath.Join(hostRoot, "config", "config.json")
	writeFile(t, h.hostConfig, `{"features":{"flags":{"agent_control":true}}}`)
	h.hostWork = evalSymlinks(t, mkdir(t, filepath.Join(hostRoot, "work")))

	// A tmux server of the host's own. The socket path has to stay well under
	// the ~104-byte sockaddr_un limit, so it is not a t.TempDir.
	tmuxDir, err := os.MkdirTemp("", "lbtmux")
	if err != nil {
		t.Fatalf("host tmux dir: %v", err)
	}
	h.hostTmuxDir = tmuxDir
	h.hostSocket = testenv.SocketPath(tmuxDir)
	// tmux does not create the socket's parent directory, and — observed, not
	// assumed — `tmux -S <missing dir>/... new-session` prints its error and
	// still exits 0, so a fixture that skipped this would look like it worked.
	// tmux also refuses a socket directory anyone else can write to.
	if err := os.MkdirAll(filepath.Dir(h.hostSocket), 0o700); err != nil {
		t.Fatal(err)
	}

	h.agentSession = "sidecar-sh-lb-agent"
	h.plainSession = "sidecar-sh-lb-plain"

	writeFile(t, filepath.Join(h.hostStateDir, "projects", "lb", "meta.json"),
		fmt.Sprintf(`{"path":%q}`, h.hostWork))
	writeFile(t, filepath.Join(h.hostStateDir, "projects", "lb", "shells.json"), fmt.Sprintf(
		`{"version":2,"shells":[`+
			`{"tmuxName":%q,"displayName":"lb agent","namespace":%q,"workDir":%q,"agentType":"codex",`+
			`"agent":{"session":{"kind":"codex","reported":true,"value":"HOST-ONLY-CONVERSATION-ID"}}},`+
			`{"tmuxName":%q,"displayName":"lb plain","namespace":%q,"workDir":%q}]}`,
		h.agentSession, h.hostSocket, h.hostWork,
		h.plainSession, h.hostSocket, h.hostWork))

	h.hostEnv = []string{
		"HOME=" + h.hostHome,
		"XDG_STATE_HOME=" + h.hostState,
		"TMUX_TMPDIR=" + h.hostTmuxDir,
		"PATH=" + hostPath,
		// A stray TMUX from the developer's own session would let a bare tmux
		// target resolve against the wrong server.
		"TMUX=",
		"TMUX_PANE=",
	}

	h.startHostSession(h.agentSession)
	h.startHostSession(h.plainSession)
	t.Cleanup(func() {
		// Only the server this fixture created, addressed by the socket it
		// created. Never a bare kill-server.
		_ = exec.Command("tmux", "-S", h.hostSocket, "kill-server").Run()
		_ = os.RemoveAll(tmuxDir)
	})

	// --- the viewer -----------------------------------------------------------
	h.viewerState = mkdir(t, filepath.Join(root, "viewer", "state"))
	h.viewerConfig = filepath.Join(root, "viewer", "config", "config.json")
	writeFile(t, h.viewerConfig, h.viewerConfigJSON())

	t.Setenv("XDG_STATE_HOME", h.viewerState)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("SIDECAR_SHELL", "")
	config.SetConfigPath(h.viewerConfig)
	t.Cleanup(func() { config.SetConfigPath(defaultTestConfigPath) })

	// Deliberately no features.Init here. The features singleton is set up only
	// by cmd/sidecar's TUI path, never by a CLI verb, so leaving it at its
	// default is what a real `sidecar agent … --host` invocation actually sees —
	// and the viewer config's own flags are what must carry the run. See
	// TestRemoteVerbsReachTheFeatureFlagFromAConfigFileAndFromAnOverride.
	features.Init(config.Default())
	t.Cleanup(func() { features.Init(config.Default()) })

	fakeSSHExecutingTheRenderedCommand(t, filepath.Join(root, "ssh"), 0)
	return h
}

func (h *loopbackHost) viewerConfigJSON() string {
	env, err := json.Marshal(h.hostEnv)
	if err != nil {
		h.t.Fatal(err)
	}
	return fmt.Sprintf(
		`{"features":{"flags":{"agent_control":true,"sidecar_remote_hosts":true}},`+
			`"hosts":{"list":[{"id":"loopback","target":"loopback","binary":%q,"config":%q,"env":%s}]}}`,
		h.bin, h.hostConfig, env)
}

// startHostSession creates one managed shell on the host's own tmux server.
//
// The client's environment becomes the server's, and the pane shell inherits
// it — which is how the fixture `codex` ends up on the pane's PATH.
func (h *loopbackHost) startHostSession(name string, extraEnv ...string) {
	h.t.Helper()
	args := []string{"-S", h.hostSocket, "new-session", "-d", "-s", name, "-c", h.hostWork}
	for _, entry := range extraEnv {
		args = append(args, "-e", entry)
	}
	cmd := exec.Command("tmux", args...)
	cmd.Env = h.processEnv()
	out, err := cmd.CombinedOutput()
	if err != nil || strings.Contains(string(out), "error") {
		h.t.Fatalf("create host session %s: %v: %s", name, err, out)
	}
}

// managedShellEnv is the whole managed-shell environment contract, which the
// fixture publishes itself and must therefore never inherit.
//
// Dropping only some of it is how this fixture used to fail for one developer
// and pass for everyone else: run the suite from inside a Sidecar shell and
// that shell's SIDECAR_TMUX_SERVER reached the host-local hook, which
// lifecycleenv.Resolve correctly rejected as a claim that did not match the
// fixture's own tmux server. The environment is a cue and tmux is the
// authority, so a leaked cue is a real mismatch, not a false alarm — the fix
// belongs here, in the fixture that should not have carried it.
var managedShellEnv = []string{
	shellstate.NameEnv,
	shellstate.SessionEnv,
	shellstate.ManagedEnv,
	shellstate.ServerEnv,
	shellstate.HostEnv,
	shellstate.BinEnv,
	shellstate.NamespaceEnv,
}

// processEnv is the environment a process running "on the host" gets. It is the
// same set the registration carries, applied to a local exec.
func (h *loopbackHost) processEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+len(h.hostEnv)+1)
	drop := map[string]bool{}
	for _, entry := range h.hostEnv {
		key, _, _ := strings.Cut(entry, "=")
		drop[key] = true
	}
	for _, key := range managedShellEnv {
		drop[key] = true
	}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if drop[key] {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, h.hostEnv...)
	return append(out, "SIDECAR_ISOLATED_STATE=1")
}

// local runs a verb as the host would run it for itself: the same binary, the
// same state tree, the same tmux server, no transport in the middle.
func (h *loopbackHost) local(args ...string) runResult {
	h.t.Helper()
	full := append([]string{"-config", h.hostConfig}, args...)
	cmd := exec.Command(h.bin, full...) //nolint:gosec // built above
	cmd.Env = h.processEnv()
	cmd.Dir = h.hostWork
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else if err != nil {
		h.t.Fatalf("local %v: %v", args, err)
	}
	return runResult{stdout: out.String(), stderr: errOut.String(), code: code}
}

// remote runs the same verb through the viewer's --host dispatch, over the
// loopback ssh.
func (h *loopbackHost) remote(args ...string) runResult {
	h.t.Helper()
	full := append([]string{"-config", h.viewerConfig}, args...)
	full = append(full, "--host", "loopback")
	var out, errOut bytes.Buffer
	handled, code := Run(full, &out, &errOut)
	if !handled {
		h.t.Fatalf("remote %v was not handled by the CLI", args)
	}
	return runResult{stdout: out.String(), stderr: errOut.String(), code: code}
}

// startAgent brings the agent session up to a live, identified, idle codex.
func (h *loopbackHost) startAgent() agentcontrol.Agent {
	h.t.Helper()
	result := h.local("agent", "start", h.agentSession, "--kind", "codex", "--timeout", "30s", "--json")
	if result.code != 0 {
		h.t.Fatalf("starting the fixture provider: exit %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}
	started := decodeAgent(h.t, result.stdout)
	// Every caller of this helper goes on to compare steady state, so settle
	// before handing the pane over. See settleAgent.
	h.settleAgent()
	return started
}

// settleAgent waits until the pane's status comes from an explicit rule in the
// provider's own manifest rather than from the known-live fallback.
//
// For Codex that rule is `sidecar.composer_idle`, the `›` composer. Upstream's
// own idle rule is `osc_title_idle`, whose matcher is `\S` on the title, and the
// Sidecar overlay disables it: tmux seeds `#{pane_title}`, so under tmux that
// rule would turn every unmatched Codex screen into an explicit idle.
//
// `agent start` returns on the first idle observation, and an early poll can
// identify the provider from its process name before it has painted anything —
// which resolves idle through `codex.known-live-fallback`. That is a correct
// answer, but it is a transient one, and comparing two independent invocations
// that each raced it would compare noise. Settling first is what lets the
// steady-state rows assert Evidence exactly.
func (h *loopbackHost) settleAgent() {
	h.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		result := h.local("agent", "get", h.agentSession, "--json")
		if result.code == 0 {
			last = decodeAgent(h.t, result.stdout).Agent.Evidence
			if last == "sidecar.composer_idle" {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("the fixture provider never settled on its own composer; last evidence was %q", last)
}

// resetAgentPane hands the pane back to a fresh interactive shell so a start
// can be run again from the same preconditions.
func (h *loopbackHost) resetAgentPane() {
	h.t.Helper()
	cmd := exec.Command("tmux", "-S", h.hostSocket, "respawn-pane", "-k", "-t", h.agentSession)
	cmd.Env = h.processEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("respawn host pane: %v: %s", err, out)
	}
	h.waitForShell(h.agentSession)
}

func (h *loopbackHost) waitForShell(session string) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		cmd := exec.Command("tmux", "-S", h.hostSocket, "list-panes", "-t", session, "-F", "#{pane_current_command}")
		cmd.Env = h.processEnv()
		out, err := cmd.Output()
		if err == nil {
			switch strings.TrimSpace(string(out)) {
			case "sh", "bash", "zsh", "dash", "fish":
				// Give the shell a moment past exec so ShellStableFor is met.
				time.Sleep(300 * time.Millisecond)
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("host session %s never returned to its interactive shell", session)
}

func (h *loopbackHost) tmuxSend(session, keys string) {
	h.t.Helper()
	cmd := exec.Command("tmux", "-S", h.hostSocket, "send-keys", "-t", session, keys, "Enter")
	cmd.Env = h.processEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("send-keys to %s: %v: %s", session, err, out)
	}
}

// ---------------------------------------------------------------------------
// Fixtures shared by every subtest
// ---------------------------------------------------------------------------

func requireTmuxBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
}

func buildBinary(t *testing.T, out, pkg string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	cmd := exec.Command("go", "build", "-o", out, filepath.Join(filepath.Dir(thisFile), pkg))
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("building %s: %v", pkg, err)
	}
	return out
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func evalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

// fakeSSHExecutingTheRenderedCommand installs the shared loopback ssh from
// scripts/loopback-ssh.sh as `ssh` on PATH. See that file for what it does
// and does not stand in for.
//
// exitOverride, when non-zero, sets SIDECAR_LOOPBACK_SSH_EXIT so the script
// refuses without running anything — which is how an older host that does not
// know a verb is simulated.
func fakeSSHExecutingTheRenderedCommand(t *testing.T, dir string, exitOverride int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := loopbackSSHScript(t)
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read shared loopback ssh: %v", err)
	}
	dest := filepath.Join(dir, "ssh")
	if err := os.WriteFile(dest, body, 0o700); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if exitOverride != 0 {
		t.Setenv("SIDECAR_LOOPBACK_SSH_EXIT", strconv.Itoa(exitOverride))
	} else {
		t.Setenv("SIDECAR_LOOPBACK_SSH_EXIT", "")
	}
	t.Setenv("SIDECAR_LOOPBACK_SSH_DELAY", "")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func loopbackSSHScript(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	src := filepath.Join(filepath.Dir(thisFile), "..", "..", "scripts", "loopback-ssh.sh")
	resolved, err := filepath.Abs(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(resolved); err != nil {
		t.Fatalf("shared loopback ssh missing: %v", err)
	}
	return resolved
}

func decodeAgent(t *testing.T, out string) agentcontrol.Agent {
	t.Helper()
	var a agentcontrol.Agent
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatalf("stdout is not an agent document: %v\n%s", err, out)
	}
	return a
}

func decodeRead(t *testing.T, out string) agentcontrol.ReadResult {
	t.Helper()
	var r agentcontrol.ReadResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("stdout is not a read document: %v\n%s", err, out)
	}
	return r
}

func errorCode(t *testing.T, stderr string) agentcontrol.ErrorCode {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var envelope agentcontrol.ErrorEnvelope
		if err := json.Unmarshal([]byte(line), &envelope); err == nil && envelope.Error != nil {
			return envelope.Error.Code
		}
	}
	t.Fatalf("stderr carries no agent error envelope:\n%s", stderr)
	return ""
}

// ---------------------------------------------------------------------------
// Normalisation
// ---------------------------------------------------------------------------

// normalizeAgent removes the fields that cannot be equal across two separate
// observations of the same live pane, and nothing else. Each one is listed with
// why, because a normaliser nobody can audit is a comparison of nothing.
//
//   - Agent.CapturedAt is the instant tmux was read. Two runs are two reads.
//   - Agent.ChangedAt is stamped by agentstatus from a tracker that each
//     one-shot process builds fresh, so it is the second read's own clock.
//   - Target.ServerIncarnation embeds the socket's ctime, and tmux rewrites the
//     socket's permission bits whenever the set of attached clients changes —
//     which observing the pane does. Target.ServerPID is the field the occupant
//     pin actually compares, it is NOT normalised, and it must be equal.
//   - Target.Host is the one intended difference and is asserted separately
//     before it is cleared.
//   - Target.PanePID is cleared only for a row that deliberately respawned the
//     pane between the two runs (start). Every other row must agree on it, and
//     does.
func normalizeAgent(a agentcontrol.Agent, panePIDChanges bool) agentcontrol.Agent {
	a.Agent.CapturedAt = time.Time{}
	a.Agent.ChangedAt = time.Time{}
	a.Target.ServerIncarnation = ""
	a.Target.Host = ""
	if panePIDChanges {
		a.Target.PanePID = 0
	}
	return a
}

func normalizeRead(r agentcontrol.ReadResult) agentcontrol.ReadResult {
	r.CapturedAt = time.Time{}
	r.Target.ServerIncarnation = ""
	r.Target.Host = ""
	return r
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// The parity suite
// ---------------------------------------------------------------------------

// TestALoopbackHostAnswersEveryAgentVerbExactlyAsTheHostItselfWould is the M5
// exit gate. Every control verb runs twice against ONE isolated host: once as
// the host running it for itself, once through the viewer's --host dispatch
// over the loopback ssh. The answers must be equal apart from the fields listed
// on normalizeAgent, and Target.Host — the one difference the design intends —
// is asserted rather than masked.
func TestALoopbackHostAnswersEveryAgentVerbExactlyAsTheHostItselfWould(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries and drives a tmux server")
	}
	h := newLoopbackHost(t)

	// One start proves the whole thread is wired before any comparison runs: a
	// parity suite over two identical failures is green and worthless.
	started := h.startAgent()
	if started.Agent.Kind != "codex" || !started.Agent.InteractiveReady {
		t.Fatalf("the fixture provider did not come up identified and ready: %+v", started.Agent)
	}

	type row struct {
		name string
		// prepare runs before each of the two invocations, so both see the same
		// preconditions.
		prepare func()
		args    []string
		// panePIDChanges marks a row whose preparation replaces the pane
		// process, so PanePID cannot be compared.
		panePIDChanges bool
		// evidenceIsARaceAtReadiness marks the one row whose compared value is
		// produced AT the moment of readiness rather than after it. See the
		// handling below.
		evidenceIsARaceAtReadiness bool
	}

	agentRows := []row{
		{name: "get", args: []string{"agent", "get", h.agentSession, "--json"}},
		{
			name: "prompt",
			args: []string{"agent", "prompt", h.agentSession, "summarise the diff", "--wait", "--timeout", "30s", "--json"},
		},
		{name: "wait", args: []string{"agent", "wait", h.agentSession, "--timeout", "30s", "--json"}},
		{name: "send-keys", args: []string{"agent", "send-keys", h.agentSession, "space", "--json"}},
		{
			name:                       "start",
			prepare:                    func() { h.resetAgentPane() },
			args:                       []string{"agent", "start", h.agentSession, "--kind", "codex", "--timeout", "30s", "--json"},
			panePIDChanges:             true,
			evidenceIsARaceAtReadiness: true,
		},
	}

	for _, tc := range agentRows {
		t.Run(tc.name, func(t *testing.T) {
			if tc.prepare != nil {
				tc.prepare()
			}
			local := h.local(tc.args...)
			if local.code != 0 {
				t.Fatalf("local %v: exit %d\nstdout %s\nstderr %s", tc.args, local.code, local.stdout, local.stderr)
			}
			if tc.prepare != nil {
				tc.prepare()
			}
			remote := h.remote(tc.args...)
			if remote.code != 0 {
				t.Fatalf("remote %v: exit %d\nstdout %s\nstderr %s", tc.args, remote.code, remote.stdout, remote.stderr)
			}

			localAgent, remoteAgent := decodeAgent(t, local.stdout), decodeAgent(t, remote.stdout)

			// The intended difference, stated rather than normalised away.
			if localAgent.Target.Host != "local" {
				t.Errorf("the host answering for itself reported host %q, want local", localAgent.Target.Host)
			}
			if remoteAgent.Target.Host != "loopback" {
				t.Errorf("the remote answer was not stamped with the host id: %q", remoteAgent.Target.Host)
			}
			// The pin field, which must be the same server on both paths.
			if localAgent.Target.ServerPID == 0 || localAgent.Target.ServerPID != remoteAgent.Target.ServerPID {
				t.Errorf("ServerPID local=%d remote=%d; both paths must be pinned to one tmux server",
					localAgent.Target.ServerPID, remoteAgent.Target.ServerPID)
			}
			// Everything a caller acts on.
			if localAgent.Agent.Kind == "" || localAgent.Agent.Status == "" || localAgent.Agent.Evidence == "" {
				t.Fatalf("the local answer is too thin to prove anything: %+v", localAgent.Agent)
			}
			if tc.evidenceIsARaceAtReadiness {
				// Evidence is the one field `start` cannot promise, and the
				// reason is a true property of Service.Start rather than a
				// wobble worth hiding. Start returns on its FIRST idle
				// observation. tmux reports a pane's foreground command the
				// instant exec happens, which is before the provider has
				// painted anything, so an early poll identifies codex against a
				// blank screen and resolves idle through
				// `codex.known-live-fallback`; a poll 100ms later sees the
				// composer and resolves the same idle through
				// `sidecar.composer_idle`. Two independent starts can land on
				// either side of that, and under a loaded `go test ./...` they
				// do — this test failed once in three full-package runs before
				// the distinction was made.
				//
				// So the row asserts both paths reached readiness by a
				// legitimate idle route and compares everything else exactly,
				// rather than normalising a field that is stable for every
				// other verb. The steady-state rows above are settled first
				// (h.settleAgent), so for them Evidence IS compared.
				for _, side := range []struct {
					name  string
					agent agentcontrol.Agent
				}{{"local", localAgent}, {"remote", remoteAgent}} {
					switch side.agent.Agent.Evidence {
					case "sidecar.composer_idle", "codex.known-live-fallback":
					default:
						t.Fatalf("%s start reached readiness by an unexpected route: %q",
							side.name, side.agent.Agent.Evidence)
					}
				}
				localAgent.Agent.Evidence, remoteAgent.Agent.Evidence = "", ""
			}
			gotLocal := mustJSON(t, normalizeAgent(localAgent, tc.panePIDChanges))
			gotRemote := mustJSON(t, normalizeAgent(remoteAgent, tc.panePIDChanges))
			if gotLocal != gotRemote {
				t.Fatalf("the two paths answered differently\n--- local ---\n%s\n--- remote ---\n%s", gotLocal, gotRemote)
			}
		})
	}

	t.Run("list", func(t *testing.T) {
		args := []string{"agent", "list", "--json"}
		local := h.local(args...)
		if local.code != 0 {
			t.Fatalf("local list: exit %d\n%s", local.code, local.stderr)
		}
		remote := h.remote(args...)
		if remote.code != 0 {
			t.Fatalf("remote list: exit %d\n%s", remote.code, remote.stderr)
		}
		decode := func(out string) []agentcontrol.Agent {
			var listed struct {
				Agents []agentcontrol.Agent `json:"agents"`
			}
			if err := json.Unmarshal([]byte(out), &listed); err != nil {
				t.Fatalf("list output is not a list document: %v\n%s", err, out)
			}
			return listed.Agents
		}
		localAgents, remoteAgents := decode(local.stdout), decode(remote.stdout)
		if len(localAgents) != 1 {
			t.Fatalf("the host lists %d agents, want exactly the one running provider", len(localAgents))
		}
		if len(remoteAgents) != len(localAgents) {
			t.Fatalf("the remote list has %d rows, the host's own has %d", len(remoteAgents), len(localAgents))
		}
		for i := range localAgents {
			if localAgents[i].Target.Host != "local" || remoteAgents[i].Target.Host != "loopback" {
				t.Errorf("row %d hosts: local=%q remote=%q", i, localAgents[i].Target.Host, remoteAgents[i].Target.Host)
			}
			if got, want := mustJSON(t, normalizeAgent(remoteAgents[i], false)), mustJSON(t, normalizeAgent(localAgents[i], false)); got != want {
				t.Fatalf("list row %d differs\n--- local ---\n%s\n--- remote ---\n%s", i, want, got)
			}
		}
	})

	t.Run("read", func(t *testing.T) {
		args := []string{"agent", "read", h.agentSession, "--source", "detection", "--lines", "20", "--json"}
		local := h.local(args...)
		if local.code != 0 {
			t.Fatalf("local read: exit %d\n%s", local.code, local.stderr)
		}
		remote := h.remote(args...)
		if remote.code != 0 {
			t.Fatalf("remote read: exit %d\n%s", remote.code, remote.stderr)
		}
		localRead, remoteRead := decodeRead(t, local.stdout), decodeRead(t, remote.stdout)
		if localRead.Text == "" || !strings.Contains(localRead.Text, "›") {
			t.Fatalf("the local read returned nothing recognisable:\n%q", localRead.Text)
		}
		// Read's result carries a Target, so it is stamped like every other
		// verb's. This assertion records a finding: Read was originally the one
		// verb that returned the host's own "local" unstamped, which left a
		// caller holding reads from two machines with nothing to tell them
		// apart by. The parity suite is what caught it.
		if remoteRead.Target.Host != "loopback" {
			t.Errorf("the remote read was not stamped with the host id: %q", remoteRead.Target.Host)
		}
		if localRead.Target.Host != "local" {
			t.Errorf("the host answering for itself reported host %q, want local", localRead.Target.Host)
		}
		if got, want := mustJSON(t, normalizeRead(remoteRead)), mustJSON(t, normalizeRead(localRead)); got != want {
			t.Fatalf("the two read paths answered differently\n--- local ---\n%s\n--- remote ---\n%s", want, got)
		}
	})

	// `session status` is not a row here because its result is a whole document
	// rather than an Agent, so it needs its own comparison:
	// TestARelayedRestorePlanArrivesWholeDespiteBeingPrettyPrinted.
}

// TestARelayedRestorePlanArrivesWholeDespiteBeingPrettyPrinted guards the one
// defect this loopback harness found that no unit test with a stubbed runner
// could have reached.
//
// `sidecar session status --json` writes an INDENTED document (cli.writeJSON
// sets a two-space indent), so several of its lines begin with `{` after
// leading whitespace. hosts.decodeRemoteResult scans stdout for lines that
// could open a JSON value and tries them LAST FIRST — a rule that exists to see
// past a login banner, and one that is exactly right for every verb whose
// --json output is the single compact line json.NewEncoder produces, which is
// what every `agent` verb writes. On a pretty-printed document the last such
// line is the innermost trailing object, and it decoded cleanly into the
// json.RawMessage the relay used to pass. So `session status --host` and
// `session restore --host` returned ONE STEP of the host's plan, wearing the
// whole document's place, at exit 0 with valid JSON and the viewer's own
// `"host"` annotation on top. A plan truncated to its last step is not
// obviously wrong to read, and this is the path a cold-restore decision is made
// on.
//
// The fix is a named result type: agentremote.SessionDocument's
// ValidRemoteResult requires a top-level `resumePolicy`, which both document
// shapes carry and no individual step or outcome has, so the decoder walks back
// past every fragment to the real document. This test is what keeps that true.
func TestARelayedRestorePlanArrivesWholeDespiteBeingPrettyPrinted(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries and drives a tmux server")
	}
	h := newLoopbackHost(t)

	local := h.local("session", "status", "--json")
	if local.code != 0 {
		t.Fatalf("local session status: exit %d\n%s", local.code, local.stderr)
	}
	remote := h.remote("session", "status", "--json")
	if remote.code != 0 {
		t.Fatalf("remote session status: exit %d\n%s", remote.code, remote.stderr)
	}

	// The host's own answer is a plan document with steps in it.
	var localDoc struct {
		Steps []map[string]any `json:"steps"`
	}
	if err := json.Unmarshal([]byte(local.stdout), &localDoc); err != nil {
		t.Fatalf("local plan is not JSON: %v\n%s", err, local.stdout)
	}
	if len(localDoc.Steps) < 2 {
		t.Fatalf("precondition: the host's plan needs at least two steps to show the truncation; got %d", len(localDoc.Steps))
	}
	if !strings.Contains(local.stdout, "\n  ") {
		t.Fatal("precondition: session status --json is no longer indented, so this defect may already be gone")
	}

	relayed := map[string]any{}
	if err := json.Unmarshal([]byte(remote.stdout), &relayed); err != nil {
		t.Fatalf("relayed plan is not JSON: %v\n%s", err, remote.stdout)
	}
	if relayed["host"] != "loopback" {
		t.Errorf("the relayed plan does not name the host it describes: %v", relayed["host"])
	}

	// The whole document, not one object out of it.
	relayedSteps, ok := relayed["steps"].([]any)
	if !ok {
		t.Fatalf("the relayed plan carries no steps at all; this is the truncation returning:\n%s", remote.stdout)
	}
	if len(relayedSteps) != len(localDoc.Steps) {
		t.Fatalf("relayed %d steps, host planned %d — a fragment of the plan is being relayed as the plan:\n%s",
			len(relayedSteps), len(localDoc.Steps), remote.stdout)
	}
	// The top-level fields a fragment could never have carried.
	for _, field := range []string{"resumePolicy", "recreateShells", "serverChanged"} {
		if _, ok := relayed[field]; !ok {
			t.Errorf("the relayed plan is missing top-level %q", field)
		}
	}
	// Every step matches the host's own, in order.
	for i, want := range localDoc.Steps {
		got, ok := relayedSteps[i].(map[string]any)
		if !ok {
			t.Fatalf("relayed step %d is not an object: %v", i, relayedSteps[i])
		}
		for _, field := range []string{"session", "action", "reason"} {
			if fmt.Sprint(got[field]) != fmt.Sprint(want[field]) {
				t.Errorf("step %d %s = %v, host said %v", i, field, got[field], want[field])
			}
		}
	}
}

// TestARemoteRefusalIsTheHostsOwnRefusalWithItsOwnExitCode proves the property
// the whole verb-level design exists for: the rules run on the machine that
// owns the pane, so a remote refusal carries the host's code, not a local
// translation of it.
func TestARemoteRefusalIsTheHostsOwnRefusalWithItsOwnExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries and drives a tmux server")
	}
	h := newLoopbackHost(t)
	h.startAgent()

	for _, tc := range []struct {
		name     string
		prepare  func()
		args     []string
		wantCode agentcontrol.ErrorCode
		wantExit int
	}{
		{
			name:     "a target the host does not own",
			args:     []string{"agent", "get", "sidecar-sh-lb-nothing", "--json"},
			wantCode: agentcontrol.ErrNotFound,
			wantExit: 3,
		},
		{
			name: "a pane with a foreground command in it",
			prepare: func() {
				h.tmuxSend(h.plainSession, "sleep 30")
				time.Sleep(700 * time.Millisecond)
			},
			args:     []string{"agent", "start", h.plainSession, "--kind", "codex", "--timeout", "2s", "--json"},
			wantCode: agentcontrol.ErrPaneBusy,
			wantExit: 5,
		},
		{
			name: "an agent that is blocked",
			prepare: func() {
				result := h.local("agent", "prompt", h.agentSession, "block", "--wait", "--timeout", "30s", "--json")
				if result.code != 0 {
					t.Fatalf("driving the agent to blocked: exit %d\n%s", result.code, result.stderr)
				}
				if got := decodeAgent(t, result.stdout).Agent.Status; got != agentcontrol.StatusBlocked {
					t.Fatalf("precondition: the agent settled at %s, want blocked", got)
				}
			},
			args:     []string{"agent", "prompt", h.agentSession, "carry on", "--json"},
			wantCode: agentcontrol.ErrBlocked,
			wantExit: 5,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.prepare != nil {
				tc.prepare()
			}
			local := h.local(tc.args...)
			remote := h.remote(tc.args...)

			if got := errorCode(t, local.stderr); got != tc.wantCode {
				t.Errorf("local refusal code = %q, want %q", got, tc.wantCode)
			}
			if got := errorCode(t, remote.stderr); got != tc.wantCode {
				t.Errorf("remote refusal code = %q, want %q", got, tc.wantCode)
			}
			if local.code != tc.wantExit || remote.code != tc.wantExit {
				t.Errorf("exit codes local=%d remote=%d, want %d", local.code, remote.code, tc.wantExit)
			}
			if strings.TrimSpace(remote.stdout) != "" {
				t.Errorf("a remote refusal wrote to stdout: %q", remote.stdout)
			}
		})
	}
}

// TestARemoteVerbWithNoExplicitTargetIsRefusedBeforeTheHostIsContacted pins the
// one target rule that deliberately differs between the two paths. The
// omitted-target rule resolves SIDECAR_SHELL, which names a shell on the
// viewer's machine; two Sidecars started in same-named projects generate the
// same session names by construction, so sending it would be a realistic
// collision rather than a theoretical one.
func TestARemoteVerbWithNoExplicitTargetIsRefusedBeforeTheHostIsContacted(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries and drives a tmux server")
	}
	h := newLoopbackHost(t)

	// Make any contact with the host fatal, so "it refused first" is proved
	// rather than inferred.
	fakeSSHExecutingTheRenderedCommand(t, filepath.Join(t.TempDir(), "trapssh"), 2)

	for _, args := range [][]string{
		{"agent", "get", "--json"},
		{"agent", "read", "--json"},
		{"agent", "wait", "--timeout", "5s", "--json"},
	} {
		result := h.remote(args...)
		if got := errorCode(t, result.stderr); got != agentcontrol.ErrNotFound {
			t.Fatalf("%v: code %q, want %q\n%s", args, got, agentcontrol.ErrNotFound, result.stderr)
		}
		if result.code != 3 {
			t.Errorf("%v: exit %d, want 3", args, result.code)
		}
		if !strings.Contains(result.stderr, "loopback") {
			t.Errorf("%v: the refusal does not say which machine the target would have to be on:\n%s", args, result.stderr)
		}
		// The trap ssh answers exit 2, so a version_skew code here would mean
		// the host was contacted before the rule was applied.
		if strings.Contains(result.stderr, string(agentcontrol.ErrVersionSkew)) {
			t.Errorf("%v: the host was contacted for a verb with no target", args)
		}
	}
}

// TestNothingAboutARemoteAgentIsWrittenIntoTheViewersOwnState is the
// host-locality half: a conversation identifier stays on the machine that owns
// the provider store, and a full remote sequence leaves the viewer's state tree
// byte-identical.
func TestNothingAboutARemoteAgentIsWrittenIntoTheViewersOwnState(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries and drives a tmux server")
	}
	h := newLoopbackHost(t)
	h.startAgent()

	viewerBefore := loopbackTreeSnapshot(t, h.viewerState)

	// Without --include-session-ref the host redacts the value and reports only
	// presence and kind.
	remote := h.remote("agent", "get", h.agentSession, "--json")
	if remote.code != 0 {
		t.Fatalf("remote get: exit %d\n%s", remote.code, remote.stderr)
	}
	redacted := decodeAgent(t, remote.stdout)
	if redacted.Agent.SessionRef == nil {
		t.Fatal("the remote answer carries no sessionRef at all; presence is what a caller needs")
	}
	if redacted.Agent.SessionRef.Kind != "codex" || !redacted.Agent.SessionRef.Reported {
		t.Errorf("sessionRef presence/capability was lost across the transport: %+v", redacted.Agent.SessionRef)
	}
	if redacted.Agent.SessionRef.Value != "" {
		t.Errorf("a conversation identifier crossed without being asked for: %q", redacted.Agent.SessionRef.Value)
	}
	if strings.Contains(remote.stdout, "HOST-ONLY-CONVERSATION-ID") {
		t.Errorf("the conversation identifier appears somewhere else in the remote output:\n%s", remote.stdout)
	}

	// The host's own shell, asking about itself locally, does get the value —
	// which is what makes the redaction above a decision rather than an absence.
	own := h.localAsShell(h.agentSession, "agent", "get", h.agentSession, "--json")
	if own.code != 0 {
		t.Fatalf("host-local get: exit %d\n%s", own.code, own.stderr)
	}
	if got := decodeAgent(t, own.stdout).Agent.SessionRef; got == nil || got.Value != "HOST-ONLY-CONVERSATION-ID" {
		t.Fatalf("a shell asking about itself did not get its own conversation: %+v", got)
	}

	// And with the explicit opt-in, the remote caller can have it.
	optIn := h.remote("agent", "get", h.agentSession, "--include-session-ref", "--json")
	if optIn.code != 0 {
		t.Fatalf("remote get --include-session-ref: exit %d\n%s", optIn.code, optIn.stderr)
	}
	if got := decodeAgent(t, optIn.stdout).Agent.SessionRef; got == nil || got.Value != "HOST-ONLY-CONVERSATION-ID" {
		t.Fatalf("--include-session-ref did not carry the value: %+v", got)
	}

	// A full sequence, then the viewer's state tree byte for byte.
	for _, args := range [][]string{
		{"agent", "list", "--json"},
		{"agent", "get", h.agentSession, "--json"},
		{"agent", "prompt", h.agentSession, "hello", "--wait", "--timeout", "30s", "--json"},
		{"agent", "read", h.agentSession, "--source", "recent", "--lines", "20", "--json"},
		{"agent", "send-keys", h.agentSession, "space", "--json"},
		{"session", "status", "--json"},
	} {
		if result := h.remote(args...); result.code != 0 {
			t.Fatalf("remote %v: exit %d\n%s", args, result.code, result.stderr)
		}
	}
	if got := loopbackTreeSnapshot(t, h.viewerState); got != viewerBefore {
		t.Fatalf("a remote sequence wrote into the viewer's own state tree\n--- before ---\n%s\n--- after ---\n%s", viewerBefore, got)
	}
}

// localAsShell runs a verb on the host as if from inside one of its managed
// shells, which is what makes the "your own conversation is not withheld from
// you" rule observable.
func (h *loopbackHost) localAsShell(session string, args ...string) runResult {
	h.t.Helper()
	full := append([]string{"-config", h.hostConfig}, args...)
	cmd := exec.Command(h.bin, full...) //nolint:gosec // built above
	cmd.Env = append(h.processEnv(), shellstate.SessionEnv+"="+session)
	cmd.Dir = h.hostWork
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	_ = cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return runResult{stdout: out.String(), stderr: errOut.String(), code: code}
}

// treeSnapshot renders every file under root as path+bytes, so a comparison is
// byte-identity rather than "the files I thought to check".
func loopbackTreeSnapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // test fixture tree
		if readErr != nil {
			return readErr
		}
		fmt.Fprintf(&b, "%s\n%s\n---\n", path, data)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return b.String()
}

// TestABoundedRemoteWaitIsNotSeveredByTheTransport proves the plumbing WaitSlack
// exists for, over the real loopback rather than against a recorded deadline.
//
// The unit half — that the deadline handed to the runner is the caller's and not
// hosts.DefaultRunTimeout — is already asserted without a transport by
// agentremote's TestAWaitsDeadlineIsTheCallersRatherThanTheTransportDefault.
// This half proves the same request survives a real process spawn and returns
// the host's own answer, and it deliberately does not spend thirty seconds
// proving a timeout constant.
func TestABoundedRemoteWaitIsNotSeveredByTheTransport(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries and drives a tmux server")
	}
	h := newLoopbackHost(t)
	h.startAgent()

	// A timeout past the transport's own 30s default. The agent is already
	// settled, so the host answers at once and nothing here waits it out.
	start := time.Now()
	result := h.remote("agent", "wait", h.agentSession, "--timeout", "45s", "--json")
	elapsed := time.Since(start)
	if result.code != 0 {
		t.Fatalf("remote wait: exit %d\n%s", result.code, result.stderr)
	}
	if elapsed > 20*time.Second {
		t.Fatalf("a settled agent took %s to answer", elapsed)
	}
	if got := decodeAgent(t, result.stdout).Agent.Status; got != agentcontrol.StatusIdle && got != agentcontrol.StatusDone {
		t.Fatalf("wait returned %s, want a settled state", got)
	}
}

// TestAnOlderHostIsReportedAsVersionSkewRatherThanAsARefusal pins the
// capability-negotiation half of the exit-code contract: remote exit 2 is "the
// two Sidecars disagree about this verb", which is fixed by updating a binary,
// and it must not be reported as the host declining.
func TestAnOlderHostIsReportedAsVersionSkewRatherThanAsARefusal(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries and drives a tmux server")
	}
	h := newLoopbackHost(t)

	// Replace the loopback ssh with one that answers every invocation the way an
	// older Sidecar answers a flag it does not have.
	fakeSSHExecutingTheRenderedCommand(t, filepath.Join(t.TempDir(), "oldssh"), 2)

	result := h.remote("agent", "get", h.agentSession, "--json")
	if got := errorCode(t, result.stderr); got != agentcontrol.ErrVersionSkew {
		t.Errorf("error code = %q, want %q\nstderr: %s", got, agentcontrol.ErrVersionSkew, result.stderr)
	}
	if result.code != 2 {
		t.Errorf("exit %d, want 2", result.code)
	}
	if !strings.Contains(result.stderr, "update Sidecar") {
		t.Errorf("the refusal does not say what to do about it:\n%s", result.stderr)
	}
}

// TestRemoteVerbsReachTheFeatureFlagFromAConfigFileAndFromAnOverride guards a
// fix, and the defect it guards is worth keeping written down.
//
// `--host` used to be gated on features.IsEnabled, which reads a process-wide
// singleton that cmd/sidecar initialises AFTER cli.Run — so in any real
// `sidecar` invocation it was nil and the flag fell through to its compiled-in
// default of false. Neither `features.flags.sidecar_remote_hosts` in the config
// file nor `--enable-feature=sidecar_remote_hosts` on the command line could
// turn it on, because Env.FeatureOverrides is not what that gate consulted.
// Every remote verb refused with feature_disabled from a shipped binary. It was
// found by running the built binary, twice and independently, and not by any
// existing test — which is the argument for this one.
//
// The second half is easy to lose: hosts.FromConfig consults the same global
// manager, so a gate fixed on its own would have let the run past and then
// reported "no host is registered as X" for a host that plainly is. Both routes
// are asserted here, and so is the host actually being found.
//
// Getting past the gate is all this proves. What happens next — an unreachable
// target — is the correct answer for a host nothing can dial, and it is the
// signal that the resolution behind the gate ran.
func TestRemoteVerbsReachTheFeatureFlagFromAConfigFileAndFromAnOverride(t *testing.T) {
	// Reproduce a real CLI process: the features singleton holds nothing but
	// compiled-in defaults, which is what made the defect visible.
	features.Init(config.Default())
	t.Cleanup(func() { features.Init(config.Default()) })

	for _, route := range []struct {
		name   string
		config string
		args   []string
	}{
		{
			name:   "config file",
			config: `{"features":{"flags":{"agent_control":true,"sidecar_remote_hosts":true}},"hosts":{"list":[{"id":"book","target":"book"}]}}`,
		},
		{
			name:   "leading override",
			config: `{"features":{"flags":{"agent_control":true,"sidecar_remote_hosts":false}},"hosts":{"list":[{"id":"book","target":"book"}]}}`,
			args:   []string{"--enable-feature=sidecar_remote_hosts"},
		},
	} {
		t.Run(route.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := filepath.Join(dir, "config.json")
			writeFile(t, cfg, route.config)
			t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
			t.Setenv("SIDECAR_ISOLATED_STATE", "1")
			// An ssh that cannot connect, so the run stops at the transport
			// rather than at the gate — and stops quickly.
			fakeSSHRefusingToConnect(t, filepath.Join(dir, "ssh"))

			for _, verb := range [][]string{
				{"agent", "get", "reviewer", "--host", "book", "--json"},
				{"session", "status", "--host", "book", "--json"},
			} {
				args := append([]string{"-config", cfg}, route.args...)
				args = append(args, verb...)
				var out, errOut bytes.Buffer
				handled, code := Run(args, &out, &errOut)
				if !handled {
					t.Fatalf("%v was not handled", args)
				}
				got := errorCode(t, errOut.String())
				if got == agentcontrol.ErrFeatureDisabled {
					t.Fatalf("%v: the %s route does not reach the gate:\n%s", args, route.name, errOut.String())
				}
				// The registered host must have been found. "no host is
				// registered as book" is the hosts.FromConfig trap, and it
				// arrives under the same code as an unreachable machine.
				if strings.Contains(errOut.String(), "no host is registered") {
					t.Fatalf("%v: a registered host was not found:\n%s", args, errOut.String())
				}
				if got != agentcontrol.ErrHostUnavailable {
					t.Fatalf("%v: code %q, want %q for a host that cannot be dialled\n%s",
						args, got, agentcontrol.ErrHostUnavailable, errOut.String())
				}
				if code != 1 {
					t.Errorf("%v: exit %d, want 1", args, code)
				}
			}
		})
	}

	// And the other flag still refuses, so "reaches the gate" has not become
	// "there is no gate".
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	writeFile(t, cfg, `{"features":{"flags":{"agent_control":false,"sidecar_remote_hosts":true}},"hosts":{"list":[{"id":"book","target":"book"}]}}`)
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfg, "agent", "get", "reviewer", "--host", "book", "--json"}, &out, &errOut)
	if !handled || errorCode(t, errOut.String()) != agentcontrol.ErrFeatureDisabled || code != 5 {
		t.Fatalf("agent control off: handled=%v code=%d stderr=%s", handled, code, errOut.String())
	}
}

// fakeSSHRefusingToConnect stands in for a machine that is not there, so a test
// about the gate does not wait out a real connect timeout.
func fakeSSHRefusingToConnect(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho 'ssh: Could not resolve hostname book' >&2\nexit 255\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o700); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestALifecycleReportFromARemoteAgentStaysInTheHostsOwnStore is Phase E step 1,
// as far as what is shipped can honestly carry it.
//
// PROVED HERE: a provider hook running on the host writes into the HOST's
// lifecycle store, the viewer's state tree does not gain a lifecycle store at
// all, and nothing the viewer can ask for over the transport returns a
// lifecycle record, a provider session id, or a hook payload.
//
// NOT PROVED HERE: that a hook-authored lane crosses SSH as a bounded semantic
// notification and that the viewer's delivery providers fire once for it.
//
// The reason is the shape of this harness, not a missing pipeline. An earlier
// draft of this comment claimed the Phase B step 3 wiring did not exist,
// reasoning from the fact that no package outside internal/agentlifecycle,
// internal/agentintegration and internal/cli imports lifecyclestore directly.
// That inference was wrong: the binding goes through agentintegration.Source,
// and internal/workspaceinventory/lifecycle.go and
// internal/plugins/workspace/lifecycle.go both construct a real store-backed
// source from config.StateDir() in production. hostserve builds a
// workspaceinventory.Collector, so a hook report on the host does reach the
// resolver and can influence the lane it publishes.
//
// What this harness cannot do is observe that. It drives one-shot CLI verbs
// over a fake ssh; a notification is produced by a running viewer consuming a
// host's serve stream, passing resolved lanes through notify.LaneTracker and
// into a delivery provider. Proving (c) needs that viewer, not another CLI
// invocation, so it belongs to a notification-side harness rather than to this
// one — and stating it that way keeps the gap honest without inventing a defect
// in wiring that is present.
func TestALifecycleReportFromARemoteAgentStaysInTheHostsOwnStore(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries and drives a tmux server")
	}
	h := newLoopbackHost(t)
	h.startAgent()

	paneID := h.panePID(h.agentSession)

	// The hook surface, run on the host, in the host's pane, exactly as a
	// provider integration would invoke it.
	report := h.localAsHook(paneID, h.agentSession,
		"agent", "report", "--state", "working",
		"--source", "sidecar.opencode.plugin", "--provider", "codex",
		"--session-id", "HOOK-ONLY-SESSION-ID", "--reason", "turn_start", "--json")
	if report.code != 0 {
		t.Fatalf("host-local lifecycle report: exit %d\nstdout %s\nstderr %s", report.code, report.stdout, report.stderr)
	}
	var accepted struct {
		Accepted bool `json:"accepted"`
		Managed  bool `json:"managed"`
	}
	if err := json.Unmarshal([]byte(report.stdout), &accepted); err != nil {
		t.Fatalf("report result is not JSON: %v\n%s", err, report.stdout)
	}
	if !accepted.Managed || !accepted.Accepted {
		t.Fatalf("the report was not recorded on the host: %s", report.stdout)
	}

	// (b) the host's store holds the record.
	hostStore := filepath.Join(h.hostStateDir, lifecyclestore.FileName)
	hostRecords, err := os.ReadFile(hostStore) //nolint:gosec // fixture path
	if err != nil {
		t.Fatalf("the host's lifecycle store is missing: %v", err)
	}
	if !strings.Contains(string(hostRecords), "sidecar.opencode.plugin") {
		t.Fatalf("the host's store does not hold the report:\n%s", hostRecords)
	}

	// (a) the viewer's store does not exist.
	viewerStore := filepath.Join(h.viewerState, "sidecar", lifecyclestore.FileName)
	if _, err := os.Stat(viewerStore); !os.IsNotExist(err) {
		t.Fatalf("a remote agent's lifecycle record reached the viewer's state tree at %s (%v)", viewerStore, err)
	}

	// (d) nothing the viewer can ask for carries the session id, the record, or
	// the hook's own vocabulary.
	before := loopbackTreeSnapshot(t, h.viewerState)
	for _, args := range [][]string{
		{"agent", "list", "--json"},
		{"agent", "get", h.agentSession, "--json"},
		{"agent", "get", h.agentSession, "--include-session-ref", "--json"},
		{"agent", "read", h.agentSession, "--source", "detection", "--lines", "40", "--json"},
		{"session", "status", "--json"},
	} {
		result := h.remote(args...)
		if result.code != 0 {
			t.Fatalf("remote %v: exit %d\n%s", args, result.code, result.stderr)
		}
		for _, leaked := range []string{"HOOK-ONLY-SESSION-ID", "sidecar.opencode.plugin", "turn_start", "sessionDigest"} {
			if strings.Contains(result.stdout, leaked) {
				t.Errorf("remote %v carried host-only lifecycle data %q:\n%s", args, leaked, result.stdout)
			}
		}
	}
	if got := loopbackTreeSnapshot(t, h.viewerState); got != before {
		t.Fatalf("reading a remote agent wrote into the viewer's state tree")
	}
}

// panePID returns the pane id of a host session's single pane.
func (h *loopbackHost) panePID(session string) string {
	h.t.Helper()
	cmd := exec.Command("tmux", "-S", h.hostSocket, "list-panes", "-t", session, "-F", "#{pane_id}")
	cmd.Env = h.processEnv()
	out, err := cmd.Output()
	if err != nil {
		h.t.Fatalf("list host panes: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// serverPID is the pid of the fixture's own tmux server, as a managed shell's
// SIDECAR_TMUX_SERVER carries it.
func (h *loopbackHost) serverPID() string {
	h.t.Helper()
	cmd := exec.Command("tmux", "-S", h.hostSocket, "display-message", "-p", "#{pid}")
	cmd.Env = h.processEnv()
	out, err := cmd.Output()
	if err != nil {
		h.t.Fatalf("host tmux server pid: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// localAsHook runs a lifecycle verb the way a provider hook does: on the host,
// with the managed-shell environment the pane carries.
func (h *loopbackHost) localAsHook(paneID, session string, args ...string) runResult {
	h.t.Helper()
	full := append([]string{"-config", h.hostConfig}, args...)
	cmd := exec.Command(h.bin, full...) //nolint:gosec // built above
	cmd.Env = append(h.processEnv(),
		shellstate.ManagedEnv+"=1",
		shellstate.SessionEnv+"="+session,
		// The real thing carries the server it was created under, and
		// lifecycleenv verifies it against the live server. Publishing the
		// fixture's own PID keeps that check exercised rather than skipped.
		shellstate.ServerEnv+"="+h.serverPID(),
		"TMUX_PANE="+paneID,
	)
	cmd.Dir = h.hostWork
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	_ = cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return runResult{stdout: out.String(), stderr: errOut.String(), code: code}
}
