package overview

import (
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	previewNoteRegionKind = "global-preview-note"
	previewNoteTabKind    = "global-preview-note-tab"
	// previewNoteBarKind is the data carried by the active note tab's bar
	// regions. The region IDs are the shared renderer's (ui.RegionScrollbar*),
	// which other surfaces' bars also use, so the payload — not the ID — is
	// what tells this pane's bar apart in the shared hit map.
	previewNoteBarKind = "global-preview-note-bar"
)

func isPreviewNoteRegion(kind string) bool {
	return kind == previewNoteRegionKind || kind == previewNoteTabKind || kind == previewNoteBarKind
}

// previewNoteTabHit is the tab stored on the note header's regions.
type previewNoteTabHit struct {
	Index int
	Close bool
}

// previewNoteBar carries one note pane's in-flight pointer gesture on its bar.
//
// noteview exposes a deliberately state-free seam, so the host owns the
// bookkeeping: the press-time params snapshot keeps a mid-gesture re-render —
// a live refresh, a resize — from shifting the mapping under the pointer, and
// OffsetAtRow clamps past both ends of the track without ever ending anything.
type previewNoteBar struct {
	params    ui.ScrollbarParams // renderer inputs at press time
	trackTopY int                // absolute row of the track top at press time
	grabDelta int                // rows between the pointer and the thumb's anchor row
	active    bool
}

type previewNote struct {
	tabs    noteview.Tabs
	root    string
	surface string
	focused bool
	epoch   uint64
	// bar is the live scrollbar gesture on the active tab, armed by a press on
	// one of this pane's bar regions and settled by release or lost-release.
	bar previewNoteBar
	// hostNotice is a connected-stale or verb-failure label for a remote
	// note that is still showing its last good body.
	hostNotice string
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
	cmd, _ := m.openPreviewNoteResult(noteID)
	return cmd
}

func (m *Model) openPreviewNoteResult(noteID string) (tea.Cmd, error) {
	workspace, ok := m.SelectedWorkspace()
	noteID = noteview.NormalizeID(noteID)
	if !ok || noteID == "" || workspace.Path == "" {
		return nil, nil
	}
	if workspace.Remote() {
		ctx, ok := m.previewDeckContext()
		if !ok {
			return nil, nil
		}
		ref, err := contentpanes.ResolveDocument(m.previewDeckConfig(ctx).Source, ctx.Source, contentlink.Pending{
			Kind: contentlink.KindInternal, Raw: noteID,
		})
		if err != nil || ref.Value == "" {
			if err == nil {
				err = errors.New("note not found on " + ctx.Source.HostID)
			}
			return remoteContentErrorCmd(err), err
		}
		noteID = ref.Value
	}
	return m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: noteID}, "Note"), nil
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
		if item.Value == nil {
			continue
		}
		matched := item.Value.ResultMatches(msg.LoadedMsg)
		if item.Value.SetResult(msg.LoadedMsg) {
			note.hostNotice = ""
			return
		}
		if !matched {
			continue
		}
		if msg.NotModified {
			note.hostNotice = ""
			return
		}
		if msg.Refresh && msg.Error != nil {
			note.hostNotice = remoteDocumentStaleNotice
		}
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
	header := m.composePreviewHeader(m.previewHostHeaderTabs(noteview.LayoutTabStrip(note.tabs, m.reserveHeader(box.W, true).TabsWidth, focused).HoverClose(m.tabCloseHoverIn(panelayout.Note)).Row, note.hostNotice), box.W, panelayout.Note)
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
	strip := noteview.LayoutTabStrip(m.preview.note.tabs, m.reserveHeader(noteBox.W, true).TabsWidth, focused)
	strip.RegisterHits(func(col, width, index int, close bool) {
		m.workspacesMouse.HitMap.AddRect(
			previewNoteTabKind,
			noteBox.X+col, noteBox.Y, width, 1,
			previewNoteTabHit{Index: index, Close: close},
		)
	})
}

// registerPreviewNoteScrollbarRegions puts the active tab's bar regions in the
// hit map. It runs from the frame's Body pass — after the leaf, tabs, title and
// close regions — so the bar wins HitMap.Test's reverse scan over everything
// drawn under its column. A tab whose content fits registers nothing: the
// reserved column is an anti-jitter spacer, not a control.
func (m *Model) registerPreviewNoteScrollbarRegions(noteBox termpreview.Box) {
	view := m.previewNoteView()
	if view == nil || !view.HasScrollbar() || noteBox.H <= termpreview.HeaderRows {
		return
	}
	params := view.ScrollbarParams()
	_, geom := ui.RenderScrollbarWithGeometry(params)
	if !geom.HasThumb || geom.TrackRect.Dy() <= 0 {
		return
	}
	barX := noteBox.X + noteBox.W - 2 // the card pads one column either side of its bar
	top := noteBox.Y + termpreview.HeaderRows
	m.workspacesMouse.HitMap.AddRect(ui.RegionScrollbarTrack, barX, top, 1, geom.TrackRect.Dy(), previewNoteBarKind)
	// The thumb is added after the track so the reverse scan hands a press on
	// their overlap to the thumb, exactly as the shared geometry orders them.
	m.workspacesMouse.HitMap.AddRect(ui.RegionScrollbarThumb, barX, top+geom.ThumbRect.Min.Y, 1, geom.ThumbRect.Dy(), previewNoteBarKind)
}

func (m *Model) previewNoteView() *noteview.Model {
	if m.preview.note == nil {
		return nil
	}
	return m.preview.note.view()
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
	if kind != previewNoteRegionKind && kind != previewNoteBarKind {
		return nil
	}
	// A press on the active tab's bar grabs or jumps-to-spot before any
	// focus-only answer can run: the reverse-scanned hit map gave the bar its
	// column, and a scrollbar press is not a click on the pane beneath it.
	if kind == previewNoteBarKind && note != nil {
		if action.Type == mouse.ActionClick || action.Type == mouse.ActionDoubleClick {
			m.pressPreviewNoteScrollbar(action)
		}
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

// pressPreviewNoteScrollbar begins the active tab's bar gesture: grabbing the
// thumb where it was pressed, or jumping to the clicked spot anchored there so
// the same gesture keeps dragging (macOS track-click). The double-click case
// rides this seam deliberately — a rapid second press re-grabs the bar instead
// of being absorbed as a stray activation of what sits under it.
func (m *Model) pressPreviewNoteScrollbar(action mouse.MouseAction) {
	note := m.preview.note
	view := m.previewNoteView()
	if note == nil || view == nil || action.Region == nil {
		return
	}
	params := view.ScrollbarParams()
	_, geom := ui.RenderScrollbarWithGeometry(params)
	if !geom.HasThumb {
		return
	}
	offset := view.ScrollOffset()
	trackTop := action.Region.Rect.Y
	grabDelta := 0
	if action.Region.ID == ui.RegionScrollbarThumb {
		trackTop -= geom.ThumbRect.Min.Y
		grabDelta = action.Y - trackTop - ui.RowForOffset(params, offset)
	} else {
		// Track press: jump-to-spot, anchored at the grabbed row.
		offset = view.OffsetAtTrackRow(action.Y - trackTop)
		view.ScrollToOffset(offset)
	}
	note.bar = previewNoteBar{
		params:    params,
		trackTopY: trackTop,
		grabDelta: grabDelta,
		active:    true,
	}
	m.workspacesMouse.StartDrag(action.X, action.Y, action.Region.ID, offset)
}

// dragPreviewNoteScrollbar applies a held gesture's mapping for one pointer
// row. Only a live gesture answers; the shared core clamps past both ends of
// the press-time track without ending anything.
func (m *Model) dragPreviewNoteScrollbar(y int) {
	note := m.preview.note
	if note == nil || !note.bar.active {
		return
	}
	if view := note.view(); view != nil {
		row := y - note.bar.trackTopY - note.bar.grabDelta
		view.ScrollToOffset(ui.OffsetAtRow(note.bar.params, row))
	}
}

// settlePreviewNoteScrollbar ends whichever bar gesture a release or a lost
// release left live. Offsets hold where the pointer left them; nothing is
// persisted. Reports whether a live gesture was actually settled.
func (m *Model) settlePreviewNoteScrollbar() bool {
	note := m.preview.note
	if note == nil || !note.bar.active {
		return false
	}
	note.bar = previewNoteBar{}
	return true
}

// previewNoteBarOwnsDrag reports that a drag named by its source belongs to
// the note pane's live bar gesture. The list's bar starts its drags under the
// same shared region IDs, so the live-gesture state — not the ID alone — is
// what tells them apart.
func (m *Model) previewNoteBarOwnsDrag(dragSource string) bool {
	note := m.preview.note
	return note != nil && note.bar.active &&
		(dragSource == ui.RegionScrollbarThumb || dragSource == ui.RegionScrollbarTrack)
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
