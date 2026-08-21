package tty

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// WheelStaysWithPointer reports that a wheel notch over a host's chrome must be
// delivered to the live pane under the pointer rather than placed by whichever
// region the notch landed in.
//
// Placing a notch by region while a pane is live lets one that drifted off the
// pane reach the list beside it, where moving the cursor rebinds the preview and
// ends the mode — so a stray trackpad notch would silently drop the user out of
// the pane they are typing in.
func WheelStaysWithPointer(live bool) bool { return live }

// WheelGesture is one wheel event over a terminal surface, in the host's own
// screen coordinates.
type WheelGesture struct {
	Delta      int
	X, Y       int
	Shift, Alt bool
	Now        time.Time
}

// WheelHandler is a host's answers about a wheel notch over a terminal surface,
// live or merely watched. The order of the acts — coalesce the flick, decide who
// owns the notch, pin the window before forwarding, note the activity, send — is
// the shared rule; a host supplies only what it alone can know: where the pane's
// grid is, how its own window scrolls, and how it sends.
type WheelHandler struct {
	// Burst coalesces the flick so the distance travelled does not depend on how
	// fast a surface happens to repaint.
	Burst *WheelBurst
	// WritesEnabled says the host may write to the pane at all. Left false, a
	// notch never leaves the host, whatever the pane is running.
	WritesEnabled bool
	// MouseReporting reports that the application in the pane has asked for
	// mouse events, which is the only condition under which it owns the wheel.
	// It answers about the pane under the pointer in every state: a host may not
	// answer it from whether that pane also holds the keyboard.
	MouseReporting func() bool
	// PaneCoords maps a screen position onto a cell of the pane's grid. A notch
	// over chrome is never the application's. Like MouseReporting it is about the
	// pane, not the mode, and must answer for a watched one too.
	PaneCoords func(x, y int) (col, row int, ok bool)
	// PinToLive returns the host's window to the live edge. While the
	// application owns the wheel it owns what the pane shows, and a window left
	// scrolled back would sit over stale rows as the application repainted.
	PinToLive func()
	// NoteActivity records the notch as user input for the host's own poll
	// cadence, which would otherwise repaint a scrolled pane at its idle tier.
	// Every notch counts, forwarded or local: a pane being scrolled is a pane
	// being read.
	NoteActivity func()
	// SendNotches delivers the notch to the application as wheel reports.
	SendNotches func(up bool, col, row, notches int) tea.Cmd
	// ScrollLocal moves the host's own window by the coalesced delta. It is what
	// makes the wheel work over a plain shell, and it answers every notch the
	// application has not claimed.
	ScrollLocal func(delta int) tea.Cmd
	// OnHold tells the host that this event changed only Burst bookkeeping. A
	// host with an expensive View can reuse the frame it just drew once, because
	// no terminal, viewport, selection, or routing state changed for this event.
	OnHold func()
}

// Handle routes one wheel event. It returns nil while the burst is still
// coalescing, which is not a dropped notch: the held-back delta rides along with
// the next event that gets through.
func (h WheelHandler) Handle(g WheelGesture) tea.Cmd {
	delta, flush := h.Burst.Add(g.Delta, g.Now)
	if !flush {
		if h.OnHold != nil {
			h.OnHold()
		}
		return nil
	}
	// The reader is reading this pane whoever ends up owning the notch: a local
	// scroll needs the next capture as much as a forwarded one, and noting it only
	// on the forwarded path leaves a pane being scrolled repainting at the idle
	// tier.
	if h.NoteActivity != nil {
		h.NoteActivity()
	}
	// A host that may not write is not asked where the pane's cells are: with the
	// capture joined there is no usable pane grid to answer from, and the notch is
	// local either way.
	reporting := h.WritesEnabled && h.MouseReporting != nil && h.MouseReporting()
	var col, row int
	inPane := false
	if reporting && h.PaneCoords != nil {
		col, row, inPane = h.PaneCoords(g.X, g.Y)
	}
	route, notches := RouteWheel(WheelInput{
		Delta:          delta,
		Shift:          g.Shift,
		Alt:            g.Alt,
		MouseReporting: reporting,
		InPane:         inPane,
		WritesEnabled:  h.WritesEnabled,
	})
	if route != WheelPane {
		if h.ScrollLocal == nil {
			return nil
		}
		return h.ScrollLocal(delta)
	}
	// From the next event of this gesture on, forwarded flushes pace
	// themselves to WheelPaneDebounce: a momentum tail over an application
	// that owns its own scrollback must not become one send plus one capture
	// per base-debounce tick after its view has stopped moving.
	h.Burst.pane = true
	if h.PinToLive != nil {
		h.PinToLive()
	}
	if h.SendNotches == nil {
		return nil
	}
	// The component polls for the frame its own send provokes; a second poll
	// scheduled here would capture every forwarded notch twice.
	return h.SendNotches(delta < 0, col, row, notches)
}
