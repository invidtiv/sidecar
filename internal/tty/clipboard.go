package tty

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
// a platform convention rather than a sidecar binding: the terminal owns the
// selection, so the emulator's own copy has nothing to act on and passes the
// chord through to us. Terminals that intercept it first (iTerm2) never deliver
// it, which is why the configurable chord stays.
const SuperCopyKey = "super+c"

// IsCopyChord reports whether a key press asks to copy the terminal selection:
// the configured copy key, or the platform copy chord.
func (c Config) IsCopyChord(msg tea.KeyPressMsg) bool {
	key := msg.String()
	return key == SuperCopyKey || (c.CopyKey != "" && key == c.CopyKey)
}

// IsPasteChord reports whether a key press asks to paste into the terminal.
func (c Config) IsPasteChord(msg tea.KeyPressMsg) bool {
	return c.PasteKey != "" && msg.String() == c.PasteKey
}

// IsSelectAllChord reports whether a key press asks to select every line of
// output.
func (c Config) IsSelectAllChord(msg tea.KeyPressMsg) bool {
	return c.SelectAllKey != "" && msg.String() == c.SelectAllKey
}

// CopyResult is the outcome of a copy: how many lines reached the clipboard, and
// why none did. Hosts phrase their own notification from it.
type CopyResult struct {
	Lines int
	// NativeErr is the system clipboard write's failure. It does not mean the
	// copy failed: the OSC 52 write to the terminal still happened, and over
	// SSH it is the half that was ever going to work — so the notice names the
	// terminal clipboard rather than claiming both or claiming failure.
	NativeErr error
	// Empty reports a copy asked for with nothing selected. A copy chord with no
	// selection must not replace the clipboard with a screen dump — cmd+c is
	// reflex, and the clipboard may hold something the user still needs.
	Empty bool
}

// CopyNoticeDuration is how long a copy notice stays up: long enough to read a
// line count, short enough not to sit over the output it describes.
const CopyNoticeDuration = 2 * time.Second

// CopyNotice is what the user is told about a copy. Every terminal surface says
// the same three things — a copy needs a selection, a failure names its error, a
// success counts its lines — so the wording lives here and each host only wraps
// it in whatever its own notification type is.
type CopyNotice struct {
	Message  string
	IsError  bool
	Duration time.Duration
}

// Notice phrases a copy result. It is the Config's because the empty case
// names the select-all chord, and a notice naming a key the surface does not
// bind is worse than none.
func (c Config) Notice(r CopyResult) CopyNotice {
	notice := CopyNotice{Duration: CopyNoticeDuration}
	switch {
	case r.Empty:
		notice.Message = "Nothing selected — " + c.SelectAllKey + " selects all output"
	case r.NativeErr != nil:
		notice.Message = fmt.Sprintf("Copied %d line(s) to the terminal clipboard", r.Lines)
	default:
		notice.Message = fmt.Sprintf("Copied %d line(s)", r.Lines)
	}
	return notice
}

// CopySelectionCmd copies selected terminal lines to every clipboard within
// reach — the system clipboard natively, the terminal's over OSC 52 — and
// reports the outcome as the host's own notification. wrap is the only part of
// a copy a surface owns: which toast type carries the notice. A host that
// phrased the notice itself would be a second wording of the same outcomes.
func (c Config) CopySelectionCmd(lines []string, wrap func(CopyNotice) tea.Msg) tea.Cmd {
	if len(lines) == 0 {
		return func() tea.Msg { return wrap(c.Notice(CopyResult{Empty: true})) }
	}
	return clip.Copy(SelectionText(lines), func(r clip.Result) tea.Msg {
		return wrap(c.Notice(CopyResult{Lines: len(lines), NativeErr: r.NativeErr}))
	})
}

// SelectionText is what selected terminal lines read as off the screen: the
// styling they were drawn with removed, joined the way they were stacked. It is
// exactly what lands on the clipboard.
func SelectionText(lines []string) string {
	stripped := make([]string, 0, len(lines))
	for _, line := range lines {
		stripped = append(stripped, ansi.Strip(line))
	}
	return strings.Join(stripped, "\n")
}
