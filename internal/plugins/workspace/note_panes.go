package workspace

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/ui"
)

// notePane is one td note leaf's tab group. The pane tree points at this,
// not at a single model.
type notePane struct {
	leafID  int
	root    string
	surface string
	tabs    noteview.Tabs
}

func (n *notePane) view() *noteview.Model {
	if n == nil {
		return nil
	}
	return n.tabs.ActiveView()
}

func (p *Plugin) activeNotePane() (*notePane, *PaneNode) {
	for id, note := range p.notes {
		if note == nil {
			continue
		}
		if leaf := FindPane(p.paneRoot, note.leafID); leaf != nil && leaf.Kind == PaneNote && leaf.ContentID == id {
			return note, leaf
		}
	}
	return nil, nil
}

func (p *Plugin) activateNoteLink(noteID string) (tea.Cmd, bool) {
	root, surface, ok := p.selectedTerminalSurface()
	if !ok {
		return nil, false
	}
	cmd := p.openNotePaneForSurface(root, surface, noteID)
	note, _ := p.activeNotePane()
	if note == nil || note.tabs.Find(noteID) < 0 {
		return nil, false
	}
	p.clearTerminalSelection()
	return cmd, true
}

func (p *Plugin) openNotePaneForSurface(root, surface, noteID string) tea.Cmd {
	noteID = noteview.NormalizeID(noteID)
	if p.paneRoot == nil || p.ctx == nil || noteID == "" {
		return nil
	}
	return p.openWorkspaceContent(root, surface, contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: noteID}, "Note")
}

func (p *Plugin) newNoteModel() *noteview.Model {
	return noteview.New(p.markdownRenderer)
}

func (p *Plugin) nextNoteModelID() int {
	p.noteModelNextID++
	return p.noteModelNextID
}

func (p *Plugin) applyNoteLoaded(msg noteview.LoadedMsg) {
	if p.ctx == nil || msg.Epoch != p.ctx.Epoch {
		return
	}
	for _, note := range p.notes {
		if note == nil {
			continue
		}
		for _, item := range note.tabs.Items {
			if item.Value == nil || item.Value.ModelID() != msg.ModelID {
				continue
			}
			item.Value.SetResult(msg)
			return
		}
	}
}

func (p *Plugin) noteFocused() bool {
	note, _ := p.focusedNotePane()
	return note != nil
}

func (p *Plugin) focusedNotePane() (*notePane, *PaneNode) {
	if !p.previewLeafFocused() {
		return nil, nil
	}
	leaf := FindPane(p.paneRoot, p.paneFocus)
	if leaf == nil || leaf.Kind != PaneNote {
		return nil, nil
	}
	note := p.notes[leaf.ContentID]
	if note == nil {
		return nil, nil
	}
	return note, leaf
}

func (p *Plugin) handleNoteKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	note, _ := p.focusedNotePane()
	if note == nil {
		return false, nil
	}
	view := note.view()
	if view != nil {
		view.SetFocused(true)
	}
	if handled, cmd := p.paneSwitcherKey(msg); handled {
		return true, cmd
	}
	switch msg.String() {
	case "tab", "shift+tab":
		return false, nil
	case "\\":
		return true, p.toggleSidebarCmd()
	case "q", "esc":
		return true, p.hideNotePane()
	case "x":
		return true, p.closeActiveNoteTab()
	case "{":
		return true, p.cycleActiveNoteTab(-1)
	case "}":
		return true, p.cycleActiveNoteTab(1)
	case "y":
		return true, p.yankFocusedNote(false)
	case "Y", "shift+y":
		return true, p.yankFocusedNote(true)
	default:
		if view == nil {
			return true, nil
		}
		beforeActive := note.tabs.Active
		beforeID, beforeScroll := view.NoteID(), view.ScrollOffset()
		_, cmd := view.HandleKey(msg)
		after := note.view()
		if note.tabs.Active != beforeActive ||
			(after != nil && (after.NoteID() != beforeID || after.ScrollOffset() != beforeScroll)) {
			p.saveSelectionState()
		}
		return true, cmd
	}
}

func (p *Plugin) cycleActiveNoteTab(delta int) tea.Cmd {
	note, _ := p.focusedNotePane()
	if note == nil || len(note.tabs.Items) < 2 {
		return nil
	}
	if p.contentDeck != nil && len(note.tabs.Items) == workspaceDeckTabCount(p.contentDeck, panelayout.Note) {
		return p.cycleWorkspaceDeckTab(panelayout.Note, delta)
	}
	note.tabs.Cycle(delta)
	p.saveSelectionState()
	return p.ensureActiveNoteTabLoaded(note)
}

func (p *Plugin) closeActiveNoteTab() tea.Cmd {
	note, leaf := p.focusedNotePane()
	if note == nil || leaf == nil {
		return nil
	}
	return p.closeNoteTabAt(note, leaf.ID, note.tabs.Active)
}

func (p *Plugin) closeNoteTabAt(note *notePane, leafID, index int) tea.Cmd {
	if note == nil || index < 0 || index >= len(note.tabs.Items) {
		return nil
	}
	if p.contentDeck != nil && len(note.tabs.Items) == workspaceDeckTabCount(p.contentDeck, panelayout.Note) {
		return p.closeWorkspaceDeckTabAt(panelayout.Note, index)
	}
	if len(note.tabs.Items) <= 1 {
		return p.closeNotePane(leafID)
	}
	note.tabs.CloseAt(index)
	p.saveSelectionState()
	return p.ensureActiveNoteTabLoaded(note)
}

func (p *Plugin) selectNoteTab(note *notePane, leafID, idx int) tea.Cmd {
	if note == nil {
		return nil
	}
	p.focusLeaf(leafID)
	p.pointer.Abandon()
	if p.viewMode == ViewModeInteractive {
		p.exitInteractiveMode()
	}
	if idx == note.tabs.Active {
		return p.ensureActiveNoteTabLoaded(note)
	}
	if p.contentDeck != nil {
		return p.selectWorkspaceDeckTab(panelayout.Note, idx)
	}
	note.tabs.Select(idx)
	p.saveSelectionState()
	return p.ensureActiveNoteTabLoaded(note)
}

func (p *Plugin) clickNoteTabAt(x, y int) (tea.Cmd, bool) {
	if !p.docVisible() {
		return nil, false
	}
	var tabs []mouse.Region
	var closeAt *mouse.Region
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionNoteTab {
			continue
		}
		if y != region.Rect.Y {
			continue
		}
		hit, ok := region.Data.(noteTabHit)
		if !ok {
			continue
		}
		if hit.Close {
			if x >= region.Rect.X && x < region.Rect.X+region.Rect.W {
				r := region
				closeAt = &r
			}
			continue
		}
		tabs = append(tabs, region)
	}
	if len(tabs) == 0 && closeAt == nil {
		return nil, false
	}
	inNoteHeader := false
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionPaneLeaf {
			continue
		}
		header := insetPanelChrome(region.Rect)
		if x >= header.X && x < header.X+header.W && y == header.Y {
			inNoteHeader = true
			break
		}
	}
	if !inNoteHeader {
		return nil, false
	}
	if closeAt != nil {
		return p.clickNoteTab(closeAt.Data), true
	}
	best := tabs[0]
	bestDist := tabRowDistance(x, best.Rect)
	for _, region := range tabs[1:] {
		if d := tabRowDistance(x, region.Rect); d < bestDist {
			best, bestDist = region, d
		}
	}
	return p.clickNoteTab(best.Data), true
}

func (p *Plugin) clickNoteTab(data any) tea.Cmd {
	hit, ok := data.(noteTabHit)
	if !ok {
		return nil
	}
	leaf := FindPane(p.paneRoot, hit.LeafID)
	if leaf == nil || leaf.Kind != PaneNote {
		return nil
	}
	note := p.notes[leaf.ContentID]
	if note == nil {
		return nil
	}
	if hit.Close {
		return p.closeNoteTabAt(note, hit.LeafID, hit.Index)
	}
	return p.selectNoteTab(note, hit.LeafID, hit.Index)
}

func (p *Plugin) hideNotePane() tea.Cmd {
	note, leaf := p.focusedNotePane()
	if note == nil || leaf == nil {
		return nil
	}
	return p.hideContentPane(leaf.ID)
}

func (p *Plugin) ensureActiveNoteTabLoaded(note *notePane) tea.Cmd {
	if note == nil || p.ctx == nil {
		return nil
	}
	view := note.view()
	if view == nil || !view.NeedsLoad() {
		return nil
	}
	id := noteview.NormalizeID(view.NoteID())
	if id == "" {
		if item, ok := note.tabs.ActiveItem(); ok {
			id = noteview.NormalizeID(item.Key)
		}
	}
	if id == "" {
		return nil
	}
	return p.loadNoteView(view, note.root, id)
}

func (p *Plugin) loadNoteView(view *noteview.Model, root, noteID string) tea.Cmd {
	if view == nil || p.ctx == nil {
		return nil
	}
	modelID := view.ModelID()
	if modelID == 0 {
		modelID = p.nextNoteModelID()
	}
	return view.Load(modelID, root, noteID, p.ctx.Epoch)
}

func encodeNoteTabs(note *notePane) ([]state.PaneNoteTabJSON, int) {
	if note == nil {
		return nil, 0
	}
	tabs := make([]state.PaneNoteTabJSON, 0, len(note.tabs.Items))
	active := 0
	for i, item := range note.tabs.Items {
		id := noteview.NormalizeID(item.Key)
		if id == "" && item.Value != nil {
			id = noteview.NormalizeID(item.Value.NoteID())
		}
		if id == "" {
			continue
		}
		scroll := 0
		if item.Value != nil {
			scroll = item.Value.ScrollOffset()
		}
		if i == note.tabs.Active {
			active = len(tabs)
		}
		tabs = append(tabs, state.PaneNoteTabJSON{Note: id, Scroll: scroll})
	}
	return tabs, active
}

func (p *Plugin) decodeNoteLeaf(saved *state.PaneLayoutJSON, root string, loads *[]tea.Cmd) *PaneNode {
	if saved == nil || p.ctx == nil || len(saved.NoteTabs) == 0 {
		return nil
	}
	wanted := saved.Active
	if wanted < 0 || wanted >= len(saved.NoteTabs) {
		wanted = 0
	}
	type restoredTab struct {
		id     string
		scroll int
	}
	var pending []restoredTab
	active := 0
	for i, tab := range saved.NoteTabs {
		id := noteview.NormalizeID(tab.Note)
		if id == "" {
			continue
		}
		if i == wanted {
			active = len(pending)
		}
		pending = append(pending, restoredTab{id: id, scroll: tab.Scroll})
	}
	if len(pending) == 0 {
		return nil
	}
	if p.notes == nil {
		p.notes = make(map[int]*notePane)
	}
	id := p.nextPaneID()
	pane := &notePane{leafID: id, root: root, surface: savedRootSurface(p, root)}
	p.notes[id] = pane
	var group noteview.Tabs
	for _, tab := range pending {
		view := p.newNoteModel()
		view.Arm(p.nextNoteModelID(), tab.id, p.ctx.Epoch)
		view.SetPendingScroll(tab.scroll)
		group.Append(tab.id, view)
	}
	group.Select(active)
	pane.tabs = group
	if load := p.ensureActiveNoteTabLoaded(pane); load != nil {
		*loads = append(*loads, load)
	}
	return &PaneNode{ID: id, Kind: PaneNote, ContentID: id}
}

func (p *Plugin) closeNotePane(leafID int) tea.Cmd {
	return p.forgetContentPane(leafID)
}

func (p *Plugin) notePaneHeaderRow(note *notePane, width int, focused bool) string {
	return p.composeContentHeader(layoutNoteTabStrip(note, ui.ReserveHeaderClose(width).TabsWidth, focused).HoverClose(p.hoverTabClose.IndexFor(noteLeafID(note))).Row, width, note != nil && p.hoverPaneClose == note.leafID)
}

func (p *Plugin) registerNotePaneRegions(note *notePane, leafID int, box Box) {
	p.mouseHandler.HitMap.AddRect(regionPaneLeaf, box.X, box.Y, box.W, box.H, leafID)
}

func (p *Plugin) registerNoteTabRegions(note *notePane, leafID int, box Box) {
	strip := layoutNoteTabStrip(note, ui.ReserveHeaderClose(box.W).TabsWidth, p.paneFocus == leafID)
	strip.RegisterHits(func(col, width, index int, close bool) {
		p.mouseHandler.HitMap.AddRect(regionNoteTab, box.X+col, box.Y, width, 1, noteTabHit{LeafID: leafID, Index: index, Close: close})
	})
}

func (p *Plugin) yankFocusedNote(idOnly bool) tea.Cmd {
	note, _ := p.focusedNotePane()
	view := note.view()
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

type noteTabHit struct {
	LeafID int
	Index  int
	Close  bool
}

func layoutNoteTabStrip(note *notePane, width int, focused bool) noteview.TabStrip {
	var group noteview.Tabs
	if note != nil {
		group = note.tabs
	}
	return noteview.LayoutTabStrip(group, width, focused)
}
