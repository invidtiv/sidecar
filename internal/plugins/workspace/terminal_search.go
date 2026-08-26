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
	Panel       bool
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
	termPanel := p.shellLeafVisible() && p.shellLeafFocused()
	if p.viewMode == ViewModeInteractive && p.interactiveState != nil {
		termPanel = p.terminalPaneIsPanel(p.interactiveState.LeafID)
	}
	source, ok := p.terminalHistoryFor(termPanel)
	if !ok {
		return nil
	}
	ownership := p.currentTerminalOwnership()
	if ownership == 0 {
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
	p.terminalSearch.Panel = termPanel
	p.terminalSearch.Generation++
	searchGen := p.terminalSearch.Generation
	base, _, absolute := source.Buffer.AbsoluteRange()
	state := p.terminalHistory[source.Key]
	// Searching is user-initiated and covers the complete tmux history, not only
	// ranges previously visited by scrolling. The reach supersedes any bounded
	// load; its generation will be ignored if it completes later.
	request, ok := state.RequestAll(base, absolute)
	if !ok {
		return nil
	}
	// A reader can search before any capture has recorded a reach for this pane,
	// and the request state is what admits the result of the read now in flight.
	if p.terminalHistory == nil {
		p.terminalHistory = make(map[string]tty.HistoryReach)
	}
	p.terminalHistory[source.Key] = state
	return func() tea.Msg {
		return p.withTerminalOwnership(ownership, func() tea.Msg {
			capture, err := workspaceCapturePaneRange(source.Target, request.Start, request.End)
			return terminalSearchHistoryLoadedMsg{
				Source:     source,
				Capture:    capture,
				RequestGen: request.Generation,
				SearchGen:  searchGen,
				Err:        err,
			}
		})
	}
}

func (p *Plugin) applyTerminalSearchHistory(msg terminalSearchHistoryLoadedMsg) tea.Cmd {
	if msg.SearchGen != p.terminalSearch.Generation {
		return nil
	}
	state := p.terminalHistory[msg.Source.Key]
	// A scroll that hit the bound while this read was in flight was coalesced
	// onto the reach rather than starting a second read of the same range, so
	// this is the only place those rows are still owed to the reader.
	scrollLines, ok := state.Accept(msg.RequestGen)
	if !ok {
		return nil
	}
	if msg.Err != nil {
		p.terminalHistory[msg.Source.Key] = state
		return nil
	}
	current, ok := p.terminalHistoryFor(p.terminalPaneIsPanel(msg.Source.LeafID))
	if !ok || current.Key != msg.Source.Key || current.Buffer != msg.Source.Buffer {
		p.terminalHistory[msg.Source.Key] = state
		return nil
	}
	oldBase, _, absolute := current.Buffer.AbsoluteRange()
	if !absolute || !current.Buffer.PrependSnapshot(msg.Capture.Output, msg.Capture.StartLine) {
		p.terminalHistory[msg.Source.Key] = state
		return nil
	}
	newBase, _, _ := current.Buffer.AbsoluteRange()
	added := oldBase - newBase
	state.Settle(newBase, msg.Capture.HistorySize)
	remainder, more := state.Remainder(scrollLines, added)
	p.terminalHistory[msg.Source.Key] = state
	// A window placed from the live bottom rides the renumbering out; only one
	// pinned to an absolute row has to be shifted by the rows just prepended.
	if p.terminalPaneIsPanel(msg.Source.LeafID) {
		p.requireShellTermPane().Freeze.Rebase(added)
		p.requireShellTermPane().Scroll = min(p.requireShellTermPane().Scroll+scrollLines, p.terminalMaxScroll(true))
	} else {
		p.primaryTermPane().Freeze.Rebase(added)
		p.primaryTermPane().Scroll = min(p.primaryTermPane().Scroll+scrollLines, p.terminalMaxScroll(false))
	}
	p.recomputeTerminalSearch()
	if !p.terminalSearch.InputActive {
		p.revealTerminalSearchMatch()
	}
	if more {
		return p.loadOlderTerminalHistory(p.terminalPaneIsPanel(msg.Source.LeafID), remainder)
	}
	return nil
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
	source, ok := p.terminalHistoryFor(search.Panel)
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
	source, ok := p.terminalHistoryFor(search.Panel)
	if !ok || source.Key != search.SourceKey {
		return
	}
	match := search.Matches[search.Current]
	base := 0
	if absoluteBase, _, absolute := source.Buffer.AbsoluteRange(); absolute {
		base = absoluteBase
	}
	localLine := match.Line - base
	// Both numbers come off the drawn window: centring a match mixes a bound
	// with a height, and taking them from two derivations of one surface puts
	// the match off centre wherever the two disagree (td-bbbbfe). No panel drawn
	// means no viewport to centre in; the clamp then pins the scroll to the top
	// of the (empty) range.
	if search.Panel {
		p.thawTerminalWindow(true)
		maxScroll := p.terminalMaxScroll(true)
		height := p.terminalViewportLayoutFor(true).DisplayHeight
		start := min(max(localLine-height/2, 0), maxScroll)
		p.requireShellTermPane().Scroll = maxScroll - start
		return
	}
	p.thawTerminalWindow(false)
	maxScroll := p.terminalMaxScroll(false)
	height := p.terminalViewportLayoutFor(false).DisplayHeight
	start := min(max(localLine-height/2, 0), maxScroll)
	p.primaryTermPane().Scroll = maxScroll - start
}

func (p *Plugin) terminalSearchMatches(termPanel bool) *terminalSearchMatches {
	search := &p.terminalSearch
	if search.Query == "" || search.Panel != termPanel {
		return nil
	}
	source, ok := p.terminalHistoryFor(termPanel)
	if !ok || source.Key != search.SourceKey {
		return nil
	}
	return &terminalSearchMatches{Items: search.Matches}
}
