package tty

import (
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// A split SGR mouse report can reach a host as ordinary key text. Two windows
// tell the difference between that and real typing: the escape that opened the
// report was delivered microseconds before its continuation, and any successfully
// parsed mouse event comes from the same burst of terminal output as the leak.
const (
	EscapeFragmentWindow = 5 * time.Millisecond
	MouseFragmentWindow  = 10 * time.Millisecond
)

// KeyGate is what a terminal surface does with a key press before any of it
// becomes input for the pane.
type KeyGate uint8

const (
	// KeyGateSend is an ordinary key: send it, preceded by whatever escape the
	// double-escape window is still holding.
	KeyGateSend KeyGate = iota
	// KeyGateDrop is a fragment of an SGR mouse report that the input reader
	// split across reads and delivered as text. Any held escape belongs to that
	// report rather than to the keyboard, so it is dropped with the fragment.
	KeyGateDrop
	// KeyGateHoldEscape is an escape that must not be forwarded yet: it may be
	// the first half of the double-escape that leaves interactive mode.
	KeyGateHoldEscape
	// KeyGateExitDoubleEscape is the second escape inside that window.
	KeyGateExitDoubleEscape
)

// KeyGateInput is one key press and the two clocks the decision reads.
type KeyGateInput struct {
	Msg tea.KeyPressMsg

	// EscapePressed and EscapeAt are the state of the double-escape window.
	EscapePressed bool
	EscapeAt      time.Time

	// LastMouseAt is when a mouse event last reached the host, whether or not it
	// was routed to this surface.
	LastMouseAt time.Time

	Now time.Time
}

// GateKey classifies a key press over a live terminal surface. It decides only
// what kind of key this is; what a host does about it — how it leaves the mode,
// how it sends — stays the host's.
//
// With all-motion mouse reporting on, the terminal emits an SGR sequence for
// every pointer movement, and the input reader can split one across reads:
//
//	Read 1: ESC        → an escape key press, or consumed internally
//	Read 2: [          → a bare bracket rune, the leak
//	Read 3: <35;10;20M → runes, or a parsed mouse event
//
// The escape window catches the case where the ESC arrived as a key press. When
// the reader consumed the ESC itself, nothing marks the window, and only the
// proximity of a real mouse event distinguishes the leak from someone typing a
// bracket.
func GateKey(in KeyGateInput) KeyGate {
	msg := in.Msg
	if msg.Code == tea.KeyEscape {
		if in.EscapePressed {
			return KeyGateExitDoubleEscape
		}
		return KeyGateHoldEscape
	}
	if len(msg.Text) > 0 && LooksLikeMouseFragment(msg.Text) {
		return KeyGateDrop
	}
	if msg.Text == "[" {
		escGate := in.EscapePressed && in.Now.Sub(in.EscapeAt) < EscapeFragmentWindow
		mouseGate := in.Now.Sub(in.LastMouseAt) < MouseFragmentWindow
		if escGate || mouseGate {
			return KeyGateDrop
		}
	}
	return KeyGateSend
}

// SnapBackCooldown suppresses snap-back for input arriving right after a scroll.
// A flick that leaks mouse-report bytes as key text would otherwise throw the
// viewport back to the live edge in the middle of the gesture.
const SnapBackCooldown = 100 * time.Millisecond

// ShouldSnapBack reports whether a key press is real typing, which a scrolled-back
// viewport owes a jump to the live edge: keystrokes that land under a window
// parked in history are invisible as they are typed.
//
// sinceScroll is how long ago the surface last scrolled.
func ShouldSnapBack(msg tea.KeyPressMsg, sinceScroll time.Duration) bool {
	if sinceScroll < SnapBackCooldown {
		return false
	}
	if len(msg.Text) > 0 {
		if LooksLikeMouseFragment(msg.Text) {
			return false
		}
		// Multi-character text is a paste or a split sequence, never one keystroke.
		return len([]rune(msg.Text)) == 1
	}
	// An escape may be the first byte of a mouse report; the double-escape window
	// owns the real one.
	if msg.Code == tea.KeyEscape {
		return false
	}
	return true
}

// ScrollbackMove is where a shift-scrollback key puts the window. Rows is a
// relative move, positive towards older output; the two jumps are absolute.
type ScrollbackMove struct {
	Rows     int
	ToOldest bool
	ToLive   bool
}

// ScrollbackState is who owns the keyboard over a terminal surface. It is what
// decides which form of a scrollback key that surface may claim: the shift
// requirement is a property of the state — an unshifted key belongs to a live
// pane — rather than of the key.
type ScrollbackState uint8

const (
	// ScrollbackLive is a pane taking input. A navigation key is the surface's
	// only with shift; everything else the keyboard sends is typing.
	ScrollbackLive ScrollbackState = iota
	// ScrollbackWatched is a pane nobody is typing into. The same navigation
	// keys answer bare, and the pager aliases answer alongside them.
	ScrollbackWatched
)

// Modifiers that make a key a chord rather than the letter printed on it.
const scrollbackChordMods = tea.ModCtrl | tea.ModAlt | tea.ModSuper | tea.ModMeta | tea.ModHyper

// IsScrollbackKey reports that a key press asks for a scrollback move in this
// state. It is the half of MapScrollbackKey that needs no page size, so a host
// resolves its layout only for the keys this claims rather than on every
// keystroke it types into a pane.
func IsScrollbackKey(state ScrollbackState, msg tea.KeyPressMsg) bool {
	_, ok := MapScrollbackKey(state, msg, 1)
	return ok
}

// MapScrollbackKey turns a navigation key into a window move over captured
// output, for a surface in the state named.
//
// One set of moves answers both states; what differs is the form of the key that
// reaches them. While a pane is live every unshifted key is the pane's, so a
// navigation key moves the window only with shift. While the pane is merely
// watched nothing is being typed into it, so the same keys answer bare — and the
// pager aliases answer too: j/k, g/G and ctrl+d/ctrl+u are text and control
// chords a live pane must receive, and shift cannot free a letter from it, it
// only capitalises it.
//
// pageSize is the drawn rows of the surface under the keys, which is what makes
// a page the same distance wherever it is pressed. A page keeps one row of
// context the way a pager does; the half-page chords take half the surface.
func MapScrollbackKey(state ScrollbackState, msg tea.KeyPressMsg, pageSize int) (ScrollbackMove, bool) {
	if move, ok := scrollbackNavKey(msg.Code, max(pageSize-1, 1)); ok {
		if state == ScrollbackLive && !msg.Mod.Contains(tea.ModShift) {
			return ScrollbackMove{}, false
		}
		return move, true
	}
	if state != ScrollbackWatched {
		return ScrollbackMove{}, false
	}
	return scrollbackPagerKey(msg, max(pageSize/2, 1))
}

// scrollbackNavKey is the set no pane types: the keys a surface can claim in
// either state.
func scrollbackNavKey(code rune, page int) (ScrollbackMove, bool) {
	switch code {
	case tea.KeyUp:
		return ScrollbackMove{Rows: 1}, true
	case tea.KeyDown:
		return ScrollbackMove{Rows: -1}, true
	case tea.KeyPgUp:
		return ScrollbackMove{Rows: page}, true
	case tea.KeyPgDown:
		return ScrollbackMove{Rows: -page}, true
	case tea.KeyHome:
		return ScrollbackMove{ToOldest: true}, true
	case tea.KeyEnd:
		return ScrollbackMove{ToLive: true}, true
	}
	return ScrollbackMove{}, false
}

// scrollbackPagerKey is the pager set every reader already has in their hands.
// It reaches the same moves as the navigation keys and is answered only for a
// watched pane, because each of these keys is input a live pane is owed.
func scrollbackPagerKey(msg tea.KeyPressMsg, half int) (ScrollbackMove, bool) {
	chord := msg.Mod & scrollbackChordMods
	if chord == tea.ModCtrl {
		switch msg.Code {
		case 'd':
			return ScrollbackMove{Rows: -half}, true
		case 'u':
			return ScrollbackMove{Rows: half}, true
		}
		return ScrollbackMove{}, false
	}
	if chord != 0 {
		return ScrollbackMove{}, false
	}
	// A shifted letter is matched as the letter it types, because that is the
	// only thing the two encodings of it agree on: a terminal reports G as g
	// with shift, and a host that builds the press itself writes the capital.
	letter := msg.Code
	if msg.Mod.Contains(tea.ModShift) {
		letter = unicode.ToUpper(letter)
	}
	switch letter {
	case 'j':
		return ScrollbackMove{Rows: -1}, true
	case 'k':
		return ScrollbackMove{Rows: 1}, true
	case 'g':
		// g and G read as a pager's pair: the top of what is held, and the live
		// edge output is arriving at.
		return ScrollbackMove{ToOldest: true}, true
	case 'G':
		return ScrollbackMove{ToLive: true}, true
	}
	return ScrollbackMove{}, false
}
