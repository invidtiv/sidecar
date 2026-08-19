package textselect

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/clip"
)

// SuperCopyKey is the platform copy chord — Cmd+C on macOS, Super+C elsewhere.
// It copies alongside the configured key rather than replacing it, because it is
// a platform convention rather than a sidecar binding: the surface owns the
// selection, so the emulator's own copy has nothing to act on and passes the
// chord through to us. Terminals that intercept it first (iTerm2) never deliver
// it, which is why the configurable chord stays.
const SuperCopyKey = "super+c"

// Keys are the chords a selectable surface answers. They are one value because
// the empty-copy notice names the select-all key: a surface that carried the
// two separately could tell the user to press a key it does not bind.
type Keys struct {
	// Copy puts the current selection on the clipboard (default "alt+c").
	Copy string
	// SelectAll selects every line the surface is showing (default "ctrl+a").
	SelectAll string
}

// IsCopyChord reports whether a key press asks to copy the selection: the
// configured copy key, or the platform copy chord.
func (k Keys) IsCopyChord(msg tea.KeyPressMsg) bool {
	key := msg.String()
	return key == SuperCopyKey || (k.Copy != "" && key == k.Copy)
}

// IsSelectAllChord reports whether a key press asks to select every line.
func (k Keys) IsSelectAllChord(msg tea.KeyPressMsg) bool {
	return k.SelectAll != "" && msg.String() == k.SelectAll
}

// CopyResult is the outcome of a copy: how many lines reached the clipboard, and
// why none did. Hosts phrase their own notification from it.
type CopyResult struct {
	Lines int
	// NativeErr is the system clipboard write's failure. It does not mean the
	// copy failed: the OSC 52 write to the terminal still happened, and over
	// SSH it is the half that was ever going to work — so the notice names
	// where the text was sent rather than claiming a failure.
	NativeErr error
	// Empty reports a copy asked for with nothing selected. A copy chord with no
	// selection must not replace the clipboard with a screen dump — cmd+c is
	// reflex, and the clipboard may hold something the user still needs.
	Empty bool
}

// CopyNoticeDuration is how long a copy notice stays up: long enough to read a
// line count, short enough not to sit over the content it describes.
const CopyNoticeDuration = 2 * time.Second

// CopyNotice is what the user is told about a copy. Every selectable surface
// says the same three things — a copy needs a selection, a failure names its
// error, a success counts its lines — so the wording lives here and each host
// only wraps it in whatever its own notification type is.
type CopyNotice struct {
	Message  string
	IsError  bool
	Duration time.Duration
}

// Notice phrases a copy result. The empty-selection half names the select-all
// key only when the surface binds one — a surface that answers just the platform
// copy chord would otherwise point at a key that does nothing — and the wording
// is a pane's rather than a terminal's, because every selectable surface shows
// it.
func (k Keys) Notice(r CopyResult) CopyNotice {
	notice := CopyNotice{Duration: CopyNoticeDuration}
	switch {
	case r.Empty && k.SelectAll != "":
		notice.Message = "Nothing selected — " + k.SelectAll + " selects everything"
	case r.Empty:
		notice.Message = "Nothing selected"
	default:
		notice.Message = clip.Result{NativeErr: r.NativeErr}.Message(fmt.Sprintf("Copied %d line(s)", r.Lines))
	}
	return notice
}

// CopySelectionCmd copies selected lines to every clipboard within reach — the
// system clipboard natively, the terminal's over OSC 52 — and reports the
// outcome as the host's own notification. wrap is the only part of a copy a
// surface owns: which toast type carries the notice. A host that phrased the
// notice itself would be a second wording of the same outcomes.
func (k Keys) CopySelectionCmd(lines []string, wrap func(CopyNotice) tea.Msg) tea.Cmd {
	if len(lines) == 0 {
		return func() tea.Msg { return wrap(k.Notice(CopyResult{Empty: true})) }
	}
	return clip.Copy(SelectionText(lines), func(r clip.Result) tea.Msg {
		return wrap(k.Notice(CopyResult{Lines: len(lines), NativeErr: r.NativeErr}))
	})
}

// SelectionText is what selected lines read as off the screen: the styling they
// were drawn with removed, joined the way they were stacked. It is exactly what
// lands on the clipboard.
func SelectionText(lines []string) string {
	stripped := make([]string, 0, len(lines))
	for _, line := range lines {
		stripped = append(stripped, ansi.Strip(line))
	}
	return strings.Join(stripped, "\n")
}
