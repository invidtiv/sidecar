package hosts

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/tty"
)

func newTestTransport(t *testing.T, host Host) *Transport {
	t.Helper()
	transport, err := NewTransport(host, t.TempDir())
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	return transport
}

func TestTransportRefusesEmptyTarget(t *testing.T) {
	if _, err := NewTransport(Host{ID: "x"}, t.TempDir()); err == nil {
		t.Fatal("empty ssh target accepted")
	}
}

// TestSSHArgsCarryTheMultiplexingRecipe pins the transport shape both remote
// plans share. Losing ControlMaster turns every in-band tmux command from a
// round trip into a full connection handshake.
func TestSSHArgsCarryTheMultiplexingRecipe(t *testing.T) {
	args := strings.Join(newTestTransport(t, Host{ID: "h", Target: "h"}).SSHArgs(), " ")
	for _, want := range []string{
		"ControlMaster=auto", "ControlPath=", "ControlPersist=",
		"ServerAliveInterval=15", "ServerAliveCountMax=4", "BatchMode=yes", "-T",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("ssh args missing %q: %s", want, args)
		}
	}
}

// TestControlCommandKeepsIgnoreSize is not a style check. Without
// `-f ignore-size` the viewer's attach becomes a sizing client on the remote
// server and resizes the session under whoever is sitting at that machine.
func TestControlCommandKeepsIgnoreSize(t *testing.T) {
	command := newTestTransport(t, Host{ID: "h", Target: "h"}).ControlCommand("proj-claude")
	if !strings.Contains(command, "-f ignore-size") {
		t.Errorf("control command would resize the remote session: %s", command)
	}
	if !strings.Contains(command, "attach-session") || !strings.Contains(command, "-C") {
		t.Errorf("control command is not a control-mode attach: %s", command)
	}
	if !strings.Contains(command, "proj-claude") {
		t.Errorf("control command lost its session: %s", command)
	}
}

// TestRemoteShellUsesLoginDashC is the finding that cost the most time in the
// Phase 0 spike, pinned so it cannot regress.
//
// `$SHELL -l -c CMD` runs the user's profile and produces clean output.
// `$SHELL -l -s` reads the command from stdin, which makes zsh run its
// interactive preexec hooks — and on a stock macOS install those write OSC 697
// sequences to STDOUT, the same pipe the JSONL protocol travels on. The stream
// is then unparseable for a reason nothing in the error points at.
func TestRemoteShellUsesLoginDashC(t *testing.T) {
	command := newTestTransport(t, Host{ID: "h", Target: "h"}).RemoteShell("sidecar host serve --stdio")
	if !strings.Contains(command, "-l -c") {
		t.Errorf("remote shell is not `-l -c`: %s", command)
	}
	// Match the flag exactly. A substring test for "-s" also matches
	// "--stdio", which made this assertion fire on a correct command.
	if strings.Contains(command, "-l -s") {
		t.Errorf("remote shell reads stdin, which triggers preexec hooks: %s", command)
	}
}

// TestSidecarCommandUsesALoginShell guards the other half of the same problem:
// a non-login ssh shell has no /opt/homebrew/bin on PATH, so a plainly
// installed tmux is reported as missing.
func TestServeCommandSetsViewerInstance(t *testing.T) {
	command := newTestTransport(t, Host{ID: "h", Target: "h"}).ServeCommand()
	if !strings.Contains(command, tty.ViewerInstanceEnv+"=") {
		t.Errorf("serve spawn is missing %s: %s", tty.ViewerInstanceEnv, command)
	}
	if !strings.Contains(command, tty.InstanceID()) {
		t.Errorf("serve spawn is missing this instance id: %s", command)
	}
}

func TestServeCommandKeepsExplicitViewerInstance(t *testing.T) {
	command := newTestTransport(t, Host{
		ID: "h", Target: "h",
		Env: []string{tty.ViewerInstanceEnv + "=other-1"},
	}).ServeCommand()
	if !strings.Contains(command, tty.ViewerInstanceEnv+"=other-1") {
		t.Errorf("explicit viewer instance was dropped: %s", command)
	}
	if strings.Count(command, tty.ViewerInstanceEnv+"=") != 1 {
		t.Errorf("viewer instance was set twice: %s", command)
	}
}

func TestSidecarCommandDoesNotSetViewerInstance(t *testing.T) {
	command := newTestTransport(t, Host{ID: "h", Target: "h"}).SidecarCommand("content", "read")
	if strings.Contains(command, tty.ViewerInstanceEnv) {
		t.Errorf("one-shot verb inherited viewer instance: %s", command)
	}
}

func TestSidecarCommandUsesALoginShell(t *testing.T) {
	command := newTestTransport(t, Host{ID: "h", Target: "h"}).ServeCommand()
	if !strings.Contains(command, "$SHELL -l -c") {
		t.Errorf("serve command does not resolve PATH through a login shell: %s", command)
	}
	// The flag is quoted because shellQuote's allow-list refuses any word
	// starting with "-": a value read as a flag by the receiving command is the
	// bug that rule exists to prevent, and quoting a flag we ourselves supplied
	// is harmless — the shell strips the quotes and argv still gets --stdio.
	for _, part := range []string{"host", "serve", "--stdio"} {
		if !strings.Contains(command, part) {
			t.Errorf("serve command is missing %q: %s", part, command)
		}
	}
}

// TestQuotedFlagsStillReachArgv proves the claim above rather than asserting
// it: render the command, run it through a real shell, and read back the argv
// the program actually received.
func TestQuotedFlagsStillReachArgv(t *testing.T) {
	transport := newTestTransport(t, Host{ID: "h", Target: "h", RemoteBinary: "/bin/echo"})
	command := transport.SidecarCommand("--stdio", "--cycles", "1", "=weird", "a b")
	// Substitute a concrete shell for $SHELL and drop -l, so the test does not
	// depend on the developer's login profile.
	command = strings.Replace(command, "$SHELL -l -c ", "/bin/sh -c ", 1)

	out, err := exec.Command("/bin/sh", "-c", command).Output()
	if err != nil {
		t.Fatalf("running %s: %v", command, err)
	}
	got := strings.TrimSpace(string(out))
	// Every flag arrives unquoted, and the value with a space stays one word.
	for _, want := range []string{"--stdio", "--cycles", "1", "=weird", "a b"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv %q is missing %q (from %s)", got, want, command)
		}
	}
	if strings.Contains(got, "'") {
		t.Errorf("quotes survived into argv: %q", got)
	}
}

func TestSidecarCommandHonoursExplicitBinaryAndConfig(t *testing.T) {
	transport := newTestTransport(t, Host{
		ID: "h", Target: "h",
		RemoteBinary: "/opt/sidecar/bin/sidecar",
		RemoteConfig: "/tmp/spike/config.json",
	})
	command := transport.ServeCommand()
	if !strings.Contains(command, "/opt/sidecar/bin/sidecar") {
		t.Errorf("explicit binary ignored: %s", command)
	}
	if !strings.Contains(command, "-config /tmp/spike/config.json") {
		t.Errorf("explicit config ignored: %s", command)
	}
}

// TestEnvIsQuotedAsAnAssignment keeps the KEY bare so the remote shell still
// parses the pair as an assignment prefix, while the VALUE is quoted so a path
// with a space cannot split into two words.
func TestEnvIsQuotedAsAnAssignment(t *testing.T) {
	transport := newTestTransport(t, Host{
		ID: "h", Target: "h",
		Env: []string{"XDG_STATE_HOME=/tmp/a b/state", "SIDECAR_ISOLATED_STATE=1"},
	})
	command := transport.ServeCommand()
	if !strings.Contains(command, "XDG_STATE_HOME=") {
		t.Errorf("env key was quoted away: %s", command)
	}
	if !strings.Contains(command, `/tmp/a b/state`) {
		t.Errorf("env value lost: %s", command)
	}
	// The key must stay bare so the inner shell parses an assignment prefix
	// rather than looking for a program with an "=" in its name. Assert on the
	// inner command, before RemoteShell wraps the whole thing for `-c`.
	inner := shellQuoteAssignment("XDG_STATE_HOME=/tmp/a b/state")
	if !strings.HasPrefix(inner, "XDG_STATE_HOME=") {
		t.Errorf("env key was quoted: %s", inner)
	}
	if !strings.Contains(inner, "'/tmp/a b/state'") {
		t.Errorf("env value was not quoted as one word: %s", inner)
	}
}

// TestShellQuoteSurvivesInjectionShapes. The values here reach a remote shell,
// so a target or path containing shell metacharacters must become one inert
// word rather than a second command.
func TestShellQuoteSurvivesInjectionShapes(t *testing.T) {
	for _, input := range []string{
		"; rm -rf /", "$(whoami)", "`id`", "a'b", `a"b`, "a b", "&& echo pwned", "|cat",
	} {
		quoted := shellQuote(input)
		if quoted == input {
			t.Errorf("%q passed through unquoted", input)
		}
		if !strings.HasPrefix(quoted, "'") || !strings.HasSuffix(quoted, "'") {
			t.Errorf("%q -> %q is not a single-quoted word", input, quoted)
		}
	}
}

func TestShellQuoteLeavesPlainWordsAlone(t *testing.T) {
	for _, input := range []string{"sidecar", "/usr/local/bin/sidecar", "proj-claude", "host.example.com"} {
		if got := shellQuote(input); got != input {
			t.Errorf("%q was needlessly quoted to %q", input, got)
		}
	}
}

// TestSocketPathStaysShort: a unix socket path is capped near 104 bytes on
// darwin, and ssh's own %-expansions routinely overflow it. A short fixed name
// inside a caller-chosen private directory is the reliable shape.
func TestSocketPathStaysShort(t *testing.T) {
	transport := newTestTransport(t, Host{ID: "h", Target: "h"})
	if got := len(transport.socketPath()); got > 100 {
		t.Errorf("control socket path is %d bytes, close to the darwin limit: %s", got, transport.socketPath())
	}
}
