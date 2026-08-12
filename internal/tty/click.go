package tty

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

// ContentWidth is the columns a terminal surface can actually render into: the
// surface minus the column the scrollbar reserves.
//
// The scrollbar's column is stable viewport chrome — reserved even while all
// output fits and RenderScrollbar draws only a spacer. Making the reservation
// depend on the current history length creates a one-frame geometry race: a new
// frame can make the scrollbar visible before the asynchronous tmux resize has
// taken effect, clipping the application's final column and reflowing it on the
// next repaint (td-0818ef). A surface with no room to give one up keeps what it
// has rather than reporting a width no pane could be sized to.
func ContentWidth(surfaceWidth int) int {
	if surfaceWidth <= 1 {
		return surfaceWidth
	}
	return surfaceWidth - 1
}
