package overview

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// Terminal search is a property of the loaded absolute buffer, not of where
// tmux runs. The only host-specific operation is RequestAll's bounded history
// read, which goes through tty.Model.CaptureRange and therefore stays in-band
// for a remote pane.
type previewTerminalSearchMatch struct {
	Line     int
	StartCol int
	EndCol   int
}

type previewTerminalSearchState struct {
	InputActive bool
	Target      tty.Target
	Query       string
	Matches     []previewTerminalSearchMatch
	Current     int
	Generation  uint64
}

type previewTerminalSearchLoadedMsg struct {
	Target     tty.Target
	Capture    tty.CaptureRange
	RequestGen uint64
	SearchGen  uint64
	Err        error
}

func previewInteractiveSearchKey(msg tea.KeyPressMsg) bool {
	return (msg.Code == 'f' || msg.Code == 'F') &&
		msg.Mod.Contains(tea.ModCtrl) && msg.Mod.Contains(tea.ModShift)
}

func (m *Model) handlePreviewTerminalSearchKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	search := &m.terminalSearch
	if search.InputActive {
		switch msg.Code {
		case tea.KeyEscape:
			search.InputActive = false
			return true, nil
		case tea.KeyEnter:
			search.InputActive = false
			m.recomputePreviewTerminalSearch()
			m.revealPreviewTerminalSearchMatch()
			return true, nil
		case tea.KeyBackspace:
			if len(search.Query) > 0 {
				runes := []rune(search.Query)
				search.Query = string(runes[:len(runes)-1])
				m.recomputePreviewTerminalSearch()
			}
			return true, nil
		}
		if msg.Text != "" && !msg.Mod.Contains(tea.ModCtrl) && !msg.Mod.Contains(tea.ModAlt) {
			search.Query += msg.Text
			m.recomputePreviewTerminalSearch()
		}
		return true, nil
	}

	trigger := (!m.PreviewInteractive() && msg.String() == "/") ||
		(m.PreviewInteractive() && previewInteractiveSearchKey(msg))
	if trigger {
		return true, m.beginPreviewTerminalSearch()
	}
	if search.Query != "" && len(search.Matches) > 0 {
		switch msg.String() {
		case "n":
			search.Current = (search.Current + 1) % len(search.Matches)
			m.revealPreviewTerminalSearchMatch()
			return true, nil
		case "N", "shift+n":
			search.Current = (search.Current - 1 + len(search.Matches)) % len(search.Matches)
			m.revealPreviewTerminalSearchMatch()
			return true, nil
		}
	}
	if search.Query != "" && msg.Code == tea.KeyEscape {
		m.clearPreviewTerminalSearch()
		return true, nil
	}
	return false, nil
}

func (m *Model) beginPreviewTerminalSearch() tea.Cmd {
	terminal := m.previewTerminalState().terminal
	buffer := m.previewBuffer()
	if terminal == nil || !terminal.IsActive() || buffer == nil {
		return nil
	}
	target := m.previewTarget()
	search := &m.terminalSearch
	if search.Target != target {
		search.Query = ""
		search.Matches = nil
		search.Current = 0
	}
	search.InputActive = true
	search.Target = target
	search.Generation++
	searchGen := search.Generation
	if info := terminal.History(); info.HasHistory {
		m.previewTerminalLeaf().History.Record(info.HistorySize)
	}
	base, _, absolute := buffer.AbsoluteRange()
	reach := m.previewTerminalLeaf().History
	request, ok := reach.RequestAll(base, absolute)
	m.previewTerminalLeaf().History = reach
	if !ok {
		m.recomputePreviewTerminalSearch()
		return nil
	}
	capturer, ok := terminal.(previewRangeCapturer)
	if !ok {
		reach.Cancel()
		m.previewTerminalLeaf().History = reach
		return nil
	}
	return func() tea.Msg {
		capture, err := capturer.CaptureRange(request.Start, request.End)
		return previewTerminalSearchLoadedMsg{
			Target: target, Capture: capture, RequestGen: request.Generation,
			SearchGen: searchGen, Err: err,
		}
	}
}

func (m *Model) applyPreviewTerminalSearchHistory(msg previewTerminalSearchLoadedMsg) tea.Cmd {
	if msg.Target != m.previewTarget() || msg.SearchGen != m.terminalSearch.Generation {
		return nil
	}
	reach := m.previewTerminalLeaf().History
	_, ok := reach.Accept(msg.RequestGen)
	if !ok || msg.Err != nil {
		m.previewTerminalLeaf().History = reach
		return nil
	}
	buffer := m.previewBuffer()
	if buffer == nil {
		return nil
	}
	oldBase, _, absolute := buffer.AbsoluteRange()
	if !absolute || !m.previewTerminalState().terminal.PrependHistory(msg.Capture.Output, msg.Capture.StartLine) {
		m.previewTerminalLeaf().History = reach
		return nil
	}
	newBase, _, _ := buffer.AbsoluteRange()
	added := oldBase - newBase
	reach.Settle(newBase, msg.Capture.HistorySize)
	m.previewTerminalLeaf().History = reach
	m.previewTerminalLeaf().Freeze.Rebase(added)
	m.recomputePreviewTerminalSearch()
	return nil
}

func (m *Model) clearPreviewTerminalSearch() {
	if m == nil {
		return
	}
	generation := m.terminalSearch.Generation + 1
	m.terminalSearch = previewTerminalSearchState{Generation: generation}
}

func (m *Model) recomputePreviewTerminalSearch() {
	search := &m.terminalSearch
	search.Matches = search.Matches[:0]
	search.Current = 0
	query := previewSearchGraphemes(search.Query)
	buffer := m.previewBuffer()
	if len(query) == 0 || buffer == nil || search.Target != m.previewTarget() {
		return
	}
	base, _, _ := buffer.AbsoluteRange()
	for row, raw := range buffer.Lines() {
		line := previewSearchGraphemes(ansi.Strip(ui.ExpandTabs(raw, tty.DefaultTabWidth)))
		for from := 0; from+len(query) <= len(line); {
			matched := true
			for i := range query {
				if !strings.EqualFold(line[from+i].Text, query[i].Text) {
					matched = false
					break
				}
			}
			if !matched {
				from++
				continue
			}
			last := from + len(query) - 1
			search.Matches = append(search.Matches, previewTerminalSearchMatch{
				Line: base + row, StartCol: line[from].StartCol,
				EndCol: max(line[last].EndCol-1, line[from].StartCol),
			})
			from += len(query)
		}
	}
}

type previewSearchGrapheme struct {
	Text             string
	StartCol, EndCol int
}

func previewSearchGraphemes(value string) []previewSearchGrapheme {
	var result []previewSearchGrapheme
	state, col := ansi.NormalState, 0
	for len(value) > 0 {
		seq, width, n, next := ansi.GraphemeWidth.DecodeSequenceInString(value, state, nil)
		if n <= 0 {
			break
		}
		if width > 0 {
			result = append(result, previewSearchGrapheme{Text: seq, StartCol: col, EndCol: col + width})
			col += width
		}
		state, value = next, value[n:]
	}
	return result
}

func (m *Model) revealPreviewTerminalSearchMatch() {
	search := &m.terminalSearch
	if len(search.Matches) == 0 || search.Current < 0 || search.Current >= len(search.Matches) {
		return
	}
	buffer := m.previewBuffer()
	if buffer == nil {
		return
	}
	base, _, _ := buffer.AbsoluteRange()
	localLine := search.Matches[search.Current].Line - base
	window := m.previewWindow()
	height := max(window.layout.DisplayHeight, 1)
	maxScroll := m.previewMaxOffset()
	start := min(max(localLine-height/2, 0), maxScroll)
	m.thawPreviewWindow()
	m.previewTerminalLeaf().Scroll = maxScroll - start
}

func (m *Model) appendPreviewTerminalSearchStatus(hints string) string {
	search := &m.terminalSearch
	if search.Target != m.previewTarget() || (search.Query == "" && !search.InputActive) {
		return hints
	}
	status := "/" + search.Query
	if search.InputActive {
		status += "▌"
	} else if len(search.Matches) == 0 {
		status = "no matches"
	} else {
		status = fmt.Sprintf("%d/%d matches · n/N", search.Current+1, len(search.Matches))
	}
	return hints + " " + status
}

func (m *Model) previewTerminalDecorator(leaf *termpanes.Leaf) func(string, int) string {
	return func(line string, absoluteLine int) string {
		line = leaf.LinkState.Decorate(line, absoluteLine)
		search := &m.terminalSearch
		if search.Query == "" || search.Target != m.previewTarget() {
			return line
		}
		for _, match := range search.Matches {
			if match.Line == absoluteLine {
				line = ui.InjectCharacterRangeBackground(line, match.StartCol, match.EndCol)
			}
		}
		return line
	}
}
