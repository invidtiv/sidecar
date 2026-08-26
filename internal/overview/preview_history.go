package overview

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
)

// How far back this surface reads is not this surface's decision. The order of
// a lazy read — one request per bound-hit, a pending scroll coalesced onto it, a
// superseded result ignored, a hard stop at tmux's oldest line — is
// tty.HistoryReach's, the same one the project Workspaces plugin adopts. What is
// here is the adapter: which pane is being read, where the rows go, and what a
// window placed from the live bottom owes the reader once they land.
//
// Before this, the browser dead-ended at its initial capture and said so. It now
// ends where tmux's history does, which is where the project surface ends.

// capturePreviewHistory is the tmux read the reach opens. It is a variable so
// the order can be proved without a tmux server behind it.
var capturePreviewHistory = tty.CapturePaneRange

// previewHistoryLoadedMsg carries one completed read. Target and Generation
// scope it: a result for a pane the preview has moved off, or for a request
// something has since superseded, is not applied to whatever is on screen now.
type previewHistoryLoadedMsg struct {
	Target     tty.Target
	Capture    tty.CaptureRange
	Generation uint64
	Err        error
}

// reachOlderPreviewHistory asks for the range immediately older than the
// window's oldest loaded line. scrollLines is what the reader is owed once those
// rows land.
func (m *Model) reachOlderPreviewHistory(scrollLines int) tea.Cmd {
	buffer := m.previewBuffer()
	if !m.previewTerminalActive() || buffer == nil {
		return nil
	}
	ownership := m.currentPreviewOwnership()
	if ownership == 0 {
		return nil
	}
	// The pane's own report of how much history it holds is the origin the
	// relative coordinates of a capture are measured from, so it is read at the
	// moment of the request rather than remembered from an older frame.
	if info := m.previewTerminalState().terminal.History(); info.HasHistory {
		m.previewTerminalLeaf().History.Record(info.HistorySize)
	}
	base, _, absolute := buffer.AbsoluteRange()
	request, outcome := m.previewTerminalLeaf().History.Request(base, absolute, scrollLines)
	switch outcome {
	case tty.HistoryRequested:
	case tty.HistoryEnded:
		return m.notePreviewScrollbackLimit()
	default:
		return nil
	}
	target, pane := m.previewHistoryTarget()
	if pane == "" {
		return nil
	}
	return func() tea.Msg {
		release, ok := m.acquirePreviewOwnership(ownership)
		if !ok {
			return nil
		}
		defer release()
		capture, err := capturePreviewHistory(pane, request.Start, request.End)
		return previewHistoryLoadedMsg{
			Target:     target,
			Capture:    capture,
			Generation: request.Generation,
			Err:        err,
		}
	}
}

// previewHistoryTarget is the pane being read, and the tmux address to read it
// by. capture-pane takes a pane where there is one; the session is the fallback
// for a target named only by its session.
func (m *Model) previewHistoryTarget() (tty.Target, string) {
	target := m.previewTarget()
	if target.Pane != "" {
		return target, target.Pane
	}
	return target, target.Session
}

// applyPreviewHistory merges a completed read into the pane's buffer and
// replays the movement waiting on it.
func (m *Model) applyPreviewHistory(msg previewHistoryLoadedMsg) tea.Cmd {
	if msg.Target != m.previewTarget() || !m.previewTerminalActive() {
		return nil
	}
	scrollLines, ok := m.previewTerminalLeaf().History.Accept(msg.Generation)
	if !ok || msg.Err != nil {
		return nil
	}
	buffer := m.previewBuffer()
	if buffer == nil {
		return nil
	}
	oldBase, _, absolute := buffer.AbsoluteRange()
	if !absolute || !m.previewTerminalState().terminal.PrependHistory(msg.Capture.Output, msg.Capture.StartLine) {
		return nil
	}
	newBase, _, _ := buffer.AbsoluteRange()
	added := oldBase - newBase
	m.previewTerminalLeaf().History.Settle(newBase, msg.Capture.HistorySize)
	// A window placed from the live bottom is not renumbered by a prepend; only
	// a gesture's pin, which names an absolute row, is. So only the reader's own
	// pending movement is replayed here.
	m.previewTerminalLeaf().Freeze.Rebase(added)
	m.scrollPreview(scrollLines)
	if remainder, more := m.previewTerminalLeaf().History.Remainder(scrollLines, added); more {
		return m.reachOlderPreviewHistory(remainder)
	}
	return nil
}
