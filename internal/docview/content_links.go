package docview

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/mouse"
)

// ContentLinkKinds is the set the Files plugin advertises. Workspace and
// global document panes scan the same kinds so a token clickable in Files is
// clickable here.
func ContentLinkKinds() contentlink.KindSet {
	return contentlink.NewKindSet(
		contentlink.KindFile,
		contentlink.KindIssue,
		contentlink.KindDiff,
		contentlink.KindResource,
		contentlink.KindURL,
		contentlink.KindInternal,
	)
}

// ContentLinkHit is one activatable span in the host's coordinate space.
type ContentLinkHit struct {
	Ref  contentlink.Ref
	Rect mouse.Rect
}

// ContentLinkFrame is ScanContentLinks' result: the body with recognized
// spans decorated, the hits those spans occupy, and any file/diff work the
// host still needs to resolve.
type ContentLinkFrame struct {
	Output  string
	Hits    []ContentLinkHit
	Pending []contentlink.Pending
}

// ContentLinksSafe reports whether this viewer is showing source (or rendered)
// document rows that map onto the frame. Placeholders, empty loads, and
// unsized models opt out.
func (m *Model) ContentLinksSafe() bool {
	if m == nil || m.width <= 0 || m.height <= 0 {
		return false
	}
	if _, ok := m.placeholder(); ok {
		return false
	}
	return m.contentWidth() > 0 && m.contentHeight() > 0
}

// ContentLinkRect is the source-text rectangle in the origin the host last
// bound: past the gutter, above the in-file search bar, and excluding the
// scrollbar. It is the same box text selection uses, so a click on a line
// number cannot activate a SHA that happens to look like a line number.
func (m *Model) ContentLinkRect() mouse.Rect {
	if !m.ContentLinksSafe() {
		return mouse.Rect{}
	}
	display := m.display()
	visible := len(display.rows) - m.scroll
	if visible < 0 {
		visible = 0
	}
	limit := m.contentHeight()
	if visible > limit {
		visible = limit
	}
	w := max(m.contentWidth()-display.gutterWidth, 0)
	if w <= 0 || visible <= 0 {
		return mouse.Rect{}
	}
	return mouse.Rect{
		X: m.originX + display.gutterWidth,
		Y: m.originY,
		W: w,
		H: visible,
	}
}

// ScanContentLinks recognizes tokens in an already-rendered View() body and
// optionally underlines them. Coordinates are visual columns of the source
// text, then offset by ContentLinkRect so a host can register them after
// chrome.
func (m *Model) ScanContentLinks(body string, opts contentlink.FrameOptions) ContentLinkFrame {
	if m == nil {
		return ContentLinkFrame{Output: body}
	}
	if opts.AllowedKinds == nil {
		opts.AllowedKinds = ContentLinkKinds()
	}
	// The viewer, not the host, knows whether these rows came out of
	// internal/markdown. Raw source rows are the document's own bytes, so an
	// OSC-8 sequence in them is authored content and keeps the terminal rule —
	// and so are the rows of a pane too narrow for Glamour, where "rendered"
	// mode is the plain-wrap fallback over that same source.
	opts.RendererOwned = m.rendered && markdown.RendersMarkdownAt(m.contentWidth())
	rect := m.ContentLinkRect()
	if rect.W <= 0 || rect.H <= 0 {
		return ContentLinkFrame{Output: body}
	}
	relX := rect.X - m.originX
	if relX < 0 {
		relX = 0
	}
	lines := strings.Split(body, "\n")
	var frame ContentLinkFrame
	for row := 0; row < rect.H && row < len(lines); row++ {
		segment := ansi.Cut(lines[row], relX, relX+rect.W)
		result := contentlink.ScanFrame(segment, opts)
		for _, span := range result.Spans {
			if span.Kind == "" || !contentlink.Activatable(span.Kind) {
				continue
			}
			w := span.EndCol - span.StartCol + 1
			if w < 1 {
				continue
			}
			frame.Hits = append(frame.Hits, ContentLinkHit{
				Ref: span.Ref(),
				Rect: mouse.Rect{
					X: rect.X + span.StartCol,
					Y: rect.Y + row,
					W: w,
					H: 1,
				},
			})
		}
		frame.Pending = append(frame.Pending, result.Pending...)
		prefix := ansi.Cut(lines[row], 0, relX)
		suffix := ansi.Cut(lines[row], relX+rect.W, ansi.StringWidth(lines[row]))
		lines[row] = prefix + result.Output + suffix
	}
	frame.Output = strings.Join(lines, "\n")
	return frame
}
