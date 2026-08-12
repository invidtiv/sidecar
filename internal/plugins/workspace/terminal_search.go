package workspace

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

type terminalSearchMatch struct {
	Line     int
	StartCol int
	EndCol   int
}

type terminalSearchMatches struct {
	Items []terminalSearchMatch
}

type terminalSearchState struct {
	InputActive bool
	SourceKey   string
	TermPanel   bool
	Query       string
	Matches     []terminalSearchMatch
	Current     int
	Generation  uint64
}

type terminalSearchHistoryLoadedMsg struct {
	Source     terminalHistorySource
	Capture    tty.CaptureRange
	RequestGen uint64
	SearchGen  uint64
	Err        error
}

func isInteractiveSearchKey(msg tea.KeyPressMsg) bool {
	return (msg.Code == 'f' || msg.Code == 'F') &&
		msg.Mod.Contains(tea.ModCtrl) && msg.Mod.Contains(tea.ModShift)
}

func (p *Plugin) handleTerminalSearchKey(msg tea.KeyPressMsg, interactive bool) (bool, tea.Cmd) {
	search := &p.terminalSearch
	if search.InputActive {
		switch msg.Code {
		case tea.KeyEscape:
			search.InputActive = false
			return true, nil
		case tea.KeyEnter:
			search.InputActive = false
			p.recomputeTerminalSearch()
			p.revealTerminalSearchMatch()
			return true, nil
		case tea.KeyBackspace:
			if len(search.Query) > 0 {
				runes := []rune(search.Query)
				search.Query = string(runes[:len(runes)-1])
				p.recomputeTerminalSearch()
			}
			return true, nil
		}
		if msg.Text != "" && !msg.Mod.Contains(tea.ModCtrl) && !msg.Mod.Contains(tea.ModAlt) {
			search.Query += msg.Text
			p.recomputeTerminalSearch()
		}
		return true, nil
	}

	trigger := (!interactive && msg.String() == "/") || (interactive && isInteractiveSearchKey(msg))
	if trigger {
		return true, p.beginTerminalSearch()
	}
	if search.Query != "" && len(search.Matches) > 0 {
		switch msg.String() {
		case "n":
			search.Current = (search.Current + 1) % len(search.Matches)
			p.revealTerminalSearchMatch()
			return true, nil
		case "N", "shift+n":
			search.Current = (search.Current - 1 + len(search.Matches)) % len(search.Matches)
			p.revealTerminalSearchMatch()
			return true, nil
		}
	}
	if search.Query != "" && msg.Code == tea.KeyEscape {
		p.clearTerminalSearch()
		return true, nil
	}
	return false, nil
}

func (p *Plugin) beginTerminalSearch() tea.Cmd {
	termPanel := p.termPanelVisible && p.termPanelFocused
	if p.viewMode == ViewModeInteractive && p.interactiveState != nil {
		termPanel = p.interactiveState.TermPanel
	}
	source, ok := p.terminalHistoryFor(termPanel)
	if !ok {
		return nil
	}
	if p.terminalSearch.SourceKey != source.Key {
		p.cancelTerminalHistoryIntentByKey(p.terminalSearch.SourceKey)
		p.terminalSearch.Query = ""
		p.terminalSearch.Matches = nil
		p.terminalSearch.Current = 0
	}
	p.terminalSearch.InputActive = true
	p.terminalSearch.SourceKey = source.Key
	p.terminalSearch.TermPanel = termPanel
	p.terminalSearch.Generation++
	searchGen := p.terminalSearch.Generation
	base, _, absolute := source.Buffer.AbsoluteRange()
	state := p.terminalHistory[source.Key]
	if !absolute || base <= 0 || state.HistorySize <= 0 {
		return nil
	}
	// Searching is user-initiated and should cover the complete tmux history,
	// not only ranges previously visited by scrolling. Supersede any bounded
	// load; its generation will be ignored if it completes later.
	state.PendingScroll = 0
	state.Loading = true
	state.RequestGen++
	requestGen := state.RequestGen
	p.terminalHistory[source.Key] = state
	relativeEnd := base - state.HistorySize - 1
	oldest := max(base-tty.HistoryLimit, 0)
	relativeStart := oldest - state.HistorySize
	return func() tea.Msg {
		capture, err := tty.CapturePaneRange(source.Target, relativeStart, relativeEnd)
		return terminalSearchHistoryLoadedMsg{
			Source:     source,
			Capture:    capture,
			RequestGen: requestGen,
			SearchGen:  searchGen,
			Err:        err,
		}
	}
}

func (p *Plugin) applyTerminalSearchHistory(msg terminalSearchHistoryLoadedMsg) {
	if msg.SearchGen != p.terminalSearch.Generation {
		return
	}
	state := p.terminalHistory[msg.Source.Key]
	if msg.RequestGen != state.RequestGen {
		return
	}
	state.Loading = false
	if msg.Err != nil {
		p.terminalHistory[msg.Source.Key] = state
		return
	}
	current, ok := p.terminalHistoryFor(msg.Source.TermPanel)
	if !ok || current.Key != msg.Source.Key || current.Buffer != msg.Source.Buffer {
		p.terminalHistory[msg.Source.Key] = state
		return
	}
	oldBase, _, absolute := current.Buffer.AbsoluteRange()
	if !absolute || !current.Buffer.PrependSnapshot(msg.Capture.Output, msg.Capture.StartLine) {
		p.terminalHistory[msg.Source.Key] = state
		return
	}
	newBase, _, _ := current.Buffer.AbsoluteRange()
	added := oldBase - newBase
	state.HistorySize = msg.Capture.HistorySize
	state.Exhausted = newBase == 0
	p.terminalHistory[msg.Source.Key] = state
	if !msg.Source.TermPanel && !p.autoScrollOutput {
		p.previewOffset += added
	}
	p.recomputeTerminalSearch()
	if !p.terminalSearch.InputActive {
		p.revealTerminalSearchMatch()
	}
}

func (p *Plugin) clearTerminalSearch() {
	sourceKey := p.terminalSearch.SourceKey
	p.terminalSearch.Generation++
	p.terminalSearch.InputActive = false
	p.terminalSearch.SourceKey = ""
	p.terminalSearch.Query = ""
	p.terminalSearch.Matches = nil
	p.terminalSearch.Current = 0
	p.cancelTerminalHistoryIntentByKey(sourceKey)
}

func (p *Plugin) recomputeTerminalSearch() {
	search := &p.terminalSearch
	var previous terminalSearchMatch
	hadPrevious := search.Current >= 0 && search.Current < len(search.Matches)
	if hadPrevious {
		previous = search.Matches[search.Current]
	}
	search.Matches = search.Matches[:0]
	search.Current = 0
	queryTokens := terminalSearchGraphemes(search.Query)
	if len(queryTokens) == 0 {
		return
	}
	source, ok := p.terminalHistoryFor(search.TermPanel)
	if !ok || source.Key != search.SourceKey || source.Buffer == nil {
		return
	}
	base := 0
	if absoluteBase, _, absolute := source.Buffer.AbsoluteRange(); absolute {
		base = absoluteBase
	}
	for i, raw := range source.Buffer.Lines() {
		plain := ansi.Strip(ui.ExpandTabs(raw, tabStopWidth))
		lineTokens := terminalSearchGraphemes(plain)
		for from := 0; from+len(queryTokens) <= len(lineTokens); {
			matched := true
			for j := range queryTokens {
				if !strings.EqualFold(lineTokens[from+j].Text, queryTokens[j].Text) {
					matched = false
					break
				}
			}
			if !matched {
				from++
				continue
			}
			last := from + len(queryTokens) - 1
			search.Matches = append(search.Matches, terminalSearchMatch{
				Line:     base + i,
				StartCol: lineTokens[from].StartCol,
				EndCol:   max(lineTokens[last].EndCol-1, lineTokens[from].StartCol),
			})
			from += len(queryTokens)
		}
	}
	if hadPrevious {
		for i, match := range search.Matches {
			if match == previous {
				search.Current = i
				break
			}
		}
	}
}

type terminalSearchGrapheme struct {
	Text             string
	StartCol, EndCol int
}

func terminalSearchGraphemes(value string) []terminalSearchGrapheme {
	var result []terminalSearchGrapheme
	state := ansi.NormalState
	col := 0
	for len(value) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(value, state, nil)
		if n <= 0 {
			break
		}
		if width > 0 {
			result = append(result, terminalSearchGrapheme{
				Text:     seq,
				StartCol: col,
				EndCol:   col + width,
			})
			col += width
		}
		state = newState
		value = value[n:]
	}
	return result
}

func (p *Plugin) revealTerminalSearchMatch() {
	search := &p.terminalSearch
	if len(search.Matches) == 0 || search.Current < 0 || search.Current >= len(search.Matches) {
		return
	}
	source, ok := p.terminalHistoryFor(search.TermPanel)
	if !ok || source.Key != search.SourceKey {
		return
	}
	match := search.Matches[search.Current]
	base := 0
	if absoluteBase, _, absolute := source.Buffer.AbsoluteRange(); absolute {
		base = absoluteBase
	}
	localLine := match.Line - base
	if search.TermPanel {
		p.releaseTermPanelDocFreeze()
		maxScroll := p.termPanelMaxScroll()
		// No panel drawn means no viewport to centre the match in; the clamp
		// below then pins the scroll to the top of the (empty) range.
		_, height, _ := p.calculateTermPanelDimensions()
		start := min(max(localLine-height/2, 0), maxScroll)
		p.termPanelScroll = maxScroll - start
		return
	}
	height := p.getPreviewVisibleHeight()
	p.previewOffset = min(max(localLine-height/2, 0), p.getMaxScrollOffset())
	p.autoScrollOutput = false
}

func (p *Plugin) terminalSearchMatches(termPanel bool) *terminalSearchMatches {
	search := &p.terminalSearch
	if search.Query == "" || search.TermPanel != termPanel {
		return nil
	}
	source, ok := p.terminalHistoryFor(termPanel)
	if !ok || source.Key != search.SourceKey {
		return nil
	}
	return &terminalSearchMatches{Items: search.Matches}
}
