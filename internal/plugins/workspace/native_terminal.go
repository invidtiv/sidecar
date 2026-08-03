package workspace

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
)

// Cursor exposes the active embedded terminal's native cursor in plugin-local
// coordinates. App-level focus, modal, and bounds checks are applied later.
func (p *Plugin) Cursor() *tea.Cursor {
	if !p.nativeTerminalActive() || !p.interactiveState.CursorVisible {
		return nil
	}
	termPanel := p.interactiveState.TermPanel
	buffer, width, height, x, y, ok := p.nativeTerminalGeometry(termPanel)
	if !ok || buffer == nil || buffer.LineCount() == 0 {
		return nil
	}

	follow := p.autoScrollOutput
	offset := p.previewOffset
	offsetFromBottom := false
	if termPanel {
		if p.selectionTermPanel && p.selection.Anchor.Valid() {
			follow = false
			offset = p.termPanelSelectionOffset
		} else {
			follow = p.termPanelScroll == 0
			offset = p.termPanelScroll
			offsetFromBottom = true
		}
	}
	absoluteBase, totalItems, loadingOlder := p.terminalHistorySummary(termPanel, buffer)
	bufferBase, hasCursorHistory := cursorBufferBase(buffer, p.interactiveState)
	// Same geometry the content render uses, or the cursor desyncs from it.
	paneWidth, paneHeight := p.interactiveState.PaneWidth, p.interactiveState.PaneHeight
	if paneWidth <= 0 || paneHeight <= 0 {
		if geometry := p.paneGeometryFor(termPanel); geometry.known() {
			paneWidth, paneHeight = geometry.Width, geometry.Height
		}
	}
	cursorX, cursorY, visible := terminalViewportCursorPosition(terminalViewportInput{
		Buffer:            buffer,
		Width:             width,
		Height:            height,
		Offset:            offset,
		OffsetFromBottom:  offsetFromBottom,
		Follow:            follow,
		Interactive:       true,
		CursorRow:         p.interactiveState.CursorRow,
		CursorCol:         p.interactiveState.CursorCol,
		CursorVisible:     p.interactiveState.CursorVisible,
		PaneHeight:        paneHeight,
		PaneWidth:         paneWidth,
		NativeCursor:      true,
		AbsoluteBase:      absoluteBase,
		TotalItems:        totalItems,
		LoadingOlder:      loadingOlder,
		CursorHistorySize: p.interactiveState.CursorHistorySize,
		BufferBase:        bufferBase,
		HasCursorHistory:  hasCursorHistory,
	})
	if !visible {
		return nil
	}
	cursor := tea.NewCursor(x+cursorX, y+cursorY)
	cursor.Shape = tea.CursorBlock
	cursor.Blink = true
	if cursor.X < 0 || cursor.X >= p.width || cursor.Y < 0 || cursor.Y >= p.height {
		return nil
	}
	return cursor
}

func (p *Plugin) nativeTerminalActive() bool {
	return p.focused && p.activePane == PanePreview &&
		p.viewMode == ViewModeInteractive && p.interactiveState != nil &&
		p.interactiveState.Active && (p.shellSelected || p.previewTab == PreviewTabOutput)
}

// PreferredMouseMode keeps hover-rich all-motion reporting for ordinary
// workspace views and requests cell motion only while a child terminal owns
// interactive input.
func (p *Plugin) PreferredMouseMode() tea.MouseMode {
	if p.nativeTerminalActive() {
		return tea.MouseModeCellMotion
	}
	return tea.MouseModeAllMotion
}

// nativeTerminalGeometry returns the terminal viewport's buffer, dimensions,
// and plugin-local origin. The origin is the first rendered terminal row after
// the pane's hint line.
func (p *Plugin) nativeTerminalGeometry(termPanel bool) (*tty.OutputBuffer, int, int, int, int, bool) {
	if p.width <= 0 || p.height <= 0 {
		return nil, 0, 0, 0, 0, false
	}
	x := panelOverhead / 2
	if p.sidebarVisible {
		available := p.width - dividerWidth
		sidebarWidth := available * p.sidebarWidth / 100
		if sidebarWidth < 15 {
			sidebarWidth = 15
		}
		if sidebarWidth > available-40 {
			sidebarWidth = available - 40
		}
		x += sidebarWidth + dividerWidth
	}

	// Preview content begins after the panel border. Worktrees add the tab row
	// and blank spacer; shells render their terminal immediately.
	y := 1
	if !p.shellSelected {
		y += 2
	}
	if !p.flashPreviewTime.IsZero() && time.Since(p.flashPreviewTime) < flashDuration {
		y++
	}

	if termPanel {
		if !p.termPanelVisible || p.termPanelOutput == nil {
			return nil, 0, 0, 0, 0, false
		}
		width, height := p.calculateTermPanelDimensions()
		if p.termPanelLayout == TermPanelRight {
			outputWidth, _ := p.calculateAgentPaneDimensions()
			x += outputWidth + 1
		} else {
			_, outputHeight := p.calculateAgentPaneDimensions()
			y += outputHeight + 2 // primary hint+rows, then divider
		}
		return p.termPanelOutput, width, height, x, y + 1, true
	}

	var buffer *tty.OutputBuffer
	if p.shellSelected {
		if shell := p.getSelectedShell(); shell != nil && shell.Agent != nil {
			buffer = shell.Agent.OutputBuf
		}
	} else if wt := p.selectedWorktree(); wt != nil && wt.Agent != nil {
		buffer = wt.Agent.OutputBuf
	}
	width, height := p.calculateAgentPaneDimensions()
	return buffer, width, height, x, y + 1, buffer != nil
}
