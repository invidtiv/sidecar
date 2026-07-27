package workspace

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
)

type terminalHistoryState struct {
	HistorySize int
	Loading     bool
	Exhausted   bool
}

type terminalHistorySource struct {
	Key       string
	Target    string
	Buffer    *tty.OutputBuffer
	TermPanel bool
}

type terminalHistoryLoadedMsg struct {
	Source      terminalHistorySource
	Capture     tty.CaptureRange
	ScrollLines int
	Err         error
}

func terminalHistoryKey(kind, target string) string {
	return kind + ":" + target
}

func (p *Plugin) recordTerminalHistory(kind, target string, historySize int) {
	if target == "" || historySize < 0 {
		return
	}
	if p.terminalHistory == nil {
		p.terminalHistory = make(map[string]terminalHistoryState)
	}
	key := terminalHistoryKey(kind, target)
	state := p.terminalHistory[key]
	state.HistorySize = historySize
	state.Exhausted = historySize == 0
	p.terminalHistory[key] = state
}

func (p *Plugin) terminalHistoryFor(termPanel bool) (terminalHistorySource, bool) {
	if termPanel {
		target := p.termPanelPaneID
		if target == "" {
			target = p.termPanelSession
		}
		if target == "" || p.termPanelOutput == nil {
			return terminalHistorySource{}, false
		}
		return terminalHistorySource{
			Key:       terminalHistoryKey("panel", p.termPanelSession),
			Target:    target,
			Buffer:    p.termPanelOutput,
			TermPanel: true,
		}, true
	}
	if p.shellSelected {
		shell := p.getSelectedShell()
		if shell == nil || shell.Agent == nil || shell.Agent.OutputBuf == nil {
			return terminalHistorySource{}, false
		}
		target := shell.Agent.TmuxPane
		if target == "" {
			target = shell.TmuxName
		}
		return terminalHistorySource{
			Key:    terminalHistoryKey("shell", shell.TmuxName),
			Target: target,
			Buffer: shell.Agent.OutputBuf,
		}, true
	}
	wt := p.selectedWorktree()
	if wt == nil || wt.Agent == nil || wt.Agent.OutputBuf == nil {
		return terminalHistorySource{}, false
	}
	target := wt.Agent.TmuxPane
	if target == "" {
		target = wt.Agent.TmuxSession
	}
	return terminalHistorySource{
		Key:    terminalHistoryKey("agent", wt.Agent.TmuxSession),
		Target: target,
		Buffer: wt.Agent.OutputBuf,
	}, true
}

// loadOlderTerminalHistory fetches only the range immediately preceding the
// currently loaded buffer. scrollLines is replayed after the async prepend.
func (p *Plugin) loadOlderTerminalHistory(termPanel bool, scrollLines int) tea.Cmd {
	source, ok := p.terminalHistoryFor(termPanel)
	if !ok || scrollLines <= 0 {
		return nil
	}
	state := p.terminalHistory[source.Key]
	base, _, absolute := source.Buffer.AbsoluteRange()
	if !absolute || base <= 0 || state.Loading || state.Exhausted {
		return nil
	}
	start := max(base-historyLoadChunk, 0)
	end := base - 1
	historySize := state.HistorySize
	if historySize <= 0 {
		return nil
	}
	state.Loading = true
	p.terminalHistory[source.Key] = state
	relativeStart := start - historySize
	relativeEnd := end - historySize
	return func() tea.Msg {
		capture, err := tty.CapturePaneRange(source.Target, relativeStart, relativeEnd)
		return terminalHistoryLoadedMsg{
			Source:      source,
			Capture:     capture,
			ScrollLines: scrollLines,
			Err:         err,
		}
	}
}

func (p *Plugin) applyTerminalHistory(msg terminalHistoryLoadedMsg) {
	state := p.terminalHistory[msg.Source.Key]
	state.Loading = false
	if msg.Err != nil {
		p.terminalHistory[msg.Source.Key] = state
		if p.ctx != nil && p.ctx.Logger != nil {
			p.ctx.Logger.Debug("terminal history capture failed", "source", msg.Source.Key, "err", msg.Err)
		}
		return
	}
	current, ok := p.terminalHistoryFor(msg.Source.TermPanel)
	if !ok || current.Key != msg.Source.Key || current.Buffer != msg.Source.Buffer {
		p.terminalHistory[msg.Source.Key] = state
		return
	}
	oldBase, _, ok := current.Buffer.AbsoluteRange()
	if !ok {
		p.terminalHistory[msg.Source.Key] = state
		return
	}
	if !current.Buffer.PrependSnapshot(msg.Capture.Output, msg.Capture.StartLine) {
		p.terminalHistory[msg.Source.Key] = state
		return
	}
	newBase, _, _ := current.Buffer.AbsoluteRange()
	added := oldBase - newBase
	state.HistorySize = msg.Capture.HistorySize
	state.Exhausted = newBase == 0
	p.terminalHistory[msg.Source.Key] = state

	if msg.Source.TermPanel {
		p.termPanelScroll = min(p.termPanelScroll+msg.ScrollLines, p.termPanelMaxScroll())
		return
	}
	// Prepending shifts the old local coordinates down by added lines. Replay
	// the user's pending upward movement in that shifted coordinate space.
	p.previewOffset = max(p.previewOffset+added-msg.ScrollLines, 0)
	p.autoScrollOutput = false
}

func (p *Plugin) terminalHistorySummary(termPanel bool, buffer *tty.OutputBuffer) (base, total int, loading bool) {
	source, ok := p.terminalHistoryFor(termPanel)
	if !ok || source.Buffer != buffer {
		return 0, buffer.LineCount(), false
	}
	base, end, absolute := buffer.AbsoluteRange()
	if !absolute {
		return 0, buffer.LineCount(), false
	}
	state := p.terminalHistory[source.Key]
	total = max(end, state.HistorySize)
	return base, total, state.Loading
}

func (m terminalHistoryLoadedMsg) String() string {
	if m.Err != nil {
		return fmt.Sprintf("terminal history %s: %v", m.Source.Key, m.Err)
	}
	return fmt.Sprintf("terminal history %s [%d,%d)", m.Source.Key, m.Capture.StartLine, m.Capture.EndLine)
}
