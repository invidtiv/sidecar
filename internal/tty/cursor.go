package tty

import (
	"os/exec"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// CursorStyle returns the cursor style using current theme colors.
// Uses bold reverse video with a bright background for maximum visibility.
// The bright cyan/white combination stands out against most terminal backgrounds
// including Claude Code's diff highlighting and colored output.
func CursorStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Reverse(true).
		Bold(true).
		Background(styles.Primary).
		Foreground(styles.BgPrimary)
}

// RenderWithCursor overlays the cursor on content at the specified position.
// cursorRow is relative to the visible content (0 = first visible line).
// cursorCol is the column within the line (0-indexed).
// Preserves ANSI escape codes in surrounding content while rendering cursor.
func RenderWithCursor(content string, cursorRow, cursorCol int, visible bool) string {
	if !visible || cursorRow < 0 || cursorCol < 0 {
		return content
	}

	lines := strings.Split(content, "\n")
	if cursorRow >= len(lines) {
		return content
	}

	lines[cursorRow] = RenderCursorLine(lines[cursorRow], cursorCol, true)
	return strings.Join(lines, "\n")
}

// RenderCursorLine overlays the cursor on one terminal line. Keeping the
// line-level primitive separate lets viewport renderers avoid splitting and
// joining the complete rendered buffer a second time.
func RenderCursorLine(line string, cursorCol int, visible bool) string {
	if !visible || cursorCol < 0 {
		return line
	}
	// Use ANSI-aware width calculation for visual position
	lineWidth := ansi.StringWidth(line)

	if cursorCol >= lineWidth {
		// Cursor past end of line: append visible cursor block
		padding := max(cursorCol-lineWidth, 0)
		return line + strings.Repeat(" ", padding) + CursorStyle().Render("\u2588")
	}

	// Use ANSI-aware slicing to preserve escape codes in before/after.
	before := ansi.Cut(line, 0, cursorCol)
	char := ansi.Cut(line, cursorCol, cursorCol+1)
	after := ansi.Cut(line, cursorCol+1, lineWidth)

	// Strip the cursor char to get clean styling.
	charStripped := ansi.Strip(char)
	if charStripped == "" || charStripped == " " {
		charStripped = "\u2588"
	}
	return before + CursorStyle().Render(charStripped) + after
}

// QueryCursorPositionSync synchronously queries cursor position for the given target.
// Used to capture cursor position atomically with output in poll goroutines.
// Returns row, col (0-indexed), paneHeight, paneWidth, visible, and ok (false if query failed).
// paneHeight is needed to calculate cursor offset when display height differs from pane height.
func QueryCursorPositionSync(target string) (row, col, paneHeight, paneWidth int, visible, ok bool) {
	if target == "" {
		return 0, 0, 0, 0, false, false
	}

	cmd := exec.Command("tmux", "display-message", "-t", target,
		"-p", "#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height},#{pane_width}")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, 0, 0, false, false
	}

	parts := strings.Split(strings.TrimSpace(string(output)), ",")
	if len(parts) < 2 {
		return 0, 0, 0, 0, false, false
	}

	col, _ = strconv.Atoi(parts[0])
	row, _ = strconv.Atoi(parts[1])
	visible = len(parts) < 3 || parts[2] != "0"
	if len(parts) >= 4 {
		paneHeight, _ = strconv.Atoi(parts[3])
	}
	if len(parts) >= 5 {
		paneWidth, _ = strconv.Atoi(parts[4])
	}
	return row, col, paneHeight, paneWidth, visible, true
}
