package overview

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tabs"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacediff"
)

const (
	previewDiffRegionKind = "global-preview-diff"
	previewDiffTabKind    = "global-preview-diff-tab"
)

type previewDiffTabHit int

// previewDiff is the memory-only Diff pane beside the selected terminal.
type previewDiff struct {
	tabs    workspacediff.Group
	root    string
	surface string
	focused bool
}

func (d *previewDiff) view() *workspacediff.View {
	if d == nil {
		return nil
	}
	return d.tabs.ActiveView()
}

func (m *Model) openPreviewDiff(target workspacediff.Target) tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return nil
	}
	if target.Identity() == "" {
		target = workspacediff.WorkingTreeTarget()
	}
	if !features.IsEnabled(features.WorkspaceDocPanes.Name) {
		return appmsg.ShowToast(features.WorkspaceDocPanesDisabledDiff, 3*time.Second)
	}
	leafID, refusal := m.ensurePreviewPane(panelayout.Diff, "Diff")
	if refusal != nil {
		return refusal
	}
	if leafID == 0 {
		return nil
	}

	wasInteractive := m.PreviewInteractive()
	if m.preview.diff == nil {
		m.preview.diff = &previewDiff{}
	}
	diff := m.preview.diff
	diff.root = previewDiffPath(workspace)
	diff.surface = workspace.ID
	m.focusPreviewPane(panelayout.Diff)
	load := m.openOrFocusPreviewDiff(diff, target, workspace.ID)

	var cmds []tea.Cmd
	if wasInteractive {
		cmds = append(cmds, m.exitPreviewInteractive())
	}
	cmds = append(cmds, load, m.syncTerminalGeometry())
	return tea.Batch(cmds...)
}

func (m *Model) newPreviewDiffView(target workspacediff.Target) *workspacediff.View {
	view := &workspacediff.View{
		Target:   target,
		ViewMode: m.diff.ViewMode,
		State:    workspacediff.LoadStateLoading,
	}
	if target.Kind == workspacediff.TargetCommit {
		view.Focus = workspacediff.FocusCommitFiles
	}
	if w := state.GetDiffTabFileListWidth(); w > 0 {
		view.SetListWidth(w)
	}
	return view
}

func (m *Model) openOrFocusPreviewDiff(diff *previewDiff, target workspacediff.Target, workspaceID string) tea.Cmd {
	if diff == nil || target.Identity() == "" {
		return nil
	}
	if idx := diff.tabs.Find(target.Identity()); idx >= 0 {
		diff.tabs.Select(idx)
		return m.ensurePreviewDiffLoaded(diff, workspaceID)
	}
	view := m.newPreviewDiffView(target)
	if _, created := diff.tabs.OpenOrFocus(target, view); !created {
		return m.ensurePreviewDiffLoaded(diff, workspaceID)
	}
	return m.loadPreviewDiffView(view, diff.root, workspaceID)
}

func (m *Model) ensurePreviewDiffLoaded(diff *previewDiff, workspaceID string) tea.Cmd {
	if diff == nil {
		return nil
	}
	view := diff.view()
	if view == nil {
		return nil
	}
	if view.State != workspacediff.LoadStateUnknown && view.State != workspacediff.LoadStateLoading && view.State != workspacediff.LoadStateError {
		return nil
	}
	return m.loadPreviewDiffView(view, diff.root, workspaceID)
}

func (m *Model) loadPreviewDiffView(view *workspacediff.View, root, workspaceID string) tea.Cmd {
	if view == nil {
		return nil
	}
	view.Bind(root, workspaceID, m.preview.contentEpoch)
	view.State = workspacediff.LoadStateLoading
	switch view.Target.Kind {
	case workspacediff.TargetCommit:
		// A tab with nothing to load must not be left on "Loading" forever.
		if view.Target.A == "" {
			view.State = workspacediff.LoadStateError
			view.Error = "commit tab has no commit"
			return nil
		}
		return view.LoadCommit(view.Target.A)
	case workspacediff.TargetRange:
		if cmd := view.LoadRange(); cmd != nil {
			return cmd
		}
		view.State = workspacediff.LoadStateError
		view.Error = "range tab has no revisions"
		return nil
	default:
		return workspacediff.LoadSnapshotCmdAt(root, "", workspaceID, m.preview.contentEpoch, view.Target.Identity())
	}
}

func (m *Model) applyPreviewDiffSnapshot(msg workspacediff.SnapshotMsg) tea.Cmd {
	var cmds []tea.Cmd
	apply := func(diff *previewDiff) {
		if diff == nil {
			return
		}
		for _, item := range diff.tabs.Items {
			if item.Value != nil {
				cmds = append(cmds, item.Value.ApplySnapshotMsg(msg, item.Value.WorkDir, item.Value.WorkspaceID))
			}
		}
	}
	apply(m.preview.diff)
	if cached, ok := m.preview.paneCache[msg.WorkspaceID]; ok {
		apply(cached.diff)
	}
	return tea.Batch(cmds...)
}

func (m *Model) applyPreviewDiffRange(msg workspacediff.RangeMsg) tea.Cmd {
	var cmds []tea.Cmd
	apply := func(diff *previewDiff) {
		if diff == nil {
			return
		}
		for _, item := range diff.tabs.Items {
			if item.Value != nil {
				cmds = append(cmds, item.Value.ApplyRangeMsg(msg))
			}
		}
	}
	apply(m.preview.diff)
	if cached, ok := m.preview.paneCache[msg.WorkspaceID]; ok {
		apply(cached.diff)
	}
	return tea.Batch(cmds...)
}

func (m *Model) applyPreviewDiffCommit(msg workspacediff.CommitDetailMsg) tea.Cmd {
	var cmds []tea.Cmd
	apply := func(diff *previewDiff) {
		if diff == nil {
			return
		}
		for _, item := range diff.tabs.Items {
			if item.Value != nil {
				cmds = append(cmds, item.Value.ApplyCommitDetail(msg))
			}
		}
	}
	apply(m.preview.diff)
	if cached, ok := m.preview.paneCache[msg.WorkspaceID]; ok {
		apply(cached.diff)
	}
	return tea.Batch(cmds...)
}

func (m *Model) applyPreviewDiffFile(msg workspacediff.CommitFileDiffMsg) tea.Cmd {
	var cmds []tea.Cmd
	apply := func(diff *previewDiff) {
		if diff == nil {
			return
		}
		for _, item := range diff.tabs.Items {
			if item.Value != nil {
				cmds = append(cmds, item.Value.ApplyCommitFileDiff(msg))
			}
		}
	}
	apply(m.preview.diff)
	if cached, ok := m.preview.paneCache[msg.WorkspaceID]; ok {
		apply(cached.diff)
	}
	return tea.Batch(cmds...)
}

func (m *Model) closePreviewDiff() tea.Cmd {
	if m.preview.diff == nil {
		return nil
	}
	m.forgetPreviewDiff(m.preview.workspaceID)
	if leaf := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Diff); leaf != nil {
		m.preview.paneRoot, m.preview.paneFocus = panelayout.Close(m.preview.paneRoot, leaf.ID)
	}
	if m.preview.doc != nil {
		m.focusPreviewPane(panelayout.Document)
		return m.syncTerminalGeometry()
	}
	if m.preview.issue != nil {
		m.focusPreviewPane(panelayout.Issue)
		return m.syncTerminalGeometry()
	}
	if m.preview.resource != nil {
		m.focusPreviewPane(panelayout.Resource)
		return m.syncTerminalGeometry()
	}
	return tea.Batch(m.focusList(), m.syncTerminalGeometry())
}

func (m *Model) forgetPreviewDiff(workspaceID string) {
	m.preview.diff = nil
	if cached, ok := m.preview.paneCache[workspaceID]; ok {
		cached.diff = nil
		m.preview.paneCache[workspaceID] = cached
	}
}

func (m *Model) closePreviewDiffTab() tea.Cmd {
	if m.preview.diff == nil {
		return nil
	}
	if len(m.preview.diff.tabs.Items) <= 1 {
		return m.closePreviewDiff()
	}
	m.preview.diff.tabs.CloseActive()
	return nil
}

func (m *Model) cyclePreviewDiffTab(delta int) tea.Cmd {
	if m.preview.diff == nil || len(m.preview.diff.tabs.Items) < 2 {
		return nil
	}
	m.preview.diff.tabs.Cycle(delta)
	return nil
}

func (m *Model) clickPreviewDiffTab(index int) tea.Cmd {
	if m.preview.diff == nil {
		return nil
	}
	m.focusPreviewPane(panelayout.Diff)
	if index == m.preview.diff.tabs.Active {
		return nil
	}
	m.preview.diff.tabs.Select(index)
	return nil
}

func (m *Model) renderPreviewDiff(diff *previewDiff, box termpreview.Box) string {
	view := diff.view()
	contentHeight := max(box.H-termpreview.HeaderRows, 0)
	focused := m.PreviewFocused() && diff.focused
	if view != nil {
		view.SetSize(box.W, contentHeight)
	}
	header := m.composePreviewHeader(layoutPreviewDiffStrip(diff.tabs, ui.ReserveHeaderClose(box.W).TabsWidth, focused).Row, box.W, panelayout.Diff)
	if contentHeight <= 0 {
		return header
	}
	body := ""
	if view != nil {
		body = view.Render(box.W, contentHeight, workspacediff.RenderOpts{
			Truncate: func(s string, w int, _ string) string { return termpreview.TruncateANSI(s, w) },
			Handle:   m.dividerHandleState(previewDiffDividerKind, 0),
		})
	}
	return header + "\n" + body
}

func layoutPreviewDiffStrip(group workspacediff.Group, width int, focused bool) tabs.Strip {
	labels := make([]tabs.Label, len(group.Items))
	for i, item := range group.Items {
		text := item.Key
		if item.Value != nil {
			text = item.Value.Target.TabLabel()
		}
		labels[i] = tabs.Label{Text: text}
	}
	return tabs.LayoutStrip(labels, group.Active, width, focused, func(text string, _, _, maxWidth int, _ bool) string {
		if maxWidth < 1 {
			return ""
		}
		return text
	})
}

// registerPreviewDiffRegion covers the Diff leaf's INNER box, which is the
// lowest-priority target inside it.
func (m *Model) registerPreviewDiffRegion(diffBox termpreview.Box) {
	if m.preview.diff == nil {
		return
	}
	m.workspacesMouse.HitMap.AddRect(
		previewDiffRegionKind,
		diffBox.X, diffBox.Y, diffBox.W, diffBox.H,
		previewDiffRegionKind,
	)
}

func (m *Model) registerPreviewDiffTabRegions(diffBox termpreview.Box) {
	if m.preview.diff == nil {
		return
	}
	focused := m.PreviewFocused() && m.preview.diff.focused
	for _, tab := range layoutPreviewDiffStrip(m.preview.diff.tabs, ui.ReserveHeaderClose(diffBox.W).TabsWidth, focused).Tabs {
		m.workspacesMouse.HitMap.AddRect(
			previewDiffTabKind,
			diffBox.X+tab.Col, diffBox.Y, tab.Width, 1,
			previewDiffTabHit(tab.Index),
		)
	}
}

// registerPreviewDiffLeafHits registers the targets the diff view owns inside
// its own body: the file rows and its list/hunk divider.
func (m *Model) registerPreviewDiffLeafHits(diffBox termpreview.Box) {
	if m.preview.diff == nil {
		return
	}
	view := m.preview.diff.view()
	if view == nil {
		return
	}
	body, ok := diffLeafBody(diffBox)
	if !ok {
		return
	}
	view.SetSize(body.W, body.H)
	for _, hit := range view.FileHits(body) {
		m.workspacesMouse.HitMap.AddRect(hit.ID, hit.Rect.X, hit.Rect.Y, hit.Rect.W, hit.Rect.H, hit.Data)
	}
	if d := view.DividerHit(body); d.W > 0 && d.H > 0 {
		m.workspacesMouse.HitMap.AddRect(previewDiffDividerKind, d.X, d.Y, d.W, d.H, previewDiffDividerHit{})
	}
}

func (m *Model) previewDiffLeafBody() (mouse.Rect, bool) {
	if m.preview.diff == nil {
		return mouse.Rect{}, false
	}
	peer, ok := m.previewPeerBox()
	if !ok {
		return mouse.Rect{}, false
	}
	diffBox, ok := m.previewPaneBox(panelayout.Diff, peer)
	if !ok {
		return mouse.Rect{}, false
	}
	return diffLeafBody(diffBox)
}

// diffLeafBody is the Diff leaf's box below its header row.
func diffLeafBody(diffBox termpreview.Box) (mouse.Rect, bool) {
	if diffBox.W < 1 {
		return mouse.Rect{}, false
	}
	body := mouse.Rect{
		X: diffBox.X, Y: diffBox.Y + termpreview.HeaderRows,
		W: diffBox.W, H: max(diffBox.H-termpreview.HeaderRows, 0),
	}
	if body.H < 1 {
		return mouse.Rect{}, false
	}
	return body, true
}

func (m *Model) previewDiffDragView() *workspacediff.View {
	if m.preview.diff != nil {
		if view := m.preview.diff.view(); view != nil {
			return view
		}
	}
	return &m.diff
}

func (m *Model) previewDiffDragWidth() int {
	if body, ok := m.previewDiffLeafBody(); ok {
		return body.W
	}
	if peer, ok := m.previewPeerBox(); ok {
		return paneframe.Inset(peer).W
	}
	return m.width
}

func (m *Model) previewDiffPaneKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	diff := m.preview.diff
	if diff == nil || !diff.focused || m.PreviewInteractive() {
		return false, nil
	}
	switch msg.String() {
	case "q", "esc":
		return true, m.closePreviewDiff()
	case "x":
		return true, m.closePreviewDiffTab()
	case "{":
		return true, m.cyclePreviewDiffTab(-1)
	case "}":
		return true, m.cyclePreviewDiffTab(1)
	case "Y", "shift+y":
		return true, m.yankPreviewDiff()
	}
	view := diff.view()
	if view == nil {
		return true, nil
	}
	cmd, handled := view.HandleKey(msg)
	m.persistDiffViewModeFrom(view)
	return handled, cmd
}

func (m *Model) persistDiffViewModeFrom(view *workspacediff.View) {
	if view == nil {
		return
	}
	m.diff.ViewMode = view.ViewMode
	m.persistDiffViewMode()
}

func (m *Model) yankPreviewDiff() tea.Cmd {
	view := m.preview.diff.view()
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

func (m *Model) handlePreviewDiffMouse(action mouse.MouseAction) tea.Cmd {
	if tab, ok := action.Region.Data.(previewDiffTabHit); ok {
		if action.Type == mouse.ActionClick || action.Type == mouse.ActionDoubleClick {
			return m.clickPreviewDiffTab(int(tab))
		}
		if action.Type == mouse.ActionScrollUp || action.Type == mouse.ActionScrollDown {
			if view := m.preview.diff.view(); view != nil {
				view.ScrollContent(action.Delta, view.Height())
			}
		}
		return nil
	}
	if m.preview.diff == nil {
		return nil
	}
	if workspacediff.IsBodyRegion(action.Region.ID) {
		m.focusPreviewPane(panelayout.Diff)
		view := m.preview.diff.view()
		if view == nil {
			return nil
		}
		switch action.Type {
		case mouse.ActionClick:
			return view.HandleClick(action.Region.ID, action.Region.Data)
		case mouse.ActionDoubleClick:
			return view.HandleDoubleClick(action.Region.ID, action.Region.Data)
		case mouse.ActionScrollUp, mouse.ActionScrollDown:
			return view.HandleWheel(action.Region.ID, action.Delta)
		}
		return nil
	}
	kind, _ := regionKind(action.Region)
	if kind != previewDiffRegionKind {
		return nil
	}
	switch action.Type {
	case mouse.ActionClick, mouse.ActionDoubleClick:
		m.focusPreviewPane(panelayout.Diff)
	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		if view := m.preview.diff.view(); view != nil {
			view.ScrollContent(action.Delta, view.Height())
		}
	}
	return nil
}
