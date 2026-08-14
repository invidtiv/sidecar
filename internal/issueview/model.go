package issueview

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
	IssueID           string
	Data              *Data
	Error             error
}

// GetEpoch allows callers to apply the normal plugin epoch checks if desired.
func (m LoadedMsg) GetEpoch() uint64 { return m.Epoch }

// Model is one td issue in one content box.
type Model struct {
	renderer *markdown.Renderer

	modelID           int
	requestGeneration uint64
	epoch             uint64
	issueID           string

	width  int
	height int
	scroll int

	loading bool
	data    *Data
	err     error

	renderWidth   int
	renderedLines []string
}

// New creates an empty issue viewer. A nil renderer uses the default markdown
// renderer.
func New(renderer *markdown.Renderer) *Model {
	if renderer == nil {
		renderer, _ = markdown.NewRenderer()
	}
	return &Model{renderer: renderer, renderWidth: -1}
}

// Load retargets the model at issueID and returns a command that fetches it.
// Only the issueview-owned LoadedMsg is broadcast.
func (m *Model) Load(modelID int, workDir, issueID string, epoch uint64) tea.Cmd {
	m.modelID = modelID
	m.requestGeneration++
	m.epoch = epoch
	m.issueID = issueID
	m.scroll = 0
	m.loading = true
	m.data = nil
	m.err = nil
	m.invalidateRender()

	generation := m.requestGeneration
	fetch := Fetch(workDir, issueID)
	return func() tea.Msg {
		msg, _ := fetch().(FetchedMsg)
		return LoadedMsg{
			ModelID: modelID, RequestGeneration: generation, Epoch: epoch,
			IssueID: issueID, Data: msg.Data, Error: msg.Error,
		}
	}
}

// SetResult applies msg if it belongs to the current load. It returns false
// without changing the model for stale model, request, epoch, or issue results.
func (m *Model) SetResult(msg LoadedMsg) bool {
	if msg.ModelID != m.modelID ||
		msg.RequestGeneration != m.requestGeneration ||
		msg.Epoch != m.epoch ||
		msg.IssueID != m.issueID {
		return false
	}
	m.loading = false
	m.data = msg.Data
	m.err = msg.Error
	m.invalidateRender()
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

// HandleKey applies issue scrolling keys. It answers false for a key it does
// not own, which is the host's cue that the key is still unspoken for — not its
// cue to pass it to whatever is behind the pane.
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

// Scroll moves the viewport by delta rows and clamps it to the issue.
func (m *Model) Scroll(delta int) {
	m.scroll += delta
	m.clampScroll()
}

// IssueID returns the issue this model is targeting.
func (m *Model) IssueID() string { return m.issueID }

// Title returns the issue's headline, or its ID before data arrives.
func (m *Model) Title() string {
	if m.data == nil {
		return m.issueID
	}
	return Heading(m.data)
}

// Loading reports whether a fetch is outstanding.
func (m *Model) Loading() bool { return m.loading }

func (m *Model) lines() []string {
	if m.loading {
		return []string{"Loading issue…", m.issueID}
	}
	if m.err != nil {
		return []string{"Issue unavailable", m.issueID, m.err.Error()}
	}
	if m.data == nil {
		return []string{"No issue"}
	}
	if m.renderWidth != m.width {
		m.renderedLines = m.build()
		m.renderWidth = m.width
	}
	return m.renderedLines
}

func (m *Model) build() []string {
	var lines []string
	appendLine := func(s string) {
		if s != "" {
			lines = append(lines, s)
		}
	}
	appendLine(Heading(m.data))
	appendLine(StatusLine(m.data))
	appendLine(ParentLine(m.data))
	appendLine(LabelsLine(m.data))
	if desc := Description(m.renderer, m.data, m.width); desc != "" {
		lines = append(lines, "")
		lines = append(lines, strings.Split(desc, "\n")...)
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
