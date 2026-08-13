package tty

import tea "charm.land/bubbletea/v2"

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
// pane rather than to the pane inside it. A host registers them as one value so
// the set and the order are the component's, not each host's: two surfaces that
// each decided where copy sits relative to scrollback would answer the same key
// differently.
//
// A host with chords genuinely its own — a search over its own buffer, a panel
// its layout owns — answers those before calling here. Nothing in this set may
// be one of them: they act on the pane's output, which every host has.
type SurfaceChords struct {
	// Copy puts the current selection on the clipboard.
	Copy func() tea.Cmd
	// SelectAll selects every line of output the surface is showing.
	SelectAll func() tea.Cmd
	// Scrollback walks the window through history. It answers for itself
	// because which keys move a window is the shared scrollback rule's, and a
	// surface with no window scrolled back declines them.
	Scrollback func(tea.KeyPressMsg) (tea.Cmd, bool)
}

// ResolveSurfaceChord answers a key on behalf of the surface, in one order
// everywhere: the two chords that act on the selection first, then scrollback.
// The order is stated rather than incidental — the sets do not overlap today,
// and a host that reordered them would be deciding that for every host.
func (c Config) ResolveSurfaceChord(msg tea.KeyPressMsg, chords SurfaceChords) (tea.Cmd, bool) {
	if chords.Copy != nil && c.IsCopyChord(msg) {
		return chords.Copy(), true
	}
	if chords.SelectAll != nil && c.IsSelectAllChord(msg) {
		return chords.SelectAll(), true
	}
	if chords.Scrollback != nil {
		if cmd, handled := chords.Scrollback(msg); handled {
			return cmd, true
		}
	}
	return nil, false
}
