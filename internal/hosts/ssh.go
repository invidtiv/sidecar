// Package hosts is the viewer's side of remote-host support: what a host is,
// how to reach it, and how to build the two SSH invocations that carry the two
// channels.
//
// The transport shape here is deliberately identical to the one the Herdr
// remote-hosts plan recorded, because it is the piece both plans share and
// neither should own alone. A single multiplexed master connection per host,
// established once, with every subsequent channel riding it as a cheap session
// rather than a fresh TCP+TLS+auth handshake. That is what makes an in-band
// tmux command over SSH cost a round trip instead of a connection.
package hosts

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Host is a registered remote machine.
type Host struct {
	// ID is the local name for this host. It scopes remote workspace IDs and
	// is what a row is grouped under in the Sessions browser.
	ID string
	// Target is the ssh destination: anything ssh_config resolves, which is
	// the point — the user's existing ssh setup is the configuration surface,
	// and Sidecar adds no second one.
	Target string
	// RemoteBinary is an explicit path to sidecar on the host. Empty means
	// resolve it through a login shell, which is required more often than not:
	// a non-interactive `ssh host sidecar` gets a PATH without Homebrew on it,
	// so the binary that is plainly installed appears missing.
	RemoteBinary string
	// RemoteConfig is an optional -config path passed to the remote sidecar.
	RemoteConfig string
	// Env is extra environment for the remote process, as KEY=VALUE. It is how
	// a proof run pins the remote to an isolated tmux server and state tree.
	Env []string
}

// Transport owns one host's multiplexed SSH connection.
//
// The master is started lazily and torn down explicitly. ControlPersist is set
// to a bounded lifetime rather than `yes`: a master that outlives Sidecar is a
// process the user did not ask for and cannot see, and the cost of
// re-establishing it after a few idle minutes is paid once.
type Transport struct {
	host    Host
	dir     string
	options []string
}

// ControlPersistIdle is how long an idle master lingers. Long enough that
// closing and reopening a pane does not re-authenticate; short enough that a
// forgotten Sidecar leaves nothing behind for long.
const ControlPersistIdle = 5 * time.Minute

// KeepaliveInterval and KeepaliveCountMax decide how fast a dropped link is
// noticed. Four missed 15-second probes is a one-minute detection floor, which
// is the right trade for a channel whose failure mode is a pane that silently
// stops updating: too aggressive and a busy link drops itself, too slow and a
// stale pane looks live.
const (
	KeepaliveInterval = 15
	KeepaliveCountMax = 4
)

// NewTransport prepares a transport. dir must be a private directory; the
// control socket is created inside it.
func NewTransport(host Host, dir string) (*Transport, error) {
	if strings.TrimSpace(host.Target) == "" {
		return nil, fmt.Errorf("hosts: empty ssh target for host %q", host.ID)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("hosts: control dir: %w", err)
	}
	transport := &Transport{host: host, dir: dir}
	transport.options = []string{
		// -T: never allocate a TTY. Both channels are byte pipes; a PTY would
		// insert line discipline between tmux and the parser and corrupt the
		// control stream.
		"-T",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + transport.socketPath(),
		"-o", "ControlPersist=" + strconv.Itoa(int(ControlPersistIdle.Seconds())),
		"-o", "ServerAliveInterval=" + strconv.Itoa(KeepaliveInterval),
		"-o", "ServerAliveCountMax=" + strconv.Itoa(KeepaliveCountMax),
		// A prompt on a channel nobody can answer is a hang, not a question.
		"-o", "BatchMode=yes",
	}
	return transport, nil
}

func (t *Transport) socketPath() string {
	// The socket name must be short: a unix socket path is capped near 104
	// bytes on darwin, and ssh's own %-expansions routinely overflow it. A
	// fixed short name inside a caller-chosen private dir is the reliable
	// shape.
	return filepath.Join(t.dir, "ctl")
}

// Host returns the host this transport serves.
func (t *Transport) Host() Host { return t.host }

// SSHArgs returns the ssh option prefix, without the target or the remote
// command. Exposed so a caller can see exactly what will run — the transport
// is meant to be inspectable, since "what ssh command did it actually build"
// is the first question every failure raises.
func (t *Transport) SSHArgs() []string {
	return append([]string(nil), t.options...)
}

// Command builds an ssh invocation running remote on the host.
//
// The remote side is wrapped in a login shell when the host does not pin an
// absolute binary path, because that is the only reliable way to find a
// Homebrew-installed binary: `ssh host sidecar` runs a non-login,
// non-interactive shell whose PATH omits /opt/homebrew/bin, and the failure
// reads as "sidecar is not installed" when it plainly is. This was observed on
// a real host during the Phase 0 spike, not anticipated.
func (t *Transport) Command(ctx context.Context, remote string) *exec.Cmd {
	args := append(t.SSHArgs(), t.host.Target, remote)
	return exec.CommandContext(ctx, "ssh", args...) //nolint:gosec // args are built here, not user-concatenated
}

// RemoteShell wraps a command so it runs under the remote user's login shell,
// with any host Env applied.
func (t *Transport) RemoteShell(command string) string {
	var builder strings.Builder
	for _, entry := range t.host.Env {
		builder.WriteString(shellQuoteAssignment(entry))
		builder.WriteByte(' ')
	}
	builder.WriteString(command)
	// `$SHELL -l -c` rather than a hardcoded shell: the host's login shell is
	// the one whose profile puts the binary on PATH.
	return "$SHELL -l -c " + shellQuote(builder.String())
}

// SidecarCommand renders the remote sidecar invocation for a subcommand.
func (t *Transport) SidecarCommand(args ...string) string {
	binary := t.host.RemoteBinary
	if binary == "" {
		binary = "sidecar"
	}
	parts := []string{shellQuote(binary)}
	if t.host.RemoteConfig != "" {
		parts = append(parts, "-config", shellQuote(t.host.RemoteConfig))
	}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return t.RemoteShell(strings.Join(parts, " "))
}

// ServeCommand is the channel-2 invocation: a headless, ephemeral, read-only
// serve process that dies with the connection.
func (t *Transport) ServeCommand() string {
	return t.SidecarCommand("host", "serve", "--stdio")
}

// ControlCommand is the channel-1 invocation: the remote tmux's own control
// protocol, which the local terminal stack already consumes verbatim.
//
// `-f ignore-size` is not optional. Without it this attach becomes a sizing
// client on the remote server and shrinks the session to whatever the viewer's
// pane happens to be — visibly resizing the window under a human sitting at
// that machine. It is the same flag the local attach uses, for the same
// reason.
func (t *Transport) ControlCommand(session string) string {
	tmux := []string{"tmux", "-C", "attach-session", "-f", "ignore-size", "-t", shellQuote(session)}
	return t.RemoteShell(strings.Join(tmux, " "))
}

// Close tears the master connection down. Leaving it to ControlPersist would
// also work, but an explicit exit means quitting Sidecar leaves no ssh process
// behind at all, which is the behaviour a user expects and can verify.
func (t *Transport) Close() error {
	args := append(t.SSHArgs(), "-O", "exit", t.host.Target)
	cmd := exec.Command("ssh", args...) //nolint:gosec // args are built here
	// A master that was never started makes this fail; that is not an error
	// worth reporting to anyone.
	_ = cmd.Run()
	return nil
}

// shellQuote renders s as a single POSIX shell word.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`&;|<>()*?[]{}~#!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellQuoteAssignment quotes the value half of a KEY=VALUE pair, leaving the
// key bare so the shell still parses it as an assignment prefix.
func shellQuoteAssignment(entry string) string {
	key, value, ok := strings.Cut(entry, "=")
	if !ok {
		return shellQuote(entry)
	}
	return key + "=" + shellQuote(value)
}
