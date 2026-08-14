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
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	previewDocRegionKind     = "global-preview-doc"
	previewDocTabKind        = "global-preview-doc-tab"
	previewSecondaryMinWidth = markdown.MinWidthForMarkdown
	previewTermMinWidth      = 12
)

func isPreviewDocRegion(kind string) bool {
	return kind == previewDocRegionKind || kind == previewDocTabKind
}

// previewDocTabHit is the tab stored on the document header region.
type previewDocTabHit int

// previewDoc is the terminal-adjacent file preview on the global surface.
// It reuses docview.Tabs; it is not the issue-preview modal.
type previewDoc struct {
	tabs    docview.Tabs
	root    string
	surface string
	focused bool
	nextID  int
	epoch   uint64
}

func (d *previewDoc) view() *docview.Model {
	if d == nil {
		return nil
	}
	return d.tabs.ActiveView()
}

func (d *previewDoc) allocID() int {
	d.nextID++
	return d.nextID
}

type previewDocLoadedMsg struct {
	docview.LoadedMsg
	WorkspaceID string
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
	return terminallink.Decorate(line, m.decoratedPreviewSpans(line))
}

// decoratedPreviewSpans keeps exactly the kinds this surface activates.
func (m *Model) decoratedPreviewSpans(line string) []terminallink.Span {
	spans := m.previewLinkSpans(line)
	bound := make([]terminallink.Span, 0, len(spans))
	for _, span := range spans {
		if span.Kind == terminallink.KindURL || span.Kind == terminallink.KindFile || span.Kind == terminallink.KindIssue {
			bound = append(bound, span)
		}
	}
	return bound
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
		if span.Kind != terminallink.KindURL && span.Kind != terminallink.KindFile && span.Kind != terminallink.KindIssue {
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
	case terminallink.KindIssue:
		cmd := m.openPreviewIssue(span.Value)
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
	file, err := openPreviewFile(root, display, abs)
	if err != nil {
		return nil
	}
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	leafID, refusal := m.ensurePreviewPane(panelayout.Document, "Document")
	if refusal != nil {
		return refusal
	}
	if leafID == 0 {
		return nil
	}
	wasInteractive := m.PreviewInteractive()
	if m.preview.doc == nil || m.preview.doc.surface != workspace.ID {
		m.preview.doc = &previewDoc{epoch: m.nextPreviewContentEpoch()}
	}
	m.preview.doc.root = root
	m.preview.doc.surface = workspace.ID
	m.focusPreviewPane(panelayout.Document)

	var load tea.Cmd
	if idx := m.preview.doc.tabs.IndexOf(display); idx >= 0 {
		load = m.selectPreviewDocTab(idx, span.Extra.Line, file)
		file = nil
	} else {
		viewer := docview.New(nil)
		load = viewer.LoadFile(m.preview.doc.allocID(), file, display, span.Extra.Line, m.preview.doc.epoch)
		file = nil
		applyPreviewDocRenderMode(viewer, display, span.Extra.Line)
		m.preview.doc.tabs.Append(viewer)
	}

	var cmds []tea.Cmd
	if wasInteractive {
		cmds = append(cmds, m.exitPreviewInteractive())
	}
	cmds = append(cmds, wrapPreviewDocLoad(load, workspace.ID), m.syncTerminalGeometry())
	return tea.Batch(cmds...)
}

func (m *Model) selectPreviewDocTab(idx, line int, file *os.File) tea.Cmd {
	doc := m.preview.doc
	if doc == nil {
		if file != nil {
			_ = file.Close()
		}
		return nil
	}
	doc.tabs.Select(idx)
	view := doc.view()
	if view == nil {
		if file != nil {
			_ = file.Close()
		}
		return nil
	}
	if !view.NeedsLoad() {
		if line > 0 {
			view.ApplyLine(line)
		}
		if file != nil {
			_ = file.Close()
		}
		return nil
	}
	rel := view.Title()
	rendered := view.Rendered()
	wrap := view.Wrap()
	var cmd tea.Cmd
	if file != nil {
		cmd = view.LoadFile(doc.allocID(), file, rel, line, doc.epoch)
	} else {
		cmd = view.Load(doc.allocID(), doc.root, rel, line, doc.epoch)
	}
	if line > 0 {
		applyPreviewDocRenderMode(view, rel, line)
	} else {
		view.SetRendered(rendered)
	}
	view.SetWrap(wrap)
	return cmd
}

func (m *Model) clickPreviewDocTab(index int) tea.Cmd {
	if m.preview.doc == nil {
		return nil
	}
	m.focusPreviewPane(panelayout.Document)
	if index == m.preview.doc.tabs.Active {
		return nil
	}
	return wrapPreviewDocLoad(m.selectPreviewDocTab(index, 0, nil), m.preview.doc.surface)
}

func (m *Model) cyclePreviewDocTab(delta int) tea.Cmd {
	if m.preview.doc == nil || len(m.preview.doc.tabs.Items) < 2 {
		return nil
	}
	m.preview.doc.tabs.Cycle(delta)
	return wrapPreviewDocLoad(m.ensurePreviewDocTabLoaded(), m.preview.doc.surface)
}

func (m *Model) closePreviewDocTab() tea.Cmd {
	if m.preview.doc == nil {
		return nil
	}
	if len(m.preview.doc.tabs.Items) <= 1 {
		return m.closePreviewDoc()
	}
	m.preview.doc.tabs.CloseActive()
	return wrapPreviewDocLoad(m.ensurePreviewDocTabLoaded(), m.preview.doc.surface)
}

func (m *Model) ensurePreviewDocTabLoaded() tea.Cmd {
	doc := m.preview.doc
	if doc == nil {
		return nil
	}
	view := doc.view()
	if view == nil || !view.NeedsLoad() {
		return nil
	}
	rendered := view.Rendered()
	wrap := view.Wrap()
	cmd := view.Load(doc.allocID(), doc.root, view.Title(), 0, doc.epoch)
	view.SetRendered(rendered)
	view.SetWrap(wrap)
	return cmd
}

func openPreviewFile(root, display, abs string) (*os.File, error) {
	if display != "" && !filepath.IsAbs(filepath.FromSlash(display)) {
		return terminallink.OpenRegular(filepath.Join(root, filepath.FromSlash(display)))
	}
	return terminallink.OpenRegular(abs)
}

func wrapPreviewDocLoad(cmd tea.Cmd, workspaceID string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if loaded, ok := msg.(docview.LoadedMsg); ok {
			return previewDocLoadedMsg{LoadedMsg: loaded, WorkspaceID: workspaceID}
		}
		return msg
	}
}

func (m *Model) applyPreviewDocLoaded(msg previewDocLoadedMsg) {
	doc := m.preview.doc
	if msg.WorkspaceID != m.preview.workspaceID {
		doc = m.preview.paneCache[msg.WorkspaceID].doc
	}
	if doc == nil || doc.surface != msg.WorkspaceID {
		return
	}
	for _, item := range doc.tabs.Items {
		if item.View != nil && item.View.SetResult(msg.LoadedMsg) {
			return
		}
	}
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
	if leaf := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document); leaf != nil {
		m.preview.paneRoot, m.preview.paneFocus = panelayout.Close(m.preview.paneRoot, leaf.ID)
	}
	if m.preview.issue != nil {
		m.focusPreviewPane(panelayout.Issue)
		return m.syncTerminalGeometry()
	}
	return tea.Batch(m.focusList(), m.syncTerminalGeometry())
}

// focusPreviewPane keeps the input owner and the layout tree's focused leaf in
// lockstep. The tree focus also selects the pane rendered in narrow layouts.
func (m *Model) focusPreviewPane(kind panelayout.Kind) bool {
	leaf := panelayout.FirstOfKind(m.preview.paneRoot, kind)
	if leaf == nil {
		return false
	}
	m.preview.paneFocus = leaf.ID
	m.preview.focus = focusPreview
	if m.preview.doc != nil {
		m.preview.doc.focused = kind == panelayout.Document
	}
	if m.preview.issue != nil {
		m.preview.issue.focused = kind == panelayout.Issue
		if m.preview.issue.view != nil {
			m.preview.issue.view.SetFocused(m.preview.issue.focused)
		}
	}
	return true
}

func previewPaneFloors() panelayout.Floors {
	return panelayout.Floors{
		Terminal: panelayout.Floor{Width: previewTermMinWidth, Height: 3},
		Doc:      panelayout.Floor{Width: previewSecondaryMinWidth, Height: 3},
		Issue:    panelayout.Floor{Width: previewSecondaryMinWidth, Height: 3},
	}
}

func (m *Model) ensurePreviewPane(kind panelayout.Kind, name string) (int, tea.Cmd) {
	if m.preview.paneRoot == nil {
		m.resetActivePreviewPanes()
	}
	plan, ok := panelayout.PlanOpen(m.preview.paneRoot, kind)
	if !ok {
		return 0, nil
	}
	if plan.Retarget != 0 {
		return plan.Retarget, nil
	}
	box, ok := m.previewBox()
	if !ok {
		return 0, nil
	}
	id := m.preview.paneNextID
	trial := panelayout.Clone(m.preview.paneRoot)
	trial, focus := panelayout.SplitLeaf(trial, plan.Split, plan.Axis, &panelayout.Node{ID: id, Kind: kind, ContentID: id})
	if focus != id {
		return 0, nil
	}
	if _, _, fits := panelayout.LayoutPanes(trial, termpreview.Box{W: box.W, H: box.H}, previewPaneFloors()); !fits {
		dimension := "wider"
		if plan.Axis == panelayout.Rows {
			dimension = "taller"
		}
		return 0, appmsg.ShowToast(name+" pane needs a "+dimension+" window; layout left unchanged", 3*time.Second)
	}
	m.preview.paneRoot, focus = panelayout.SplitLeaf(m.preview.paneRoot, plan.Split, plan.Axis, &panelayout.Node{ID: id, Kind: kind, ContentID: id})
	if focus != id {
		return 0, nil
	}
	m.preview.paneFocus = focus
	m.preview.paneNextID = panelayout.MaxID(m.preview.paneRoot) + 1
	return id, nil
}

func (m *Model) layoutPreviewPanes(box termpreview.Box) (panelayout.Layout, bool) {
	if m.preview.paneRoot == nil {
		m.resetActivePreviewPanes()
	}
	return panelayout.LayoutTree(m.preview.paneRoot, box, previewPaneFloors(), m.preview.paneFocus)
}

func (m *Model) previewPaneBox(kind panelayout.Kind, box termpreview.Box) (termpreview.Box, bool) {
	layout, ok := m.layoutPreviewPanes(box)
	if !ok {
		return termpreview.Box{}, false
	}
	for _, leaf := range layout.Leaves {
		if leaf.Node.Kind == kind {
			return leaf.Box, true
		}
	}
	return termpreview.Box{}, false
}

func (m *Model) previewTerminalBox() (termpreview.Box, bool) {
	box, ok := m.previewBox()
	if !ok {
		return termpreview.Box{}, false
	}
	return m.previewPaneBox(panelayout.Terminal, box)
}

func (m *Model) registerPreviewDocRegions(box termpreview.Box) {
	if m.preview.doc == nil {
		return
	}
	docBox, split := m.previewPaneBox(panelayout.Document, box)
	if !split {
		return
	}
	m.workspacesMouse.HitMap.AddRect(previewDocRegionKind, docBox.X, docBox.Y, docBox.W, docBox.H, previewDocRegionKind)
}

func (m *Model) registerPreviewDocTabRegions(box termpreview.Box) {
	if m.preview.doc == nil {
		return
	}
	docBox, ok := m.previewPaneBox(panelayout.Document, box)
	if !ok {
		return
	}
	for _, tab := range docview.LayoutTabStrip(m.preview.doc.tabs, docBox.W, m.PreviewFocused() && m.preview.doc.focused).Tabs {
		m.workspacesMouse.HitMap.AddRect(previewDocTabKind, docBox.X+tab.Col, docBox.Y, tab.Width, 1, previewDocTabHit(tab.Index))
	}
}

func (m *Model) handlePreviewDocMouse(action mouse.MouseAction) tea.Cmd {
	if tab, ok := action.Region.Data.(previewDocTabHit); ok {
		if action.Type == mouse.ActionClick || action.Type == mouse.ActionDoubleClick {
			return m.clickPreviewDocTab(int(tab))
		}
		if m.preview.doc != nil && m.preview.doc.view() != nil {
			switch action.Type {
			case mouse.ActionScrollUp, mouse.ActionScrollDown:
				m.preview.doc.view().Scroll(action.Delta)
			}
		}
		return nil
	}
	kind, _ := regionKind(action.Region)
	if kind != previewDocRegionKind || m.preview.doc == nil {
		return nil
	}
	switch action.Type {
	case mouse.ActionClick, mouse.ActionDoubleClick:
		m.focusPreviewPane(panelayout.Document)
		return nil
	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		if view := m.preview.doc.view(); view != nil {
			view.Scroll(action.Delta)
		}
	}
	return nil
}

func (m *Model) previewDocKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if m.preview.doc == nil || m.PreviewInteractive() {
		return false, nil
	}
	key := msg.String()
	if m.preview.doc.focused {
		switch key {
		case "q", "esc":
			return true, m.closePreviewDoc()
		case "m":
			if view := m.preview.doc.view(); view != nil {
				view.ToggleRenderMode()
			}
			return true, nil
		case "x":
			return true, m.closePreviewDocTab()
		case "{":
			return true, m.cyclePreviewDocTab(-1)
		case "}":
			return true, m.cyclePreviewDocTab(1)
		case "Y", "shift+y":
			if view := m.preview.doc.view(); view != nil {
				return true, docview.YankPath(view.Title())
			}
			return true, nil
		case "r":
			// Refresh rebuilds the preview and would drop this document.
			return true, nil
		case "enter", interactiveEnterKeyAlt:
			m.preview.doc.focused = false
			return true, m.enterPreviewInteractive()
		}
		if view := m.preview.doc.view(); view != nil && view.HandleKey(msg) {
			return true, nil
		}
	}
	return false, nil
}

func (m *Model) renderPreviewDoc(doc *previewDoc, box termpreview.Box) string {
	view := doc.view()
	contentHeight := max(box.H-termpreview.HeaderRows, 0)
	if view != nil {
		view.SetSize(box.W, contentHeight)
	}
	header := docview.LayoutTabStrip(doc.tabs, box.W, m.PreviewFocused() && doc.focused).Row
	body := ""
	if view != nil {
		body = view.View()
	}
	if contentHeight <= 0 {
		return header
	}
	return header + "\n" + body
}

func renderPreviewPaneDivider(divider panelayout.Divider, focused bool) string {
	style := lipgloss.NewStyle().Foreground(styles.BorderNormal)
	if focused {
		style = lipgloss.NewStyle().Foreground(styles.BorderActive)
	}
	if divider.Axis == panelayout.Rows {
		return style.Render(strings.Repeat("─", max(divider.Box.W, 0)))
	}
	if divider.Box.H <= 0 {
		return ""
	}
	return style.Render(strings.TrimSuffix(strings.Repeat("│\n", divider.Box.H), "\n"))
}
