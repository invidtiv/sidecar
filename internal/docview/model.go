package docview

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/plugins/filebrowser"
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
	Result            filebrowser.PreviewResult
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

	loading  bool
	rendered bool
	result   filebrowser.PreviewResult

	renderWidth   int
	renderedLines []string
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
	m.modelID = modelID
	m.requestGeneration++
	m.epoch = epoch
	m.path = relPath
	m.targetLine = max(line, 0)
	m.scroll = 0
	m.loading = true
	m.rendered = line <= 0
	m.result = filebrowser.PreviewResult{}
	m.invalidateRender()

	generation := m.requestGeneration
	load := filebrowser.LoadPreview(rootDir, relPath, epoch)
	return func() tea.Msg {
		msg, ok := load().(filebrowser.PreviewLoadedMsg)
		if !ok {
			return LoadedMsg{
				ModelID: modelID, RequestGeneration: generation, Epoch: epoch, Path: relPath,
				Result: filebrowser.PreviewResult{Error: fmt.Errorf("unexpected preview load result")},
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
		m.scroll = m.targetLine - 1
	}
	m.clampScroll()
	return true
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

	lines := m.lines()
	rows := make([]string, m.height)
	for i := range rows {
		lineIndex := m.scroll + i
		line := ""
		if lineIndex < len(lines) {
			line = lines[lineIndex]
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

// Title returns the document's relative path.
func (m *Model) Title() string { return m.path }

func (m *Model) lines() []string {
	if m.loading {
		return []string{"Loading document…", m.path}
	}
	if m.result.Error != nil {
		return []string{"Document unavailable", m.path, m.result.Error.Error()}
	}
	if m.result.IsImage {
		return []string{"Image preview is not supported"}
	}
	if m.result.IsBinary {
		return []string{"Binary preview is not supported"}
	}
	if m.result.Content == "" {
		return []string{"Empty document"}
	}
	var lines []string
	if !m.rendered {
		if len(m.result.HighlightedLines) > 0 {
			lines = m.result.HighlightedLines
		} else {
			lines = m.result.Lines
		}
	} else {
		if m.renderWidth != m.width {
			m.renderedLines = m.renderer.RenderContent(m.result.Content, m.width)
			m.renderWidth = m.width
		}
		lines = m.renderedLines
	}
	if m.result.IsTruncated {
		return append([]string{"Preview truncated", ""}, lines...)
	}
	return lines
}

func (m *Model) invalidateRender() {
	m.renderWidth = -1
	m.renderedLines = nil
}

func (m *Model) maxScroll() int {
	return max(len(m.lines())-m.height, 0)
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
