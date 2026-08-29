package tty

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
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
