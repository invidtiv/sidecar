package notes

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/ui"
)

type noteSearchMatch struct {
	Line     int
	StartCol int
	EndCol   int
}

func (p *Plugin) startNoteSearch() {
	p.noteSearchMode = true
	p.noteSearchCommitted = false
	p.noteSearchQuery = ""
	p.noteSearchMatches = nil
	p.noteSearchCursor = 0
}

func (p *Plugin) clearNoteSearch() {
	p.noteSearchMode = false
	p.noteSearchCommitted = false
	p.noteSearchQuery = ""
	p.noteSearchMatches = nil
	p.noteSearchCursor = 0
}

func (p *Plugin) handleNoteSearchKey(msg tea.KeyPressMsg) (pluginResult, tea.Cmd) {
	key := msg.String()
	text := ui.PrintableKeyText(msg)

	if key == "esc" {
		p.clearNoteSearch()
		return p, nil
	}

	if !p.noteSearchCommitted {
		switch key {
		case "enter":
			if p.noteSearchQuery != "" {
				p.noteSearchCommitted = true
			}
		case "backspace":
			if p.noteSearchQuery != "" {
				runes := []rune(p.noteSearchQuery)
				p.noteSearchQuery = string(runes[:len(runes)-1])
				p.updateNoteSearchMatches()
			}
		default:
			if text != "" {
				p.noteSearchQuery += text
				p.updateNoteSearchMatches()
			}
		}
		return p, nil
	}

	switch key {
	case "n":
		p.cycleNoteSearch(1)
	case "N":
		p.cycleNoteSearch(-1)
	case "enter":
		p.noteSearchMode = false
		p.noteSearchCommitted = false
	case "j", "down", "ctrl+n":
		p.ensureViewSurface()
		if p.previewCursorLine < len(p.viewSurface.Lines)-1 {
			p.previewCursorLine++
		}
		p.ensurePreviewCursorVisible()
	case "k", "up", "ctrl+p":
		if p.previewCursorLine > 0 {
			p.previewCursorLine--
		}
		p.ensurePreviewCursorVisible()
	}
	return p, nil
}

func (p *Plugin) cycleNoteSearch(delta int) {
	if len(p.noteSearchMatches) == 0 {
		return
	}
	n := len(p.noteSearchMatches)
	p.noteSearchCursor = (p.noteSearchCursor + delta + n) % n
	p.scrollToNoteSearchMatch()
}

func (p *Plugin) updateNoteSearchMatches() {
	p.noteSearchMatches = nil
	p.noteSearchCursor = 0
	if p.noteSearchQuery == "" {
		return
	}
	p.ensureViewSurface()
	query := strings.ToLower(p.noteSearchQuery)
	for lineNo, line := range p.viewSurface.Lines {
		plain := strings.ToLower(ansi.Strip(line))
		start := 0
		for {
			idx := strings.Index(plain[start:], query)
			if idx < 0 {
				break
			}
			abs := start + idx
			p.noteSearchMatches = append(p.noteSearchMatches, noteSearchMatch{
				Line:     lineNo,
				StartCol: abs,
				EndCol:   abs + len(p.noteSearchQuery),
			})
			start = abs + 1
		}
	}
	if len(p.noteSearchMatches) > 0 {
		p.scrollToNoteSearchMatch()
	}
}

func (p *Plugin) scrollToNoteSearchMatch() {
	if p.noteSearchCursor < 0 || p.noteSearchCursor >= len(p.noteSearchMatches) {
		return
	}
	match := p.noteSearchMatches[p.noteSearchCursor]
	p.previewCursorLine = match.Line
	p.ensurePreviewCursorVisible()
}

func (p *Plugin) highlightNoteSearchLine(lineNo int, line string) string {
	if !p.noteSearchMode || p.noteSearchQuery == "" {
		return line
	}
	var ranges []docview.MatchRange
	for i, m := range p.noteSearchMatches {
		if m.Line == lineNo {
			ranges = append(ranges, docview.MatchRange{Index: i, Start: m.StartCol, End: m.EndCol})
		}
	}
	if len(ranges) == 0 {
		return line
	}
	return docview.InjectHighlights(line, ranges, p.noteSearchCursor)
}

func (p *Plugin) renderNoteSearchPrompt() string {
	count := ""
	if p.noteSearchQuery != "" {
		if len(p.noteSearchMatches) == 0 {
			count = " 0/0"
		} else {
			count = " " + strconv.Itoa(p.noteSearchCursor+1) + "/" + strconv.Itoa(len(p.noteSearchMatches))
		}
	}
	cursor := ""
	if p.noteSearchMode && !p.noteSearchCommitted {
		cursor = "_"
	}
	return "/" + p.noteSearchQuery + cursor + count
}
