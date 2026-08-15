package docview

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/ui"
)

const tabStopWidth = 8

// LoadedMsg is the result of a Model load. Its identity fields ensure a result
// can only be applied to the model, request, and plugin epoch that issued it.
type LoadedMsg struct {
	ModelID           int
	RequestGeneration uint64
	Epoch             uint64
	Path              string
	Result            filepreview.PreviewResult
}

// GetEpoch allows callers to apply the normal plugin epoch checks if desired.
func (m LoadedMsg) GetEpoch() uint64 { return m.Epoch }

// Model is one markdown document in one content box.
type Model struct {
	renderer *markdown.Renderer

	modelID           int
	requestGeneration uint64
	epoch             uint64
	path              string
	targetLine        int

	width  int
	height int
	scroll int

	pendingScroll    int
	hasPendingScroll bool

	loading  bool
	rendered bool
	wrap     bool
	result   filepreview.PreviewResult

	renderWidth   int
	renderedLines []string

	// Laying out a document is O(lines): every source line is measured, given a
	// gutter cell, and possibly wrapped. View, maxScroll and displayRowForLine
	// all need the result, and clampScroll runs on every scroll key, so a large
	// file would pay that walk several times per keystroke. Hold the last pass
	// and rebuild it only when something it depends on moves.
	layout       displayRows
	layoutKey    layoutKey
	layoutValid  bool
	contentGen   uint64
	layoutBuilds int // test-visible count of full layout passes
}

// New creates an empty document viewer. A nil renderer uses the default
// markdown renderer.
func New(renderer *markdown.Renderer) *Model {
	if renderer == nil {
		renderer, _ = markdown.NewRenderer()
	}
	return &Model{renderer: renderer, rendered: true, renderWidth: -1}
}

// Load retargets the model and returns a command that wraps the existing file
// browser loader. Only the docview-owned LoadedMsg is broadcast.
func (m *Model) Load(modelID int, rootDir, relPath string, line int, epoch uint64) tea.Cmd {
	return m.load(modelID, relPath, line, epoch, filepreview.LoadPreview(rootDir, relPath, epoch))
}

// LoadFile retargets the model from an already-open file. The returned command
// owns and closes file after reading it.
func (m *Model) LoadFile(modelID int, file *os.File, relPath string, line int, epoch uint64) tea.Cmd {
	return m.load(modelID, relPath, line, epoch, filepreview.LoadPreviewFile(file, relPath, epoch))
}

func (m *Model) load(modelID int, relPath string, line int, epoch uint64, load tea.Cmd) tea.Cmd {
	m.modelID = modelID
	m.requestGeneration++
	m.epoch = epoch
	m.path = relPath
	m.targetLine = max(line, 0)
	m.scroll = 0
	m.loading = true
	m.rendered = line <= 0
	m.result = filepreview.PreviewResult{}
	m.invalidateRender()

	generation := m.requestGeneration
	return func() tea.Msg {
		msg, ok := load().(filepreview.PreviewLoadedMsg)
		if !ok {
			return LoadedMsg{
				ModelID: modelID, RequestGeneration: generation, Epoch: epoch, Path: relPath,
				Result: filepreview.PreviewResult{Error: fmt.Errorf("unexpected preview load result")},
			}
		}
		return LoadedMsg{
			ModelID: modelID, RequestGeneration: generation, Epoch: msg.Epoch,
			Path: msg.Path, Result: msg.Result,
		}
	}
}

// SetResult applies msg if it belongs to the current load. It returns false
// without changing the model for stale model, request, epoch, or path results.
func (m *Model) SetResult(msg LoadedMsg) bool {
	if msg.ModelID != m.modelID ||
		msg.RequestGeneration != m.requestGeneration ||
		msg.Epoch != m.epoch ||
		msg.Path != m.path {
		return false
	}

	m.loading = false
	m.result = msg.Result
	m.invalidateRender()
	if m.targetLine > 0 && msg.Result.Error == nil {
		m.rendered = false
		m.scroll = m.displayRowForLine(m.targetLine)
	} else if m.hasPendingScroll {
		m.scroll = m.pendingScroll
	}
	m.hasPendingScroll = false
	m.clampScroll()
	return true
}

// Arm shows the loading placeholder for a restored tab without issuing a load.
func (m *Model) Arm(modelID int, relPath string, epoch uint64) {
	m.modelID = modelID
	m.epoch = epoch
	m.path = relPath
	m.loading = true
}

// NeedsLoad reports whether this model has never been asked to Load.
func (m *Model) NeedsLoad() bool { return m.requestGeneration == 0 }

// ScrollOffset is the current (or still-pending restore) viewport offset.
func (m *Model) ScrollOffset() int {
	if m.hasPendingScroll {
		return m.pendingScroll
	}
	return m.scroll
}

// SetPendingScroll remembers an offset to apply after the next successful load.
func (m *Model) SetPendingScroll(offset int) {
	m.pendingScroll = max(offset, 0)
	m.hasPendingScroll = true
}

// ApplyLine jumps to line (1-based) and forces raw mode so the line is visible.
func (m *Model) ApplyLine(line int) {
	if line <= 0 {
		return
	}
	m.targetLine = line
	m.rendered = false
	m.hasPendingScroll = false
	if !m.loading {
		m.scroll = m.displayRowForLine(line)
		m.clampScroll()
	}
}

// SetSize sets the content box dimensions. A width change invalidates rendered
// markdown because wrapping depends on it.
func (m *Model) SetSize(width, height int) {
	width = max(width, 0)
	height = max(height, 0)
	if width != m.width {
		m.invalidateRender()
	}
	m.width = width
	m.height = height
	m.clampScroll()
}

// View returns exactly the configured number of rows, each no wider than the
// configured width in terminal cells.
func (m *Model) View() string {
	if m.height <= 0 {
		return ""
	}

	display := m.display()
	rows := make([]string, m.height)
	for i := range rows {
		lineIndex := m.scroll + i
		line := ""
		if lineIndex < len(display.rows) {
			line = display.rows[lineIndex]
		}
		rows[i] = fitLine(line, m.width)
	}
	return strings.Join(rows, "\n")
}

// HandleKey applies document scrolling keys.
func (m *Model) HandleKey(k tea.KeyMsg) bool {
	switch k.String() {
	case "j", "down":
		m.Scroll(1)
	case "k", "up":
		m.Scroll(-1)
	case "ctrl+d", "pgdown":
		m.Scroll(max(m.height/2, 1))
	case "ctrl+u", "pgup":
		m.Scroll(-max(m.height/2, 1))
	case "g", "home":
		m.scroll = 0
	case "G", "end":
		m.scroll = m.maxScroll()
	default:
		return false
	}
	return true
}

// Scroll moves the viewport by delta rows and clamps it to the document.
func (m *Model) Scroll(delta int) {
	m.scroll += delta
	m.clampScroll()
}

// ToggleRenderMode switches between rendered markdown and raw highlighted
// source. Each mode keeps the current offset where possible.
func (m *Model) ToggleRenderMode() {
	m.rendered = !m.rendered
	m.clampScroll()
}

// Rendered reports whether markdown is shown rendered rather than raw.
func (m *Model) Rendered() bool { return m.rendered }

// SetRendered restores the persisted display mode.
func (m *Model) SetRendered(rendered bool) {
	m.rendered = rendered
	m.clampScroll()
}

// Wrap reports whether long lines wrap instead of truncating.
func (m *Model) Wrap() bool { return m.wrap }

// SetWrap restores the persisted wrap flag.
func (m *Model) SetWrap(wrap bool) {
	m.wrap = wrap
	m.clampScroll()
}

// ToggleWrap flips line wrapping.
func (m *Model) ToggleWrap() {
	m.wrap = !m.wrap
	m.clampScroll()
}

// Title returns the document's relative path.
func (m *Model) Title() string { return m.path }

// displayRows is one render pass worth of visual rows plus the mapping back to
// source lines. starts[n-1] is the row index where source line n begins, so
// scrolling and ApplyLine stay expressible in source-line terms even when wrap
// turns one source line into several rows.
type displayRows struct {
	rows   []string
	starts []int
}

// docContent is the document's text before layout. banner counts leading rows
// that are viewer chrome rather than source lines, and numbered reports whether
// the remaining lines map 1:1 onto source lines.
type docContent struct {
	lines    []string
	banner   int
	numbered bool
}

// layoutKey names everything a laid-out document depends on. Width changes the
// gutter and the wrap points, wrap and rendered change what the lines are, and
// contentGen moves whenever the document itself is replaced.
type layoutKey struct {
	width      int
	wrap       bool
	rendered   bool
	contentGen uint64
}

func (m *Model) currentLayoutKey() layoutKey {
	return layoutKey{width: m.width, wrap: m.wrap, rendered: m.rendered, contentGen: m.contentGen}
}

func (m *Model) display() displayRows {
	// A placeholder is a handful of lines that depend on transient state the
	// cache key does not track - whether a load is in flight, which path was
	// armed, what the error said. It is cheap enough to lay out every time, and
	// doing so keeps the cache honest about the one thing it is for: documents.
	if lines, ok := m.placeholder(); ok {
		return m.layOutContent(docContent{lines: lines})
	}

	key := m.currentLayoutKey()
	if m.layoutValid && m.layoutKey == key {
		return m.layout
	}
	rows := m.layOut()
	m.layout = rows
	m.layoutKey = key
	m.layoutValid = true
	return rows
}

func (m *Model) layOut() displayRows {
	m.layoutBuilds++
	return m.layOutContent(m.content())
}

func (m *Model) layOutContent(content docContent) displayRows {
	gutter := Gutter{}
	if content.numbered {
		gutter = NewGutterForWidth(len(content.lines)-content.banner, m.width)
	}
	textWidth := m.width - gutter.Width()

	out := displayRows{rows: make([]string, 0, len(content.lines))}
	for i, line := range content.lines {
		first, cont := gutter.Blank(), gutter.Blank()
		if i >= content.banner {
			first = gutter.Number(i - content.banner + 1)
			out.starts = append(out.starts, len(out.rows))
		}
		if !m.wrap || textWidth <= 0 {
			out.rows = append(out.rows, first+line)
			continue
		}
		for wi, segment := range wrapLine(line, textWidth) {
			prefix := cont
			if wi == 0 {
				prefix = first
			}
			out.rows = append(out.rows, prefix+segment)
		}
	}
	return out
}

// displayRowForLine maps a 1-based source line to the row that shows it.
func (m *Model) displayRowForLine(line int) int {
	if line <= 1 {
		return 0
	}
	starts := m.display().starts
	if idx := line - 1; idx < len(starts) {
		return starts[idx]
	}
	if len(starts) > 0 {
		return starts[len(starts)-1]
	}
	return line - 1
}

func (m *Model) content() docContent {
	if lines, ok := m.placeholder(); ok {
		return docContent{lines: lines}
	}
	var lines []string
	numbered := false
	if !m.rendered {
		// Raw source: rows and source lines are the same thing.
		numbered = true
		if len(m.result.HighlightedLines) > 0 {
			lines = m.result.HighlightedLines
		} else {
			lines = m.result.Lines
		}
	} else {
		// Glamour output has no 1:1 mapping back to source lines, so numbering
		// it would be a lie.
		if m.renderWidth != m.width {
			m.renderedLines = m.renderer.RenderContent(m.result.Content, m.width)
			m.renderWidth = m.width
		}
		lines = m.renderedLines
	}
	if m.result.IsTruncated {
		return docContent{
			lines:    append([]string{"Preview truncated", ""}, lines...),
			banner:   2,
			numbered: numbered,
		}
	}
	return docContent{lines: lines, numbered: numbered}
}

// placeholder returns the stand-in text shown when there is no document body to
// number.
func (m *Model) placeholder() ([]string, bool) {
	if m.loading {
		return []string{"Loading document…", m.path}, true
	}
	if m.result.Error != nil {
		return []string{"Document unavailable", m.path, m.result.Error.Error()}, true
	}
	if m.result.IsImage {
		return []string{"Image preview is not supported"}, true
	}
	if m.result.IsBinary {
		return []string{"Binary preview is not supported"}, true
	}
	if m.result.Content == "" {
		return []string{"Empty document"}, true
	}
	return nil, false
}

func (m *Model) invalidateRender() {
	m.renderWidth = -1
	m.renderedLines = nil
	m.contentGen++
	m.layoutValid = false
	m.layout = displayRows{}
}

func (m *Model) maxScroll() int {
	return max(len(m.display().rows)-m.height, 0)
}

func (m *Model) clampScroll() {
	m.scroll = min(max(m.scroll, 0), m.maxScroll())
}

func fitLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	// A terminal expands tabs based on their current visual column, while ANSI
	// width helpers count them as zero-width control characters. Normalize them
	// first so the subsequent clamp describes what the terminal will display.
	line = ui.ExpandTabs(line, tabStopWidth)
	line = ansi.Truncate(line, width, "")
	if padding := width - ansi.StringWidth(line); padding > 0 {
		line += strings.Repeat(" ", padding)
	}
	return line
}
