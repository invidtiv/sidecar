package workspace

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// diffPane is one Diff leaf's tab group. The pane tree points at this,
// not at a single view. The surface is what lets a selection change collapse
// the leaf rather than carry diffs from one shell into another.
type diffPane struct {
	leafID  int
	root    string
	surface string
	tabs    workspacediff.Group
}

func (d *diffPane) view() *workspacediff.View {
	if d == nil {
		return nil
	}
	return d.tabs.ActiveView()
}

// activeDiffPane returns the first live Diff leaf. A second Diff open
// retargets this leaf rather than splitting again.
func (p *Plugin) activeDiffPane() (*diffPane, *PaneNode) {
	for id, diff := range p.diffs {
		if diff == nil {
			continue
		}
		if leaf := FindPane(p.paneRoot, diff.leafID); leaf != nil && leaf.Kind == PaneDiff && leaf.ContentID == id {
			return diff, leaf
		}
	}
	return nil, nil
}

// showDiffCmd opens the working-tree Diff leaf on the selected surface.
func (p *Plugin) showDiffCmd() tea.Cmd {
	if p.paneRoot == nil {
		return appmsg.ShowToast(features.WorkspaceDocPanesDisabledDiff, 3*time.Second)
	}
	root, surface, ok := p.selectedTerminalSurface()
	if !ok {
		return nil
	}
	return p.openDiffPaneForSurface(root, surface, workspacediff.WorkingTreeTarget())
}

// openDiffPaneForSurface opens target in the pane tree at the place
// planPaneOpen names. The split is trialled on a clone first, exactly as a
// document's is: a box that cannot hold the result leaves the terminal at the
// size it already has rather than reflowing an agent for a pane that will not
// be drawn.
func (p *Plugin) openDiffPaneForSurface(root, surface string, target workspacediff.Target) tea.Cmd {
	if p.paneRoot == nil {
		return appmsg.ShowToast(features.WorkspaceDocPanesDisabledDiff, 3*time.Second)
	}
	if p.ctx == nil {
		return nil
	}
	if target.Identity() == "" {
		target = workspacediff.WorkingTreeTarget()
	}
	reopen := p.reopenHiddenDiffPane()
	plan, ok := p.planOpen(PaneDiff)
	if !ok {
		return reopen
	}
	if p.ctx.Logger != nil {
		kind := "split"
		if plan.Retarget != 0 {
			kind = "retarget"
		}
		p.ctx.Logger.Debug("openDiffPaneForSurface",
			"surface", surface, "target", target.Identity(), "plan", kind)
	}
	if plan.Retarget != 0 {
		leaf := FindPane(p.paneRoot, plan.Retarget)
		if leaf == nil || leaf.Split != nil {
			return reopen
		}
		load := p.attachDiffPane(leaf.ContentID, root, surface, target)
		if p.diffs[leaf.ContentID] == nil || p.diffs[leaf.ContentID].view() == nil {
			return reopen
		}
		p.paneFocus = leaf.ID
		p.activePane = PanePreview
		p.saveSelectionState()
		return tea.Batch(reopen, load)
	}

	content, placed := p.previewContentBox()
	if !placed {
		return reopen
	}
	id := p.paneNextID
	trial, trialFocus := SplitLeaf(clonePaneTree(p.paneRoot), plan.Split, plan.Axis,
		&PaneNode{ID: id, Kind: PaneDiff, ContentID: id})
	if trialFocus != id {
		return reopen
	}
	if _, _, fits := LayoutPanes(trial, content, paneTreeFloors()); !fits {
		p.toastMessage = paneFitMessage("Diff", plan.Axis)
		p.toastTime = time.Now()
		return reopen
	}

	newLeaf := &PaneNode{ID: id, Kind: PaneDiff, ContentID: id}
	treeRoot, focus := SplitLeaf(p.paneRoot, plan.Split, plan.Axis, newLeaf)
	if focus != newLeaf.ID {
		return reopen
	}
	p.paneRoot, p.paneFocus = treeRoot, focus
	p.paneNextID = maxInt(p.paneNextID, maxPaneID(p.paneRoot)+1)
	p.activePane = PanePreview
	load := p.attachDiffPane(newLeaf.ContentID, root, surface, target)
	p.saveSelectionState()
	return tea.Batch(reopen, load, p.resizeDocTerminalCmd())
}

// attachDiffPane points the content behind leafID at target and returns its
// load when a new tab is created or a restored tab still needs one.
func (p *Plugin) attachDiffPane(leafID int, root, surface string, target workspacediff.Target) tea.Cmd {
	if p.ctx == nil || target.Identity() == "" {
		return nil
	}
	if p.diffs == nil {
		p.diffs = make(map[int]*diffPane)
	}
	pane := p.diffs[leafID]
	if pane == nil {
		pane = &diffPane{leafID: leafID}
		p.diffs[leafID] = pane
	}
	pane.root, pane.surface = root, surface
	return p.openOrFocusDiff(pane, target)
}

func (p *Plugin) newDiffView(target workspacediff.Target) *workspacediff.View {
	view := &workspacediff.View{
		Target:   target,
		ViewMode: p.diff.ViewMode,
		State:    workspacediff.LoadStateLoading,
	}
	if target.Kind == workspacediff.TargetCommit {
		view.Focus = workspacediff.FocusCommitFiles
	}
	if w := state.GetDiffTabFileListWidth(); w > 0 {
		view.SetListWidth(w)
	}
	p.attachDiffPaintTo(view)
	return view
}

func (p *Plugin) diffWorkspaceID(root, surface string) string {
	if wt := p.selectedWorktree(); wt != nil {
		if key := wt.IdentityKey(); key != "" {
			return key
		}
	}
	if surface != "" {
		return surface
	}
	return root
}

func (p *Plugin) selectedDiffBaseRef() string {
	if wt := p.selectedWorktree(); wt != nil {
		return wt.BaseBranch
	}
	return ""
}

// openOrFocusDiff selects an existing tab for target or appends a fresh
// view and loads it.
func (p *Plugin) openOrFocusDiff(pane *diffPane, target workspacediff.Target) tea.Cmd {
	if pane == nil || p.ctx == nil || target.Identity() == "" {
		return nil
	}
	if idx := pane.tabs.Find(target.Identity()); idx >= 0 {
		pane.tabs.Select(idx)
		return p.ensureActiveDiffTabLoaded(pane)
	}
	view := p.newDiffView(target)
	if _, created := pane.tabs.OpenOrFocus(target, view); !created {
		return p.ensureActiveDiffTabLoaded(pane)
	}
	return p.loadDiffView(view, pane.root, pane.surface)
}

func (p *Plugin) applyDiffLoadedToLeaves(msg workspacediff.SnapshotMsg) tea.Cmd {
	var cmds []tea.Cmd
	for _, pane := range p.diffs {
		if pane == nil {
			continue
		}
		for _, item := range pane.tabs.Items {
			if item.Value == nil {
				continue
			}
			cmds = append(cmds, item.Value.ApplySnapshotMsg(msg, item.Value.WorkDir, item.Value.WorkspaceID))
		}
	}
	return tea.Batch(cmds...)
}

func (p *Plugin) applyCommitDetailToLeaves(msg workspacediff.CommitDetailMsg) tea.Cmd {
	var cmds []tea.Cmd
	for _, pane := range p.diffs {
		if pane == nil {
			continue
		}
		for _, item := range pane.tabs.Items {
			if item.Value == nil {
				continue
			}
			cmds = append(cmds, item.Value.ApplyCommitDetail(msg))
		}
	}
	return tea.Batch(cmds...)
}

func (p *Plugin) applyRangeToLeaves(msg workspacediff.RangeMsg) tea.Cmd {
	var cmds []tea.Cmd
	for _, pane := range p.diffs {
		if pane == nil {
			continue
		}
		for _, item := range pane.tabs.Items {
			if item.Value == nil {
				continue
			}
			cmds = append(cmds, item.Value.ApplyRangeMsg(msg))
		}
	}
	return tea.Batch(cmds...)
}

func (p *Plugin) applyCommitFileDiffToLeaves(msg workspacediff.CommitFileDiffMsg) tea.Cmd {
	var cmds []tea.Cmd
	for _, pane := range p.diffs {
		if pane == nil {
			continue
		}
		for _, item := range pane.tabs.Items {
			if item.Value == nil {
				continue
			}
			cmds = append(cmds, item.Value.ApplyCommitFileDiff(msg))
		}
	}
	return tea.Batch(cmds...)
}

// diffFocused is the Diff leaf's own version of issueFocused: the focused
// leaf is a Diff. Without this the keys under a highlighted Diff pane are
// still the agent terminal's — q would open the quit confirmation.
func (p *Plugin) diffFocused() bool {
	diff, _ := p.focusedDiffPane()
	return diff != nil
}

func (p *Plugin) focusedDiffPane() (*diffPane, *PaneNode) {
	if !p.previewLeafFocused() {
		return nil, nil
	}
	leaf := FindPane(p.paneRoot, p.paneFocus)
	if leaf == nil || leaf.Kind != PaneDiff {
		return nil, nil
	}
	diff := p.diffs[leaf.ContentID]
	if diff == nil {
		return nil, nil
	}
	return diff, leaf
}

// handleDiffKey is the focused Diff leaf's input context. A key this pane
// does not own must not fall through to the terminal behind it.
func (p *Plugin) handleDiffKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	diff, _ := p.focusedDiffPane()
	if diff == nil {
		return false, nil
	}
	view := diff.view()
	switch msg.String() {
	case "tab", "shift+tab":
		return false, nil
	case "\\":
		return true, p.toggleSidebarCmd()
	case "q", "esc":
		return true, p.hideDiffPane()
	case "x":
		return true, p.closeActiveDiffTab()
	case ",":
		return true, p.cycleActiveDiffTab(-1)
	case ".":
		return true, p.cycleActiveDiffTab(1)
	case "Y", "shift+y":
		return true, p.yankFocusedDiff()
	case "+":
		return true, p.resizeFocusedLeaf(5)
	case "-":
		return true, p.resizeFocusedLeaf(-5)
	case "f":
		return true, p.openFilePicker()
	default:
		if view == nil {
			return true, nil
		}
		if box, ok := p.paneLeafBox(diff.leafID); ok {
			view.SetSize(box.W, maxInt(box.H-terminalHeaderRows, 0))
		}
		p.attachDiffPaintTo(view)
		beforeActive := diff.tabs.Active
		beforeIdent, beforeScroll := view.Target.Identity(), view.Scroll
		cmd, _ := view.HandleKey(msg)
		p.persistDiffViewModeFrom(view)
		after := diff.view()
		if diff.tabs.Active != beforeActive ||
			(after != nil && (after.Target.Identity() != beforeIdent || after.Scroll != beforeScroll)) {
			p.saveSelectionState()
		}
		return true, cmd
	}
}

func (p *Plugin) persistDiffViewModeFrom(view *workspacediff.View) {
	if view == nil {
		return
	}
	p.diff.ViewMode = view.ViewMode
	p.persistDiffViewMode()
}

func (p *Plugin) cycleActiveDiffTab(delta int) tea.Cmd {
	diff, _ := p.focusedDiffPane()
	if diff == nil || len(diff.tabs.Items) < 2 {
		return nil
	}
	diff.tabs.Cycle(delta)
	p.saveSelectionState()
	return p.ensureActiveDiffTabLoaded(diff)
}

func (p *Plugin) closeActiveDiffTab() tea.Cmd {
	diff, leaf := p.focusedDiffPane()
	if diff == nil {
		return nil
	}
	if len(diff.tabs.Items) <= 1 {
		return p.closeDiffPane(leaf.ID)
	}
	diff.tabs.CloseActive()
	p.saveSelectionState()
	return p.ensureActiveDiffTabLoaded(diff)
}

func (p *Plugin) selectDiffTab(diff *diffPane, leafID, idx int) tea.Cmd {
	if diff == nil {
		return nil
	}
	p.focusLeaf(leafID)
	p.pointer.Abandon()
	if p.viewMode == ViewModeInteractive {
		p.exitInteractiveMode()
	}
	if idx == diff.tabs.Active {
		return p.ensureActiveDiffTabLoaded(diff)
	}
	diff.tabs.Select(idx)
	p.saveSelectionState()
	return p.ensureActiveDiffTabLoaded(diff)
}

func (p *Plugin) clickDiffTab(data any) tea.Cmd {
	hit, ok := data.(diffTabHit)
	if !ok {
		return nil
	}
	leaf := FindPane(p.paneRoot, hit.LeafID)
	if leaf == nil || leaf.Kind != PaneDiff {
		return nil
	}
	diff := p.diffs[leaf.ContentID]
	if diff == nil {
		return nil
	}
	return p.selectDiffTab(diff, hit.LeafID, hit.Index)
}

func (p *Plugin) hideDiffPane() tea.Cmd {
	diff, leaf := p.focusedDiffPane()
	if diff == nil || leaf == nil {
		return nil
	}
	root, surface, ok := p.selectedTerminalSurface()
	if ok {
		p.rememberHiddenPaneLayout(root, surface)
	}
	if !p.closeContentLeaf(leaf.ID) {
		p.hiddenPaneLayout = nil
		return nil
	}
	p.activePane = PanePreview
	p.saveSelectionState()
	return p.resizeDocTerminalCmd()
}

func (p *Plugin) reopenHiddenDiffPane() tea.Cmd {
	if diff, _ := p.activeDiffPane(); diff != nil {
		return nil
	}
	_, surface, ok := p.selectedTerminalSurface()
	if !ok {
		return nil
	}
	layout := p.hiddenLayoutFor(surface)
	if layout == nil || !paneLayoutHasDiffTabs(layout) {
		return nil
	}
	if p.liveContentBesides(PaneDiff) {
		return p.reinsertHiddenDiffLeaf(layout)
	}
	p.hiddenPaneLayout = nil
	return p.restorePaneLayout(layout)
}

func (p *Plugin) reinsertHiddenDiffLeaf(layout *state.PaneLayoutJSON) tea.Cmd {
	return p.reinsertHiddenContentLeaf(PaneDiff, firstLayoutLeafOfKind(layout, contentKindDiff), "Diff")
}

func (p *Plugin) ensureActiveDiffTabLoaded(diff *diffPane) tea.Cmd {
	if diff == nil || p.ctx == nil {
		return nil
	}
	view := diff.view()
	if view == nil {
		return nil
	}
	if view.State != workspacediff.LoadStateUnknown && view.State != workspacediff.LoadStateLoading && view.State != workspacediff.LoadStateError {
		return nil
	}
	return p.loadDiffView(view, diff.root, diff.surface)
}

func (p *Plugin) loadDiffView(view *workspacediff.View, root, surface string) tea.Cmd {
	if view == nil || p.ctx == nil {
		return nil
	}
	workspaceID := p.diffWorkspaceID(root, surface)
	view.Bind(root, workspaceID, p.ctx.Epoch)
	p.attachDiffPaintTo(view)
	view.State = workspacediff.LoadStateLoading
	switch view.Target.Kind {
	case workspacediff.TargetCommit:
		if view.Target.A == "" {
			return nil
		}
		return view.LoadCommit(view.Target.A)
	case workspacediff.TargetRange:
		return view.LoadRange()
	default:
		return workspacediff.LoadSnapshotCmdAt(root, p.selectedDiffBaseRef(), workspaceID, p.ctx.Epoch, view.Target.Identity())
	}
}

func encodeDiffTabs(diff *diffPane) ([]state.PaneDiffTabJSON, int) {
	if diff == nil {
		return nil, 0
	}
	tabs := make([]state.PaneDiffTabJSON, 0, len(diff.tabs.Items))
	active := 0
	for i, item := range diff.tabs.Items {
		spec := item.Key
		if spec == "" && item.Value != nil {
			spec = item.Value.Target.Identity()
		}
		if spec == "" {
			continue
		}
		tab := state.PaneDiffTabJSON{Spec: spec}
		if item.Value != nil {
			tab.Path = item.Value.SelectedFileName()
			tab.Scope = item.Value.Scope.Persist()
			tab.Mode = item.Value.ViewMode.Persist()
			tab.Scroll = item.Value.Scroll
		}
		if i == diff.tabs.Active {
			active = len(tabs)
		}
		tabs = append(tabs, tab)
	}
	return tabs, active
}

func (p *Plugin) decodeDiffLeaf(saved *state.PaneLayoutJSON, root string, loads *[]tea.Cmd) *PaneNode {
	if saved == nil || p.ctx == nil || len(saved.DiffTabs) == 0 {
		return nil
	}
	wanted := saved.Active
	if wanted < 0 || wanted >= len(saved.DiffTabs) {
		wanted = 0
	}
	type restoredTab struct {
		target workspacediff.Target
		path   string
		scope  workspacediff.Scope
		mode   workspacediff.ViewMode
		scroll int
	}
	var pending []restoredTab
	active := 0
	for i, tab := range saved.DiffTabs {
		target, ok := workspacediff.ParseSpec(tab.Spec)
		if !ok {
			continue
		}
		if tab.Path != "" {
			target.Path = tab.Path
		}
		if i == wanted {
			active = len(pending)
		}
		pending = append(pending, restoredTab{
			target: target,
			path:   tab.Path,
			scope:  workspacediff.ParseScope(tab.Scope),
			mode:   workspacediff.ParseViewMode(tab.Mode),
			scroll: tab.Scroll,
		})
	}
	if len(pending) == 0 {
		return nil
	}
	if p.diffs == nil {
		p.diffs = make(map[int]*diffPane)
	}
	id := p.nextPaneID()
	pane := &diffPane{leafID: id, root: root, surface: savedRootSurface(p, root)}
	p.diffs[id] = pane
	var group workspacediff.Group
	for _, tab := range pending {
		view := p.newDiffView(tab.target)
		view.Scope = tab.scope
		if tab.mode != 0 {
			view.ViewMode = tab.mode
		}
		view.Scroll = tab.scroll
		view.Bind(root, p.diffWorkspaceID(root, pane.surface), p.ctx.Epoch)
		group.Append(tab.target.Identity(), view)
	}
	group.Select(active)
	pane.tabs = group
	if load := p.ensureActiveDiffTabLoaded(pane); load != nil {
		*loads = append(*loads, load)
	}
	return &PaneNode{ID: id, Kind: PaneDiff, ContentID: id}
}

func (p *Plugin) closeDiffPane(leafID int) tea.Cmd {
	if !p.closeContentLeaf(leafID) {
		return nil
	}
	p.hiddenPaneLayout = nil
	p.activePane = PanePreview
	p.saveSelectionState()
	return p.resizeDocTerminalCmd()
}

func (p *Plugin) diffPaneHeaderRow(diff *diffPane, width int, focused bool) string {
	return layoutDiffTabStrip(diff, width, focused).Row
}

func (p *Plugin) registerDiffPaneRegions(diff *diffPane, leafID int, box Box) {
	p.mouseHandler.HitMap.AddRect(regionPaneLeaf, box.X, box.Y, box.W, box.H, leafID)
}

func (p *Plugin) registerDiffTargetTabRegions(diff *diffPane, leafID int, box Box) {
	for _, tab := range layoutDiffTabStrip(diff, box.W, p.paneFocus == leafID).Tabs {
		p.mouseHandler.HitMap.AddRect(regionDiffTargetTab, box.X+tab.Col, box.Y, tab.Width, 1, diffTabHit{LeafID: leafID, Index: tab.Index})
	}
}

func (p *Plugin) registerDiffLeafHits(diff *diffPane, box Box) {
	view := diff.view()
	if view == nil {
		return
	}
	body := mouse.Rect{X: box.X, Y: box.Y + terminalHeaderRows, W: box.W, H: maxInt(box.H-terminalHeaderRows, 0)}
	if body.H < 1 {
		return
	}
	view.SetSize(body.W, body.H)
	p.attachDiffPaintTo(view)
	for _, hit := range view.FileHits(body) {
		p.mouseHandler.HitMap.AddRect(hit.ID, hit.Rect.X, hit.Rect.Y, hit.Rect.W, hit.Rect.H, hit.Data)
	}
	if d := view.DividerHit(body); d.W > 0 && d.H > 0 {
		p.mouseHandler.HitMap.AddRect(regionDiffTabDivider, d.X, d.Y, d.W, d.H, nil)
	}
}

func (p *Plugin) yankFocusedDiff() tea.Cmd {
	diff, _ := p.focusedDiffPane()
	view := diff.view()
	if view == nil {
		return nil
	}
	ident := view.Target.Identity()
	if ident == "" {
		return nil
	}
	return func() tea.Msg {
		if err := clipboard.WriteAll(ident); err != nil {
			return appmsg.ToastMsg{Message: "Copy failed: " + err.Error(), Duration: 2 * time.Second, IsError: true}
		}
		return appmsg.ToastMsg{Message: "Yanked: " + ident, Duration: 2 * time.Second}
	}
}

func (p *Plugin) resizeFocusedLeaf(delta int) tea.Cmd {
	leaf := FindPane(p.paneRoot, p.paneFocus)
	if leaf == nil || leaf.Split != nil || leaf.Kind == PaneTerminal {
		return nil
	}
	parent, inA := enclosingSplit(p.paneRoot, leaf.ID)
	if parent == nil || parent.Split == nil {
		return nil
	}
	if inA {
		SetRatio(p.paneRoot, parent.ID, parent.Split.Ratio+delta)
	} else {
		SetRatio(p.paneRoot, parent.ID, parent.Split.Ratio-delta)
	}
	p.saveSelectionState()
	return p.resizeDocTerminalCmd()
}

func (p *Plugin) paneLeafBox(leafID int) (Box, bool) {
	content, ok := p.previewContentBox()
	if !ok {
		return Box{}, false
	}
	layout, laid := LayoutPaneTree(p.paneRoot, content, paneTreeFloors(), p.paneFocus)
	if !laid {
		return Box{}, false
	}
	for _, placement := range layout.Leaves {
		if placement.Node != nil && placement.Node.ID == leafID {
			return placement.Box, true
		}
	}
	return Box{}, false
}

func (p *Plugin) activeDiffView() *workspacediff.View {
	if diff, _ := p.activeDiffPane(); diff != nil {
		if view := diff.view(); view != nil {
			return view
		}
	}
	return &p.diff
}

func (p *Plugin) diffLeafShowing() bool {
	diff, _ := p.activeDiffPane()
	return diff != nil && p.paneTreeShowing()
}
