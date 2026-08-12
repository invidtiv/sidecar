package workspace

import (
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

	// Same buffer window and pane geometry the content render uses, or the
	// cursor desyncs from the pixels it is supposed to sit on.
	follow, offset, offsetFromBottom := p.terminalScrollState(termPanel,
		p.selectionTermPanel && p.selection.Anchor.Valid())
	absoluteBase, totalItems, loadingOlder := p.terminalHistorySummary(termPanel, buffer)
	paneWidth, paneHeight := p.resolvedPaneGeometry(termPanel, p.interactiveDescribes(termPanel))
	cursorX, cursorY, visible := terminalViewportCursorPosition(terminalViewportInput{
		Buffer:           buffer,
		Width:            width,
		Height:           height,
		Offset:           offset,
		OffsetFromBottom: offsetFromBottom,
		Follow:           follow,
		Interactive:      true,
		CursorRow:        p.interactiveState.CursorRow,
		CursorCol:        p.interactiveState.CursorCol,
		CursorVisible:    p.interactiveState.CursorVisible,
		PaneHeight:       paneHeight,
		PaneWidth:        paneWidth,
		NativeCursor:     true,
		AbsoluteBase:     absoluteBase,
		TotalItems:       totalItems,
		LoadingOlder:     loadingOlder,
	})
	if !visible {
		return nil
	}
	cursor := tty.PlaceCursor(x+cursorX, y+cursorY)
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

// nativeTerminalGeometry returns the terminal viewport's buffer plus the
// surface geometry it is drawn with. It is a thin buffer-resolving wrapper over
// terminalSurfaceGeometry, which owns the arithmetic.
func (p *Plugin) nativeTerminalGeometry(termPanel bool) (*tty.OutputBuffer, int, int, int, int, bool) {
	surface := p.terminalSurfaceGeometry(termPanel)
	if !surface.OK {
		return nil, 0, 0, 0, 0, false
	}

	if termPanel {
		if p.termPanelOutput == nil {
			return nil, 0, 0, 0, 0, false
		}
		return p.termPanelOutput, surface.Width, surface.Height, surface.X, surface.Y, true
	}

	var buffer *tty.OutputBuffer
	if p.shellSelected {
		if shell := p.getSelectedShell(); shell != nil && shell.Agent != nil {
			buffer = shell.Agent.OutputBuf
		}
	} else if wt := p.selectedWorktree(); wt != nil && wt.Agent != nil {
		buffer = wt.Agent.OutputBuf
	}
	return buffer, surface.Width, surface.Height, surface.X, surface.Y, buffer != nil
}
