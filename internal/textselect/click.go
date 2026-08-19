package textselect

import "github.com/marcus/sidecar/internal/mouse"

// The vocabulary here is the embedded terminal's, because that is the surface
// these rules were proven on and its hosts name them. Nothing in them is
// terminal-specific: a surface with no pane behind it reads "terminal" as "the
// content it is selecting", and answers ClickActivate for the click its rows
// already respond to. [Surface] does exactly that.

// ClickIntent is everything a host knows about a mouse-down over a terminal
// surface at the moment it has to say what a release without motion will mean.
// It is deliberately not "where the keyboard is": focus follows the press rather
// than gating it, or a click from elsewhere into a passive terminal would need a
// second click to do what one click does anywhere else in the surface.
type ClickIntent struct {
	// Live reports that the surface is already forwarding keys to its pane.
	Live bool

	// MouseReporting reports that the application running in the pane has asked
	// for mouse events.
	MouseReporting bool

	// Modified is a shift- or alt-click, which is a selection gesture on every
	// terminal surface and never an activation.
	Modified bool

	// LinkClaimed reports that something with a stronger claim on the click — a
	// validated link — has already taken it.
	LinkClaimed bool
}

// ResolveClick decides what a release without motion means: make a passive
// terminal live, hand the click to the application running in a live one, or
// nothing at all.
func ResolveClick(in ClickIntent) ClickResolution {
	switch {
	case in.Modified || in.LinkClaimed:
		return ClickNone
	case !in.Live:
		return ClickActivate
	case in.MouseReporting:
		return ClickForward
	}
	return ClickNone
}

// PressLeavesTerminal reports that a press landed away from every region that
// draws a terminal, which ends both the pointer gesture such a surface armed and
// the interactive mode it was armed in.
//
// Which regions draw a terminal is the host's to name; the verdict is not.
// Ending only the gesture leaves a live pane holding the keyboard behind
// whatever the press started — a divider drag, a row selection — and ending only
// the mode fires the armed click under a selection the user has moved away from.
func PressLeavesTerminal(region string, terminalRegions ...string) bool {
	for _, terminal := range terminalRegions {
		if region == terminal {
			return false
		}
	}
	return true
}

// PointerIntent is what a pointer action over a terminal means, independent of
// what any one host does about it.
type PointerIntent uint8

const (
	// PointerIgnore is an action the terminal has no answer for.
	PointerIgnore PointerIntent = iota
	// PointerPress arms a gesture at the pressed cell, which the release
	// resolves into an activation, a forwarded click, or nothing.
	PointerPress
	// PointerSelectWord and PointerSelectLine are the double and triple click.
	PointerSelectWord
	PointerSelectLine
	// PointerWheel is a notch over the surface.
	PointerWheel
	// PointerDrag extends the selection the press anchored.
	PointerDrag
	// PointerFinish is the release that resolves the gesture.
	PointerFinish
	// PointerAbandon is the release the surface never saw — the pointer left the
	// window, or focus changed — which ends the gesture where it stands.
	PointerAbandon
)

// PointerIntentInput is what a host knows about one pointer action: what the
// action was, whether it landed on a terminal, and whether the gesture in
// flight started on one.
type PointerIntentInput struct {
	Action mouse.ActionType

	// OverTerminal reports that the action landed on a region drawing a
	// terminal.
	OverTerminal bool

	// FromTerminal reports that the gesture in flight began on one. A drag and
	// its release are answered by where they started, never by where the pointer
	// has since travelled — a selection dragged off the pane is still that
	// pane's selection.
	FromTerminal bool

	// LostRelease reports a button-less motion that ended a drag no release was
	// ever seen for.
	LostRelease bool
}

// PointerIntentFor maps a pointer action over a terminal onto the gesture it
// means. The bodies are each host's — one arms a tmux click, another moves a
// browser's window — but which of them an action asks for is one rule, or the
// two surfaces answer the same gesture differently.
func PointerIntentFor(in PointerIntentInput) PointerIntent {
	if in.LostRelease {
		if in.FromTerminal {
			return PointerAbandon
		}
		return PointerIgnore
	}
	switch in.Action {
	case mouse.ActionDrag:
		if in.FromTerminal {
			return PointerDrag
		}
	case mouse.ActionDragEnd:
		if in.FromTerminal {
			return PointerFinish
		}
	case mouse.ActionClick:
		if in.OverTerminal {
			return PointerPress
		}
	case mouse.ActionDoubleClick:
		if in.OverTerminal {
			return PointerSelectWord
		}
	case mouse.ActionTripleClick:
		if in.OverTerminal {
			return PointerSelectLine
		}
	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		if in.OverTerminal {
			return PointerWheel
		}
	}
	return PointerIgnore
}

// PressesTerminal reports the actions that put a button down, which is where a
// press away from a terminal is decided.
func PressesTerminal(action mouse.ActionType) bool {
	switch action {
	case mouse.ActionClick, mouse.ActionDoubleClick, mouse.ActionTripleClick:
		return true
	}
	return false
}
