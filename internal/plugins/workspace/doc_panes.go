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
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/ui"
)

// docPane is one document leaf's tab group. The pane tree points at this, not
// at a single model.
type docPane struct {
	leafID  int
	root    string
	surface string
	tabs    docview.Tabs

	// mode is the search surface this pane is showing over its document, or nil.
	// It is rooted at this pane's own root, which is what makes the same code
	// serve project and global Workspaces.
	mode *docSearchMode
	// modeRegions are the surface's hit regions from the last render, already at
	// their true positions. They are registered after the pane tree's own, so a
	// click inside the modal is not taken by the leaf drawn under it.
	modeRegions []mouse.Region
	// boxW and boxH are the box the leaf was last given, so a surface that sizes
	// itself on input rather than on render has an answer before the first frame.
	// boxX and boxY place that box, which is what a click-away test needs.
	boxW, boxH, boxX, boxY int
}

// boxContains reports whether a plugin-local point is inside the pane's last
// drawn box. A pane that has not been drawn contains nothing.
func (d *docPane) boxContains(x, y int) bool {
	if d == nil || d.boxW <= 0 || d.boxH <= 0 {
		return false
	}
	return x >= d.boxX && x < d.boxX+d.boxW && y >= d.boxY && y < d.boxY+d.boxH
}

func newDocPane(leafID int, root, surface string, view *docview.Model) *docPane {
	d := &docPane{leafID: leafID, root: root, surface: surface}
	if view != nil {
		d.tabs.Append(view)
	}
	return d
}

func (d *docPane) view() *docview.Model {
	if d == nil {
		return nil
	}
	return d.tabs.ActiveView()
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
		identity = workspaceSurfaceIdentity(wt)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", false
	}
	return filepath.Clean(resolved), identity, true
}

func workspaceSurfaceIdentity(wt *Worktree) string {
	if wt == nil {
		return ""
	}
	key := wt.IdentityKey()
	if wt.Key == "" {
		if canonical, err := projectdir.WorktreeKey(wt.Path); err == nil {
			key = canonical
		}
	}
	if key == "" {
		key = stablePathKey(wt.Path)
	}
	return "workspace:" + key
}

func legacyWorkspaceSurfaceIdentity(wt *Worktree) string {
	if wt == nil || wt.Path == "" {
		return ""
	}
	return "workspace:" + stablePathKey(wt.Path)
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

func (p *Plugin) activeDocPaneOrNil() *docPane {
	doc, _ := p.activeDocPane()
	return doc
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
	rel = docview.NormalizeTabPath(rel)
	if rel == "" || rel == "." {
		return nil
	}
	reopen := p.reopenHiddenDocPane()
	epoch := p.ctx.Epoch
	plan, planned := planPaneOpen(p.paneRoot, PaneDoc)
	if !planned {
		return reopen
	}
	if plan.Retarget != 0 {
		leaf := FindPane(p.paneRoot, plan.Retarget)
		if leaf == nil {
			return reopen
		}
		doc := p.docs[leaf.ContentID]
		if doc == nil {
			return reopen
		}
		doc.root = root
		doc.surface = surface
		p.paneFocus = leaf.ID
		p.activePane = PanePreview
		cmd, consumed := p.docPaneLoadTab(doc, leaf.ContentID, rel, line, file, false)
		if consumed {
			file = nil
		}
		p.saveSelectionState()
		return tea.Batch(reopen, cmd)
	}

	docID := p.paneNextID
	newLeaf := &PaneNode{ID: docID, Kind: PaneDoc, ContentID: docID}
	trial := clonePaneTree(p.paneRoot)
	trialDoc := &PaneNode{ID: docID, Kind: PaneDoc, ContentID: docID}
	trial, trialFocus := SplitLeaf(trial, plan.Split, plan.Axis, trialDoc)
	if trialFocus != trialDoc.ID {
		return reopen
	}
	if content, ok := p.previewContentBox(); !ok {
		return reopen
	} else if _, _, fits := LayoutPanes(trial, content, paneTreeFloors()); !fits {
		p.toastMessage = paneFitMessage("Document", plan.Axis)
		p.toastTime = time.Now()
		return reopen
	}

	treeRoot, focus := SplitLeaf(p.paneRoot, plan.Split, plan.Axis, newLeaf)
	if focus != newLeaf.ID {
		return reopen
	}
	p.paneRoot, p.paneFocus = treeRoot, focus
	p.paneNextID = maxInt(p.paneNextID, maxPaneID(p.paneRoot)+1)
	viewer := docview.New(nil)
	var load tea.Cmd
	if file != nil {
		load = viewer.LoadFile(docID, file, rel, line, epoch)
		file = nil
	} else {
		load = viewer.Load(docID, root, rel, line, epoch)
	}
	applyDocRenderMode(viewer, rel, line)
	p.docs[docID] = newDocPane(p.paneFocus, root, surface, viewer)
	p.activePane = PanePreview
	p.saveSelectionState()
	return tea.Batch(reopen, load, p.resizeDocTerminalCmd())
}

// docPaneLoadTab puts rel at line into an existing pane and reports whether it
// consumed file. An already-open path is selected rather than opened twice.
// replaceActive swaps the active tab's document instead of appending one, which
// is what a plain pick in the pane's own search does; a click on a path in the
// terminal appends, as it always has.
//
// This is the one path a document enters a pane by, so a caller cannot open a
// file in a way that skips the tab bookkeeping.
func (p *Plugin) docPaneLoadTab(doc *docPane, modelID int, rel string, line int, file *os.File, replaceActive bool) (tea.Cmd, bool) {
	if doc == nil || p.ctx == nil {
		return nil, false
	}
	rel = docview.NormalizeTabPath(rel)
	if rel == "" || rel == "." {
		return nil, false
	}
	if idx := doc.tabs.IndexOf(rel); idx >= 0 {
		return p.selectDocTab(doc, modelID, idx, line, file)
	}
	viewer := docview.New(nil)
	var cmd tea.Cmd
	consumed := false
	if file != nil {
		cmd = viewer.LoadFile(modelID, file, rel, line, p.ctx.Epoch)
		consumed = true
	} else {
		cmd = viewer.Load(modelID, doc.root, rel, line, p.ctx.Epoch)
	}
	applyDocRenderMode(viewer, rel, line)
	if replaceActive && doc.view() != nil {
		doc.tabs.Items[doc.tabs.Active].View = viewer
	} else {
		doc.tabs.Append(viewer)
	}
	return cmd, consumed
}

func (p *Plugin) selectDocTab(doc *docPane, modelID, idx, line int, file *os.File) (tea.Cmd, bool) {
	if doc == nil {
		return nil, false
	}
	p.closeDocInfo()
	doc.tabs.Select(idx)
	view := doc.view()
	if view == nil {
		return nil, false
	}
	if !view.NeedsLoad() {
		if line > 0 {
			view.ApplyLine(line)
		}
		return nil, false
	}
	if p.ctx == nil {
		return nil, false
	}
	rel := view.Title()
	rendered := view.Rendered()
	wrap := view.Wrap()
	var cmd tea.Cmd
	consumed := false
	if file != nil {
		cmd = view.LoadFile(modelID, file, rel, line, p.ctx.Epoch)
		consumed = true
	} else {
		cmd = view.Load(modelID, doc.root, rel, line, p.ctx.Epoch)
	}
	if line > 0 {
		applyDocRenderMode(view, rel, line)
	} else {
		view.SetRendered(rendered)
	}
	view.SetWrap(wrap)
	return cmd, consumed
}

func (p *Plugin) closeActiveDocTab() tea.Cmd {
	doc, _ := p.activeDocPane()
	if doc == nil {
		return nil
	}
	if len(doc.tabs.Items) <= 1 {
		return p.closeDocPane()
	}
	p.closeDocInfo()
	doc.tabs.CloseActive()
	p.saveSelectionState()
	return p.ensureActiveDocTabLoaded(doc)
}

// clickDocTabAt selects a file tab from a pointer position. The Files plugin
// does this by testing the tab row first, because the preview pane region
// covers the header and a one-cell miss becomes a terminal click. The same
// steal happens here (plus the widened pane-tree divider), so a click on the
// exact document header row picks the tab under X, or the closest tab on that
// row. X is constrained to the document leaf so the
// terminal header that shares the row keeps Output/Diff/Task.
func (p *Plugin) clickDocTabAt(x, y int) (tea.Cmd, bool) {
	if !p.docVisible() {
		return nil, false
	}
	var tabs []mouse.Region
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionDocTab {
			continue
		}
		if y != region.Rect.Y {
			continue
		}
		tabs = append(tabs, region)
	}
	if len(tabs) == 0 {
		return nil, false
	}
	inDocHeader := false
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionPaneLeaf {
			continue
		}
		// Content leaves share one region, so the tree says which of them the
		// row belongs to: only a document's header row carries file tabs.
		leafID, ok := region.Data.(int)
		if !ok {
			continue
		}
		if leaf := FindPane(p.paneRoot, leafID); leaf == nil || leaf.Kind != PaneDoc {
			continue
		}
		if x >= region.Rect.X && x < region.Rect.X+region.Rect.W && y == region.Rect.Y {
			inDocHeader = true
			break
		}
	}
	if !inDocHeader {
		return nil, false
	}
	best := tabs[0]
	bestDist := tabRowDistance(x, best.Rect)
	for _, region := range tabs[1:] {
		if d := tabRowDistance(x, region.Rect); d < bestDist {
			best, bestDist = region, d
		}
	}
	return p.clickDocTab(best.Data), true
}

func tabRowDistance(x int, r mouse.Rect) int {
	if x < r.X {
		return r.X - x
	}
	if x >= r.X+r.W {
		return x - (r.X + r.W) + 1
	}
	return 0
}

func (p *Plugin) clickDocTab(data any) tea.Cmd {
	hit, ok := data.(docTabHit)
	if !ok {
		return nil
	}
	leaf := FindPane(p.paneRoot, hit.LeafID)
	if leaf == nil || leaf.Kind != PaneDoc {
		return nil
	}
	doc := p.docs[leaf.ContentID]
	if doc == nil {
		return nil
	}
	p.activePane = PanePreview
	p.paneFocus = hit.LeafID
	p.termPanelFocused = false
	p.pointer.Abandon()
	if p.viewMode == ViewModeInteractive {
		p.exitInteractiveMode()
	}
	if hit.Index == doc.tabs.Active {
		return nil
	}
	cmd, _ := p.selectDocTab(doc, leaf.ContentID, hit.Index, 0, nil)
	p.saveSelectionState()
	return cmd
}

func (p *Plugin) cycleActiveDocTab(delta int) tea.Cmd {
	doc, _ := p.activeDocPane()
	if doc == nil || len(doc.tabs.Items) < 2 {
		return nil
	}
	p.closeDocInfo()
	doc.tabs.Cycle(delta)
	p.saveSelectionState()
	return p.ensureActiveDocTabLoaded(doc)
}

func (p *Plugin) ensureActiveDocTabLoaded(doc *docPane) tea.Cmd {
	if doc == nil || p.ctx == nil {
		return nil
	}
	view := doc.view()
	if view == nil || !view.NeedsLoad() {
		return nil
	}
	leaf := FindPane(p.paneRoot, doc.leafID)
	if leaf == nil {
		return nil
	}
	rendered := view.Rendered()
	wrap := view.Wrap()
	cmd := view.Load(leaf.ContentID, doc.root, view.Title(), 0, p.ctx.Epoch)
	view.SetRendered(rendered)
	view.SetWrap(wrap)
	return cmd
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
	p.closeDocInfo()
	if !p.closeDocPaneState() {
		return nil
	}
	p.hiddenPaneLayout = nil
	p.activePane = PanePreview
	p.saveSelectionState()
	return p.resizeDocTerminalCmd()
}

// hideDocPane collapses the live split and remembers the tab set. q/esc hide;
// last-x forgets through closeDocPane.
func (p *Plugin) hideDocPane() tea.Cmd {
	p.closeDocInfo()
	doc, _ := p.activeDocPane()
	if doc == nil {
		return nil
	}
	root, surface, ok := p.selectedTerminalSurface()
	if ok {
		p.rememberHiddenPaneLayout(root, surface)
	}
	if !p.closeDocPaneState() {
		p.hiddenPaneLayout = nil
		return nil
	}
	p.activePane = PanePreview
	p.saveSelectionState()
	return p.resizeDocTerminalCmd()
}

// reopenHiddenDocPane rebuilds a hidden split at the last ratio so a file
// click can focus or append against the remembered set.
func (p *Plugin) reopenHiddenDocPane() tea.Cmd {
	if p.activeDocPaneOrNil() != nil {
		return nil
	}
	_, surface, ok := p.selectedTerminalSurface()
	if !ok {
		return nil
	}
	layout := p.hiddenLayoutFor(surface)
	if layout == nil {
		return nil
	}
	if p.liveContentBesides(PaneDoc) {
		if !paneLayoutHasDocTabs(layout) {
			return nil
		}
		return p.reinsertHiddenDocLeaf(layout)
	}
	p.hiddenPaneLayout = nil
	return p.restorePaneLayout(layout)
}

func (p *Plugin) hiddenLayoutFor(surface string) *state.PaneLayoutJSON {
	if surface == "" {
		return nil
	}
	if p.hiddenPaneLayout != nil && p.hiddenPaneLayout.Surface == surface && paneLayoutHasRetainedTabs(p.hiddenPaneLayout) {
		return p.hiddenPaneLayout
	}
	layout := p.savedPaneLayoutForCurrentSurface(surface)
	if layout == nil || state.PaneLayoutOpen(layout) || !paneLayoutHasRetainedTabs(layout) {
		return nil
	}
	return layout
}

func paneLayoutHasDocTabs(layout *state.PaneLayoutJSON) bool {
	if layout == nil {
		return false
	}
	if len(layout.Tabs) > 0 {
		return true
	}
	if layout.Split == nil {
		return false
	}
	return paneLayoutHasDocTabs(layout.Split.A) || paneLayoutHasDocTabs(layout.Split.B)
}

func paneLayoutHasIssueTabs(layout *state.PaneLayoutJSON) bool {
	if layout == nil {
		return false
	}
	if len(layout.IssueTabs) > 0 {
		return true
	}
	if terminallink.IssueID(strings.TrimSpace(layout.Issue)) {
		return true
	}
	if layout.Split == nil {
		return false
	}
	return paneLayoutHasIssueTabs(layout.Split.A) || paneLayoutHasIssueTabs(layout.Split.B)
}

// paneLayoutHasRetainedTabs is the hide/reopen predicate: a q-hidden surface
// keeps document tabs, issue tabs, or a legacy issue leaf.
func paneLayoutHasRetainedTabs(layout *state.PaneLayoutJSON) bool {
	return paneLayoutHasDocTabs(layout) || paneLayoutHasIssueTabs(layout)
}

// rememberHiddenPaneLayout merges the live tree into the surface's hidden
// snapshot so a later q on the remaining sibling keeps the first-hidden tabs.
func (p *Plugin) rememberHiddenPaneLayout(root, surface string) {
	live := p.encodePaneNode(p.paneRoot)
	if live == nil {
		return
	}
	live.Root = root
	live.Surface = surface
	live.Open = false
	merged := mergeHiddenPaneLayout(p.hiddenPaneLayout, live)
	if merged == nil {
		return
	}
	merged.Root = root
	merged.Surface = surface
	merged.Open = false
	normalizePersistedIssueLeaves(merged)
	p.hiddenPaneLayout = merged
}

func mergeHiddenPaneLayout(existing, live *state.PaneLayoutJSON) *state.PaneLayoutJSON {
	if existing == nil {
		return clonePaneLayout(live)
	}
	if live == nil {
		return clonePaneLayout(existing)
	}
	liveDoc := firstLayoutLeafOfKind(live, contentKindDoc)
	liveIssue := firstLayoutLeafOfKind(live, contentKindIssue)
	existDoc := firstLayoutLeafOfKind(existing, contentKindDoc)
	existIssue := firstLayoutLeafOfKind(existing, contentKindIssue)
	doc := liveDoc
	if doc == nil {
		doc = existDoc
	}
	issue := liveIssue
	if issue == nil {
		issue = existIssue
	}
	if doc == nil && issue == nil {
		return clonePaneLayout(live)
	}
	existBoth := existDoc != nil && existIssue != nil
	liveBoth := liveDoc != nil && liveIssue != nil
	if doc != nil && issue != nil && !existBoth && !liveBoth {
		return composeStackedHidden(live, doc, issue)
	}
	template := existing
	if !existBoth && liveBoth {
		template = live
	}
	out := clonePaneLayout(template)
	replaceLayoutLeaf(out, contentKindDoc, doc)
	replaceLayoutLeaf(out, contentKindIssue, issue)
	out.Open = false
	return out
}

func composeStackedHidden(template, doc, issue *state.PaneLayoutJSON) *state.PaneLayoutJSON {
	cols, rows := 50, 50
	var root, surface string
	if template != nil {
		root, surface = template.Root, template.Surface
		if template.Split != nil {
			cols = template.Split.Ratio
			if inner := template.Split.B; inner != nil && inner.Split != nil {
				rows = inner.Split.Ratio
			} else if inner := template.Split.A; inner != nil && inner.Split != nil {
				rows = inner.Split.Ratio
			}
		}
	}
	return &state.PaneLayoutJSON{
		Root: root, Surface: surface, Open: false,
		Split: &state.PaneSplitJSON{
			Axis: "cols", Ratio: cols,
			A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
			B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
				Axis: "rows", Ratio: rows,
				A: copyContentLeaf(doc),
				B: copyContentLeaf(issue),
			}},
		},
	}
}

func clonePaneLayout(src *state.PaneLayoutJSON) *state.PaneLayoutJSON {
	if src == nil {
		return nil
	}
	out := *src
	if src.Tabs != nil {
		out.Tabs = append([]state.PaneDocTabJSON(nil), src.Tabs...)
	}
	if src.IssueTabs != nil {
		out.IssueTabs = append([]state.PaneIssueTabJSON(nil), src.IssueTabs...)
	}
	if src.Split != nil {
		split := *src.Split
		split.A = clonePaneLayout(src.Split.A)
		split.B = clonePaneLayout(src.Split.B)
		out.Split = &split
	}
	return &out
}

func copyContentLeaf(src *state.PaneLayoutJSON) *state.PaneLayoutJSON {
	if src == nil {
		return nil
	}
	out := &state.PaneLayoutJSON{
		Kind: src.Kind, Active: src.Active,
		Issue: src.Issue, Scroll: src.Scroll,
	}
	if src.Tabs != nil {
		out.Tabs = append([]state.PaneDocTabJSON(nil), src.Tabs...)
	}
	if src.IssueTabs != nil {
		out.IssueTabs = append([]state.PaneIssueTabJSON(nil), src.IssueTabs...)
	}
	return out
}

func firstLayoutLeafOfKind(layout *state.PaneLayoutJSON, kind string) *state.PaneLayoutJSON {
	if layout == nil {
		return nil
	}
	if layout.Split == nil {
		if layout.Kind == kind {
			return layout
		}
		return nil
	}
	if leaf := firstLayoutLeafOfKind(layout.Split.A, kind); leaf != nil {
		return leaf
	}
	return firstLayoutLeafOfKind(layout.Split.B, kind)
}

func replaceLayoutLeaf(tree *state.PaneLayoutJSON, kind string, leaf *state.PaneLayoutJSON) {
	target := firstLayoutLeafOfKind(tree, kind)
	if target == nil || leaf == nil {
		return
	}
	target.Kind = leaf.Kind
	target.Active = leaf.Active
	target.Issue = leaf.Issue
	target.Scroll = leaf.Scroll
	target.Split = nil
	if leaf.Tabs != nil {
		target.Tabs = append([]state.PaneDocTabJSON(nil), leaf.Tabs...)
	} else {
		target.Tabs = nil
	}
	if leaf.IssueTabs != nil {
		target.IssueTabs = append([]state.PaneIssueTabJSON(nil), leaf.IssueTabs...)
	} else {
		target.IssueTabs = nil
	}
}

func (p *Plugin) liveContentBesides(kind PaneKind) bool {
	for _, id := range p.contentLeafIDs() {
		leaf := FindPane(p.paneRoot, id)
		if leaf != nil && leaf.Kind != kind {
			return true
		}
	}
	return false
}

func (p *Plugin) reinsertHiddenDocLeaf(layout *state.PaneLayoutJSON) tea.Cmd {
	return p.reinsertHiddenContentLeaf(PaneDoc, firstLayoutLeafOfKind(layout, contentKindDoc), "Document")
}

func (p *Plugin) reinsertHiddenContentLeaf(kind PaneKind, saved *state.PaneLayoutJSON, name string) tea.Cmd {
	if saved == nil || p.paneRoot == nil || p.ctx == nil {
		return nil
	}
	root, _, ok := p.selectedTerminalSurface()
	if !ok {
		return nil
	}
	plan, planned := planPaneOpen(p.paneRoot, kind)
	if !planned || plan.Retarget != 0 {
		return nil
	}
	var loads []tea.Cmd
	var node *PaneNode
	switch kind {
	case PaneDoc:
		node = p.decodeDocLeaf(saved, root, &loads)
	case PaneIssue:
		node = p.decodeIssueLeaf(saved, root, &loads)
	}
	if node == nil {
		return nil
	}
	if !p.splitOnPlannedLeaf(plan, node, name) {
		switch kind {
		case PaneDoc:
			delete(p.docs, node.ContentID)
		case PaneIssue:
			delete(p.issues, node.ContentID)
		}
		return nil
	}
	p.hiddenPaneLayout = nil
	p.activePane = PanePreview
	return tea.Batch(append(loads, p.resizeDocTerminalCmd())...)
}

func (p *Plugin) splitOnPlannedLeaf(plan paneOpen, node *PaneNode, name string) bool {
	content, placed := p.previewContentBox()
	if !placed {
		return false
	}
	trialNode := clonePaneTree(node)
	trial, trialFocus := SplitLeaf(clonePaneTree(p.paneRoot), plan.Split, plan.Axis, trialNode)
	if trialFocus != trialNode.ID {
		return false
	}
	if _, _, fits := LayoutPanes(trial, content, paneTreeFloors()); !fits {
		p.toastMessage = paneFitMessage(name, plan.Axis)
		p.toastTime = time.Now()
		return false
	}
	treeRoot, focus := SplitLeaf(p.paneRoot, plan.Split, plan.Axis, node)
	if focus != node.ID {
		return false
	}
	p.paneRoot, p.paneFocus = treeRoot, focus
	p.paneNextID = maxInt(p.paneNextID, maxPaneID(p.paneRoot)+1)
	return true
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
	p.closeDocInfo()
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
	for _, item := range doc.tabs.Items {
		if item.View != nil && item.View.SetResult(msg) {
			return
		}
	}
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
	if p.docInfo != nil {
		switch msg.String() {
		case "q", "esc", "I", "i":
			p.closeDocInfo()
			return true, nil
		}
		closed, cmd := p.docInfo.HandleKey(msg)
		if closed {
			p.closeDocInfo()
		}
		return true, cmd
	}
	if !p.docFocused() {
		return false, nil
	}
	doc := p.focusedDocPane()
	if doc == nil {
		return false, nil
	}
	// A live search surface owns every key in the pane, exactly as the document
	// under it owns every key it is handed: esc closes it, and nothing it does
	// not use reaches the workspace behind the pane.
	if doc.mode != nil {
		return true, p.handleDocSearchKey(doc, msg)
	}
	switch msg.String() {
	case "ctrl+p":
		return true, p.openDocFinder(doc)
	case "f":
		return true, p.openDocProjectSearch(doc)
	case "\\":
		return true, p.toggleSidebarCmd()
	case "q", "esc":
		return true, p.hideDocPane()
	case "x":
		return true, p.closeActiveDocTab()
	case "{":
		return true, p.cycleActiveDocTab(-1)
	case "}":
		return true, p.cycleActiveDocTab(1)
	case "m":
		p.toggleDocRenderMode()
		return true, nil
	case "w":
		p.toggleDocWrap()
		return true, nil
	case "I":
		return true, p.openDocInfo()
	case "ctrl+r":
		return true, p.revealActiveDoc()
	case "Y":
		return true, p.yankActiveDocPath()
	case "+":
		return true, p.resizeFocusedDoc(5)
	case "-":
		return true, p.resizeFocusedDoc(-5)
	default:
		if view := doc.view(); view != nil {
			before := view.ScrollOffset()
			view.HandleKey(msg)
			if view.ScrollOffset() != before {
				p.saveSelectionState()
			}
		}
		// A focused document is its own input context. Absorb keys it does not
		// own so they cannot trigger workspace actions behind the pane.
		return true, nil
	}
}

func (p *Plugin) closeDocInfo() {
	p.docInfo = nil
}

func (p *Plugin) toggleDocWrap() {
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view() == nil {
		return
	}
	doc.view().ToggleWrap()
	p.saveSelectionState()
}

func (p *Plugin) openDocInfo() tea.Cmd {
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view() == nil || doc.view().Title() == "" {
		return nil
	}
	info, cmd := docview.OpenInfo(doc.root, doc.view().Title())
	p.docInfo = info
	return cmd
}

func (p *Plugin) revealActiveDoc() tea.Cmd {
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view() == nil || doc.view().Title() == "" {
		return nil
	}
	return docview.Reveal(doc.root, doc.view().Title())
}

func (p *Plugin) yankActiveDocPath() tea.Cmd {
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view() == nil || doc.view().Title() == "" {
		return nil
	}
	return docview.YankPath(doc.view().Title())
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
	// that will come back attached to the wrong workspace. Switch-away writes
	// the previous surface before the index changes, so this is a safety net
	// for a save that still races the selection.
	for _, leafID := range p.contentLeafIDs() {
		paneRoot, paneSurface, ok := p.contentLeafSurface(leafID)
		if ok && (filepath.Clean(paneRoot) != root || paneSurface != surface) {
			return &state.PaneLayoutJSON{Root: root, Surface: surface, Kind: contentKindTerminal, Open: true}
		}
	}
	return p.encodeSurfacePaneLayout(root, surface)
}

func (p *Plugin) encodeSurfacePaneLayout(root, surface string) *state.PaneLayoutJSON {
	if p.hiddenPaneLayout != nil && p.hiddenPaneLayout.Surface == surface && paneLayoutHasRetainedTabs(p.hiddenPaneLayout) {
		layout := p.hiddenPaneLayout
		layout.Root = root
		layout.Surface = surface
		layout.Open = false
		normalizePersistedIssueLeaves(layout)
		return layout
	}
	layout := p.encodePaneNode(p.paneRoot)
	if layout == nil {
		return nil
	}
	layout.Root = root
	layout.Surface = surface
	layout.Open = true
	return layout
}

func (p *Plugin) readWorkspaceState() state.WorkspaceState {
	if p.ctx == nil {
		return state.WorkspaceState{}
	}
	wt := p.shellStartupHooks.withDefaults().getWorkspaceState(p.ctx.ProjectRoot)
	state.MigratePaneLayouts(&wt)
	return wt
}

func (p *Plugin) savedPaneLayoutForCurrentSurface(surface string) *state.PaneLayoutJSON {
	wtState := p.readWorkspaceState()
	legacy := ""
	if wt := p.selectedWorktree(); wt != nil {
		legacy = legacyWorkspaceSurfaceIdentity(wt)
	}
	layout, changed := state.RekeyPaneLayout(&wtState, legacy, surface)
	if changed {
		wtState.PaneLayout = nil
		p.writeWorkspaceState(wtState)
	}
	return layout
}

func (p *Plugin) forgetPaneSurfaces(surfaces ...string) {
	wtState := p.readWorkspaceState()
	if state.ForgetPaneLayouts(&wtState, surfaces...) {
		p.writeWorkspaceState(wtState)
	}
}

func (p *Plugin) forgetWorktreePaneLayout(wt *Worktree) {
	if wt == nil {
		return
	}
	p.forgetPaneSurfaces(workspaceSurfaceIdentity(wt), legacyWorkspaceSurfaceIdentity(wt))
}

func (p *Plugin) writeWorkspaceState(wt state.WorkspaceState) {
	if p.ctx == nil {
		return
	}
	_ = p.shellStartupHooks.withDefaults().setWorkspaceState(p.ctx.ProjectRoot, wt)
}

func (p *Plugin) liveTreeRepresents(surface string) bool {
	if surface == "" {
		return false
	}
	hasContent := false
	for _, leafID := range p.contentLeafIDs() {
		_, paneSurface, ok := p.contentLeafSurface(leafID)
		if !ok {
			continue
		}
		hasContent = true
		if paneSurface == surface {
			return true
		}
	}
	if hasContent {
		return false
	}
	return p.paneLayoutSurface == surface
}

func (p *Plugin) storeLivePaneLayout(root, surface string) {
	if p.paneRoot == nil || surface == "" {
		return
	}
	layout := p.encodeSurfacePaneLayout(root, surface)
	if layout == nil {
		return
	}
	wt := p.readWorkspaceState()
	if wt.PaneLayouts == nil {
		wt.PaneLayouts = make(map[string]*state.PaneLayoutJSON)
	}
	wt.PaneLayouts[surface] = layout
	wt.PaneLayout = nil
	p.writeWorkspaceState(wt)
}

func (p *Plugin) restoreIncomingPaneLayout() {
	p.restoreSurfacePaneLayout(false)
}

// restoreIncomingPaneLayoutHonoringOpen is the relaunch/retarget path: a
// hidden set stays in the map and the live tree stays terminal-only. An
// explicit surface switch uses restoreIncomingPaneLayout so q-hidden tabs
// come back.
func (p *Plugin) restoreIncomingPaneLayoutHonoringOpen() {
	p.restoreSurfacePaneLayout(true)
}

func (p *Plugin) restoreSurfacePaneLayout(honorOpen bool) {
	if p.paneRoot == nil {
		return
	}
	_, surface, ok := p.selectedTerminalSurface()
	p.resetPaneTreeToTerminal()
	if !ok {
		p.paneLayoutSurface = ""
		p.paneRestoreCmd = nil
		return
	}
	p.paneLayoutSurface = surface
	layout := p.savedPaneLayoutForCurrentSurface(surface)
	if layout == nil {
		p.paneRestoreCmd = nil
		return
	}
	if honorOpen && !state.PaneLayoutOpen(layout) {
		if paneLayoutHasRetainedTabs(layout) {
			p.hiddenPaneLayout = layout
		}
		p.paneRestoreCmd = nil
		return
	}
	p.paneRestoreCmd = p.restorePaneLayout(layout)
}

func (p *Plugin) takePaneRestoreCmd() tea.Cmd {
	cmd := p.paneRestoreCmd
	p.paneRestoreCmd = nil
	return cmd
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
		tabs, active := encodeIssueTabs(p.issues[node.ContentID])
		if len(tabs) == 0 {
			return nil
		}
		return &state.PaneLayoutJSON{Kind: contentKindIssue, IssueTabs: tabs, Active: active}
	}
	doc := p.docs[node.ContentID]
	tabs, active := encodeDocTabs(doc)
	if len(tabs) == 0 {
		return nil
	}
	return &state.PaneLayoutJSON{Kind: contentKindDoc, Tabs: tabs, Active: active}
}

func encodeDocTabs(doc *docPane) ([]state.PaneDocTabJSON, int) {
	if doc == nil {
		return nil, 0
	}
	tabs := make([]state.PaneDocTabJSON, 0, len(doc.tabs.Items))
	active := 0
	for i, item := range doc.tabs.Items {
		view := item.View
		if view == nil || view.Title() == "" {
			continue
		}
		if i == doc.tabs.Active {
			active = len(tabs)
		}
		mode := "raw"
		if view.Rendered() {
			mode = "rendered"
		}
		tabs = append(tabs, state.PaneDocTabJSON{
			Path:   docview.NormalizeTabPath(view.Title()),
			Mode:   mode,
			Wrap:   view.Wrap(),
			Scroll: view.ScrollOffset(),
		})
	}
	return tabs, active
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
		return p.decodeDocLeaf(saved, root, loads)
	case contentKindIssue:
		return p.decodeIssueLeaf(saved, root, loads)
	}
	return nil
}

func (p *Plugin) decodeDocLeaf(saved *state.PaneLayoutJSON, root string, loads *[]tea.Cmd) *PaneNode {
	if saved == nil || len(saved.Tabs) == 0 || p.ctx == nil {
		return nil
	}
	wanted := saved.Active
	if wanted < 0 || wanted >= len(saved.Tabs) {
		wanted = 0
	}
	type restoredTab struct {
		rel    string
		mode   string
		wrap   bool
		scroll int
	}
	var pending []restoredTab
	active := 0
	for i, tab := range saved.Tabs {
		rel, _, valid := resolveTerminalPath(root, tab.Path)
		// ResolveFile may accept a file outside root, reporting it as an
		// absolute display path. A restored layout only ever addresses the
		// viewer with a root-relative path, so an escaping tab is dropped
		// rather than joined onto root as if it were relative.
		if !valid || filepath.IsAbs(rel) {
			continue
		}
		if i == wanted {
			active = len(pending)
		}
		pending = append(pending, restoredTab{
			rel:    filepath.ToSlash(rel),
			mode:   tab.Mode,
			wrap:   tab.Wrap,
			scroll: tab.Scroll,
		})
	}
	if len(pending) == 0 {
		return nil
	}
	id := p.nextPaneID()
	items := make([]docview.Item, 0, len(pending))
	for _, tab := range pending {
		viewer := docview.New(nil)
		viewer.SetRendered(tab.mode != "raw")
		viewer.SetWrap(tab.wrap)
		viewer.SetPendingScroll(tab.scroll)
		viewer.Arm(id, tab.rel, p.ctx.Epoch)
		items = append(items, docview.Item{View: viewer})
	}
	tabs := docview.Tabs{Items: items, Active: active}
	view := tabs.ActiveView()
	rendered := view.Rendered()
	wrap := view.Wrap()
	load := view.Load(id, root, view.Title(), 0, p.ctx.Epoch)
	view.SetRendered(rendered)
	view.SetWrap(wrap)
	p.docs[id] = &docPane{leafID: id, root: root, surface: savedRootSurface(p, root), tabs: tabs}
	*loads = append(*loads, load)
	return &PaneNode{ID: id, Kind: PaneDoc, ContentID: id}
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
	p.closeDocInfo()
	p.docs = make(map[int]*docPane)
	p.issues = make(map[int]*issuePane)
	p.hiddenPaneLayout = nil
	p.paneNextID = 1
	p.paneRoot = &PaneNode{ID: p.nextPaneID(), Kind: PaneTerminal}
	p.paneFocus = p.paneRoot.ID
}

// docPaneHeaderRow is the doc leaf's header: the tab strip only. focused is
// the frame's answer, so the tab a click lands on matches the one the leaf drew.
func (p *Plugin) docPaneHeaderRow(doc *docPane, width int, focused bool) string {
	return layoutDocTabStrip(doc, width, focused).Row
}

func (p *Plugin) toggleDocRenderMode() {
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view() == nil {
		return
	}
	if !terminallink.Markdown(doc.view().Title()) {
		return
	}
	doc.view().ToggleRenderMode()
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
	p.mouseHandler.HitMap.AddRect(regionPaneLeaf, box.X, box.Y, box.W, box.H, leafID)
}

func (p *Plugin) registerDocTabRegions(doc *docPane, leafID int, box Box) {
	for _, tab := range layoutDocTabStrip(doc, box.W, p.paneFocus == leafID).Tabs {
		p.mouseHandler.HitMap.AddRect(regionDocTab, box.X+tab.Col, box.Y, tab.Width, 1, docTabHit{LeafID: leafID, Index: tab.Index})
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
			box := Box{X: origin.X, Y: origin.Y, W: zoomed.Box.W, H: zoomed.Box.H}
			p.registerPaneLeafRegions(zoomed.Node, box)
			p.registerPaneTabRegions(zoomed.Node, box)
		}
		// One leaf is still composed, not returned: the clip-and-pad the
		// compositor guarantees is what makes the leaf's box the leaf's box, and
		// a lone leaf that keeps its own shape is the one placement nothing
		// holds to it.
		canvas.Blit(zoomed.Box, p.renderPaneLeaf(zoomed, origin, true))
		// Last, because the render above is what places a live search surface's
		// regions and they have to beat the leaf region drawn under them.
		p.registerDocSearchRegions()
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
	// Last, because a live search surface is drawn over its leaf and its regions
	// have to beat the leaf's own.
	p.registerDocSearchRegions()
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
// plugin-local coordinates. Tab hits come from the same strip the header
// draws, so a click cannot land on a tab that was never rendered. A terminal
// leaf registers nothing here: its regions belong to the legacy renderer inside it.
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
			p.registerDocPaneRegions(doc, node.ID, box)
		}
	case PaneIssue:
		if issue := p.issues[node.ContentID]; issue != nil {
			p.registerIssuePaneRegions(issue, node.ID, box)
		}
	}
}

func (p *Plugin) registerPaneTabRegions(node *PaneNode, box Box) {
	if node == nil || node.Split != nil {
		return
	}
	switch node.Kind {
	case PaneDoc:
		if doc := p.docs[node.ContentID]; doc != nil {
			p.registerDocTabRegions(doc, node.ID, box)
		}
	case PaneIssue:
		if issue := p.issues[node.ContentID]; issue != nil {
			p.registerIssueTabRegions(issue, node.ID, box)
		}
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
	// it, and widened hit targets overlap once splits nest. Two targets can
	// only overlap when one split encloses the other, because sibling subtrees
	// are held apart by the divider between them — so registering the enclosing
	// split first is what leaves the enclosed one last, and HitMap.Test's
	// reverse scan returns it for a point both claim.
	for _, split := range dividers {
		hit := paneDividerHitBox(split)
		p.mouseHandler.HitMap.AddRect(regionPaneTreeDivider,
			absolute.X+hit.X, absolute.Y+hit.Y, hit.W, hit.H, split.SplitID)
	}
	// File tabs are last so they win the one cell the column divider reaches
	// into the document header — the cell a click on the leftmost tab lands on.
	for _, placement := range leaves {
		p.registerPaneTabRegions(placement.Node, Box{
			X: absolute.X + placement.Box.X, Y: absolute.Y + placement.Box.Y,
			W: placement.Box.W, H: placement.Box.H,
		})
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
		// The lower leaf starts with a header row, so only widen upward.
		// Reaching below the divider would mask an issue's whole header.
		hit.H = dividerHitWidth - 1
		hit.Y--
	}
	return hit
}
