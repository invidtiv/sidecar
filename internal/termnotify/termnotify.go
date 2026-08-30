// Package termnotify encodes a notification as a terminal escape sequence for
// the fixed set of terminals Sidecar supports.
//
// It exists for one situation: Sidecar running directly inside an ordinary SSH
// terminal. There is no local viewer to hand the event to, and the remote host
// has no desktop notification service or audio device worth reaching — the
// outer terminal on the far end of the connection is the only thing that can
// get the user's attention.
//
// The package is deliberately state-free and byte-exact. Every sequence it
// produces is asserted literally in the tests, because the contract here is the
// bytes themselves: a sequence that is one character wrong is not a degraded
// notification, it is arbitrary text painted into the user's terminal. For the
// same reason the sequences are spelled out here rather than borrowed from a
// helper library, whose choice of terminator could change under us.
//
// Nothing in this package performs I/O, reads configuration, or knows about
// Bubble Tea. Callers supply the environment lookup and the writer.
package termnotify

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Terminal names one supported outer terminal. These values match the
// notifications.ssh.terminal config vocabulary.
type Terminal string

const (
	Ghostty Terminal = "ghostty"
	ITerm2  Terminal = "iterm2"
	WezTerm Terminal = "wezterm"
	Kitty   Terminal = "kitty"
)

var (
	// ErrUnsupportedTerminal is returned for any terminal outside the fixed
	// matrix. There is no generic fallback on purpose: BEL or a guessed escape
	// in an unknown terminal is noise or garbage on screen, not a notification.
	ErrUnsupportedTerminal = errors.New("termnotify: unsupported terminal")
	// ErrEmptyNotification refuses a sequence with nothing left to say, which is
	// what a title and body made entirely of control characters sanitize down to.
	ErrEmptyNotification = errors.New("termnotify: notification has no displayable text")
)

// Supported lists the terminals with a fixed encoder, in config order.
func Supported() []Terminal { return []Terminal{Ghostty, ITerm2, WezTerm, Kitty} }

// ParseTerminal resolves a configured terminal name. It accepts only the exact
// vocabulary; a near miss is a refusal, never a guess at what was meant.
func ParseTerminal(name string) (Terminal, bool) {
	for _, candidate := range Supported() {
		if string(candidate) == name {
			return candidate, true
		}
	}
	return "", false
}

// Notification is the bounded input to an encoder. ID correlates the two
// chunks of a Kitty notification and is ignored by the OSC 9 encoders.
type Notification struct {
	ID    string
	Title string
	Body  string
}

const (
	esc = "\x1b"
	bel = "\x07"
	// st is the 7-bit string terminator. The C1 form (0x9c) is one byte
	// shorter and is misread as a UTF-8 continuation byte by enough terminals
	// that it is not worth the saving.
	st = esc + `\`

	// tmuxPassthroughPrefix opens tmux's DCS passthrough.
	tmuxPassthroughPrefix = esc + "Ptmux;"

	// maxTitleRunes and maxBodyRunes bound what leaves Sidecar for the outer
	// terminal. The delivery layer already truncates to the same limits; the
	// encoder repeats them so a caller that builds a Notification by hand
	// cannot push a multi-kilobyte escape sequence at somebody's terminal.
	maxTitleRunes = 120
	maxBodyRunes  = 500

	// maxIDRunes bounds the Kitty notification identifier. Kitty only needs it
	// to match the chunks of one notification to each other.
	maxIDRunes = 32

	// fallbackID is used when the caller supplies no usable identifier. Two
	// concurrent notifications sharing it is harmless: Kitty would coalesce
	// their chunks, and Sidecar's delivery ledger already prevents two
	// notifications from being delivered at once on one host.
	fallbackID = "sidecar"
)

// zeroWidthJoiner is U+200D, the one Cf character worth keeping — emoji
// sequences are built from it and a workspace name may contain one.
const zeroWidthJoiner = '‍'

// Encode returns the exact bytes that deliver n through term.
//
// tmux reports whether this process is inside a tmux client, in which case
// every sequence is wrapped in tmux's DCS passthrough so it reaches the outer
// terminal instead of being swallowed by tmux.
func Encode(term Terminal, n Notification, tmux bool) (string, error) {
	title := truncate(Sanitize(n.Title), maxTitleRunes)
	body := truncate(Sanitize(n.Body), maxBodyRunes)
	if title == "" {
		// A body with no title reads as a notification with no text at all in
		// every one of these terminals, so promote it rather than lose it.
		title, body = body, ""
	}
	if title == "" {
		return "", ErrEmptyNotification
	}
	switch term {
	case Ghostty, ITerm2, WezTerm:
		return passthrough(osc9(joinOSC9(title, body)), tmux), nil
	case Kitty:
		return encodeKitty(sanitizeID(n.ID), title, body, tmux), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedTerminal, term)
	}
}

// osc9 is the single-field notification OSC that Ghostty, iTerm2 and WezTerm
// all implement. BEL rather than ST terminates it: iTerm2 documents BEL, and
// BEL is what survives tmux's DCS passthrough in every terminal this matrix
// covers.
func osc9(text string) string { return esc + "]9;" + text + bel }

// joinOSC9 flattens title and body into the one field OSC 9 offers. The
// separator is a plain colon so the result stays unambiguous when a title
// already ends in punctuation.
func joinOSC9(title, body string) string {
	if body == "" {
		return title
	}
	return title + ": " + body
}

// encodeKitty writes Kitty's structured OSC 99 form: metadata as
// colon-separated key/value pairs, then the payload. Title and body are
// separate chunks sharing one identifier, and only the last chunk carries d=1,
// which is what tells Kitty the notification is complete and may be shown.
func encodeKitty(id, title, body string, tmux bool) string {
	if body == "" {
		return passthrough(osc99(id, "1", "title", title), tmux)
	}
	return passthrough(osc99(id, "0", "title", title), tmux) +
		passthrough(osc99(id, "1", "body", body), tmux)
}

func osc99(id, done, payloadType, payload string) string {
	return esc + "]99;i=" + id + ":d=" + done + ":p=" + payloadType + ";" + payload + st
}

// passthrough puts one complete sequence inside tmux's DCS passthrough.
//
// Every ESC in the payload must be doubled. tmux ends the passthrough at the
// first undoubled ESC, which would leave the tail of our own sequence to be
// read as ordinary terminal input — the exact breakout this transport must not
// have. The closing ST belongs to the passthrough itself and is not doubled.
//
// Passthrough also requires `set -g allow-passthrough on` in the user's tmux
// configuration. Sidecar cannot detect that from inside the pane, so the
// delivery layer reports it as a caveat rather than pretending to know.
func passthrough(sequence string, tmux bool) string {
	if !tmux {
		return sequence
	}
	return tmuxPassthroughPrefix + strings.ReplaceAll(sequence, esc, esc+esc) + st
}

// Sanitize removes everything that could terminate or redirect the sequence
// carrying it.
//
// Notification titles and bodies are attacker-influenced: they carry branch
// names, agent output and workspace names from outside Sidecar. An ESC, a BEL
// or a string terminator in one of them would end the OSC early and leave the
// remainder to be executed as terminal commands. Newlines and tabs collapse to
// single spaces for the same reason, and format characters go because a body
// containing U+202E would render the notification backwards.
func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			// Collapse any run of whitespace — including the newlines and tabs
			// that would otherwise be dropped as control characters — to one
			// space, deferred so trailing runs vanish.
			space = b.Len() > 0
		case unicode.IsControl(r):
			// Drop: ESC, BEL, DEL and the rest of C0/C1. C1 matters as much as
			// C0 here: U+009D is the single-byte OSC introducer and U+009C the
			// single-byte string terminator, and neither is visible in a diff.
		case r == unicode.ReplacementChar:
			// Invalid UTF-8 decodes to U+FFFD. Terminals disagree about how
			// wide it is, so drop it rather than let it shift a title.
		case unicode.Is(unicode.Cf, r) && r != zeroWidthJoiner:
			// Drop format characters, including the bidi overrides.
		default:
			if space {
				b.WriteRune(' ')
				space = false
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitizeID reduces an identifier to the alphabet Kitty accepts. Sidecar's
// notification IDs already fit, but the encoder cannot assume its one current
// caller is its only future one.
func sanitizeID(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '+' || r == '.':
			b.WriteRune(r)
		}
		if b.Len() >= maxIDRunes {
			break
		}
	}
	if b.Len() == 0 {
		return fallbackID
	}
	return b.String()
}

// truncate bounds text by runes, never bytes, so a limit cannot land in the
// middle of a multi-byte character and emit an invalid UTF-8 payload.
func truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return strings.TrimRight(string(runes[:limit]), " ")
}
