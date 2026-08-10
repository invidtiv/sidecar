package tasks

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// setupHint is the sidecar-side instruction appended to whatever Tasks itself
// reported. Tasks owns the diagnosis; sidecar only says what to do next.
const setupHint = "Configure Tasks, then restart sidecar to load the tab."

// panelInset is the horizontal padding of the diagnostic panel, applied on both
// sides so the text is actually inset rather than merely wrapped short.
const panelInset = 2

// renderUnavailable renders the diagnostic shown when the embedded Tasks model
// could not be built — most often because Tasks is unconfigured, in which case
// the reason is Tasks' own configuration-required message.
//
// This is deliberately an error surface, not an empty task list: a blank list
// reads as "you have nothing to do", which is a lie when the real answer is
// "sidecar never found your tasks".
//
// Vertical space is spent on content before decoration. Clipping the whole
// panel to MaxHeight would spend a tiny box entirely on the title and blank
// separators, leaving the user with "Tasks is unavailable" and no way to learn
// why or what to do — the two things the panel exists to say. So the reason and
// the hint get their line first, the title next, and blank separators only out
// of what is left over.
func renderUnavailable(reason string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	theme := styles.GetCurrentTheme()
	inner := max(width-2*panelInset, 1)

	title := wrap(lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(theme.Colors.Error)).
		Render("Tasks is unavailable"), inner)
	body := wrap(strings.TrimSpace(reason), inner)
	hint := wrap(setupHint, inner)

	blocks := []([]string){title, body, hint}
	// Display order is title, reason, hint; scarcity order is the reverse of
	// how ornamental each block is.
	budget := allocate(height, []int{len(title), len(body), len(hint)}, []int{1, 2, 0})

	// Separators are the first thing sacrificed and the last thing restored.
	used := budget[0] + budget[1] + budget[2]
	spare := height - used
	lead, gapTitle, gapBody := 0, 0, 0
	if spare > 0 && budget[0] > 0 && budget[1] > 0 {
		gapTitle, spare = 1, spare-1
	}
	if spare > 0 && budget[1] > 0 && budget[2] > 0 {
		gapBody, spare = 1, spare-1
	}
	if spare > 0 {
		lead = 1
	}

	var lines []string
	for range lead {
		lines = append(lines, "")
	}
	for i, gap := range []int{gapTitle, gapBody, 0} {
		lines = append(lines, clip(blocks[i], budget[i], inner)...)
		for range gap {
			lines = append(lines, "")
		}
	}

	pad := strings.Repeat(" ", panelInset)
	for i, line := range lines {
		if line != "" {
			lines[i] = pad + line
		}
	}
	return strings.Join(lines, "\n")
}

// allocate distributes height across blocks: one line each in priority order
// while there is room, then the remainder in the same order until every block
// is whole.
func allocate(height int, wants []int, priority []int) []int {
	got := make([]int, len(wants))
	left := height

	for _, i := range priority {
		if left <= 0 {
			break
		}
		if wants[i] > 0 {
			got[i], left = 1, left-1
		}
	}
	for _, i := range priority {
		if left <= 0 {
			break
		}
		extra := min(wants[i]-got[i], left)
		if extra > 0 {
			got[i], left = got[i]+extra, left-extra
		}
	}
	return got
}

// clip trims a block to n lines, marking the cut so a truncated reason does not
// read as a complete one.
func clip(lines []string, n, width int) []string {
	if n <= 0 {
		return nil
	}
	if len(lines) <= n {
		return lines
	}
	out := make([]string, n)
	copy(out, lines[:n])
	last := out[n-1]
	if ansi.StringWidth(last)+1 > width {
		last = ansi.Truncate(last, max(width-1, 1), "")
	}
	out[n-1] = last + "…"
	return out
}

// wrap reflows text to width and strips the trailing padding lipgloss adds, so
// a clipped line's ellipsis sits against the text rather than at the far edge.
func wrap(text string, width int) []string {
	rendered := lipgloss.NewStyle().Width(width).Render(text)
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return lines
}
