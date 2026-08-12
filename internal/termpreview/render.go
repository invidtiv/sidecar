package termpreview

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/ui"
)

// Snapshot is one immutable capture of a pane's visible output. It is a value:
// a consumer that receives one cannot write through it to a live buffer, and
// nothing here ever hands it to disk. Captured terminal contents stay in memory
// for exactly as long as the surface showing them does.
type Snapshot struct {
	// PaneID is the pane the capture came from. It is carried with the lines so
	// a late capture for a pane the user has already moved off can be rejected
	// by identity rather than by timing.
	PaneID     string
	Lines      []string
	CapturedAt time.Time
	Err        error
}

// Empty reports a snapshot with nothing to draw.
func (s Snapshot) Empty() bool { return s.Err != nil || len(s.Lines) == 0 }

// Source is the seam between a preview surface and whatever produces its
// snapshots. Implementations may capture a pane, replay a fixture, or return
// nothing at all; none of them may forward input, because there is no method
// here through which a keystroke could travel.
type Source interface {
	// Snapshot returns the current immutable capture for the selected item, and
	// false when there is nothing to show.
	Snapshot() (Snapshot, bool)
}

// ReadOnlyOptions describes one read-only terminal box: the header row's chips
// and hints, the box size, and how far back the viewer has scrolled.
type ReadOnlyOptions struct {
	Width, Height int
	Chips         []string
	Hints         string
	// Offset is rows scrolled back from the live bottom. Zero follows output.
	Offset int
	// Message replaces the body when there is no capture to draw: no pane, an
	// ambiguous match, a failed capture. Multi-line messages are rendered as
	// written, so a caller can put the item's metadata under the reason.
	Message string
	// Truncate is the consumer's ANSI-aware truncation cache; nil uses
	// TruncateANSI.
	Truncate func(string, int) string
	// Scrollbar draws a reserved right-hand column. It costs one content column
	// whether or not the content overflows, so the body never shifts under a
	// scrolling viewer.
	Scrollbar bool
}

// ReadOnlyResult is the rendered box plus what the viewer needs to scroll it.
type ReadOnlyResult struct {
	View string
	// Rows is the body height, header row excluded.
	Rows int
	// MaxOffset is the furthest back Offset may go.
	MaxOffset int
	// Start is the first buffer line drawn.
	Start int
}

// RenderReadOnly draws a captured pane into a fixed box: one header row, then
// the body, padded to exactly Width columns and Height rows.
//
// It renders no cursor. A read-only capture has no live cursor to place — the
// pane it came from is being driven by somebody else — and drawing one would
// invite the reader to type into a surface that forwards nothing.
func RenderReadOnly(snap Snapshot, opts ReadOnlyOptions) ReadOnlyResult {
	width, height := opts.Width, opts.Height
	if width < 1 || height < 1 {
		return ReadOnlyResult{}
	}
	truncate := opts.Truncate
	if truncate == nil {
		truncate = TruncateANSI
	}
	header := HeaderRow(opts.Chips, opts.Hints, width, 0, truncate)

	body := height - HeaderRows
	if body < 1 {
		return ReadOnlyResult{View: fill(header, width, truncate)}
	}

	contentWidth := width
	if opts.Scrollbar {
		contentWidth = max(1, width-1)
	}

	if opts.Message != "" || snap.Empty() {
		message := opts.Message
		if message == "" {
			message = "No output captured"
		}
		lines := make([]string, 0, body)
		for _, line := range strings.Split(message, "\n") {
			lines = append(lines, fill(line, width, truncate))
		}
		return ReadOnlyResult{View: strings.Join(append([]string{fill(header, width, truncate)}, padRows(lines, body, width)...), "\n"), Rows: body}
	}

	maxOffset := max(0, len(snap.Lines)-body)
	offset := min(max(opts.Offset, 0), maxOffset)
	start := maxOffset - offset
	end := min(start+body, len(snap.Lines))

	visible := make([]string, 0, body)
	for _, line := range snap.Lines[start:end] {
		visible = append(visible, fill(line, contentWidth, truncate))
	}
	visible = padRows(visible, body, contentWidth)

	rows := []string{fill(header, width, truncate)}
	if opts.Scrollbar {
		scrollbar := ui.RenderScrollbar(ui.ScrollbarParams{
			TotalItems:   len(snap.Lines),
			ScrollOffset: start,
			VisibleItems: body,
			TrackHeight:  body,
		})
		joined := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(visible, "\n"), scrollbar)
		rows = append(rows, strings.Split(joined, "\n")...)
	} else {
		rows = append(rows, visible...)
	}
	return ReadOnlyResult{View: strings.Join(rows[:height], "\n"), Rows: body, MaxOffset: maxOffset, Start: start}
}

// SnapshotLines splits a raw capture into rows, dropping the trailing blank
// lines tmux emits past the last written row. Sparse shell output otherwise
// scrolls a screenful of nothing.
func SnapshotLines(output string) []string {
	if output == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	last := -1
	for i, line := range lines {
		if strings.TrimSpace(ansi.Strip(line)) != "" {
			last = i
		}
	}
	if last < 0 {
		return nil
	}
	return lines[:last+1]
}

// fill truncates a line to width and right-pads it, so every rendered row is
// exactly width columns of background.
func fill(line string, width int, truncate func(string, int) string) string {
	if width < 1 {
		return ""
	}
	line = ui.ExpandTabs(line, 8)
	if ansi.StringWidth(line) > width {
		line = truncate(line, width)
	}
	if gap := width - ansi.StringWidth(line); gap > 0 {
		line += strings.Repeat(" ", gap)
	}
	return line
}

func padRows(lines []string, target, width int) []string {
	for len(lines) < target {
		lines = append(lines, strings.Repeat(" ", max(width, 0)))
	}
	return lines[:target]
}
