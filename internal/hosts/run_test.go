package hosts

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

// testRunClient builds a client that is connected as far as the request seam
// is concerned, with an injected invoker. Nothing here touches ssh, a network,
// or a second machine.
func testRunClient(t *testing.T, host Host, invoke Invoker) *Client {
	t.Helper()
	client := NewClient(host, ClientOptions{
		ControlDir: t.TempDir(),
		Dial: func(context.Context) (*Conn, error) {
			return nil, errors.New("this test never dials")
		},
		Invoke: invoke,
	})
	client.setHealth(StateOnline, "", 0)
	return client
}

func stubInvoker(stdout, stderr string, exitCode int) Invoker {
	return func(context.Context, *exec.Cmd) (Output, error) {
		return Output{Stdout: []byte(stdout), Stderr: []byte(stderr), ExitCode: exitCode}, nil
	}
}

// TestSidecarCommandRidesTheExistingMaster is the point of the whole seam. A
// mutation must cost a round trip on the connection the serve stream and the
// pane channels already hold open, not a second master with a second
// authentication.
func TestSidecarCommandRidesTheExistingMaster(t *testing.T) {
	client := testRunClient(t, Host{ID: "mac-mini", Target: "mac-mini"}, nil)
	run, err := client.SidecarCommand(context.Background(), "shell", "rename", "a b")
	if err != nil {
		t.Fatalf("SidecarCommand: %v", err)
	}
	control := client.ControlCommand(context.Background(), "proj-claude")
	if control == nil {
		t.Fatal("ControlCommand returned nil")
	}
	socket := ""
	for _, arg := range control.Args {
		if strings.HasPrefix(arg, "ControlPath=") {
			socket = arg
		}
	}
	if socket == "" {
		t.Fatalf("control channel has no ControlPath: %v", control.Args)
	}
	joined := strings.Join(run.Args, " ")
	if !strings.Contains(joined, socket) {
		t.Errorf("one-shot invocation does not share the control socket %q: %v", socket, run.Args)
	}
	for _, want := range []string{"ControlMaster=auto", "BatchMode=yes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("one-shot invocation is missing %q: %v", want, run.Args)
		}
	}
	remote := run.Args[len(run.Args)-1]
	if !strings.Contains(remote, "$SHELL -l -c") {
		t.Errorf("one-shot invocation does not resolve PATH through a login shell: %s", remote)
	}
	if !strings.Contains(remote, "'a b'") {
		t.Errorf("an argument with a space was not quoted as one word: %s", remote)
	}
}

// TestIsolationReachesTheRemoteEnvironment does not inspect the rendered string
// and call it proof — it runs the rendered command through a real shell and
// reads back the environment the remote process would actually get.
//
// A proof run that can reach a real machine must never be able to mutate that
// machine's real state tree (td-8d18de). The remote's own CheckStateIsolation
// is the fail-closed half of that, and it can only fire if this variable
// arrives.
func TestIsolationReachesTheRemoteEnvironment(t *testing.T) {
	if !config.IsolationAsserted() {
		t.Fatal("a test binary must assert state isolation")
	}
	client := testRunClient(t, Host{ID: "h", Target: "h", RemoteBinary: "/usr/bin/env"}, nil)
	cmd, err := client.SidecarCommand(context.Background())
	if err != nil {
		t.Fatalf("SidecarCommand: %v", err)
	}
	remote := cmd.Args[len(cmd.Args)-1]
	// Substitute a concrete shell for $SHELL and drop -l, so the test does not
	// depend on the developer's login profile.
	script := strings.Replace(remote, "$SHELL -l -c ", "/bin/sh -c ", 1)
	out, err := exec.Command("/bin/sh", "-c", script).Output() //nolint:gosec // built above
	if err != nil {
		t.Fatalf("running %s: %v", script, err)
	}
	if !strings.Contains(string(out), config.IsolationEnv+"=1") {
		t.Errorf("the remote process would not see %s=1; environment was:\n%s", config.IsolationEnv, out)
	}
}

// TestIsolationIsNotInventedForAnOrdinaryRun: an ordinary Sidecar is not
// isolated, and telling a remote host otherwise would make every real mutation
// refuse.
func TestIsolationIsNotInventedForAnOrdinaryRun(t *testing.T) {
	t.Setenv(config.IsolationEnv, "")
	t.Setenv(config.AllowRealStateEnv, "1")
	if config.IsolationAsserted() {
		t.Skip("state isolation is asserted by something this test cannot unset")
	}
	client := testRunClient(t, Host{ID: "h", Target: "h"}, nil)
	cmd, err := client.SidecarCommand(context.Background(), "shell", "list")
	if err != nil {
		t.Fatalf("SidecarCommand: %v", err)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), config.IsolationEnv) {
		t.Errorf("an unisolated viewer claimed isolation to the host: %v", cmd.Args)
	}
}

// TestHostEnvIsolationWins: a registration that sets the variable itself is a
// statement about that host, and the seam must not overwrite it.
func TestHostEnvIsolationWins(t *testing.T) {
	host := Host{ID: "h", Target: "h", Env: []string{config.IsolationEnv + "=0"}}
	got := runHost(host)
	if len(got.Env) != 1 || got.Env[0] != config.IsolationEnv+"=0" {
		t.Errorf("runHost overrode an explicit host env: %v", got.Env)
	}
}

func TestRunSidecarDecodesJSONResult(t *testing.T) {
	client := testRunClient(t, Host{ID: "h", Target: "h"},
		stubInvoker(`{"shell":{"displayName":"Demo","session":"proj-demo"}}`+"\n", "", 0))

	var result struct {
		Shell struct {
			DisplayName string `json:"displayName"`
			Session     string `json:"session"`
		} `json:"shell"`
	}
	if err := client.RunSidecar(context.Background(), []string{"create", "shell", "--json"}, &result); err != nil {
		t.Fatalf("RunSidecar: %v", err)
	}
	if result.Shell.DisplayName != "Demo" || result.Shell.Session != "proj-demo" {
		t.Errorf("decoded %+v, want the shell result", result.Shell)
	}
}

// TestRunSidecarToleratesALeadingBanner mirrors what the protocol decoder does
// with blank lines: find the result rather than demand that stdout be nothing
// but the result. A login profile that prints one line is not a broken host.
func TestRunSidecarToleratesALeadingBanner(t *testing.T) {
	stdout := "Welcome to mac-mini!\nLast login: Tue\n{\n  \"ok\": true\n}\n"
	client := testRunClient(t, Host{ID: "h", Target: "h"}, stubInvoker(stdout, "", 0))

	var result struct {
		OK bool `json:"ok"`
	}
	if err := client.RunSidecar(context.Background(), []string{"shell", "list", "--json"}, &result); err != nil {
		t.Fatalf("RunSidecar: %v", err)
	}
	if !result.OK {
		t.Error("the result behind the banner was not decoded")
	}
}

// TestRunSidecarNamesTheBannerCause is the wording requirement, not a style
// check. A raw `invalid character 'W'` sends a first-time user looking for a
// bug in Sidecar; naming the shell banner sends them to the one file that
// actually needs changing.
func TestRunSidecarNamesTheBannerCause(t *testing.T) {
	client := testRunClient(t, Host{ID: "mac-mini", Target: "mac-mini"},
		stubInvoker("Welcome to mac-mini!\nyou have mail\n", "", 0))

	var result struct{}
	err := client.RunSidecar(context.Background(), []string{"shell", "list", "--json"}, &result)
	if got := RunFailure(err); got != FailNotResult {
		t.Fatalf("failure = %q, want %q (err %v)", got, FailNotResult, err)
	}
	message := err.Error()
	for _, want := range []string{"not the expected result", "shell banner", "Welcome to mac-mini!"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q does not mention %q", message, want)
		}
	}
	if strings.Contains(message, "invalid character") {
		t.Errorf("a raw JSON syntax error reached the user: %q", message)
	}
	var runErr *RunError
	if errors.As(err, &runErr) && runErr.Fix() == "" {
		t.Error("a not-result failure has no suggested fix")
	}
}

func TestRunSidecarNamesAnEmptyStdout(t *testing.T) {
	client := testRunClient(t, Host{ID: "h", Target: "h"}, stubInvoker("", "", 0))
	var result struct{}
	err := client.RunSidecar(context.Background(), []string{"shell", "list", "--json"}, &result)
	if got := RunFailure(err); got != FailNotResult {
		t.Fatalf("failure = %q, want %q (err %v)", got, FailNotResult, err)
	}
}

// TestRunSidecarIgnoresStdoutWhenNoResultIsWanted: a verb whose result the
// caller does not read must not fail because the host printed a banner.
func TestRunSidecarIgnoresStdoutWhenNoResultIsWanted(t *testing.T) {
	client := testRunClient(t, Host{ID: "h", Target: "h"}, stubInvoker("Welcome!\n", "", 0))
	if err := client.RunSidecar(context.Background(), []string{"shell", "rename", "x"}, nil); err != nil {
		t.Fatalf("RunSidecar: %v", err)
	}
}

// TestRunSidecarClassifiesExitCodes pins the mapping from the CLI's documented
// statuses (internal/cli/registry.go) onto something a viewer can render.
//
// The 2/5 split is the one that carries information and the one this table used
// to have collapsed. Exit 2 is "the command as written is not usable", which
// between two copies of this repo means version skew and is fixed by updating a
// binary. Exit 5 is "that value is not usable", which is fixed by typing a
// different one. Mapping a rename collision onto 2 told users to upgrade
// Sidecar in answer to "another shell is already named Demo".
func TestRunSidecarClassifiesExitCodes(t *testing.T) {
	cases := []struct {
		name     string
		exitCode int
		stderr   string
		want     Failure
		wantText string
	}{
		{"state failure", 1, "another shell is already named \"Demo\"\n", FailRefused, "already named"},
		{"usage error", 2, "unknown flag \"--display-name\"\n", FailUnsupported, "unknown flag"},
		{"rejected name", 5, "another shell is already named \"Demo\"\n", FailRejected, "already named"},
		{"rejected plan", 5, "branch \"phase-c\" already exists\n", FailRejected, "already exists"},
		{"unowned target", 3, "no registered Sidecar shell or worktree session named \"x\"\n", FailNoTarget, "no registered Sidecar shell"},
		{"instance declined", 4, "the window is too small to split\n", FailRefused, "too small"},
		{"no sidecar", 127, "zsh: command not found: sidecar\n", FailNoSidecar, "command not found"},
		{"ssh failure", 255, "ssh: connect to host mac-mini port 22: Host is down\n", FailTransport, "Host is down"},
		{"other status", 7, "something else went wrong\n", FailExit, "something else"},
		{"exit 1 without a message", 1, "", FailRefused, "without saying why"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := testRunClient(t, Host{ID: "mac-mini", Target: "mac-mini"},
				stubInvoker("", testCase.stderr, testCase.exitCode))
			var result struct{}
			err := client.RunSidecar(context.Background(), []string{"create", "shell", "--json"}, &result)
			if got := RunFailure(err); got != testCase.want {
				t.Fatalf("failure = %q, want %q (err %v)", got, testCase.want, err)
			}
			if !strings.Contains(err.Error(), testCase.wantText) {
				t.Errorf("message %q does not carry %q", err.Error(), testCase.wantText)
			}
			var runErr *RunError
			if !errors.As(err, &runErr) {
				t.Fatalf("error is not a *RunError: %v", err)
			}
			if runErr.ExitCode != testCase.exitCode {
				t.Errorf("ExitCode = %d, want %d", runErr.ExitCode, testCase.exitCode)
			}
			if runErr.HostID != "mac-mini" {
				t.Errorf("HostID = %q, want mac-mini", runErr.HostID)
			}
			if testCase.stderr != "" && !strings.Contains(runErr.Stderr, strings.TrimSpace(testCase.stderr)) {
				t.Errorf("Stderr = %q, want it to carry the remote's own words", runErr.Stderr)
			}
		})
	}
}

// TestRunSidecarRefusesAnUnhealthyHost: attempting a mutation on a host that is
// not connected means waiting out an ssh connect timeout inside whatever asked
// for it. hostControlSpawner returns nil for the same reason; this refuses with
// a reason instead of hanging.
func TestRunSidecarRefusesAnUnhealthyHost(t *testing.T) {
	for _, state := range []State{StateUnreachable, StateConnecting, StateNoSidecar, StateProtocol, StateDisabled} {
		t.Run(string(state), func(t *testing.T) {
			var attempts atomic.Int64
			client := testRunClient(t, Host{ID: "mac-mini", Target: "mac-mini"},
				func(context.Context, *exec.Cmd) (Output, error) {
					attempts.Add(1)
					return Output{}, nil
				})
			client.setHealth(state, "the machine is off", 1)

			err := client.RunSidecar(context.Background(), []string{"create", "shell"}, nil)
			if got := RunFailure(err); got != FailUnavailable {
				t.Fatalf("failure = %q, want %q (err %v)", got, FailUnavailable, err)
			}
			if attempts.Load() != 0 {
				t.Error("an unreachable host was contacted anyway")
			}
			if !strings.Contains(err.Error(), string(state)) {
				t.Errorf("message %q does not say what the host's condition is", err.Error())
			}
		})
	}
}

// TestRunSidecarOnAStaleHostIsAttempted: stale means connected but quiet, and
// the rows are still shown. Refusing a mutation there would make a busy host
// unusable.
func TestRunSidecarOnAStaleHostIsAttempted(t *testing.T) {
	var attempts atomic.Int64
	client := testRunClient(t, Host{ID: "h", Target: "h"},
		func(context.Context, *exec.Cmd) (Output, error) {
			attempts.Add(1)
			return Output{ExitCode: 0}, nil
		})
	client.setHealth(StateStale, "no update for 2m", 0)
	if err := client.RunSidecar(context.Background(), []string{"shell", "rename", "x"}, nil); err != nil {
		t.Fatalf("RunSidecar on a stale host: %v", err)
	}
	if attempts.Load() != 1 {
		t.Errorf("invoker ran %d times, want 1", attempts.Load())
	}
}

func TestRegistryRunSidecarRefusesAnUnknownHost(t *testing.T) {
	registry := NewRegistry(ClientOptions{})
	err := registry.RunSidecar(context.Background(), "ghost", []string{"shell", "list"}, nil)
	if got := RunFailure(err); got != FailUnavailable {
		t.Fatalf("failure = %q, want %q (err %v)", got, FailUnavailable, err)
	}
}

// TestRunSidecarTimesOutPromptly runs the production invoker against a fake ssh
// that never returns, and against one whose descendant keeps the pipes open
// after the child is killed.
//
// Both are the td-052329 failure class: remote work that outlives the
// interaction that asked for it. A Bubble Tea command that never returns is
// indistinguishable from a frozen app, and at shutdown it is a quit that hangs.
func TestRunSidecarTimesOutPromptly(t *testing.T) {
	fakeSSHOnPath(t, "#!/bin/sh\n/bin/sleep 5 &\n/bin/sleep 30\n")

	client := testRunClient(t, Host{ID: "mac-mini", Target: "mac-mini"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := client.RunSidecar(ctx, []string{"create", "shell", "--json"}, nil)
	elapsed := time.Since(start)

	if got := RunFailure(err); got != FailTimeout {
		t.Fatalf("failure = %q, want %q (err %v)", got, FailTimeout, err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("a wedged host held the call for %s", elapsed)
	}
}

func TestRunSidecarReportsCancellation(t *testing.T) {
	fakeSSHOnPath(t, "#!/bin/sh\n/bin/sleep 30\n")

	client := testRunClient(t, Host{ID: "h", Target: "h"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := client.RunSidecar(ctx, []string{"create", "shell"}, nil)
	if got := RunFailure(err); got != FailCanceled {
		t.Fatalf("failure = %q, want %q (err %v)", got, FailCanceled, err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("a cancelled call took %s to return", elapsed)
	}
}

// TestRunSidecarReportsATransportFailure: ssh's own non-zero status, with its
// own message, must not be presented as the host refusing the operation.
func TestRunSidecarReportsATransportFailure(t *testing.T) {
	fakeSSHOnPath(t, "#!/bin/sh\necho 'ssh: Could not resolve hostname nope' >&2\nexit 255\n")

	client := testRunClient(t, Host{ID: "nope", Target: "nope"}, nil)
	err := client.RunSidecar(context.Background(), []string{"shell", "list", "--json"}, nil)
	if got := RunFailure(err); got != FailTransport {
		t.Fatalf("failure = %q, want %q (err %v)", got, FailTransport, err)
	}
	if !strings.Contains(err.Error(), "Could not resolve hostname") {
		t.Errorf("message %q lost ssh's own words", err.Error())
	}
}

// TestRunCommandBoundsOutput: an unbounded read from a remote pipe is the
// hazard hostproto.MaxLineBytes exists to prevent, one layer out. The child
// must still be allowed to finish writing — a diagnostic cap that kills the
// command it is diagnosing is worse than no cap.
func TestRunCommandBoundsOutput(t *testing.T) {
	script := "dd if=/dev/zero bs=1024 count=3000 2>/dev/null | tr '\\0' 'x'; " +
		"dd if=/dev/zero bs=1024 count=64 2>/dev/null | tr '\\0' 'e' >&2"
	cmd := exec.Command("/bin/sh", "-c", script) //nolint:gosec // fixed script
	output, err := runCommand(context.Background(), cmd)
	if err != nil {
		t.Fatalf("runCommand: %v", err)
	}
	if output.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (a bounded reader must not kill the child)", output.ExitCode)
	}
	if len(output.Stdout) != MaxRunOutputBytes {
		t.Errorf("stdout kept %d bytes, want the %d-byte cap", len(output.Stdout), MaxRunOutputBytes)
	}
	if len(output.Stderr) != MaxRunStderrBytes {
		t.Errorf("stderr kept %d bytes, want the %d-byte cap", len(output.Stderr), MaxRunStderrBytes)
	}
	if !output.Truncated {
		t.Error("Truncated is false after both caps fired")
	}
}

// TestRunCommandReportsExitCodesAndOutput checks the production invoker's
// contract: a non-zero exit is a result to classify, not an error to return.
func TestRunCommandReportsExitCodesAndOutput(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "printf 'out'; printf 'err' >&2; exit 2")
	output, err := runCommand(context.Background(), cmd)
	if err != nil {
		t.Fatalf("runCommand returned an error for a non-zero exit: %v", err)
	}
	if output.ExitCode != 2 {
		t.Errorf("ExitCode = %d, want 2", output.ExitCode)
	}
	if string(output.Stdout) != "out" || string(output.Stderr) != "err" {
		t.Errorf("captured stdout %q stderr %q", output.Stdout, output.Stderr)
	}
}

// fakeSSHOnPath puts a scripted `ssh` ahead of the real one, so a test can
// exercise the production invoker without a network or a second machine.
func fakeSSHOnPath(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o700); err != nil { //nolint:gosec // test fixture
		t.Fatalf("write fake ssh: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
