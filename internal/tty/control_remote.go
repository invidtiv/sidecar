package tty

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/clip"
)

// This file is the whole of the terminal stack's remote-host support.
//
// It is short because the control-mode consumer was already transport-agnostic
// behind one seam: newProcessControlChannelCommand wires any exec.Cmd's pipes
// into the line parser, and nothing below it knows or cares whether the
// command is a local tmux or an ssh carrying a remote one. Everything
// downstream — the single ordered actor, seed transactions and race detection,
// the byte-fed screen model at 30fps, %pause/%continue reseeds, the capture
// path's 12ms coalesce, and the polling fallback — is carried across unchanged
// by swapping the command.
//
// The local path is untouched: NewControlManager still builds exactly the
// factory it always did.

// ControlSpawner builds the command that carries one tmux session's control
// channel. Production's local spawner runs `tmux -C attach-session`; a remote
// spawner runs the same thing through ssh.
//
// It returns an *exec.Cmd rather than a pair of pipes so the caller owns
// process lifetime, environment, and cancellation — an ssh child needs all
// three handled differently from a local tmux, and the channel itself should
// not have to know which it got.
type ControlSpawner func(session string) *exec.Cmd

// NewRemoteControlManager builds a manager whose control channels are spawned
// by spawn instead of by a local tmux.
//
// The coalesce window is deliberately the same 12ms the local manager uses.
// It bounds how long the capture path waits to batch, and it is a property of
// how fast a human perceives a pane rather than of how far away tmux is;
// raising it for a slow link would trade a real latency increase for a
// bandwidth saving that the %output delta stream has already made small. If a
// link ever needs a different number, it should be measured and named, not
// guessed at here.
func NewRemoteControlManager(spawn ControlSpawner) *ControlManager {
	return newControlManager(spawnedControlChannelFactory(spawn), 12*time.Millisecond)
}

// spawnedControlChannelFactory adapts a ControlSpawner to the factory the
// manager consumes.
func spawnedControlChannelFactory(spawn ControlSpawner) controlChannelFactory {
	return func(session string) (controlChannel, error) {
		if spawn == nil {
			return nil, fmt.Errorf("tmux control: nil spawner")
		}
		cmd := spawn(session)
		if cmd == nil {
			return nil, fmt.Errorf("tmux control: spawner produced no command for session %q", session)
		}
		return newProcessControlChannelCommand(session, cmd)
	}
}

// UseRemoteControl points this terminal at a tmux server on another machine.
//
// The model's host-dependent operations all move onto the control connection:
//
//   - Interactive input is serialized through the ordinary ordered send queue.
//   - Geometry is protected by the same lease protocol as local viewers, with
//     this control connection's own input as its activity evidence.
//   - Bounded history capture is issued in-band. The ambient fallback remains
//     disabled rather than left pointing at local
//     tmux. This is the important one: pane IDs are per-server, so a local
//     `capture-pane -t %4` for a remote pane %4 does not fail — it succeeds,
//     against an unrelated local pane, and paints someone else's session into
//     the remote pane's view. An empty pane is a visible problem; the wrong
//     pane's content is an invisible one.
//
// Everything else is the ordinary local path: the same ordered actor, the same
// byte-fed screen model, the same seed and reseed behaviour.
func (m *Model) UseRemoteControl(spawn ControlSpawner) {
	manager := NewRemoteControlManager(spawn)
	backend := newRemoteTerminalBackend(m, manager)
	m.control = controlManagerSource{manager: manager}
	m.input = inBandInputSender{backend: backend}
	m.capture = unavailableCaptureSource{}
	m.remoteInputMu.Lock()
	m.remoteBackend = backend
	m.remoteInteractive = false
	m.remoteInputGeneration++
	m.remote = true
	m.remoteInputMu.Unlock()
}

// UseLocalControl restores the ordinary local transport, sender and capture.
//
// This is not symmetry for its own sake. The preview reuses ONE tty.Model
// across row selections, so without a way back a Model that had once shown a
// remote pane stayed remote forever: the next LOCAL row would be opened by
// `ssh <host> tmux -C attach-session -t <local session name>`. Both machines
// run Sidecar and derive session names the same way, so that attach often
// SUCCEEDS — painting the other machine's pane into a local workspace's
// preview, offering interactive mode that silently swallows every keystroke,
// and never resizing the pane again.
//
// Callers must set the mode on every activation rather than only when it
// changes. UseRemoteControl and this are the two halves of one decision.
func (m *Model) UseLocalControl() {
	m.releaseRemoteInput()
	m.control = defaultControlSource()
	m.input = defaultTerminalInputSender{model: m}
	m.capture = defaultTerminalCaptureSource{}
	m.remoteInputMu.Lock()
	m.remoteBackend = nil
	m.remote = false
	m.remoteInputGeneration++
	m.remoteInputMu.Unlock()
}

// IsRemote reports whether this terminal is served by another machine.
func (m *Model) IsRemote() bool { return m != nil && m.remote }

// ErrRemoteCaptureUnavailable is what the fallback capture path reports for a
// remote pane. Snapshot fallback remains disabled: bounded history has an
// explicit in-band path, while a fallback that ran local capture-pane could
// silently read an unrelated local pane with the same ID.
var ErrRemoteCaptureUnavailable = errors.New(
	"tmux capture: remote fallback unavailable; use in-band capture")

type unavailableCaptureSource struct{}

func (unavailableCaptureSource) Capture(string, int) (string, PaneState, error) {
	return "", PaneState{}, ErrRemoteCaptureUnavailable
}

// remoteTerminalBackend is every operation that belongs to the tmux server on
// another host. It never calls the ambient tmux binary: input, bounded history,
// lease storage, size queries, and resize all travel on manager's existing ssh
// control pipe.
type remoteTerminalBackend struct {
	model   *Model
	manager *ControlManager
	lease   *leaseKeeper
	input   atomic.Uint64
}

func newRemoteTerminalBackend(model *Model, manager *ControlManager) *remoteTerminalBackend {
	backend := &remoteTerminalBackend{model: model, manager: manager}
	backend.lease = newLeaseKeeper(remoteLeaseStore{backend: backend}, DefaultLeasePolicy, time.Second)
	return backend
}

func (b *remoteTerminalBackend) session() string {
	if b == nil || b.model == nil || b.model.State == nil {
		return ""
	}
	return b.model.State.TargetSession
}

func (b *remoteTerminalBackend) noteInput() {
	if b == nil {
		return
	}
	b.input.Add(1)
	b.lease.noteInput()
}

func (b *remoteTerminalBackend) captureRange(start, end int) (CaptureRange, error) {
	if start > end {
		return CaptureRange{}, fmt.Errorf("capture pane range: start %d after end %d", start, end)
	}
	target, session := b.model.GetTarget(), b.session()
	if target == "" || session == "" {
		return CaptureRange{}, fmt.Errorf("capture pane range: empty remote target")
	}
	commands := []string{
		"display-message -t " + controlQuote(target) + " -p '#{history_size}'",
		"capture-pane -p -e -N -t " + controlQuote(target) + " -S " + strconv.Itoa(start) + " -E " + strconv.Itoa(end),
	}
	responses, err := b.manager.requestControlBatch(session, commands...)
	if err != nil {
		return CaptureRange{}, fmt.Errorf("capture pane range: %w", err)
	}
	if len(responses) != 2 || len(responses[0].Lines) == 0 {
		return CaptureRange{}, fmt.Errorf("capture pane range: missing history metadata")
	}
	return parseCapturePaneRange(responses[0].Lines[0]+"\n"+strings.Join(responses[1].Lines, "\n"), start)
}

func (b *remoteTerminalBackend) querySize(target string) (width, height int, ok bool) {
	responses, err := b.manager.requestControlBatch(b.session(),
		"display-message -t "+controlQuote(target)+" -p '#{pane_width},#{pane_height}'")
	if err != nil || len(responses) != 1 || len(responses[0].Lines) == 0 {
		return 0, 0, false
	}
	parts := strings.SplitN(responses[0].Lines[0], ",", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, errW := strconv.Atoi(parts[0])
	height, errH := strconv.Atoi(parts[1])
	return width, height, errW == nil && errH == nil
}

func (b *remoteTerminalBackend) resize(target string, width, height int) bool {
	if b == nil || target == "" || width <= 0 || height <= 0 || !b.lease.allow(target) {
		return false
	}
	if actualW, actualH, ok := b.querySize(target); ok && actualW == width && actualH == height {
		return false
	}
	command := "resize-window -t " + controlQuote(target) + " -x " + strconv.Itoa(width) + " -y " + strconv.Itoa(height)
	if _, err := b.manager.requestControlBatch(b.session(), command); err != nil {
		fallback := "resize-pane -t " + controlQuote(target) + " -x " + strconv.Itoa(width) + " -y " + strconv.Itoa(height)
		if _, fallbackErr := b.manager.requestControlBatch(b.session(), fallback); fallbackErr != nil {
			return false
		}
	}
	return true
}

// remoteLeaseStore keeps cross-host arbitration in the tmux session option,
// but reaches that option through the remote control pipe. inputMark is the
// viewer's own successful-send counter; probing remote client tty/activity
// would confuse input on another machine with input here.
type remoteLeaseStore struct{ backend *remoteTerminalBackend }

func (s remoteLeaseStore) read(target string) (session, token string, ok bool) {
	if s.backend == nil || s.backend.session() == "" {
		return "", "", false
	}
	responses, err := s.backend.manager.requestControlBatch(s.backend.session(),
		"display-message -t "+controlQuote(target)+" -p '#{session_name}\t#{"+leaseOptionName+"}'")
	if err != nil || len(responses) != 1 || len(responses[0].Lines) == 0 {
		return "", "", false
	}
	session, token, found := strings.Cut(responses[0].Lines[0], "\t")
	return session, strings.TrimSpace(token), found && session != ""
}

func (s remoteLeaseStore) set(session, token string) {
	if s.backend != nil {
		_ = s.backend.manager.sendControlBatch(s.backend.session(),
			"set-option -t "+controlQuote(session)+" "+leaseOptionName+" "+controlQuote(token))
	}
}

func (s remoteLeaseStore) clear(session string) {
	if s.backend != nil {
		// A release is a lifetime barrier, not an advisory write. The manager
		// retains the client until tmux executes the unset, even if the pane's last
		// subscription closes immediately afterward. Sending stays non-blocking so
		// Exit and ReleaseInput never stall Bubble Tea on a network round trip.
		_ = s.backend.manager.sendControlBarrier(s.backend.session(),
			"set-option -u -t "+controlQuote(session)+" "+leaseOptionName)
	}
}

func (s remoteLeaseStore) inputMark(string) string {
	if s.backend == nil {
		return ""
	}
	return strconv.FormatUint(s.backend.input.Load(), 10)
}

type inBandInputSender struct{ backend *remoteTerminalBackend }

func (s inBandInputSender) send(scope MessageScope, commands ...string) tea.Cmd {
	if s.backend == nil || s.backend.model == nil || !s.backend.model.remoteInputOwned(s.backend, 0) {
		return nil
	}
	session := s.backend.session()
	queueKey := fmt.Sprintf("remote:%p:%s", s.backend.manager, session)
	return awaitOrderedSend(scope, SendOrdered(queueKey, func() error {
		return s.backend.model.withActivationError(scope, func() error {
			if err := s.backend.manager.sendControlBatch(session, commands...); err != nil {
				return err
			}
			s.noteInput()
			return nil
		})
	}))
}

func (s inBandInputSender) noteInput() { s.backend.noteInput() }

func (s inBandInputSender) SendKeys(scope MessageScope, target string, keys ...KeySpec) tea.Cmd {
	return s.send(scope, InBandSendKeys(target, keys...)...)
}

func (s inBandInputSender) pasteText(text string) string {
	if s.backend.model.State != nil && s.backend.model.State.BracketedPasteEnabled {
		return "\x1b[200~" + text + "\x1b[201~"
	}
	return text
}

func (s inBandInputSender) SendPaste(scope MessageScope, target, text string) tea.Cmd {
	return s.send(scope, InBandSendLiteral(target, s.pasteText(text)))
}

func (s inBandInputSender) SendEscapePaste(scope MessageScope, target, text string) tea.Cmd {
	commands := InBandSendKeys(target, KeySpec{Value: "Escape"})
	commands = append(commands, InBandSendLiteral(target, s.pasteText(text)))
	return s.send(scope, commands...)
}

func (s inBandInputSender) PasteClipboard(scope MessageScope, target string) tea.Cmd {
	if s.backend == nil || !s.backend.model.remoteInputOwned(s.backend, 0) {
		return nil
	}
	var result PasteResultMsg
	session := s.backend.session()
	queueKey := fmt.Sprintf("remote:%p:%s", s.backend.manager, session)
	done := SendOrdered(queueKey, func() error {
		return s.backend.model.withActivationError(scope, func() error {
			result.Scope = scope
			text, err := clip.ReadAll()
			if err != nil || text == "" {
				if recent, ok := clip.LastCopied(); ok && recent != "" {
					text, err = recent, nil
				}
			}
			if err != nil {
				result.Err = err
				return nil
			}
			if text == "" {
				result.Empty = true
				return nil
			}
			result.Err = s.backend.manager.sendControlBatch(session, InBandSendLiteral(target, s.pasteText(text)))
			if result.Err == nil {
				s.noteInput()
			}
			return nil
		})
	})
	return func() tea.Msg {
		<-done
		if result.Scope.Owner == 0 {
			return nil
		}
		return result
	}
}

func (s inBandInputSender) SendMouse(scope MessageScope, target string, col, row int) tea.Cmd {
	if col <= 0 || row <= 0 {
		return nil
	}
	press := fmt.Sprintf("\x1b[<0;%d;%dM", col, row)
	release := fmt.Sprintf("\x1b[<0;%d;%dm", col, row)
	return s.send(scope, InBandSendLiteral(target, press), InBandSendLiteral(target, release))
}

func (s inBandInputSender) SendWheel(scope MessageScope, target string, up bool, col, row, notches int) tea.Cmd {
	if col <= 0 || row <= 0 || notches <= 0 {
		return nil
	}
	button := SGRWheelDown
	if up {
		button = SGRWheelUp
	}
	report := fmt.Sprintf("\x1b[<%d;%d;%dM", button, col, row)
	return s.send(scope, InBandSendLiteral(target, strings.Repeat(report, notches)))
}

// CaptureRange reads bounded history from the terminal's own host.
func (m *Model) CaptureRange(start, end int) (CaptureRange, error) {
	if m.remote {
		if m.remoteBackend == nil {
			return CaptureRange{}, ErrRemoteCaptureUnavailable
		}
		return m.remoteBackend.captureRange(start, end)
	}
	return CapturePaneRange(m.GetTarget(), start, end)
}

// ActivateInput turns a remote viewer into an interactive claimant. Local
// terminals already claim through their existing surface path, so this is a
// no-op for them.
func (m *Model) ActivateInput() tea.Cmd {
	if !m.remote || m.remoteBackend == nil || !m.IsActive() {
		return nil
	}
	m.remoteInputMu.Lock()
	m.remoteInteractive = true
	m.remoteInputGeneration++
	generation := m.remoteInputGeneration
	backend := m.remoteBackend
	m.remoteInputMu.Unlock()
	scope, target := m.Scope(), m.GetTarget()
	width, height := m.Width, m.Height
	return func() tea.Msg {
		return m.withActivationMessage(scope, func() tea.Msg {
			m.remoteInputMu.Lock()
			defer m.remoteInputMu.Unlock()
			if !m.remote || !m.remoteInteractive || m.remoteBackend != backend ||
				m.remoteInputGeneration != generation {
				return nil
			}
			// Interactive ownership is a standing geometry-driving path, just
			// like an attached client: periodic ordinary arbitration ticks keep
			// the token fresh and its idle evidence honest at a settled size.
			backend.lease.hold(target)
			if backend.resize(target, width, height) {
				return PaneResizedMsg{Scope: scope}
			}
			return nil
		})
	}
}

func (m *Model) releaseRemoteInput() {
	if m == nil {
		return
	}
	m.remoteInputMu.Lock()
	defer m.remoteInputMu.Unlock()
	if !m.remoteInteractive || m.remoteBackend == nil {
		return
	}
	m.remoteInteractive = false
	m.remoteInputGeneration++
	m.remoteBackend.lease.release()
}

// remoteInputOwned fences work queued for an older input activation. A zero
// generation asks only whether backend currently owns input.
func (m *Model) remoteInputOwned(backend *remoteTerminalBackend, generation uint64) bool {
	if m == nil {
		return false
	}
	m.remoteInputMu.Lock()
	defer m.remoteInputMu.Unlock()
	return m.remote && m.remoteInteractive && m.remoteBackend == backend &&
		(generation == 0 || m.remoteInputGeneration == generation)
}

// SetApplicationFocused releases a remote geometry claim on blur and reclaims
// it on focus if this pane still owns input.
func (m *Model) SetApplicationFocused(focused bool) tea.Cmd {
	if !m.remote || m.remoteBackend == nil {
		return nil
	}
	m.remoteInputMu.Lock()
	backend, interactive := m.remoteBackend, m.remoteInteractive
	backend.lease.setFocused(focused)
	m.remoteInputMu.Unlock()
	if focused && interactive {
		return m.ActivateInput()
	}
	return nil
}

// InBandSendKeys renders a key batch as tmux command lines to be written to an
// open control channel's stdin.
//
// This is the bottom half of the Phase B remote input sender, kept state-free
// so it can be tested without a channel, a session, or a network — and so a
// headless caller could adopt it unchanged.
//
// Why in-band at all: the local sender spawns one `tmux send-keys` subprocess
// per key batch. Over ssh that becomes one remote process execution per
// keystroke, which is both slow and — more importantly — no longer ordered by
// anything, because N concurrent ssh sessions have no defined relative
// ordering. Writing the same commands to the already-open control channel is
// one write on one pipe, and tmux executes them in the order it read them.
// That is the FIFO property the local send queue exists to guarantee
// (td-8fcd2e), preserved rather than reimplemented.
func InBandSendKeys(target string, keys ...KeySpec) []string {
	commands := make([]string, 0, len(keys))
	for _, key := range keys {
		if key.Literal {
			commands = append(commands, inBandLiteral(target, key.Value))
			continue
		}
		commands = append(commands, "send-keys -t "+controlQuote(target)+" "+controlQuote(key.Value))
	}
	return commands
}

// InBandSendLiteral renders literal text as a single hex-encoded send-keys.
func InBandSendLiteral(target, text string) string {
	return inBandLiteral(target, text)
}

// inBandLiteral always hex-encodes.
//
// The local literal path only falls back to -H when the text contains a
// semicolon, because tmux treats a bare `;` in argv as a command separator.
// In-band there is no argv: the whole line is parsed by tmux's command parser,
// where semicolons, quotes, backslashes and newlines are all live. Rather than
// re-deriving which characters are safe on a control line — the kind of
// analysis that is wrong once and then wrong forever — every literal is hex.
// The cost is two characters per byte on a channel that carries kilobytes of
// pane output per frame.
func inBandLiteral(target, text string) string {
	var builder strings.Builder
	builder.WriteString("send-keys -t ")
	builder.WriteString(controlQuote(target))
	builder.WriteString(" -H")
	for _, b := range []byte(text) {
		_, _ = fmt.Fprintf(&builder, " %02x", b)
	}
	return builder.String()
}

// controlQuote renders s as one tmux command-line word.
//
// tmux's parser understands single quotes as fully literal, with no escape
// sequence inside them at all — so a value containing a single quote cannot be
// single-quoted. Those go to double quotes with backslash escapes, which is
// tmux's other quoting form.
func controlQuote(s string) string {
	if s == "" {
		return "''"
	}
	if isPlainControlWord(s) {
		return s
	}
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	// Inside double quotes tmux expands $ and backticks and honours
	// backslashes, so all four are neutralised. A newline cannot be escaped in
	// either quoting form, so it is stripped: the transport rejects multiline
	// commands outright, and this keeps controlQuote's own contract — one word
	// — true on its own rather than relying on that check.
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`", "\n", "", "\r", "")
	return `"` + replacer.Replace(s) + `"`
}

// isPlainControlWord reports whether s can go on a tmux command line unquoted.
//
// An allow-list, for the reason the shell one is: tmux's parser has more
// special forms than a deny-list keeps up with. Two that a character-class
// deny-list misses entirely, both verified against tmux 3.6b:
//
//   - A word starting with "-" is consumed as a flag by whichever command
//     receives it, so a literal "-X" reaches send-keys' own getopt.
//   - "%" followed by a letter lexes as a directive (%if, %hidden) and is a
//     syntax error for the whole line. Pane IDs are %<digits>, which is why
//     digits after % are allowed and letters are not.
func isPlainControlWord(s string) bool {
	if strings.HasPrefix(s, "-") {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '/' || r == '@' || r == ':' || r == '+' || r == ',' || r == '-':
		case r == '%' && i == 0 && len(s) > 1 && s[1] >= '0' && s[1] <= '9':
		default:
			return false
		}
	}
	return true
}
