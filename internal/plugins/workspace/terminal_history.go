package workspace

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
)

type terminalHistoryState struct {
	HistorySize   int
	Loading       bool
	Exhausted     bool
	PendingScroll int
	RequestGen    uint64
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
	RequestGen  uint64
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
	if p.terminalSearch.SourceKey == key && p.terminalSearch.Query != "" {
		p.recomputeTerminalSearch()
	}
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
	state.PendingScroll += scrollLines
	if state.Loading {
		p.terminalHistory[source.Key] = state
		return nil
	}
	base, _, absolute := source.Buffer.AbsoluteRange()
	if !absolute || base <= 0 || state.Exhausted {
		state.PendingScroll = 0
		p.terminalHistory[source.Key] = state
		return nil
	}
	start := max(base-historyLoadChunk, 0)
	end := base - 1
	historySize := state.HistorySize
	if historySize <= 0 {
		return nil
	}
	state.Loading = true
	state.RequestGen++
	requestGen := state.RequestGen
	p.terminalHistory[source.Key] = state
	relativeStart := start - historySize
	relativeEnd := end - historySize
	return func() tea.Msg {
		capture, err := tty.CapturePaneRange(source.Target, relativeStart, relativeEnd)
		return terminalHistoryLoadedMsg{
			Source:     source,
			Capture:    capture,
			RequestGen: requestGen,
			Err:        err,
		}
	}
}

func (p *Plugin) applyTerminalHistory(msg terminalHistoryLoadedMsg) tea.Cmd {
	state := p.terminalHistory[msg.Source.Key]
	if msg.RequestGen != 0 && msg.RequestGen != state.RequestGen {
		return nil
	}
	state.Loading = false
	scrollLines := state.PendingScroll
	if scrollLines == 0 {
		scrollLines = msg.ScrollLines
	}
	state.PendingScroll = 0
	if msg.Err != nil {
		p.terminalHistory[msg.Source.Key] = state
		if p.ctx != nil && p.ctx.Logger != nil {
			p.ctx.Logger.Debug("terminal history capture failed", "source", msg.Source.Key, "err", msg.Err)
		}
		return nil
	}
	current, ok := p.terminalHistoryFor(msg.Source.TermPanel)
	if !ok || current.Key != msg.Source.Key || current.Buffer != msg.Source.Buffer {
		p.terminalHistory[msg.Source.Key] = state
		return nil
	}
	oldBase, _, ok := current.Buffer.AbsoluteRange()
	if !ok {
		p.terminalHistory[msg.Source.Key] = state
		return nil
	}
	if !current.Buffer.PrependSnapshot(msg.Capture.Output, msg.Capture.StartLine) {
		p.terminalHistory[msg.Source.Key] = state
		return nil
	}
	newBase, _, _ := current.Buffer.AbsoluteRange()
	added := oldBase - newBase
	state.HistorySize = msg.Capture.HistorySize
	state.Exhausted = newBase == 0
	p.terminalHistory[msg.Source.Key] = state
	if p.terminalSearch.SourceKey == msg.Source.Key && p.terminalSearch.Query != "" {
		p.recomputeTerminalSearch()
	}

	if msg.Source.TermPanel {
		p.termPanelScroll = min(p.termPanelScroll+scrollLines, p.termPanelMaxScroll())
		if scrollLines > added && !state.Exhausted {
			return p.loadOlderTerminalHistory(true, scrollLines-added)
		}
		return nil
	}
	// Prepending shifts the old local coordinates down by added lines. Replay
	// the user's pending upward movement in that shifted coordinate space.
	p.previewOffset = max(p.previewOffset+added-scrollLines, 0)
	p.autoScrollOutput = false
	if scrollLines > added && !state.Exhausted {
		return p.loadOlderTerminalHistory(false, scrollLines-added)
	}
	return nil
}

func (p *Plugin) cancelTerminalHistoryIntent(termPanel bool) {
	source, ok := p.terminalHistoryFor(termPanel)
	if !ok {
		return
	}
	p.cancelTerminalHistoryIntentByKey(source.Key)
}

func (p *Plugin) cancelTerminalHistoryIntentByKey(key string) {
	if key == "" {
		return
	}
	state := p.terminalHistory[key]
	state.PendingScroll = 0
	state.Loading = false
	state.RequestGen++
	p.terminalHistory[key] = state
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
