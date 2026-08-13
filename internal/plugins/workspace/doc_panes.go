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

func (p *Plugin) selectedTerminalRoot() (string, bool) {
	root, _, ok := p.selectedTerminalSurface()
	return root, ok
}

// selectedTerminalSurface identifies the actual terminal selection, not only
// its filesystem root. Project shells deliberately share ctx.WorkDir, so the
// tmux name is required to distinguish shell A from shell B.
func (p *Plugin) selectedTerminalSurface() (root, identity string, ok bool) {
	if p.ctx == nil {
		return "", "", false
	}
	root = p.ctx.WorkDir
	if p.shellSelected {
		shell := p.getSelectedShell()
		if shell == nil || shell.TmuxName == "" {
			return "", "", false
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
		if leaf := FindPane(p.paneRoot, doc.leafID); leaf != nil && leaf.Kind == PaneDoc && leaf.DocID == id {
			return doc, leaf
		}
	}
	return nil, nil
}

func paneTreeFloors() Floors {
	return Floors{
		Terminal: PaneFloor{Width: termPanelMinBoxCols, Height: termPanelMinBoxRows},
		Doc:      PaneFloor{Width: markdown.MinWidthForMarkdown, Height: termPanelMinBoxRows},
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
			cmd = doc.view.LoadFile(leaf.DocID, file, rel, line, epoch)
			file = nil
		} else {
			cmd = doc.view.Load(leaf.DocID, root, rel, line, epoch)
		}
		applyDocRenderMode(doc.view, rel, line)
		p.saveSelectionState()
		return cmd
	}

	docID := p.paneNextID
	newLeaf := &PaneNode{ID: docID, Kind: PaneDoc, DocID: docID}
	trial := clonePaneTree(p.paneRoot)
	trialDoc := &PaneNode{ID: docID, Kind: PaneDoc, DocID: docID}
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
	if root == nil {
		return 0
	}
	if root.Split == nil {
		if root.Kind == PaneTerminal {
			return root.ID
		}
		return 0
	}
	if id := terminalLeafID(root.Split.A); id != 0 {
		return id
	}
	return terminalLeafID(root.Split.B)
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
	leaf := FindPane(p.paneRoot, doc.leafID)
	delete(p.docs, leaf.DocID)
	p.paneRoot, p.paneFocus = ClosePane(p.paneRoot, leaf.ID)
	return true
}

func (p *Plugin) resetDocPanesForSelection() bool {
	doc, _ := p.activeDocPane()
	if doc == nil {
		return false
	}
	root, surface, ok := p.selectedTerminalSurface()
	if ok && filepath.Clean(doc.root) == root && doc.surface == surface {
		return false
	}
	return p.closeDocPaneState()
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

// docVisible reports whether the document split is on screen. Diff and Task
// replace that split without clearing paneFocus, so a still-selected doc leaf
// is not the keyboard owner while those tabs are showing.
func (p *Plugin) docVisible() bool {
	doc, _ := p.activeDocPane()
	return doc != nil && (p.shellSelected || p.previewTab == PreviewTabOutput)
}

func (p *Plugin) docFocused() bool {
	if !p.docVisible() {
		return false
	}
	leaf := FindPane(p.paneRoot, p.paneFocus)
	return p.activePane == PanePreview && leaf != nil && leaf.Split == nil && leaf.Kind == PaneDoc
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
	if doc, _ := p.activeDocPane(); doc != nil && (filepath.Clean(doc.root) != root || doc.surface != surface) {
		return &state.PaneLayoutJSON{Root: root, Surface: surface, Kind: "terminal"}
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
		return &state.PaneLayoutJSON{Kind: "terminal"}
	}
	doc := p.docs[node.DocID]
	if doc == nil || doc.view == nil || doc.view.Title() == "" {
		return nil
	}
	mode := "raw"
	if doc.view.Rendered() {
		mode = "rendered"
	}
	return &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: doc.view.Title(), Mode: mode}}}
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
	p.paneNextID = 1
	terminalCount := 0
	var loads []tea.Cmd
	restored := p.decodePaneNode(layout, root, &terminalCount, &loads)
	if restored == nil || terminalCount != 1 || !supportedDocPaneTree(restored) {
		p.resetPaneTreeToTerminal()
		return nil
	}
	p.paneRoot = restored
	p.paneFocus = terminalLeafID(restored)
	p.paneNextID = maxPaneID(restored) + 1
	return tea.Batch(loads...)
}

// Phase 4 can render the shipped terminal-plus-one-document journey. The JSON
// remains structural for future leaves, but forward or hand-edited nested trees
// must not silently drive terminal geometry that the renderer cannot display.
func supportedDocPaneTree(root *PaneNode) bool {
	if root == nil {
		return false
	}
	if root.Split == nil {
		return root.Kind == PaneTerminal
	}
	if root.Split.A == nil || root.Split.B == nil || root.Split.A.Split != nil || root.Split.B.Split != nil {
		return false
	}
	return (root.Split.A.Kind == PaneTerminal && root.Split.B.Kind == PaneDoc) ||
		(root.Split.A.Kind == PaneDoc && root.Split.B.Kind == PaneTerminal)
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
	case "terminal":
		*terminalCount++
		if *terminalCount > 1 {
			return nil
		}
		return &PaneNode{ID: p.nextPaneID(), Kind: PaneTerminal}
	case "doc":
		if len(saved.Tabs) == 0 {
			return nil
		}
		active := clampInt(saved.Active, 0, len(saved.Tabs)-1)
		ordered := append([]state.PaneDocTabJSON{saved.Tabs[active]}, saved.Tabs[:active]...)
		ordered = append(ordered, saved.Tabs[active+1:]...)
		for _, tab := range ordered {
			rel, _, valid := resolveTerminalPath(root, tab.Path)
			if !valid {
				continue
			}
			id := p.nextPaneID()
			viewer := docview.New(nil)
			load := viewer.Load(id, root, filepath.ToSlash(rel), 0, p.ctx.Epoch)
			viewer.SetRendered(tab.Mode != "raw")
			p.docs[id] = &docPane{leafID: id, root: root, surface: savedRootSurface(p, root), view: viewer}
			*loads = append(*loads, load)
			return &PaneNode{ID: id, Kind: PaneDoc, DocID: id}
		}
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

func (p *Plugin) docHeaderChips(doc *docPane, width int) []string {
	// Keep each chip whole so the shared header layout can drop it cleanly at
	// narrow widths. Bound the path before styling so a deep path does not crowd
	// out the mode and close affordances.
	pathBudget := maxInt(width/2, 8)
	path := p.truncateCache.Truncate(doc.view.Title(), pathBudget, "…")
	pathStyle := styles.BarChip
	if p.paneFocus == doc.leafID {
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

func (p *Plugin) renderDocPane(doc *docPane, box Box) string {
	contentHeight := maxInt(box.H-terminalHeaderRows, 0)
	doc.view.SetSize(box.W, contentHeight)
	action := "raw"
	if !doc.view.Rendered() {
		action = "render"
	}
	header := p.terminalHeader(p.docHeaderChips(doc, box.W), dimText("q close · m "+action), box.W, 0)
	return header + "\n" + doc.view.View()
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

func (p *Plugin) registerDocPaneRegions(doc *docPane, leafID int, box Box) {
	p.mouseHandler.HitMap.AddRect(regionDocPane, box.X, box.Y, box.W, box.H, leafID)
	chips := p.docHeaderChips(doc, box.W)
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
	doc, _ := p.activeDocPane()
	leaves, dividers, fits := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	if !fits {
		if p.docFocused() {
			if absolute, ok := p.previewContentBox(); ok {
				p.registerDocPaneRegions(doc, doc.leafID, Box{X: absolute.X, Y: absolute.Y, W: width, H: height})
			}
			return p.renderDocPane(doc, Box{W: width, H: height}), true
		}
		return "", false
	}
	if len(leaves) != 2 || len(dividers) != 1 {
		return "", false
	}
	var terminal, document string
	for _, placement := range leaves {
		switch placement.Node.Kind {
		case PaneTerminal:
			terminal = p.renderPreviewContentLegacy(placement.Box.W, placement.Box.H)
		case PaneDoc:
			document = p.renderDocPane(doc, placement.Box)
			if absolute, ok := p.previewContentBox(); ok {
				p.registerDocPaneRegions(doc, placement.Node.ID, Box{
					X: absolute.X + placement.Box.X, Y: absolute.Y + placement.Box.Y,
					W: placement.Box.W, H: placement.Box.H,
				})
			}
		}
	}
	if absolute, ok := p.previewContentBox(); ok {
		for _, split := range dividers {
			hit := split.Box
			if split.Axis == SplitCols {
				hit.W = dividerHitWidth
				hit.X--
			} else {
				hit.H = dividerHitWidth
				hit.Y--
			}
			p.mouseHandler.HitMap.AddRect(regionPaneTreeDivider,
				absolute.X+hit.X, absolute.Y+hit.Y, hit.W, hit.H, split.SplitID)
		}
	}
	if dividers[0].Axis == SplitRows {
		divider := renderPaneTreeDividerH(width, p.docFocused())
		if leaves[0].Node.Kind == PaneTerminal {
			return lipgloss.JoinVertical(lipgloss.Left, terminal, divider, document), true
		}
		return lipgloss.JoinVertical(lipgloss.Left, document, divider, terminal), true
	}
	divider := renderPaneTreeDividerV(height, p.docFocused())
	if leaves[0].Node.Kind == PaneTerminal {
		return lipgloss.JoinHorizontal(lipgloss.Top, terminal, divider, document), true
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, document, divider, terminal), true
}
