package workspace

import (
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/ui"
)

type docPane struct {
	leafID int
	root   string
	view   *docview.Model
}

func docPaneTarget(rel string, resolvedInsideSelectedSurface bool) bool {
	if !resolvedInsideSelectedSurface {
		return false
	}
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}

func (p *Plugin) selectedTerminalRoot() (string, bool) {
	if p.ctx == nil {
		return "", false
	}
	root := p.ctx.WorkDir
	if !p.shellSelected {
		wt := p.selectedWorktree()
		if wt == nil {
			return "", false
		}
		root = wt.Path
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	return filepath.Clean(resolved), true
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
	if p.paneRoot == nil || p.ctx == nil {
		return nil
	}
	epoch := p.ctx.Epoch
	if doc, leaf := p.activeDocPane(); doc != nil {
		doc.root = root
		p.paneFocus = leaf.ID
		p.activePane = PanePreview
		return doc.view.Load(leaf.DocID, root, rel, line, epoch)
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
		p.toastMessage = "Window is too narrow to open a document pane"
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
	p.docs[docID] = &docPane{leafID: p.paneFocus, root: root, view: viewer}
	p.activePane = PanePreview
	return tea.Batch(viewer.Load(docID, root, rel, line, epoch), p.resizeDocTerminalCmd())
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
	root, ok := p.selectedTerminalRoot()
	if ok && filepath.Clean(doc.root) == root {
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
	root, ok := p.selectedTerminalRoot()
	if !ok || filepath.Clean(doc.root) != root {
		return
	}
	doc.view.SetResult(msg)
}

func (p *Plugin) docFocused() bool {
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
	case "q", "esc":
		return true, p.closeDocPane()
	case "r":
		doc.view.ToggleRenderMode()
		return true, nil
	default:
		doc.view.HandleKey(msg)
		// A focused document is its own input context. Absorb keys it does not
		// own so they cannot trigger workspace actions behind the pane.
		return true, nil
	}
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

func (p *Plugin) renderDocPane(doc *docPane, box Box) string {
	contentHeight := maxInt(box.H-terminalHeaderRows, 0)
	doc.view.SetSize(box.W, contentHeight)
	header := p.terminalHeader([]string{p.paneFocusChip(doc.view.Title(), p.paneFocus == doc.leafID)}, dimText("q close · r raw"), box.W, 0)
	return header + "\n" + doc.view.View()
}

func (p *Plugin) renderDocumentSplit(width, height int) (string, bool) {
	doc, _ := p.activeDocPane()
	if doc == nil || !(p.shellSelected || p.previewTab == PreviewTabOutput) {
		return "", false
	}
	leaves, dividers, fits := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	if !fits {
		if p.docFocused() {
			p.mouseHandler.HitMap.AddRect(regionDocPane, p.previewSplit().ContentX, p.previewContentY(), width, height, doc.leafID)
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
				p.mouseHandler.HitMap.AddRect(regionDocPane, absolute.X+placement.Box.X, absolute.Y+placement.Box.Y, placement.Box.W, placement.Box.H, placement.Node.ID)
			}
		}
	}
	divider := ui.RenderDivider(height)
	return lipgloss.JoinHorizontal(lipgloss.Top, terminal, divider, document), true
}
