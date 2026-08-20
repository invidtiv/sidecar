package filebrowser

import (
	"github.com/marcus/sidecar/internal/docview"
)

// highlightMarkdownLineMatches injects search highlighting into a
// Glamour-rendered ANSI line. The injection itself lives in
// internal/docview — this plugin's preview renderer is not a docview.Model,
// so it keeps its own search state and row painting, but it must not keep a
// second copy of the ANSI walker. See the Phase 5 note in
// docs/plans/active/doc-pane-search-and-edit.md.
func (p *Plugin) highlightMarkdownLineMatches(lineNo int) string {
	if lineNo >= len(p.markdownRendered) {
		return ""
	}
	ansiLine := p.markdownRendered[lineNo]

	var ranges []docview.MatchRange
	for i, m := range p.contentSearchMatches {
		if m.LineNo == lineNo {
			ranges = append(ranges, docview.MatchRange{
				Index: i,
				Start: m.StartCol,
				End:   m.EndCol,
			})
		}
	}

	if len(ranges) == 0 {
		return ansiLine
	}

	return docview.InjectHighlights(ansiLine, ranges, p.contentSearchCursor)
}
