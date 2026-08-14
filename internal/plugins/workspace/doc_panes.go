package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/ui"
)

type docPane struct {
	leafID  int
	root    string
	surface string
	view    *docview.Model
}

func docPaneTarget(path string) bool {
	return strings.TrimSpace(path) != ""
}

// selectedTerminalSurface identifies the actual terminal selection, not only
// its filesystem root. Project shells deliberately share ctx.WorkDir, so the
// tmux name is required to distinguish shell A from shell B.
func (p *Plugin) selectedTerminalSurface() (root, identity string, ok bool) {
	if p.ctx == nil {
		return "", "", false
	}
	root = p.ctx.WorkDir
	if p.selectingShell() {
		shell := p.getSelectedShell()
		if shell == nil || shell.TmuxName == "" {
			return "", "", false
		}
		if shell.WorkDir != "" {
			root = shell.WorkDir
		}
		identity = "shell:" + shell.TmuxName
	} else {
		wt := p.selectedWorktree()
		if wt == nil {
			return "", "", false
		}
		root = wt.Path
		identity = "workspace:" + stablePathKey(wt.Path)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", false
	}
	return filepath.Clean(resolved), identity, true
}

func (p *Plugin) activeDocPane() (*docPane, *PaneNode) {
	for id, doc := range p.docs {
		if doc == nil {
			continue
		}
		if leaf := FindPane(p.paneRoot, doc.leafID); leaf != nil && leaf.Kind == PaneDoc && leaf.ContentID == id {
			return doc, leaf
		}
	}
	return nil, nil
}

func paneTreeFloors() Floors {
	return Floors{
		Terminal: PaneFloor{Width: termPanelMinBoxCols, Height: termPanelMinBoxRows},
		Doc:      PaneFloor{Width: markdown.MinWidthForMarkdown, Height: termPanelMinBoxRows},
		// An issue's body is markdown wrapped by the same renderer, so it needs
		// the width that renderer stops being markdown below.
		Issue: PaneFloor{Width: markdown.MinWidthForMarkdown, Height: termPanelMinBoxRows},
	}
}

func (p *Plugin) openDocPane(root, rel string, line int) tea.Cmd {
	_, surface, _ := p.selectedTerminalSurface()
	return p.openDocPaneForSurface(root, surface, rel, line)
}

func (p *Plugin) openDocPaneForSurface(root, surface, rel string, line int) tea.Cmd {
	return p.openDocPaneFileForSurface(root, surface, rel, line, nil)
}

func (p *Plugin) openDocPaneFileForSurface(root, surface, rel string, line int, file *os.File) tea.Cmd {
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	if p.paneRoot == nil || p.ctx == nil {
		return nil
	}
	epoch := p.ctx.Epoch
	if doc, leaf := p.activeDocPane(); doc != nil {
		doc.root = root
		doc.surface = surface
		p.paneFocus = leaf.ID
		p.activePane = PanePreview
		var cmd tea.Cmd
		if file != nil {
			cmd = doc.view.LoadFile(leaf.ContentID, file, rel, line, epoch)
			file = nil
		} else {
			cmd = doc.view.Load(leaf.ContentID, root, rel, line, epoch)
		}
		applyDocRenderMode(doc.view, rel, line)
		p.saveSelectionState()
		return cmd
	}

	docID := p.paneNextID
	newLeaf := &PaneNode{ID: docID, Kind: PaneDoc, ContentID: docID}
	trial := clonePaneTree(p.paneRoot)
	trialDoc := &PaneNode{ID: docID, Kind: PaneDoc, ContentID: docID}
	trial, trialFocus := SplitLeaf(trial, terminalLeafID(trial), SplitCols, trialDoc)
	if trialFocus != trialDoc.ID {
		return nil
	}
	if content, ok := p.previewContentBox(); !ok {
		return nil
	} else if _, _, fits := LayoutPanes(trial, content, paneTreeFloors()); !fits {
		p.toastMessage = "Document pane needs a wider window; terminal left unchanged"
		p.toastTime = time.Now()
		return nil
	}

	terminalID := terminalLeafID(p.paneRoot)
	treeRoot, focus := SplitLeaf(p.paneRoot, terminalID, SplitCols, newLeaf)
	if focus != newLeaf.ID {
		return nil
	}
	p.paneRoot, p.paneFocus = treeRoot, focus
	p.paneNextID = maxInt(p.paneNextID, maxPaneID(p.paneRoot)+1)
	viewer := docview.New(nil)
	p.docs[docID] = &docPane{leafID: p.paneFocus, root: root, surface: surface, view: viewer}
	p.activePane = PanePreview
	var load tea.Cmd
	if file != nil {
		load = viewer.LoadFile(docID, file, rel, line, epoch)
		file = nil
	} else {
		load = viewer.Load(docID, root, rel, line, epoch)
	}
	applyDocRenderMode(viewer, rel, line)
	p.saveSelectionState()
	return tea.Batch(load, p.resizeDocTerminalCmd())
}

func applyDocRenderMode(view *docview.Model, path string, line int) {
	if view == nil {
		return
	}
	if !terminallink.Markdown(path) || line > 0 {
		view.SetRendered(false)
		return
	}
	view.SetRendered(true)
}

func clonePaneTree(node *PaneNode) *PaneNode {
	if node == nil {
		return nil
	}
	clone := *node
	if node.Split != nil {
		split := *node.Split
		split.A = clonePaneTree(node.Split.A)
		split.B = clonePaneTree(node.Split.B)
		clone.Split = &split
	}
	return &clone
}

func terminalLeafID(root *PaneNode) int {
	if leaf := firstPaneLeafOfKind(root, PaneTerminal); leaf != nil {
		return leaf.ID
	}
	return 0
}

func (p *Plugin) closeDocPane() tea.Cmd {
	if !p.closeDocPaneState() {
		return nil
	}
	p.activePane = PanePreview
	p.saveSelectionState()
	return p.resizeDocTerminalCmd()
}

func (p *Plugin) closeDocPaneState() bool {
	doc, _ := p.activeDocPane()
	if doc == nil {
		return false
	}
	return p.closeContentLeaf(doc.leafID)
}

// closeContentLeaf drops one content leaf's state and collapses its box into
// its sibling. It is the one close path for every non-terminal leaf, so a kind
// added to the tree cannot be closed by a route that forgets to release it.
func (p *Plugin) closeContentLeaf(leafID int) bool {
	leaf := FindPane(p.paneRoot, leafID)
	if leaf == nil || leaf.Split != nil {
		return false
	}
	switch leaf.Kind {
	case PaneDoc:
		delete(p.docs, leaf.ContentID)
	case PaneIssue:
		delete(p.issues, leaf.ContentID)
	default:
		return false
	}
	p.paneRoot, p.paneFocus = ClosePane(p.paneRoot, leaf.ID)
	return true
}

// resetDocPanesForSelection collapses every content leaf that belongs to a
// terminal surface other than the selected one. A leaf is bound to the surface
// its link was clicked in, so a shell switch takes its documents and issues
// with it rather than showing them against the wrong workspace.
func (p *Plugin) resetDocPanesForSelection() bool {
	root, surface, selected := p.selectedTerminalSurface()
	closed := false
	for _, leafID := range p.contentLeafIDs() {
		paneRoot, paneSurface, ok := p.contentLeafSurface(leafID)
		if ok && selected && filepath.Clean(paneRoot) == root && paneSurface == surface {
			continue
		}
		closed = p.closeContentLeaf(leafID) || closed
	}
	return closed
}

// contentLeafIDs lists the non-terminal leaves of the tree in placement order.
// The list is taken before any close, because closing collapses the tree the
// walk would otherwise be reading.
func (p *Plugin) contentLeafIDs() []int {
	var ids []int
	var walk func(node *PaneNode)
	walk = func(node *PaneNode) {
		if node == nil {
			return
		}
		if node.Split != nil {
			walk(node.Split.A)
			walk(node.Split.B)
			return
		}
		if node.Kind != PaneTerminal {
			ids = append(ids, node.ID)
		}
	}
	walk(p.paneRoot)
	return ids
}

// contentLeafSurface reports the terminal surface a content leaf was opened
// against. ok is false for a leaf whose content is gone, which is a leaf
// nothing can still claim belongs to the selection.
func (p *Plugin) contentLeafSurface(leafID int) (root, surface string, ok bool) {
	leaf := FindPane(p.paneRoot, leafID)
	if leaf == nil || leaf.Split != nil {
		return "", "", false
	}
	switch leaf.Kind {
	case PaneDoc:
		if doc := p.docs[leaf.ContentID]; doc != nil {
			return doc.root, doc.surface, true
		}
	case PaneIssue:
		if issue := p.issues[leaf.ContentID]; issue != nil {
			return issue.root, issue.surface, true
		}
	}
	return "", "", false
}

func (p *Plugin) resizeDocTerminalCmd() tea.Cmd {
	return tea.Batch(p.docTerminalResizeCmds()...)
}

// docTerminalResizeCmds names the exact resize fan-out for a tree geometry
// change. Keeping it inspectable lets tests prove one command per visible tmux
// surface without executing tmux against the developer's live server.
func (p *Plugin) docTerminalResizeCmds() []tea.Cmd {
	cmds := make([]tea.Cmd, 0, 2)
	if cmd := p.resizeSelectedPaneCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if p.termPanelVisible {
		if cmd := p.resizeTermPanelPaneCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func (p *Plugin) applyDocLoaded(msg docview.LoadedMsg) {
	doc := p.docs[msg.ModelID]
	if doc == nil || p.ctx == nil || msg.Epoch != p.ctx.Epoch {
		return
	}
	root, surface, ok := p.selectedTerminalSurface()
	if !ok || filepath.Clean(doc.root) != root || doc.surface != surface {
		return
	}
	doc.view.SetResult(msg)
}

// docVisible reports whether the pane split is on screen. It asks the tree for
// any content leaf rather than for a document, because a terminal beside a td
// issue is the same split with the same geometry. Diff and Task replace that
// split without clearing paneFocus, so a still-selected content leaf is not the
// keyboard owner while those tabs are showing.
func (p *Plugin) docVisible() bool {
	live := false
	for _, leafID := range p.contentLeafIDs() {
		if _, _, ok := p.contentLeafSurface(leafID); ok {
			live = true
			break
		}
	}
	return live && (p.selectingShell() || p.previewTab == PreviewTabOutput)
}

// previewLeafFocused reports whether a visible content leaf holds the preview's
// keyboard focus, whatever it is showing. The frame reads this — zoom, divider
// styling — because those are properties of a leaf being focused, not of it
// being a document.
func (p *Plugin) previewLeafFocused() bool {
	if !p.docVisible() || p.activePane != PanePreview {
		return false
	}
	leaf := FindPane(p.paneRoot, p.paneFocus)
	return leaf != nil && leaf.Split == nil && leaf.Kind != PaneTerminal
}

// docFocused is the narrower question the document's own keys ask: not "a
// content leaf holds focus" but "the focused leaf is a document". An issue leaf
// answers false here, so nothing routes a document key into it.
func (p *Plugin) docFocused() bool {
	if !p.previewLeafFocused() {
		return false
	}
	leaf := FindPane(p.paneRoot, p.paneFocus)
	return leaf != nil && leaf.Kind == PaneDoc && p.docs[leaf.ContentID] != nil
}

func (p *Plugin) handleDocKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if !p.docFocused() {
		return false, nil
	}
	doc, _ := p.activeDocPane()
	if doc == nil {
		return false, nil
	}
	switch msg.String() {
	case "\\":
		return true, p.toggleSidebarCmd()
	case "q", "esc":
		return true, p.closeDocPane()
	case "m":
		p.toggleDocRenderMode()
		return true, nil
	case "+":
		return true, p.resizeFocusedDoc(5)
	case "-":
		return true, p.resizeFocusedDoc(-5)
	default:
		doc.view.HandleKey(msg)
		// A focused document is its own input context. Absorb keys it does not
		// own so they cannot trigger workspace actions behind the pane.
		return true, nil
	}
}

func (p *Plugin) resizeFocusedDoc(delta int) tea.Cmd {
	_, leaf := p.activeDocPane()
	if leaf == nil {
		return nil
	}
	parent, docInA := enclosingSplit(p.paneRoot, leaf.ID)
	if parent == nil || parent.Split == nil {
		return nil
	}
	if docInA {
		SetRatio(p.paneRoot, parent.ID, parent.Split.Ratio+delta)
	} else {
		SetRatio(p.paneRoot, parent.ID, parent.Split.Ratio-delta)
	}
	p.saveSelectionState()
	return p.resizeDocTerminalCmd()
}

func enclosingSplit(node *PaneNode, leafID int) (*PaneNode, bool) {
	if node == nil || node.Split == nil {
		return nil, false
	}
	if FindPane(node.Split.A, leafID) != nil {
		if node.Split.A.ID == leafID {
			return node, true
		}
		if parent, inA := enclosingSplit(node.Split.A, leafID); parent != nil {
			return parent, inA
		}
	}
	if node.Split.B.ID == leafID {
		return node, false
	}
	return enclosingSplit(node.Split.B, leafID)
}

func (p *Plugin) persistedPaneLayout() *state.PaneLayoutJSON {
	if p.paneRoot == nil {
		return nil
	}
	root, surface, ok := p.selectedTerminalSurface()
	if !ok {
		return nil
	}
	// A content leaf still holding another surface's target is a layout about to
	// be collapsed; persist the terminal alone rather than a document or an issue
	// that will come back attached to the wrong workspace.
	for _, leafID := range p.contentLeafIDs() {
		paneRoot, paneSurface, ok := p.contentLeafSurface(leafID)
		if ok && (filepath.Clean(paneRoot) != root || paneSurface != surface) {
			return &state.PaneLayoutJSON{Root: root, Surface: surface, Kind: contentKindTerminal}
		}
	}
	layout := p.encodePaneNode(p.paneRoot)
	if layout == nil {
		return nil
	}
	layout.Root = root
	layout.Surface = surface
	return layout
}

func (p *Plugin) encodePaneNode(node *PaneNode) *state.PaneLayoutJSON {
	if node == nil {
		return nil
	}
	if node.Split != nil {
		axis := "cols"
		if node.Split.Axis == SplitRows {
			axis = "rows"
		}
		return &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
			Axis: axis, Ratio: clampPaneRatio(node.Split.Ratio),
			A: p.encodePaneNode(node.Split.A), B: p.encodePaneNode(node.Split.B),
		}}
	}
	if node.Kind == PaneTerminal {
		return &state.PaneLayoutJSON{Kind: contentKindTerminal}
	}
	if node.Kind == PaneIssue {
		// The issue ID is the leaf's durable target, the way a path is a
		// document's: restore re-fetches rather than persisting a fetched body
		// that td may have moved on from.
		issue := p.issues[node.ContentID]
		if issue == nil || issue.view == nil || issue.view.IssueID() == "" {
			return nil
		}
		return &state.PaneLayoutJSON{Kind: contentKindIssue, Issue: issue.view.IssueID()}
	}
	doc := p.docs[node.ContentID]
	if doc == nil || doc.view == nil || doc.view.Title() == "" {
		return nil
	}
	mode := "raw"
	if doc.view.Rendered() {
		mode = "rendered"
	}
	return &state.PaneLayoutJSON{Kind: contentKindDoc, Tabs: []state.PaneDocTabJSON{{Path: doc.view.Title(), Mode: mode}}}
}

func (p *Plugin) restorePaneLayout(layout *state.PaneLayoutJSON) tea.Cmd {
	if p.paneRoot == nil || layout == nil || p.ctx == nil {
		return nil
	}
	root, surface, ok := p.selectedTerminalSurface()
	if !ok || filepath.Clean(layout.Root) != root || layout.Surface != surface {
		return nil
	}
	p.docs = make(map[int]*docPane)
	p.issues = make(map[int]*issuePane)
	p.paneNextID = 1
	terminalCount := 0
	var loads []tea.Cmd
	restored := p.decodePaneNode(layout, root, &terminalCount, &loads)
	if restored == nil || terminalCount != 1 || !supportedPaneTree(restored) {
		p.resetPaneTreeToTerminal()
		return nil
	}
	p.paneRoot = restored
	p.paneFocus = terminalLeafID(restored)
	p.paneNextID = maxPaneID(restored) + 1
	return tea.Batch(loads...)
}

// supportedPaneTree accepts any tree whose leaves are kinds this build can
// draw, nested to any depth: the compositor places and clips every leaf the
// layout returns, so the old terminal-beside-one-document restriction was
// refusing shapes it could already render — the steel thread's terminal beside
// a stacked document and issue among them. What it still refuses is a leaf of
// an unknown kind, which a forward or hand-edited layout can carry and which
// would drive terminal geometry for a box nothing draws into.
//
// Exactly one terminal leaf remains the decoder's rule, counted there.
func supportedPaneTree(root *PaneNode) bool {
	if root == nil {
		return false
	}
	if root.Split == nil {
		switch root.Kind {
		case PaneTerminal, PaneDoc, PaneIssue:
			return true
		default:
			return false
		}
	}
	return root.Split.A != nil && root.Split.B != nil &&
		supportedPaneTree(root.Split.A) && supportedPaneTree(root.Split.B)
}

func (p *Plugin) decodePaneNode(saved *state.PaneLayoutJSON, root string, terminalCount *int, loads *[]tea.Cmd) *PaneNode {
	if saved == nil {
		return nil
	}
	if saved.Split != nil {
		axis := SplitCols
		switch saved.Split.Axis {
		case "cols":
		case "rows":
			axis = SplitRows
		default:
			return nil
		}
		a := p.decodePaneNode(saved.Split.A, root, terminalCount, loads)
		b := p.decodePaneNode(saved.Split.B, root, terminalCount, loads)
		if a == nil {
			return b
		}
		if b == nil {
			return a
		}
		id := p.nextPaneID()
		return &PaneNode{ID: id, Split: &PaneSplit{Axis: axis, Ratio: clampPaneRatio(saved.Split.Ratio), A: a, B: b}}
	}
	switch saved.Kind {
	case contentKindTerminal:
		*terminalCount++
		if *terminalCount > 1 {
			return nil
		}
		return &PaneNode{ID: p.nextPaneID(), Kind: PaneTerminal}
	case contentKindDoc:
		if len(saved.Tabs) == 0 {
			return nil
		}
		active := clampInt(saved.Active, 0, len(saved.Tabs)-1)
		ordered := append([]state.PaneDocTabJSON{saved.Tabs[active]}, saved.Tabs[:active]...)
		ordered = append(ordered, saved.Tabs[active+1:]...)
		for _, tab := range ordered {
			rel, _, valid := resolveTerminalPath(root, tab.Path)
			// ResolveFile may accept a file outside root, reporting it as an
			// absolute display path. A restored layout only ever addresses the
			// viewer with a root-relative path, so an escaping tab is dropped
			// rather than joined onto root as if it were relative.
			if !valid || filepath.IsAbs(rel) {
				continue
			}
			id := p.nextPaneID()
			viewer := docview.New(nil)
			load := viewer.Load(id, root, filepath.ToSlash(rel), 0, p.ctx.Epoch)
			viewer.SetRendered(tab.Mode != "raw")
			p.docs[id] = &docPane{leafID: id, root: root, surface: savedRootSurface(p, root), view: viewer}
			*loads = append(*loads, load)
			return &PaneNode{ID: id, Kind: PaneDoc, ContentID: id}
		}
	case contentKindIssue:
		// An issue leaf with no durable target is dropped, and the collapse in
		// the split above gives its box back to its sibling: one unreadable leaf
		// costs its own pane, never the whole layout. Whether td still knows the
		// issue is the fetch's answer, arriving as this leaf's "Issue
		// unavailable" body rather than as a reason to reset the tree.
		issueID := strings.TrimSpace(saved.Issue)
		if issueID == "" {
			return nil
		}
		id := p.nextPaneID()
		if load := p.attachIssuePane(id, root, savedRootSurface(p, root), issueID); load != nil {
			*loads = append(*loads, load)
		}
		return &PaneNode{ID: id, Kind: PaneIssue, ContentID: id}
	}
	return nil
}

func savedRootSurface(p *Plugin, root string) string {
	selectedRoot, surface, ok := p.selectedTerminalSurface()
	if !ok || selectedRoot != root {
		return ""
	}
	return surface
}

func (p *Plugin) nextPaneID() int {
	id := p.paneNextID
	p.paneNextID++
	return id
}

func (p *Plugin) resetPaneTreeToTerminal() {
	p.docs = make(map[int]*docPane)
	p.issues = make(map[int]*issuePane)
	p.paneNextID = 1
	p.paneRoot = &PaneNode{ID: p.nextPaneID(), Kind: PaneTerminal}
	p.paneFocus = p.paneRoot.ID
}

func (p *Plugin) cycleDocumentFocus(reverse bool) {
	_, docLeaf := p.activeDocPane()
	if docLeaf == nil {
		return
	}
	terminalID := terminalLeafID(p.paneRoot)
	switch {
	case p.activePane == PaneSidebar:
		p.activePane = PanePreview
		if reverse {
			p.paneFocus = docLeaf.ID
		} else {
			p.paneFocus = terminalID
		}
	case p.paneFocus == docLeaf.ID:
		if reverse || !p.sidebarVisible {
			p.paneFocus = terminalID
		} else {
			p.activePane = PaneSidebar
		}
	default:
		if reverse && p.sidebarVisible {
			p.activePane = PaneSidebar
		} else {
			p.activePane = PanePreview
			p.paneFocus = docLeaf.ID
		}
	}
	p.termPanelFocused = false
}

// docHeaderChips renders the doc leaf's header chips. title is the content's
// own answer rather than a second read of the viewer, so the row the frame
// draws and the regions it registers cannot name the leaf differently.
func (p *Plugin) docHeaderChips(doc *docPane, title string, width int, focused bool) []string {
	// Keep each chip whole so the shared header layout can drop it cleanly at
	// narrow widths. Bound the path before styling so a deep path does not crowd
	// out the mode and close affordances.
	pathBudget := maxInt(width/2, 8)
	path := p.truncateCache.Truncate(title, pathBudget, "…")
	pathStyle := styles.BarChip
	if focused {
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

// docPaneHeaderRow is the doc leaf's header row, drawn above the viewer rather
// than by it. focused is the frame's answer, so the chip a click lands on and
// the chip the leaf drew cannot disagree about which leaf holds focus.
func (p *Plugin) docPaneHeaderRow(doc *docPane, title string, width int, focused bool) string {
	action := "raw"
	if !doc.view.Rendered() {
		action = "render"
	}
	return p.terminalHeader(p.docHeaderChips(doc, title, width, focused), dimText("q close · m "+action), width, 0)
}

func (p *Plugin) toggleDocRenderMode() {
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view == nil {
		return
	}
	doc.view.ToggleRenderMode()
	p.saveSelectionState()
}

func paneTreeDividerStyle(focused bool) lipgloss.Style {
	if focused {
		return lipgloss.NewStyle().Foreground(styles.BorderActive)
	}
	return lipgloss.NewStyle().Foreground(styles.BorderNormal)
}

func renderPaneTreeDividerV(height int, focused bool) string {
	if height <= 0 {
		return ""
	}
	return paneTreeDividerStyle(focused).Render(
		strings.TrimSuffix(strings.Repeat("│\n", height), "\n"))
}

func renderPaneTreeDividerH(width int, focused bool) string {
	return paneTreeDividerStyle(focused).Render(strings.Repeat("─", maxInt(width, 0)))
}

func (p *Plugin) registerDocPaneRegions(doc *docPane, title string, leafID int, box Box) {
	p.mouseHandler.HitMap.AddRect(regionDocPane, box.X, box.Y, box.W, box.H, leafID)
	chips := p.docHeaderChips(doc, title, box.W, p.paneFocus == leafID)
	for index, chip := range layoutHeaderChips(chips, box.W, 0) {
		if !chip.Drawn {
			continue
		}
		switch index {
		case len(chips) - 2:
			p.mouseHandler.HitMap.AddRect(regionDocMode, box.X+chip.Col, box.Y, chip.Width, 1, leafID)
		case len(chips) - 1:
			p.mouseHandler.HitMap.AddRect(regionDocClose, box.X+chip.Col, box.Y, chip.Width, 1, leafID)
		}
	}
}

func (p *Plugin) renderDocumentSplit(width, height int) (string, bool) {
	if !p.docVisible() {
		return "", false
	}
	layout, ok := LayoutPaneTree(p.paneRoot, Box{W: width, H: height}, paneTreeFloors(), p.paneFocus)
	if !ok {
		return "", false
	}
	origin, placed := p.previewContentBox()
	canvas := ui.NewCanvas(width, height)
	if layout.Zoomed {
		// The zoomed leaf is drawn from here only when it is content the preview
		// owns: a terminal leaf, or a content leaf still holding paneFocus while
		// the sidebar has the keyboard, is the legacy renderer's box.
		if !p.previewLeafFocused() {
			return "", false
		}
		zoomed := layout.Leaves[0]
		if placed {
			p.registerPaneLeafRegions(zoomed.Node,
				Box{X: origin.X, Y: origin.Y, W: zoomed.Box.W, H: zoomed.Box.H})
		}
		// One leaf is still composed, not returned: the clip-and-pad the
		// compositor guarantees is what makes the leaf's box the leaf's box, and
		// a lone leaf that keeps its own shape is the one placement nothing
		// holds to it.
		canvas.Blit(zoomed.Box, p.renderPaneLeaf(zoomed, origin, true))
		return canvas.String(), true
	}

	// Every leaf and divider is drawn onto the box LayoutPanes gave it. Joining
	// the blocks back together instead would re-derive that geometry in string
	// space at every level of nesting, and the levels only have to disagree by a
	// cell for a divider to walk sideways.
	for _, placement := range layout.Leaves {
		canvas.Blit(placement.Box, p.renderPaneLeaf(placement, origin, false))
	}
	for _, split := range layout.Dividers {
		canvas.Blit(split.Box, p.renderPaneTreeDivider(split))
	}
	p.registerPaneTreeRegions(layout.Leaves, layout.Dividers)
	return canvas.String(), true
}

// renderPaneLeaf draws one placed leaf through the content contract, so the
// compose path never asks what kind of leaf it is drawing. A leaf with no
// content draws nothing and the canvas leaves its box blank, rather than
// shifting its neighbours into it.
//
// origin is the preview content box, which turns the leaf's box into the
// plugin-local rectangle a pointer is tested against.
//
// Sizing inside a frame is what the document viewer already required, and both
// contents answer nil: a live terminal is resized from the state change that
// moved its box, not from a render. A content that does answer one is asserting
// geometry beyond this process, and a render has nothing to dispatch it with, so
// the frame holds it for the next update rather than dropping it — the earliest
// a frame-time answer can reach the runtime. The first content that answers one
// in production moves sizing ahead of the frame entirely.
func (p *Plugin) renderPaneLeaf(placement Placement, origin Box, zoomed bool) string {
	content := p.paneContent(placement.Node)
	if content == nil {
		return ""
	}
	p.sizePaneContent(content, Size{Width: placement.Box.W, Height: placement.Box.H})
	return content.View(Render{
		Focused: p.paneFocus == placement.Node.ID,
		Zoomed:  zoomed,
		Origin: Box{
			X: origin.X + placement.Box.X, Y: origin.Y + placement.Box.Y,
			W: placement.Box.W, H: placement.Box.H,
		},
	})
}

// sizePaneContent gives a content its box and keeps what it answered. A command
// here is a content asserting geometry it owns beyond this process, and the
// render path has no runtime to dispatch one with, so it is queued for the next
// update instead of discarded.
func (p *Plugin) sizePaneContent(content Content, size Size) {
	if cmd := content.SetSize(size); cmd != nil {
		p.paneSizeCmds = append(p.paneSizeCmds, cmd)
	}
}

// takePaneSizeCmds empties the queue as it hands it over: a geometry assertion
// dispatched on every update after the render that made it is a resize storm.
func (p *Plugin) takePaneSizeCmds() []tea.Cmd {
	cmds := p.paneSizeCmds
	p.paneSizeCmds = nil
	return cmds
}

func (p *Plugin) renderPaneTreeDivider(split Divider) string {
	if split.Axis == SplitRows {
		return renderPaneTreeDividerH(split.Box.W, p.previewLeafFocused())
	}
	return renderPaneTreeDividerV(split.Box.H, p.previewLeafFocused())
}

// registerPaneLeafRegions registers one placed leaf's hit regions, in
// plugin-local coordinates. The title a region is registered under is the
// content's own, the same answer the header row was drawn from, so a chip
// cannot be hit-tested at a width it was never rendered at. A terminal leaf
// registers nothing here: its regions belong to the legacy renderer inside it.
func (p *Plugin) registerPaneLeafRegions(node *PaneNode, box Box) {
	if node == nil || node.Split != nil {
		return
	}
	content := p.paneContent(node)
	if content == nil {
		return
	}
	switch node.Kind {
	case PaneDoc:
		if doc := p.docs[node.ContentID]; doc != nil {
			p.registerDocPaneRegions(doc, content.Title(), node.ID, box)
		}
	case PaneIssue:
		p.registerIssuePaneRegions(content.Title(), node.ID, box)
	}
}

// registerPaneTreeRegions registers hit regions from the same placements the
// canvas drew from, so a click cannot land on geometry the frame did not draw.
func (p *Plugin) registerPaneTreeRegions(leaves []Placement, dividers []Divider) {
	absolute, ok := p.previewContentBox()
	if !ok {
		return
	}
	for _, placement := range leaves {
		p.registerPaneLeafRegions(placement.Node, Box{
			X: absolute.X + placement.Box.X, Y: absolute.Y + placement.Box.Y,
			W: placement.Box.W, H: placement.Box.H,
		})
	}
	// Dividers arrive in LayoutPanes' order, each split before the splits inside
	// it, and three-cell hit targets overlap once splits nest. Two targets can
	// only overlap when one split encloses the other, because sibling subtrees
	// are held apart by the divider between them — so registering the enclosing
	// split first is what leaves the enclosed one last, and HitMap.Test's
	// reverse scan returns it for a point both claim.
	for _, split := range dividers {
		hit := paneDividerHitBox(split)
		p.mouseHandler.HitMap.AddRect(regionPaneTreeDivider,
			absolute.X+hit.X, absolute.Y+hit.Y, hit.W, hit.H, split.SplitID)
	}
}

// paneDividerHitBox widens a divider's one-cell box into the target a pointer is
// tested against, in the tree's own coordinates. A divider is a cell wide and a
// drag has to be startable on it, so the target reaches one cell into the leaf
// before it.
func paneDividerHitBox(split Divider) Box {
	hit := split.Box
	if split.Axis == SplitCols {
		hit.W = dividerHitWidth
		hit.X--
	} else {
		hit.H = dividerHitWidth
		hit.Y--
	}
	return hit
}
