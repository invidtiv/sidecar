package tty

import (
	"os/exec"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
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

// PaneState is what one display-message reads about a pane beside its output:
// where the cursor is, how big the grid is, and whether the application running
// there has asked for mouse events.
type PaneState struct {
	CursorRow     int
	CursorCol     int
	PaneHeight    int
	PaneWidth     int
	CursorVisible bool

	// MouseReporting is tmux's #{mouse_any_flag}. It is read here rather than
	// scanned out of the capture because `capture-pane -e` emits rendering
	// escapes only: the DECSET sequences that turn tracking on never reach it,
	// so detection over a capture answers false for every mouse-aware app.
	MouseReporting bool
}

// QueryPaneStateSync reads a pane's cursor, geometry and mouse-tracking flag in
// the one display-message the capture path already pays for.
func QueryPaneStateSync(target string) (PaneState, bool) {
	if target == "" {
		return PaneState{}, false
	}

	cmd := exec.Command("tmux", "display-message", "-t", target,
		"-p", "#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height},#{pane_width},#{mouse_any_flag}")
	output, err := cmd.Output()
	if err != nil {
		return PaneState{}, false
	}

	parts := strings.Split(strings.TrimSpace(string(output)), ",")
	if len(parts) < 2 {
		return PaneState{}, false
	}

	var state PaneState
	state.CursorCol, _ = strconv.Atoi(parts[0])
	state.CursorRow, _ = strconv.Atoi(parts[1])
	state.CursorVisible = len(parts) < 3 || parts[2] != "0"
	if len(parts) >= 4 {
		state.PaneHeight, _ = strconv.Atoi(parts[3])
	}
	if len(parts) >= 5 {
		state.PaneWidth, _ = strconv.Atoi(parts[4])
	}
	if len(parts) >= 6 {
		state.MouseReporting = parts[5] != "0" && parts[5] != ""
	}
	return state, true
}

// PlaceCursor is the terminal cursor every embedded surface draws: a blinking
// block at the origin's cell. Shape and blink are the terminal's own, not each
// host's, so a pane looks the same wherever it is embedded.
func PlaceCursor(x, y int) *tea.Cursor {
	cursor := tea.NewCursor(x, y)
	cursor.Shape = tea.CursorBlock
	cursor.Blink = true
	return cursor
}
