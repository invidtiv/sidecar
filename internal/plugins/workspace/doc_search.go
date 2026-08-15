package workspace

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/filefind"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panemodal"
	"github.com/marcus/sidecar/internal/projectsearch"
	"github.com/marcus/sidecar/internal/ui"
)

// A document pane can host the same two search surfaces the Files plugin has:
// the fuzzy file finder (ctrl+p) and the project-wide ripgrep search (f). Both
// are rooted at the pane's own doc.root, which is what makes this work
// unchanged in project and global Workspaces — a pane carries the workspace or
// shell directory it was opened against, and neither surface asks anything else
// about the host.
//
// The surface is drawn as a modal scoped to the pane's box (internal/panemodal),
// below the pane's header row: sized to its own content and centred with the
// pane's document dimmed around it when the pane has room for a readable
// margin, and taking the whole box when it does not. It is never a full-screen
// takeover — a pane on a large monitor is bigger than any picker needs — and
// never a bare inline widget stranded in a large pane. The header row is never
// covered either way, so a pane always says both which file it holds and what
// it is doing.

// docSearchKind names which surface a pane is showing.
type docSearchKind int

const (
	docSearchFinder docSearchKind = iota + 1
	docSearchProject
)

// docSearchMode is one pane's live search surface. Exactly one of finder and
// search is set; the host talks to it through the small vocabulary below so
// neither the key path nor the render path asks which surface it is driving.
type docSearchMode struct {
	kind   docSearchKind
	finder *filefind.Finder
	search *projectsearch.Search
}

// docSearchOutcome is the two surfaces' Result narrowed to what this host can
// act on: drop the mode, or load a file into the pane.
type docSearchOutcome struct {
	Cancelled bool
	Open      bool
	Path      string
	Line      int
	NewTab    bool
}

func (m *docSearchMode) name() string {
	if m != nil && m.kind == docSearchProject {
		return "Search"
	}
	return "Find"
}

// query is what the user has typed, for the pane header.
func (m *docSearchMode) query() string {
	if m == nil {
		return ""
	}
	if m.kind == docSearchProject {
		if m.search != nil && m.search.State != nil {
			return m.search.State.Query
		}
		return ""
	}
	if m.finder != nil {
		return m.finder.Query()
	}
	return ""
}

// headerLabel is the mode's identity in the pane header: the surface's name and
// the query so far, so a pane in search mode never reads as a pane showing a
// file.
func (m *docSearchMode) headerLabel() string {
	if m == nil {
		return ""
	}
	label := "⌕ " + m.name()
	if q := m.query(); q != "" {
		label += " " + q
	}
	return label
}

// close releases whatever the surface still owns. The finder owns nothing (its
// file list belongs to the plugin's per-root cache); the project search owns a
// running ripgrep process, which would otherwise keep going to its timeout.
func (m *docSearchMode) close() {
	if m == nil {
		return
	}
	if m.kind == docSearchProject {
		m.search.Close()
	}
}

func (m *docSearchMode) setSize(width, height int) {
	if m == nil || m.kind != docSearchProject || m.search == nil {
		return
	}
	if width > 0 && height > 0 {
		m.search.SetSize(width, height)
	}
}

// view draws the surface at the given size. fill is panemodal's answer to
// whether the box has room to show the pane around the modal; passing it
// through is what makes the tight case a modal that owns the pane rather than a
// small box floating on an empty field.
func (m *docSearchMode) view(width, height int, fill bool, handler *mouse.Handler) string {
	if m == nil {
		return ""
	}
	if m.kind == docSearchProject {
		if m.search == nil {
			return ""
		}
		m.search.SetFill(fill)
		return m.search.View(width, height, handler)
	}
	if m.finder == nil {
		return ""
	}
	m.finder.SetFill(fill)
	return m.finder.View(width, height, handler)
}

func (m *docSearchMode) handleKey(msg tea.KeyPressMsg) (docSearchOutcome, tea.Cmd) {
	if m == nil {
		return docSearchOutcome{Cancelled: true}, nil
	}
	if m.kind == docSearchProject {
		if m.search == nil {
			return docSearchOutcome{Cancelled: true}, nil
		}
		res, cmd := m.search.HandleKey(msg)
		return projectSearchOutcome(res), cmd
	}
	if m.finder == nil {
		return docSearchOutcome{Cancelled: true}, nil
	}
	// The finder has no "beside what I am looking at" key of its own: it reports
	// NewTab only because hosts that have tabs need somewhere to put the answer.
	// shift+enter is that key here, matching the project search's.
	if msg.String() == "shift+enter" {
		matches := m.finder.Matches()
		if cursor := m.finder.Cursor(); cursor >= 0 && cursor < len(matches) {
			path := matches[cursor].Path
			m.finder.Reset()
			return docSearchOutcome{Open: true, Path: path, NewTab: true}, nil
		}
		return docSearchOutcome{}, nil
	}
	res, cmd := m.finder.HandleKey(msg)
	return finderOutcome(res), cmd
}

func (m *docSearchMode) handleMouse(msg tea.MouseMsg, handler *mouse.Handler) (docSearchOutcome, tea.Cmd) {
	if m == nil {
		return docSearchOutcome{}, nil
	}
	if m.kind == docSearchProject {
		if m.search == nil {
			return docSearchOutcome{}, nil
		}
		res, cmd := m.search.HandleMouse(msg, handler)
		return projectSearchOutcome(res), cmd
	}
	if m.finder == nil {
		return docSearchOutcome{}, nil
	}
	res, cmd := m.finder.HandleMouse(msg, handler)
	return finderOutcome(res), cmd
}

// update feeds the surface its own async traffic. Both surfaces drop messages
// stamped with an epoch other than the one they were opened at.
func (m *docSearchMode) update(msg tea.Msg) tea.Cmd {
	if m == nil {
		return nil
	}
	if m.kind == docSearchProject {
		if m.search == nil {
			return nil
		}
		return m.search.Update(msg)
	}
	if m.finder == nil {
		return nil
	}
	return m.finder.Update(msg)
}

func finderOutcome(res filefind.Result) docSearchOutcome {
	switch res.Outcome {
	case filefind.OutcomeCancelled:
		return docSearchOutcome{Cancelled: true}
	case filefind.OutcomeOpen:
		return docSearchOutcome{Open: true, Path: res.Path, Line: res.Line, NewTab: res.NewTab}
	}
	return docSearchOutcome{}
}

func projectSearchOutcome(res projectsearch.Result) docSearchOutcome {
	switch res.Outcome {
	case projectsearch.OutcomeCancelled:
		return docSearchOutcome{Cancelled: true}
	// A pane has no external editor to hand a hit to, and the gestures that
	// produce it — ctrl+e, a double-click on a row — are the user asking for the
	// hit rather than for an editor. They open it in the pane.
	case projectsearch.OutcomeOpen, projectsearch.OutcomeOpenExternal:
		return docSearchOutcome{Open: true, Path: res.Path, Line: res.Line, NewTab: res.NewTab}
	}
	return docSearchOutcome{}
}

// docSearchMsg wraps one search surface's own async message on its way back to
// the pane that issued it. The surfaces' messages are broadcast types the Files
// plugin also uses, and a file scan carries no root, so an unwrapped
// filefind.ScannedMsg from another plugin's finder would land in this pane's
// cache as if it described this pane's directory. Wrapping keeps a pane's
// traffic its own, and the leaf ID keeps it the right pane's.
type docSearchMsg struct {
	LeafID int
	Msg    tea.Msg
}

// docSearchCmd tags a surface's command with the pane that issued it.
func docSearchCmd(leafID int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if msg == nil {
			return nil
		}
		return docSearchMsg{LeafID: leafID, Msg: msg}
	}
}

// focusedDocPane is the document pane that holds the keyboard, or nil. It is
// the narrower question activeDocPane answers loosely: with two document panes
// open, only one of them is taking keys.
func (p *Plugin) focusedDocPane() *docPane {
	if !p.docFocused() {
		return nil
	}
	leaf := FindPane(p.paneRoot, p.paneFocus)
	if leaf == nil || leaf.Kind != PaneDoc {
		return nil
	}
	return p.docs[leaf.ContentID]
}

// docSearchPane is the focused document pane when it is showing a search
// surface.
func (p *Plugin) docSearchPane() *docPane {
	doc := p.focusedDocPane()
	if doc == nil || doc.mode == nil {
		return nil
	}
	return doc
}

// docSearchActive reports whether a pane-scoped search surface owns the
// keyboard. The footer, the focus context, and the global key gate all read it.
func (p *Plugin) docSearchActive() bool {
	return p.docSearchPane() != nil
}

// docPaneByLeaf finds a pane by its tree leaf, which is how a wrapped async
// message names the pane that issued it.
func (p *Plugin) docPaneByLeaf(leafID int) *docPane {
	for _, doc := range p.docs {
		if doc != nil && doc.leafID == leafID {
			return doc
		}
	}
	return nil
}

// docFinderCacheTTL is how long a pane's file list is trusted without a rescan.
//
// The Files plugin has a filesystem watcher and marks its cache dirty the moment
// the tree moves; Workspaces has no such signal — its only watcher is on the
// shell manifest — so there is nothing here to invalidate the list precisely.
// A short lifetime is the honest substitute: the second ctrl+p in a working
// session costs nothing, and a finder opened minutes later still sees files
// created since.
const docFinderCacheTTL = 30 * time.Second

// docFinderCache is one root's file list plus when its last scan was started.
type docFinderCache struct {
	cache   *filefind.Cache
	scanned time.Time
}

// finderCache returns the file list for root, shared by every pane rooted
// there. Panes on one root are looking at one directory tree, so they walk it
// once between them rather than once per ctrl+p: a fresh cache per open meant
// every open paid a full ScanPaths walk (up to 50k files), and open/esc/open
// spawned walks whose results were then thrown away.
func (p *Plugin) finderCache(root string) *filefind.Cache {
	if p.docFinderCaches == nil {
		p.docFinderCaches = make(map[string]*docFinderCache)
	}
	entry := p.docFinderCaches[root]
	if entry == nil {
		entry = &docFinderCache{cache: &filefind.Cache{}}
		p.docFinderCaches[root] = entry
	}
	if !entry.scanned.IsZero() && time.Since(entry.scanned) > docFinderCacheTTL {
		entry.cache.MarkDirty()
	}
	return entry.cache
}

// noteFinderScan records that a scan of root has just been issued. Only a scan
// that actually started moves the clock, so a cache that answered from memory
// keeps the age of the walk it is still showing.
func (p *Plugin) noteFinderScan(root string, started bool) {
	if !started {
		return
	}
	if entry := p.docFinderCaches[root]; entry != nil {
		entry.scanned = time.Now()
	}
}

// openDocFinder opens the fuzzy file finder in the focused document pane,
// rooted at that pane's directory.
func (p *Plugin) openDocFinder(doc *docPane) tea.Cmd {
	if doc == nil || p.ctx == nil {
		return nil
	}
	finder := filefind.NewFinder(p.finderCache(doc.root), doc.root, p.ctx.Epoch)
	doc.mode = &docSearchMode{kind: docSearchFinder, finder: finder}
	scan := finder.Open()
	p.noteFinderScan(doc.root, scan != nil)
	return docSearchCmd(doc.leafID, scan)
}

// openDocProjectSearch opens the ripgrep project search in the focused document
// pane, rooted at that pane's directory.
func (p *Plugin) openDocProjectSearch(doc *docPane) tea.Cmd {
	if doc == nil || p.ctx == nil {
		return nil
	}
	search := projectsearch.New(doc.root, p.ctx.Epoch)
	mode := &docSearchMode{kind: docSearchProject, search: search}
	mode.setSize(doc.boxW, doc.boxH)
	doc.mode = mode
	return nil
}

// closeDocSearch drops the surface and gives the document back the keyboard.
func (p *Plugin) closeDocSearch(doc *docPane) {
	if doc == nil {
		return
	}
	doc.mode.close()
	doc.mode = nil
	doc.modeRegions = nil
}

// closeUnfocusedDocSearches drops any pane search whose pane no longer holds
// the keyboard. It is the one rule this surface has about focus: a search is a
// modal scoped to its pane, and a modal that has lost the keyboard is dismissed
// rather than left drawn and inert. Enforcing it at the single focus writer
// (setFocusTarget) means every gesture that moves focus — Tab, a click on the
// sidebar or another leaf, a shortcut that focuses a pane — obeys it without
// each one having to remember to.
func (p *Plugin) closeUnfocusedDocSearches() {
	focused := p.focusedDocPane()
	for _, doc := range p.docs {
		if doc == nil || doc.mode == nil || doc == focused {
			continue
		}
		p.closeDocSearch(doc)
	}
}

// handleDocSearchKey routes a keypress to the live surface. Every key belongs
// to it while it is open, the same way a focused document absorbs the keys it
// does not use, so nothing leaks to the workspace behind the pane.
func (p *Plugin) handleDocSearchKey(doc *docPane, msg tea.KeyPressMsg) tea.Cmd {
	if doc == nil || doc.mode == nil {
		return nil
	}
	doc.mode.setSize(doc.boxW, doc.boxH)
	out, cmd := doc.mode.handleKey(msg)
	return p.applyDocSearchOutcome(doc, out, cmd)
}

// handleDocSearchMouse routes a mouse event to the live surface. The surface
// hit-tests the regions its last render registered, which panemodal placed at
// the pane's true position, so a click inside the modal hits the modal rather
// than the document under it.
func (p *Plugin) handleDocSearchMouse(doc *docPane, msg tea.MouseMsg) tea.Cmd {
	if doc == nil || doc.mode == nil {
		return nil
	}
	// Outside the pane is outside the modal. A press there dismisses the
	// surface, the way a click on a full-screen modal's backdrop does, and the
	// click is spent on the dismissal rather than on whatever it landed over.
	// Every other event — motion, wheel, release — is swallowed: it is over the
	// modal's backdrop, and the file-info modal answers a backdrop event the
	// same way, by consuming it and doing nothing. Routing them into the
	// surface instead made the wheel over a terminal pane scroll the finder's
	// list, which no modal in this app does.
	if msg != nil {
		pos := msg.Mouse()
		if !doc.boxContains(pos.X, pos.Y) {
			if _, isClick := msg.(tea.MouseClickMsg); isClick {
				p.closeDocSearch(doc)
			}
			return nil
		}
	}
	out, cmd := doc.mode.handleMouse(msg, p.mouseHandler)
	return p.applyDocSearchOutcome(doc, out, cmd)
}

func (p *Plugin) applyDocSearchOutcome(doc *docPane, out docSearchOutcome, cmd tea.Cmd) tea.Cmd {
	wrapped := docSearchCmd(doc.leafID, cmd)
	switch {
	case out.Cancelled:
		p.closeDocSearch(doc)
		return wrapped
	case out.Open && out.Path != "":
		p.closeDocSearch(doc)
		return tea.Batch(wrapped, p.loadDocSearchResult(doc, out))
	}
	return wrapped
}

// loadDocSearchResult puts the chosen file into the pane through the pane's own
// tab machinery: an already-open file is focused (and jumps to the line), a
// plain pick replaces the active tab, and shift+enter opens a new one.
func (p *Plugin) loadDocSearchResult(doc *docPane, out docSearchOutcome) tea.Cmd {
	if doc == nil {
		return nil
	}
	leaf := FindPane(p.paneRoot, doc.leafID)
	if leaf == nil || leaf.Kind != PaneDoc {
		return nil
	}
	cmd, _ := p.docPaneLoadTab(doc, leaf.ContentID, out.Path, out.Line, nil, !out.NewTab)
	p.activePane = PanePreview
	p.paneFocus = leaf.ID
	p.saveSelectionState()
	return cmd
}

// applyDocSearchMsg delivers a surface's own async result back to the pane that
// issued it. A pane that has since closed its surface drops the message, and a
// stale epoch is dropped inside the surface.
func (p *Plugin) applyDocSearchMsg(msg docSearchMsg) tea.Cmd {
	doc := p.docPaneByLeaf(msg.LeafID)
	if doc == nil || doc.mode == nil {
		return nil
	}
	return docSearchCmd(doc.leafID, doc.mode.update(msg.Msg))
}

// renderDocSearchOverlay composites the live surface over the pane's own
// content, inside the pane's box. background is the leaf's rendered body, which
// is already exactly the box; the result is too, which is what keeps the app
// header on screen.
//
// The surface draws into a scratch handler rather than the plugin's, because
// the pane-tree regions are registered after this render and would otherwise
// win the reverse hit-test for the cells the modal is drawn on. The regions are
// kept on the pane and added last (see registerDocSearchRegions).
func (p *Plugin) renderDocSearchOverlay(doc *docPane, background string, origin mouse.Rect, size Size) string {
	if doc == nil || doc.mode == nil || size.Width <= 0 || size.Height <= 0 {
		return background
	}
	box := panemodal.Box{X: origin.X, Y: origin.Y, W: size.Width, H: size.Height}
	scratch := mouse.NewHandler()
	out := panemodal.RenderFunc(box, ui.FitBlock(background, size.Width, size.Height), scratch, doc.mode.view)
	doc.modeRegions = scratch.HitMap.Regions()
	return out
}

// clearDocSearchRegions drops every pane's remembered surface regions at the
// start of a frame. Only a pane that is drawn puts its regions back (see
// renderDocSearchOverlay), which is what keeps a pane the frame did not draw —
// the panes a zoomed leaf hides, above all — from registering hit regions over
// the pane that was drawn — a zoomed leaf hides its siblings, and a pane the
// frame skipped must not keep claiming the cells the drawn one occupies.
func (p *Plugin) clearDocSearchRegions() {
	for _, doc := range p.docs {
		if doc != nil {
			doc.modeRegions = nil
		}
	}
}

// registerDocSearchRegions puts a live surface's hit regions into the plugin's
// hit map last, so they beat the pane-tree leaf region drawn under them.
func (p *Plugin) registerDocSearchRegions() {
	for _, doc := range p.docs {
		if doc == nil || doc.mode == nil {
			continue
		}
		for _, region := range doc.modeRegions {
			p.mouseHandler.HitMap.Add(region.ID, region.Rect, region.Data)
		}
	}
}

// openFinderPane is the workspace list's F: a new document pane, split beside
// the terminal, opened straight into the file finder. A pane that already
// exists is reused rather than doubled, which is the same answer clicking a
// file path gives.
func (p *Plugin) openFinderPane() tea.Cmd {
	// Kanban draws no pane tree, so a pane opened from it would take keys with
	// nothing on screen to take them for.
	if p.paneRoot == nil || p.ctx == nil || p.viewMode != ViewModeList {
		return nil
	}
	root, surface, ok := p.selectedTerminalSurface()
	if !ok {
		return nil
	}
	if doc, leaf := p.activeDocPane(); doc != nil && leaf != nil {
		p.paneFocus = leaf.ID
		p.activePane = PanePreview
		p.termPanelFocused = false
		return p.openDocFinder(doc)
	}
	plan, planned := planPaneOpen(p.paneRoot, PaneDoc)
	if !planned {
		return p.docPaneToast("Document")
	}
	docID := p.paneNextID
	node := &PaneNode{ID: docID, Kind: PaneDoc, ContentID: docID}
	if !p.splitOnPlannedLeaf(plan, node, "Document") {
		// splitOnPlannedLeaf already set the fit toast when that was the reason.
		if p.toastMessage == "" {
			return p.docPaneToast("Document")
		}
		return nil
	}
	// A pane opened for the finder has no file in it yet: the finder is what
	// chooses the first tab.
	p.docs[docID] = newDocPane(p.paneFocus, root, surface, nil)
	p.activePane = PanePreview
	p.termPanelFocused = false
	p.saveSelectionState()
	return tea.Batch(p.openDocFinder(p.docs[docID]), p.resizeDocTerminalCmd())
}

func (p *Plugin) docPaneToast(name string) tea.Cmd {
	p.toastMessage = paneFitMessage(name, SplitCols)
	p.toastTime = time.Now()
	return nil
}
