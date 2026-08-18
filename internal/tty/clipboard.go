package tty

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/textselect"
)

// The copy pipeline is the selection engine's; a Config only says which keys
// reach it. SelectionKeys is that binding, so the notice a terminal shows and
// the notice any other selectable surface shows are the same sentence.
func (c Config) SelectionKeys() textselect.Keys {
	return textselect.Keys{Copy: c.CopyKey, SelectAll: c.SelectAllKey}
}

// SuperCopyKey is the platform copy chord — Cmd+C on macOS, Super+C elsewhere.
const SuperCopyKey = textselect.SuperCopyKey

// CopyNoticeDuration is how long a copy notice stays up.
const CopyNoticeDuration = textselect.CopyNoticeDuration

type (
	// CopyResult is the outcome of a copy.
	CopyResult = textselect.CopyResult
	// CopyNotice is what the user is told about one.
	CopyNotice = textselect.CopyNotice
)

// SelectionText is what selected lines read as off the screen.
var SelectionText = textselect.SelectionText

// IsCopyChord reports whether a key press asks to copy the terminal selection:
// the configured copy key, or the platform copy chord.
func (c Config) IsCopyChord(msg tea.KeyPressMsg) bool {
	return c.SelectionKeys().IsCopyChord(msg)
}

// IsPasteChord reports whether a key press asks to paste into the terminal. It
// is the terminal's own: a pane is the only surface with somewhere to paste to.
func (c Config) IsPasteChord(msg tea.KeyPressMsg) bool {
	return c.PasteKey != "" && msg.String() == c.PasteKey
}

// IsSelectAllChord reports whether a key press asks to select every line of
// output.
func (c Config) IsSelectAllChord(msg tea.KeyPressMsg) bool {
	return c.SelectionKeys().IsSelectAllChord(msg)
}

// Notice phrases a copy result.
func (c Config) Notice(r CopyResult) CopyNotice { return c.SelectionKeys().Notice(r) }

// CopySelectionCmd copies selected terminal lines to every clipboard within
// reach and reports the outcome as the host's own notification.
func (c Config) CopySelectionCmd(lines []string, wrap func(CopyNotice) tea.Msg) tea.Cmd {
	return c.SelectionKeys().CopySelectionCmd(lines, wrap)
}
