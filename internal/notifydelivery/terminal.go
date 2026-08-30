package notifydelivery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/termnotify"
)

const (
	// terminalProvider names the transport before a terminal has been chosen.
	// Once one is, the provider becomes "terminal:ghostty" and friends, so
	// status can say which encoder is in force rather than only that one is.
	terminalProvider       = "terminal"
	terminalProviderPrefix = terminalProvider + ":"

	// terminalOffReason is what an untouched configuration reports. The
	// transport is off by default: a Sidecar upgrade must not start writing
	// escape sequences into somebody's SSH session.
	terminalOffReason = "direct terminal notifications are off; set notifications.ssh.terminal to auto or a terminal name"

	// terminalBestEffort is the whole of what this transport can promise. The
	// outer terminal owns the banner, so nothing downstream of the write is
	// observable from here. Status says so rather than implying the delivery
	// guarantees the desktop providers give.
	terminalBestEffort = "best effort: the outer terminal owns the banner, so there is no Sidecar sound, click activation, replacement, removal, or delivery receipt"

	// terminalTmuxCaveat is the one setup fact Sidecar cannot determine from
	// inside a pane. tmux drops a passthrough sequence unless the user has
	// enabled it, and there is no reply to ask for.
	terminalTmuxCaveat = "inside tmux this needs `set -g allow-passthrough on`"
)

// TerminalOptions binds the pure encoders in internal/termnotify to the native
// notifier seam.
//
// Write and Flush are injected rather than resolved here because the human TUI
// and these protocol bytes share one process and one file descriptor. Only an
// app-owned writer knows when it is safe to emit them, and a package that
// picked its own output stream would eventually mix an escape sequence into
// structured CLI output.
type TerminalOptions struct {
	// Getenv supplies the environment the terminal is detected from.
	Getenv func(string) string
	// Selected reads the configured terminal. It is called per probe and per
	// delivery, so a Configuration change applies without a restart.
	Selected func() config.TerminalNotifier
	// Write emits one complete sequence.
	Write func([]byte) (int, error)
	// Flush is called after a successful write. A nil Flush means the writer
	// buffers nothing, which is true of the unbuffered default below.
	Flush func() error
}

// terminalNative delivers a notification to the terminal Sidecar is attached
// to. It holds no encoder state: every field is either injected or read fresh.
type terminalNative struct {
	getenv    func(string) string
	selection func() config.TerminalNotifier
	write     func([]byte) (int, error)
	flush     func() error

	// mu serializes writes. Two deliveries interleaving inside one OSC would
	// leave the terminal parsing a sequence that neither of them wrote.
	mu sync.Mutex
}

var _ NativeNotifier = (*terminalNative)(nil)

// NewTerminalNative builds the opt-in direct-terminal transport. Construction
// performs no I/O and reads no configuration.
func NewTerminalNative(opts TerminalOptions) NativeNotifier {
	selection := opts.Selected
	if selection == nil {
		selection = func() config.TerminalNotifier { return config.TerminalNotifierOff }
	}
	return &terminalNative{
		getenv: opts.Getenv, selection: selection,
		write: opts.Write, flush: opts.Flush,
	}
}

func (t *terminalNative) Probe(context.Context) Capability {
	_, capability := t.resolve()
	return capability
}

// resolve answers which encoder is in force and why, for both Probe and
// Deliver. Keeping them on one path is what stops status from advertising a
// terminal that delivery would then refuse.
func (t *terminalNative) resolve() (termnotify.Terminal, Capability) {
	if t == nil || t.write == nil {
		return "", Capability{Provider: terminalProvider, Reason: "no terminal writer is bound"}
	}
	switch selected := t.selection(); selected {
	case config.TerminalNotifierOff, "":
		return "", Capability{Provider: terminalProvider, Reason: terminalOffReason}
	case config.TerminalNotifierAuto:
		// Detection is allowed to fail, and says why. SSH commonly drops
		// TERM_PROGRAM and tmux replaces TERM, so there are real sessions where
		// nothing identifies the outer terminal — and an unidentified terminal
		// stays unavailable rather than receiving a guessed sequence.
		detected := termnotify.Detect(t.getenv)
		if !detected.OK() {
			return "", Capability{Provider: terminalProvider, Reason: detected.Reason}
		}
		return detected.Terminal, t.capabilityFor(detected.Terminal, "detected from "+detected.Marker)
	default:
		encoder, ok := termnotify.ParseTerminal(string(selected))
		if !ok {
			// config.ValidateNotifications refuses these before a save, so
			// reaching here means a hand-edited config.json.
			return "", Capability{Provider: terminalProvider, Reason: fmt.Sprintf("%q has no Sidecar encoder", selected)}
		}
		return encoder, t.capabilityFor(encoder, "")
	}
}

func (t *terminalNative) capabilityFor(encoder termnotify.Terminal, how string) Capability {
	notes := make([]string, 0, 3)
	if how != "" {
		notes = append(notes, how)
	}
	notes = append(notes, terminalBestEffort)
	if termnotify.InsideTmux(t.getenv) {
		notes = append(notes, terminalTmuxCaveat)
	}
	return Capability{
		Available: true,
		Provider:  terminalProviderPrefix + string(encoder),
		Reason:    strings.Join(notes, "; "),
	}
}

func (t *terminalNative) Deliver(ctx context.Context, message Message) (ProviderReceipt, error) {
	encoder, capability := t.resolve()
	receipt := ProviderReceipt{Provider: capability.Provider, At: time.Now().UTC()}
	if !capability.Available {
		return receipt, fmt.Errorf("direct terminal notifications unavailable: %s", capability.Reason)
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	// The encoder sanitizes and frames. Nothing between here and the writer may
	// reshape the bytes: the sequence it returns is the contract.
	sequence, err := termnotify.Encode(encoder, termnotify.Notification{
		ID:    message.NotificationID,
		Title: message.Title,
		Body:  message.Body,
	}, termnotify.InsideTmux(t.getenv))
	if err != nil {
		return receipt, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.write([]byte(sequence)); err != nil {
		return receipt, fmt.Errorf("write terminal notification: %w", err)
	}
	if t.flush != nil {
		if err := t.flush(); err != nil {
			return receipt, fmt.Errorf("flush terminal notification: %w", err)
		}
	}
	// Delivered means the sequence left Sidecar. The outer terminal never
	// answers, so this is the last fact available; terminalBestEffort is how
	// status says that out loud.
	receipt.Delivered = true
	return receipt, nil
}

// Remove is unsupported by contract. None of these encoders can withdraw a
// banner the outer terminal has already shown, and Service.Remove treats
// ErrUnsupported as "nothing to do" rather than a failure.
func (t *terminalNative) Remove(context.Context, string) error { return ErrUnsupported }

// RemoteCapable marks this transport as honest inside SSH. See
// remoteCapableNative.
func (t *terminalNative) RemoteCapable() bool { return true }

// StderrTerminalWriter is the default writer: standard error, and only when
// standard error is a terminal.
//
// Standard error rather than standard output, because `sidecar notify ...
// --json` puts machine-readable output on stdout and protocol bytes must never
// be mixed into it. Refusing a non-terminal stderr keeps the transport honest
// under a redirect or a pipe, where the sequence would be noise in a log file
// instead of a notification.
func StderrTerminalWriter(p []byte) (int, error) {
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return 0, errors.New("standard error is not a terminal")
	}
	return os.Stderr.Write(p)
}

// NewNativeWithTerminal routes native delivery by locality.
//
// A local process uses the platform desktop provider. A process inside SSH
// uses the direct-terminal transport, which is the only native provider that
// can do anything useful from there — the desktop providers stay fail-closed
// remote, and sound never crosses the boundary at all.
func NewNativeWithTerminal(local, terminal NativeNotifier, getenv func(string) string) NativeNotifier {
	return &terminalRoutedNative{local: local, terminal: terminal, getenv: getenv}
}

type terminalRoutedNative struct {
	local    NativeNotifier
	terminal NativeNotifier
	getenv   func(string) string
}

var _ NativeNotifier = (*terminalRoutedNative)(nil)

func (n *terminalRoutedNative) active() NativeNotifier {
	if remoteSession(n.getenv) {
		return n.terminal
	}
	return n.local
}

func (n *terminalRoutedNative) Probe(ctx context.Context) Capability {
	active := n.active()
	if active == nil {
		return Capability{Reason: "no native notification provider"}
	}
	return active.Probe(ctx)
}

func (n *terminalRoutedNative) Deliver(ctx context.Context, message Message) (ProviderReceipt, error) {
	active := n.active()
	if active == nil {
		return ProviderReceipt{At: time.Now().UTC()}, errors.New("no native notification provider")
	}
	return active.Deliver(ctx, message)
}

func (n *terminalRoutedNative) Remove(ctx context.Context, group string) error {
	active := n.active()
	if active == nil {
		return ErrUnsupported
	}
	return active.Remove(ctx, group)
}

// RemoteCapable is true because one of the two routes is. Whether the remote
// route can actually deliver is Probe's answer, not this one's, so the decision
// path stays free of configuration reads.
func (n *terminalRoutedNative) RemoteCapable() bool { return true }

// remoteCapableNative reports whether a native provider is honest inside SSH.
//
// Only the direct-terminal transport is: it writes to the terminal this process
// is already attached to, rather than a desktop notification service that does
// not exist on the remote host. Every other provider stays fail-closed remote,
// which is the M3 refusal this must not weaken — a provider that does not
// declare itself remote-capable is never probed or invoked inside SSH.
func remoteCapableNative(notifier NativeNotifier) bool {
	remote, ok := notifier.(interface{ RemoteCapable() bool })
	return ok && remote.RemoteCapable()
}
