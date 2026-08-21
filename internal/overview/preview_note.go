package overview

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	previewNoteRegionKind = "global-preview-note"
	previewNoteTabKind    = "global-preview-note-tab"
)

func isPreviewNoteRegion(kind string) bool {
	return kind == previewNoteRegionKind || kind == previewNoteTabKind
}

type previewNoteTabHit struct {
	Index int
	Close bool
}

type previewNote struct {
	tabs    noteview.Tabs
	root    string
	surface string
	focused bool
	epoch   uint64
}

func (n *previewNote) view() *noteview.Model {
	if n == nil {
		return nil
	}
	return n.tabs.ActiveView()
}

type previewNoteLoadedMsg struct {
	noteview.LoadedMsg
	WorkspaceID string
}

func (m *Model) openPreviewNote(noteID string) tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	noteID = noteview.NormalizeID(noteID)
	if !ok || noteID == "" || workspace.Path == "" {
		return nil
	}
	return m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: noteID}, "Note")
}

func wrapPreviewNoteLoad(cmd tea.Cmd, workspaceID string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if loaded, ok := msg.(noteview.LoadedMsg); ok {
			return previewNoteLoadedMsg{LoadedMsg: loaded, WorkspaceID: workspaceID}
		}
		return msg
	}
}

func (m *Model) previewNoteForWorkspace(workspaceID string) *previewNote {
	if m.preview.note != nil && m.preview.workspaceID == workspaceID {
		return m.preview.note
	}
	if cached, ok := m.preview.paneCache[workspaceID]; ok {
		return cached.note
	}
	return nil
}

func (m *Model) applyPreviewNoteLoaded(msg previewNoteLoadedMsg) {
	note := m.previewNoteForWorkspace(msg.WorkspaceID)
	if note == nil || note.surface != msg.WorkspaceID {
		return
	}
	for _, item := range note.tabs.Items {
		if item.Value == nil || item.Value.ModelID() != msg.ModelID {
			continue
		}
		item.Value.SetResult(msg.LoadedMsg)
		return
	}
}

func (m *Model) closePreviewNote() tea.Cmd {
	if m.preview.deck == nil {
		return nil
	}
	m.preview.deck.FocusLeaf(m.preview.deck.Leaf(panelayout.Note))
	for m.preview.deck.Leaf(panelayout.Note) != 0 {
		m.preview.deck.CloseActive()
	}
	if ctx, ok := m.previewDeckContext(); ok {
		m.syncPreviewDeckProjection(ctx)
	}
	if m.preview.doc != nil {
		m.focusPreviewPane(panelayout.Document)
		return m.syncTerminalGeometry()
	}
	if m.preview.issue != nil {
		m.focusPreviewPane(panelayout.Issue)
		return m.syncTerminalGeometry()
	}
	if m.preview.diff != nil {
		m.focusPreviewPane(panelayout.Diff)
		return m.syncTerminalGeometry()
	}
	if m.preview.resource != nil {
		m.focusPreviewPane(panelayout.Resource)
		return m.syncTerminalGeometry()
	}
	return tea.Batch(m.focusList(), m.syncTerminalGeometry())
}

func (m *Model) closePreviewNoteTab() tea.Cmd {
	if m.preview.note == nil {
		return nil
	}
	return m.closePreviewNoteTabAt(m.preview.note.tabs.Active)
}

func (m *Model) closePreviewNoteTabAt(index int) tea.Cmd {
	if m.preview.deck == nil {
		return nil
	}
	m.preview.deck.CloseTab(m.preview.deck.Leaf(panelayout.Note), index)
	return m.finishPreviewDeckClose()
}

func (m *Model) cyclePreviewNoteTab(delta int) tea.Cmd {
	if m.preview.deck == nil || !m.preview.deck.FocusLeaf(m.preview.deck.Leaf(panelayout.Note)) {
		return nil
	}
	cmd := m.preview.deck.CycleTab(delta)
	if ctx, ok := m.previewDeckContext(); ok {
		m.syncPreviewDeckProjection(ctx)
	}
	return cmd
}

func (m *Model) clickPreviewNoteTab(index int) tea.Cmd {
	if m.preview.deck == nil {
		return nil
	}
	cmd := m.preview.deck.SelectTab(m.preview.deck.Leaf(panelayout.Note), index)
	if ctx, ok := m.previewDeckContext(); ok {
		m.syncPreviewDeckProjection(ctx)
	}
	return cmd
}

func (m *Model) renderPreviewNote(note *previewNote, box termpreview.Box) string {
	view := note.view()
	contentHeight := max(box.H-termpreview.HeaderRows, 0)
	focused := m.PreviewFocused() && note.focused
	if view != nil {
		view.SetSize(box.W, contentHeight)
		view.SetFocused(focused)
	}
	header := m.composePreviewHeader(noteview.LayoutTabStrip(note.tabs, ui.ReserveHeaderClose(box.W).TabsWidth, focused).Row, box.W, panelayout.Note)
	if contentHeight <= 0 {
		return header
	}
	body := ""
	if view != nil {
		body = view.View()
	}
	return header + "\n" + body
}

func (m *Model) registerPreviewNoteRegion(noteBox termpreview.Box) {
	if m.preview.note == nil {
		return
	}
	m.workspacesMouse.HitMap.AddRect(
		previewNoteRegionKind,
		noteBox.X, noteBox.Y, noteBox.W, noteBox.H,
		previewNoteRegionKind,
	)
}

func (m *Model) registerPreviewNoteTabRegions(noteBox termpreview.Box) {
	if m.preview.note == nil {
		return
	}
	focused := m.PreviewFocused() && m.preview.note.focused
	strip := noteview.LayoutTabStrip(m.preview.note.tabs, ui.ReserveHeaderClose(noteBox.W).TabsWidth, focused)
	strip.RegisterHits(func(col, width, index int, close bool) {
		m.workspacesMouse.HitMap.AddRect(
			previewNoteTabKind,
			noteBox.X+col, noteBox.Y, width, 1,
			previewNoteTabHit{Index: index, Close: close},
		)
	})
}

func (m *Model) handlePreviewNoteMouse(action mouse.MouseAction) tea.Cmd {
	if tab, ok := action.Region.Data.(previewNoteTabHit); ok {
		if action.Type == mouse.ActionClick || action.Type == mouse.ActionDoubleClick {
			if tab.Close {
				return m.closePreviewNoteTabAt(tab.Index)
			}
			return m.clickPreviewNoteTab(tab.Index)
		}
		if view := m.preview.note.view(); view != nil {
			switch action.Type {
			case mouse.ActionScrollUp, mouse.ActionScrollDown:
				view.Scroll(action.Delta)
			}
		}
		return nil
	}
	note := m.preview.note
	kind, _ := regionKind(action.Region)
	if kind != previewNoteRegionKind || note == nil {
		return nil
	}
	view := note.view()
	switch action.Type {
	case mouse.ActionClick, mouse.ActionDoubleClick:
		m.focusPreviewPane(panelayout.Note)
		return nil
	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		if view != nil {
			view.Scroll(action.Delta)
		}
	}
	return nil
}

func (m *Model) previewNoteKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	note := m.preview.note
	if note == nil || !note.focused || m.PreviewInteractive() {
		return false, nil
	}
	switch msg.String() {
	case "q", "esc":
		return true, m.closePreviewNote()
	case "x":
		return true, m.closePreviewNoteTab()
	case "{":
		return true, m.cyclePreviewNoteTab(-1)
	case "}":
		return true, m.cyclePreviewNoteTab(1)
	case "y":
		return true, m.yankPreviewNote(false)
	case "Y", "shift+y":
		return true, m.yankPreviewNote(true)
	}
	view := note.view()
	if view == nil {
		return true, nil
	}
	note.focused = true
	view.SetFocused(true)
	handled, cmd := view.HandleKey(msg)
	if handled {
		return true, cmd
	}
	return false, nil
}

func (m *Model) yankPreviewNote(idOnly bool) tea.Cmd {
	view := m.preview.note.view()
	if view == nil {
		return nil
	}
	data := view.Data()
	if data == nil {
		return nil
	}
	if idOnly {
		return noteview.CopyID(data)
	}
	return noteview.CopyMarkdown(data)
}
