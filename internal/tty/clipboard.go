package tty

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
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
	Err   error
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
	case r.Err != nil:
		notice.Message = "Copy failed: " + r.Err.Error()
		notice.IsError = true
	default:
		notice.Message = fmt.Sprintf("Copied %d line(s)", r.Lines)
	}
	return notice
}

// CopySelectionNotice copies selected terminal lines and phrases the outcome, so
// a host needs only its own notification type to report a copy.
func (c Config) CopySelectionNotice(lines []string) CopyNotice {
	return c.Notice(CopySelection(lines))
}

// CopySelectionCmd copies selected terminal lines and reports the outcome as
// the host's own notification. wrap is the only part of a copy a surface owns:
// which toast type carries the notice. A host that phrased the notice itself
// would be a second wording of the same three outcomes.
func (c Config) CopySelectionCmd(lines []string, wrap func(CopyNotice) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return wrap(c.CopySelectionNotice(lines))
	}
}

// CopySelection writes selected terminal lines to the system clipboard, without
// the styling they were drawn with.
func CopySelection(lines []string) CopyResult {
	if len(lines) == 0 {
		return CopyResult{Empty: true}
	}
	stripped := make([]string, 0, len(lines))
	for _, line := range lines {
		stripped = append(stripped, ansi.Strip(line))
	}
	if err := clipboard.WriteAll(strings.Join(stripped, "\n")); err != nil {
		return CopyResult{Err: err}
	}
	return CopyResult{Lines: len(stripped)}
}
