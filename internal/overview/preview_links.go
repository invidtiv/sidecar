package overview

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	previewDocRegionKind = "global-preview-doc"
	previewDocCloseKind  = "global-preview-doc-close"
	previewDocModelID    = 1
	previewDocMinWidth   = markdown.MinWidthForMarkdown
	previewTermMinWidth  = 12
	previewDocSplitRatio = 50
)

// previewDoc is the terminal-adjacent file preview on the global surface.
// It reuses docview; it is not the issue-preview modal.
type previewDoc struct {
	view    *docview.Model
	root    string
	path    string
	surface string
	focused bool
}

type previewDocLoadedMsg struct {
	docview.LoadedMsg
}

func (m *Model) previewResolveRoot() string {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return ""
	}
	return workspace.Path
}

func (m *Model) previewLinkSpans(line string) []terminallink.Span {
	root := m.previewResolveRoot()
	if root == "" {
		return terminallink.Scan(line, nil)
	}
	return terminallink.Scan(line, func(raw string) (string, terminallink.Extra, bool) {
		display, _, ok := terminallink.ResolveFile(root, raw)
		if !ok {
			return "", terminallink.Extra{}, false
		}
		return display, terminallink.Extra{Raw: raw}, true
	})
}

func (m *Model) decoratePreviewLine(line string, _ int) string {
	line = terminallink.StripOSC8(line)
	return terminallink.Decorate(line, m.previewLinkSpans(line))
}

func (m *Model) previewLinkAt(action mouse.MouseAction) (terminallink.Span, bool) {
	geometry, ok := m.previewGeometry()
	if !ok {
		return terminallink.Span{}, false
	}
	buffer := m.previewBuffer()
	cell, ok := tty.CellAt(geometry, buffer, action.X, action.Y)
	if !ok {
		return terminallink.Span{}, false
	}
	line, ok := tty.LineTextAt(buffer, cell.Line)
	if !ok {
		return terminallink.Span{}, false
	}
	line = ui.ExpandTabs(line, tty.DefaultTabWidth)
	for _, span := range m.previewLinkSpans(line) {
		if span.Kind != terminallink.KindURL && span.Kind != terminallink.KindFile {
			continue
		}
		if cell.Col >= span.StartCol && cell.Col <= span.EndCol {
			return span, true
		}
	}
	return terminallink.Span{}, false
}

func (m *Model) activatePreviewLinkAt(action mouse.MouseAction, modified bool) (tea.Cmd, bool) {
	if modified {
		return nil, false
	}
	span, ok := m.previewLinkAt(action)
	if !ok {
		return nil, false
	}
	switch span.Kind {
	case terminallink.KindURL:
		m.clearPreviewSelection()
		return terminallink.OpenHTTP(span.Value), true
	case terminallink.KindFile:
		cmd := m.openPreviewDoc(span)
		if cmd == nil {
			return nil, false
		}
		m.clearPreviewSelection()
		return cmd, true
	default:
		return nil, false
	}
}

func (m *Model) openPreviewDoc(span terminallink.Span) tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return nil
	}
	root := workspace.Path
	raw := span.Extra.Raw
	if raw == "" {
		raw = span.Value
	}
	display, abs, ok := terminallink.ResolveFile(root, raw)
	if !ok {
		return nil
	}
	box, hasBox := m.previewBox()
	if !hasBox || !m.previewDocFits(box) {
		return appmsg.ShowToast("Document pane needs a wider window; terminal left unchanged", 3*time.Second)
	}
	file, err := openPreviewFile(root, display, abs)
	if err != nil {
		return nil
	}
	wasInteractive := m.PreviewInteractive()
	if m.preview.doc == nil {
		m.preview.doc = &previewDoc{view: docview.New(nil)}
	}
	m.preview.doc.root = root
	m.preview.doc.path = display
	m.preview.doc.surface = workspace.ID
	m.preview.doc.focused = true
	load := m.preview.doc.view.LoadFile(previewDocModelID, file, display, span.Extra.Line, uint64(m.preview.generation))
	applyPreviewDocRenderMode(m.preview.doc.view, display, span.Extra.Line)
	m.preview.focus = focusPreview
	var cmds []tea.Cmd
	if wasInteractive {
		cmds = append(cmds, m.exitPreviewInteractive())
	}
	cmds = append(cmds, wrapPreviewDocLoad(load), m.syncTerminalGeometry())
	return tea.Batch(cmds...)
}

func openPreviewFile(root, display, abs string) (*os.File, error) {
	if display != "" && !filepath.IsAbs(filepath.FromSlash(display)) {
		return terminallink.OpenRegular(filepath.Join(root, filepath.FromSlash(display)))
	}
	return terminallink.OpenRegular(abs)
}

func wrapPreviewDocLoad(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if loaded, ok := msg.(docview.LoadedMsg); ok {
			return previewDocLoadedMsg{LoadedMsg: loaded}
		}
		return msg
	}
}

func (m *Model) applyPreviewDocLoaded(msg previewDocLoadedMsg) {
	if m.preview.doc == nil || m.preview.doc.view == nil {
		return
	}
	m.preview.doc.view.SetResult(msg.LoadedMsg)
}

func applyPreviewDocRenderMode(view *docview.Model, path string, line int) {
	if view == nil {
		return
	}
	if !terminallink.Markdown(path) || line > 0 {
		view.SetRendered(false)
		return
	}
	view.SetRendered(true)
}

func (m *Model) closePreviewDoc() tea.Cmd {
	if m.preview.doc == nil {
		return nil
	}
	m.preview.doc = nil
	return m.syncTerminalGeometry()
}

func (m *Model) previewDocFits(box termpreview.Box) bool {
	return box.W >= previewTermMinWidth+previewDocDividerWidth+previewDocMinWidth && box.H >= 3
}

const previewDocDividerWidth = 1

func (m *Model) previewDocLayout(box termpreview.Box) (termBox, docBox termpreview.Box, split bool) {
	if m.preview.doc == nil || !m.previewDocFits(box) {
		return box, termpreview.Box{}, false
	}
	available := box.W - previewDocDividerWidth
	termW := available * previewDocSplitRatio / 100
	if termW < previewTermMinWidth {
		termW = previewTermMinWidth
	}
	docW := available - termW
	if docW < previewDocMinWidth {
		docW = previewDocMinWidth
		termW = available - docW
	}
	if termW < previewTermMinWidth {
		return box, termpreview.Box{}, false
	}
	termBox = termpreview.Box{X: box.X, Y: box.Y, W: termW, H: box.H}
	docBox = termpreview.Box{X: box.X + termW + previewDocDividerWidth, Y: box.Y, W: docW, H: box.H}
	return termBox, docBox, true
}

func (m *Model) previewTerminalBox() (termpreview.Box, bool) {
	box, ok := m.previewBox()
	if !ok {
		return termpreview.Box{}, false
	}
	term, _, _ := m.previewDocLayout(box)
	return term, true
}

func (m *Model) registerPreviewDocRegions(box termpreview.Box) {
	if m.preview.doc == nil {
		return
	}
	_, docBox, split := m.previewDocLayout(box)
	if !split {
		return
	}
	m.workspacesMouse.HitMap.AddRect(previewDocRegionKind, docBox.X, docBox.Y, docBox.W, docBox.H, previewDocRegionKind)
	chips := previewDocHeaderChips(m.preview.doc, docBox.W)
	for index, chip := range termpreview.LayoutChips(chips, docBox.W, 0) {
		if index == len(chips)-1 && chip.Drawn {
			m.workspacesMouse.HitMap.AddRect(previewDocCloseKind, docBox.X+chip.Col, docBox.Y, chip.Width, 1, previewDocCloseKind)
		}
	}
}

func (m *Model) handlePreviewDocMouse(action mouse.MouseAction) tea.Cmd {
	kind, _ := regionKind(action.Region)
	if kind == previewDocCloseKind {
		if action.Type == mouse.ActionClick || action.Type == mouse.ActionDoubleClick {
			return m.closePreviewDoc()
		}
		return nil
	}
	if kind != previewDocRegionKind || m.preview.doc == nil {
		return nil
	}
	switch action.Type {
	case mouse.ActionClick, mouse.ActionDoubleClick:
		m.preview.doc.focused = true
		m.preview.focus = focusPreview
		return nil
	case mouse.ActionScrollUp:
		m.preview.doc.view.Scroll(action.Delta)
	case mouse.ActionScrollDown:
		m.preview.doc.view.Scroll(action.Delta)
	}
	return nil
}

func (m *Model) previewDocKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if m.preview.doc == nil || m.PreviewInteractive() {
		return false, nil
	}
	key := msg.String()
	if key == "q" || key == "esc" {
		return true, m.closePreviewDoc()
	}
	if m.preview.doc.focused {
		switch key {
		case "r":
			m.preview.doc.view.ToggleRenderMode()
			return true, nil
		case "enter", interactiveEnterKeyAlt:
			m.preview.doc.focused = false
			return true, m.enterPreviewInteractive()
		}
		if m.preview.doc.view.HandleKey(msg) {
			return true, nil
		}
	}
	return false, nil
}

func previewDocHeaderChips(doc *previewDoc, width int) []string {
	pathBudget := max(width/2, 8)
	path := doc.view.Title()
	if ansiWidth := lipgloss.Width(path); ansiWidth > pathBudget {
		path = termpreview.TruncateANSI(path, pathBudget)
	}
	pathStyle := styles.BarChip
	if doc.focused {
		pathStyle = styles.BarChipActive
	}
	mode := "Rendered"
	if !doc.view.Rendered() {
		mode = "Raw"
	}
	return []string{
		styles.RenderPillWithStyle(path, pathStyle, nil),
		styles.RenderPillWithStyle(mode, styles.BarChip, nil),
		styles.RenderPillWithStyle("×", styles.BarChip, nil),
	}
}

func (m *Model) renderPreviewDoc(doc *previewDoc, box termpreview.Box) string {
	contentHeight := max(box.H-termpreview.HeaderRows, 0)
	doc.view.SetSize(box.W, contentHeight)
	action := "raw"
	if !doc.view.Rendered() {
		action = "render"
	}
	header := termpreview.HeaderRow(previewDocHeaderChips(doc, box.W), styles.Muted.Render("q close · r "+action), box.W, 0, termpreview.TruncateANSI)
	body := doc.view.View()
	if contentHeight <= 0 {
		return header
	}
	return header + "\n" + body
}

func renderPreviewDocDivider(height int, focused bool) string {
	style := lipgloss.NewStyle().Foreground(styles.BorderNormal)
	if focused {
		style = lipgloss.NewStyle().Foreground(styles.BorderActive)
	}
	if height <= 0 {
		return ""
	}
	return style.Render(strings.TrimSuffix(strings.Repeat("│\n", height), "\n"))
}

func joinPreviewDoc(term, document string, height int, focused bool) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, term, renderPreviewDocDivider(height, focused), document)
}
