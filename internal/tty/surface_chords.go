package tty

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/textselect"
)

// The ways in to a live pane from the surface around it. Both hosts answer both
// chords for the same act, and the command palette and the preview hint name
// them, so they are literals in one place rather than one per surface.
const (
	// EnterInteractiveKey is the primary alternate to enter/double-click.
	EnterInteractiveKey = "i"
	// EnterInteractiveKeyAlt is what the hints advertise.
	EnterInteractiveKeyAlt = "E"
)

// SurfaceChords are the acts that belong to the surface drawn around a live
// pane rather than to the pane inside it.
type SurfaceChords = textselect.SurfaceChords

// ResolveSurfaceChord answers a key on behalf of the surface, in one order
// everywhere.
func (c Config) ResolveSurfaceChord(msg tea.KeyPressMsg, chords SurfaceChords) (tea.Cmd, bool) {
	return c.SelectionKeys().ResolveSurfaceChord(msg, chords)
}
