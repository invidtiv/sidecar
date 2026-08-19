package textselect

import tea "charm.land/bubbletea/v2"

// SurfaceChords are the acts that belong to the surface drawn around selectable
// content rather than to the content itself. A host registers them as one value
// so the set and the order are the component's, not each host's: two surfaces
// that each decided where copy sits relative to scrollback would answer the same
// key differently.
//
// A host with chords genuinely its own — a search over its own buffer, a panel
// its layout owns — answers those before calling here. Nothing in this set may
// be one of them: they act on the content, which every host has.
type SurfaceChords struct {
	// Copy puts the current selection on the clipboard.
	Copy func() tea.Cmd
	// SelectAll selects every line the surface is showing.
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
func (k Keys) ResolveSurfaceChord(msg tea.KeyPressMsg, chords SurfaceChords) (tea.Cmd, bool) {
	if chords.Copy != nil && k.IsCopyChord(msg) {
		return chords.Copy(), true
	}
	if chords.SelectAll != nil && k.IsSelectAllChord(msg) {
		return chords.SelectAll(), true
	}
	if chords.Scrollback != nil {
		if cmd, handled := chords.Scrollback(msg); handled {
			return cmd, true
		}
	}
	return nil, false
}
