package overview

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// The global Workspaces tab is a second projection of the same cross-project
// catalog the Agents board renders. It does not own a collector, a cache, a
// generation, or a poll: syncBoard rebuilds both projections from `m.results`,
// so switching tabs re-renders what is already collected instead of launching a
// duplicate tmux/Git fan-out.
//
// The list hosts cross-project presentation state, while shell creation and
// rename persistence run through shared state-free operations. Worktree and
// Git lifecycle actions join it only after their refusal/execution rules have
// moved behind the same kind of boundary. The other thing the browser drives
// is an existing live pane:
// Enter, a click in the pane, or E hands the keyboard to the pane behind the
// selected row (internal/overview/interactive.go), which creates nothing and
// destroys nothing — it types into a session that is already there. The list
// stays the browse surface; there is no watched-preview keyboard mode.

// syncWorkspaces rebuilds the list projection from the same results map the
// board uses. It is called from syncBoard so the two projections can never
// disagree about what has been collected.
func (m *Model) syncWorkspaces() {
	items := make([]workspacelist.Item, 0, len(m.catalog))
	m.catalog = make(map[string]workspaceinventory.Workspace)
	var failures []string
	for order, project := range m.projects {
		key := projectKey(project)
		result, loaded := m.results[key]
		if !loaded {
			continue
		}
		if err, failed := m.projectErrors[key]; failed {
			failures = append(failures, project.Name+" unavailable: "+err.Error())
		}
		if result.Err != nil && len(result.Workspaces) == 0 {
			continue
		}
		for _, workspace := range result.Workspaces {
			m.catalog[workspace.ID] = workspace
			item := listItem(workspace.Item(), project.Name, order, m.stale[key])
			if badge, hasBadge := m.pendingViewBadge(workspace.TmuxName); hasBadge {
				item.NameMeta = append(item.NameMeta, workspacelist.RowField{Text: badge, Rendered: styles.Muted.Render(badge)})
			}
			if !m.showIdleWorktrees && item.Group == workspacelist.GroupNoSession {
				continue
			}
			items = append(items, item)
		}
	}
	sort.SliceStable(failures, func(a, b int) bool { return failures[a] < failures[b] })
	m.workspaces.SetItems(items)
	m.syncCreateActions()
	if !m.loading {
		m.pruneGonePins()
	}
	m.workspaces.SetFailures(failures)
	m.workspaces.SetLoading(m.loading)
	m.workspaces.SetEmptyText(workspacesEmptyText(m.showIdleWorktrees))
}

func (m *Model) syncCreateActions() {
	header := &workspacelist.SidebarAction{ID: globalCreateActionID, Label: "+"}
	var sectionActions map[string]*workspacelist.SidebarAction
	if m.workspaces.Sort() == workspacelist.SortProject {
		sectionActions = make(map[string]*workspacelist.SidebarAction, len(m.projects))
		for _, project := range m.projects {
			key := projectKey(project)
			sectionActions[key] = &workspacelist.SidebarAction{ID: globalCreateActionID + ":" + key, Label: "+"}
		}
	}
	m.workspaces.SetCreateActions(header, sectionActions)
}

func (m *Model) pruneGonePins() {
	ids := m.workspaces.PinnedIDs()
	if len(ids) == 0 {
		return
	}
	kept := make([]string, 0, len(ids))
	dropped := false
	for _, id := range ids {
		if _, ok := m.catalog[id]; ok {
			kept = append(kept, id)
			continue
		}
		dropped = true
	}
	if !dropped {
		return
	}
	m.workspaces.SetPinned(kept)
	_ = savePinnedWorkspaceIDs(kept)
}

// listItem projects one catalog row into the shared list component's display
// model. Plain shells and worktrees carry no agentstatus value, so this is
// where they receive their presentation bucket — "live" or "no session" —
// instead of a fabricated semantic state.
func listItem(item workspaceinventory.Item, projectName string, order int, stale bool) workspacelist.Item {
	row := workspacelist.Item{
		ID:           item.ID,
		Name:         item.Name,
		Project:      projectName,
		ProjectKey:   item.ProjectKey,
		ProjectOrder: order,
		Branch:       item.Branch,
		Task:         item.TaskID,
		Provider:     item.Provider,
		TmuxName:     item.TmuxName,
		Kind:         string(item.Kind),
	}
	if row.Project == "" {
		row.Project = item.ProjectName
	}
	switch {
	case item.Agent != nil:
		row.Status = item.Agent.Label
		row.ChangedAt = item.Agent.ChangedAt
		row.Group = laneGroup(item.Agent.Lane)
		row.Marker = workspacelist.RowMarker{Icon: item.Agent.Icon, Lane: string(item.Agent.Lane)}
		if item.Agent.Health {
			row.Marker.Lane = ""
			switch item.Agent.Icon {
			case "✗":
				row.Marker.Tone = workspacelist.MarkerError
			case "⚠":
				row.Marker.Tone = workspacelist.MarkerWarning
			default:
				row.Marker.Tone = workspacelist.MarkerMuted
			}
		}
		if !item.Live && !item.Ambiguous {
			row.Group = workspacelist.GroupNoSession
		}
	case item.Ambiguous:
		row.Status, row.Group = "ambiguous panes", workspacelist.GroupPaused
		row.Marker = workspacelist.RowMarker{Icon: "?", Tone: workspacelist.MarkerWarning}
	case item.Live:
		row.Status, row.Group = "live", workspacelist.GroupLive
		row.Marker = workspacelist.RowMarker{Icon: "◎", Tone: workspacelist.MarkerLive}
	default:
		row.Status, row.Group = "no session", workspacelist.GroupNoSession
		if item.IsMain {
			row.Marker = workspacelist.RowMarker{Icon: "◉", Tone: workspacelist.MarkerMain}
		} else {
			row.Marker = workspacelist.RowMarker{Icon: "○", Tone: workspacelist.MarkerMuted}
		}
	}
	if stale {
		row.Status += " · stale"
	}
	// A shell has neither task nor branch, and its tmux session name is an
	// identity key rather than something a reader acts on, so it shows nothing.
	detail := item.TaskID
	if detail == "" {
		detail = item.Branch
	}
	row.Detail = detail
	return row
}

// laneGroup is the vertical projection of the shared Kanban lanes. The mapping
// itself lives in workspacelist so the project sidebar and this list cannot
// come to different conclusions about where a blocked agent belongs.
func laneGroup(lane agentstatus.LaneID) workspacelist.Group {
	return workspacelist.GroupForLane(string(lane))
}

// workspacesLayout is the tab's one placement rule. Three arrangements are
// possible — the preview alone (hidden sidebar, or narrow and focused), the
// list alone (narrow), and the ordinary split — and both the renderer and the
// live terminal read the answer from here. A second derivation is how the
// terminal ends up sized against a box nobody drew.
type workspacesLayout struct {
	// previewOnly is the preview filling the tab, with no list beside it.
	previewOnly bool
	// listOnly is the narrow arrangement, where the list has the tab to itself
	// and there is no preview box on screen to place anything in.
	listOnly bool
	// previewDrawn reports that the preview box has room to draw in, and
	// therefore that box is meaningful. A degenerate size is not an arrangement.
	previewDrawn bool
	// peer is the preview panel's OUTER rectangle — the peer of the list,
	// including the chrome a lone terminal still spends once. The pane tree is
	// laid out in this box so each leaf owns its own border, exactly as the
	// project workspace does. Inner geometry is paneframe.Inset(peer).
	peer  termpreview.Box
	split termpreview.Split
}

func (m *Model) workspacesLayout() workspacesLayout {
	drawable := m.width >= 1 && m.height-paneframe.BorderWidth >= 1
	if !m.sidebarVisible || (m.previewNarrow() && m.preview.full) {
		layout := workspacesLayout{previewOnly: true, previewDrawn: drawable}
		if drawable {
			layout.peer = termpreview.Box{X: 0, Y: 0, W: m.width, H: m.height}
		}
		return layout
	}
	if m.previewNarrow() {
		return workspacesLayout{listOnly: true}
	}
	split := m.previewSplit(m.width)
	layout := workspacesLayout{previewDrawn: drawable, split: split}
	if drawable {
		layout.peer = termpreview.Box{X: split.PreviewX, Y: 0, W: split.PreviewWidth, H: m.height}
	}
	return layout
}

// WorkspacesView renders the global Workspaces tab: the list on the left and
// exactly one terminal box on the right.
//
// At widths that cannot sustain two useful panes the list is full width and the
// preview replaces it when focused, rather than shrinking both into unreadable
// columns.
func (m *Model) WorkspacesView(width, height int) string {
	m.width, m.height = width, height
	if m.reuseWorkspacesViewOnce && m.workspacesViewCacheOK &&
		m.workspacesViewCacheW == width && m.workspacesViewCacheH == height {
		m.reuseWorkspacesViewOnce = false
		return m.workspacesViewCache
	}
	m.reuseWorkspacesViewOnce = false
	if m.workspacesMouse == nil {
		m.workspacesMouse = mouse.NewHandler()
	}
	m.workspacesMouse.Clear()
	layout := m.workspacesLayout()
	var view string
	if layout.previewOnly {
		m.addPreviewRegion(0, width, height)
		view = m.renderPreviewPeer(layout.peer)
	} else if layout.listOnly {
		m.addSidebarRegion(0, width, height)
		view = styles.RenderPanel(m.renderWorkspaceList(globalContentInset, 1, width-globalPanelOverhead, height-2), width, height, true)
	} else {
		split := layout.split
		m.addSidebarRegion(0, split.SidebarWidth, height)
		m.addPreviewRegion(split.PreviewX, split.PreviewWidth, height)
		list := m.renderWorkspaceList(globalContentInset, 1, split.SidebarContentWidth, height-2)

		leftPane := styles.RenderPanel(list, split.SidebarWidth, height, !m.PreviewFocused())
		rightPane := m.renderPreviewPeer(layout.peer)
		divider := ui.RenderHandle(height, true, m.dividerHandleState(workspacesDividerRegion, 0))
		// Register the forgiving three-column divider target last, above both pane
		// regions and any list row that reaches the content edge.
		m.workspacesMouse.HitMap.AddRect(workspacesDividerRegion, split.SidebarWidth, 0, 3, height, workspacesDividerRegion)
		view = lipgloss.JoinHorizontal(lipgloss.Top, leftPane, divider, rightPane)
	}
	if m.renameOpen {
		view = m.overlayRenameShell(view, width, height)
	}
	if m.createOpen {
		view = m.overlayCreateShell(view, width, height)
	}
	if m.deleteOpen {
		view = m.overlayDelete(view, width, height)
	}
	if m.viewFlyoutOpen {
		view = m.overlayViewFlyout(view, width, height)
	}
	m.workspacesViewCache = view
	m.workspacesViewCacheW = width
	m.workspacesViewCacheH = height
	m.workspacesViewCacheOK = true
	return view
}

// renderWorkspaceList draws the list and registers its regions at an x offset,
// so a click lands on the row the list actually drew there.
func (m *Model) renderWorkspaceList(x, y, width, height int) string {
	m.syncCreateActions()
	rendered := m.workspaces.Render(workspacelist.RenderOptions{
		Width:   width,
		Height:  height,
		Title:   "Workspaces",
		Focused: !m.PreviewFocused(),
		Now:     m.now(),
	})
	for _, region := range rendered.Regions {
		m.workspacesMouse.HitMap.AddRect(string(region.Kind), x+region.X, y+region.Y, region.W, region.H, region)
	}
	return rendered.View
}

// previewRegionKind is the hit region covering the preview box.
const previewRegionKind = "global-preview"

const (
	workspacesSidebarRegion = "global-workspaces-sidebar"
	workspacesDividerRegion = "global-workspaces-divider"
	previewPaneDividerKind  = "global-preview-pane-divider"
)

type previewPaneDividerHit int

func (m *Model) addSidebarRegion(x, width, height int) {
	if width > 0 && height > 0 {
		m.workspacesMouse.HitMap.AddRect(workspacesSidebarRegion, x, 0, width, height, workspacesSidebarRegion)
	}
}

func (m *Model) addPreviewRegion(x, width, height int) {
	if width < 1 || height < 1 {
		return
	}
	m.workspacesMouse.HitMap.AddRect(previewRegionKind, x, 0, width, height, previewRegionKind)
}

// registerPreviewOutputRegions registers this frame's targets from the same
// placements the canvas drew from, so a click cannot land on geometry the frame
// did not draw. The ORDER is the shared frame's — leaves, then dividers, then
// tab strips, then close buttons, then content-owned targets — which is what
// keeps a click on a stacked leaf's header behaving identically here and in the
// project workspace.
func (m *Model) registerPreviewOutputRegions(peer termpreview.Box) {
	layout, ok := m.layoutPreviewPanes(peer)
	if !ok {
		return
	}
	paneframe.RegisterRegions(paneRegions{m}, layout)
}

func (m *Model) setHandleHover(action mouse.MouseAction) {
	m.hoverHandleRegion = ""
	m.hoverHandleSplit = 0
	if action.Region == nil {
		return
	}
	switch action.Region.ID {
	case workspacesDividerRegion, previewDiffDividerKind:
		m.hoverHandleRegion = action.Region.ID
	case previewPaneDividerKind:
		m.hoverHandleRegion = action.Region.ID
		if hit, ok := action.Region.Data.(previewPaneDividerHit); ok {
			m.hoverHandleSplit = int(hit)
		}
	}
}

func (m *Model) dividerHandleState(region string, splitID int) ui.HandleState {
	dragging := m.workspacesMouse != nil && m.workspacesMouse.IsDragging() && m.workspacesMouse.DragRegion() == region
	hovering := m.hoverHandleRegion == region
	if region != previewPaneDividerKind {
		return ui.HandleStateFrom(hovering, dragging)
	}
	return paneframe.HandleStateFor(splitID, dragging, m.preview.paneDragSplitID, hovering, m.hoverHandleSplit)
}

func (m *Model) setPreviewCloseHover(action mouse.MouseAction) {
	if action.Region == nil {
		m.previewCloseHover = false
		m.hoverPreviewClose = 0
		return
	}
	hit, ok := action.Region.Data.(previewPaneCloseHit)
	m.previewCloseHover = ok
	if ok {
		m.hoverPreviewClose = hit.Kind
		return
	}
	m.hoverPreviewClose = 0
}

func (m *Model) scrollPreviewClose(kind panelayout.Kind, delta int) tea.Cmd {
	switch kind {
	case panelayout.Document:
		if m.preview.doc != nil && m.preview.doc.view() != nil {
			m.preview.doc.view().Scroll(delta)
		}
	case panelayout.Issue:
		if m.preview.issue != nil && m.preview.issue.view() != nil {
			m.preview.issue.view().Scroll(delta)
		}
	case panelayout.Diff:
		if view := m.preview.diff.view(); view != nil {
			view.ScrollContent(delta, view.Height())
		}
	}
	return nil
}

func (m *Model) previewCloseWheelAtBoundary(kind panelayout.Kind, delta int) bool {
	switch kind {
	case panelayout.Document:
		if m.preview.doc == nil {
			return true
		}
		view := m.preview.doc.view()
		return view == nil || view.ScrollAtBoundary(delta)
	case panelayout.Issue:
		if m.preview.issue == nil {
			return true
		}
		view := m.preview.issue.view()
		return view == nil || view.ScrollAtBoundary(delta)
	case panelayout.Diff:
		view := m.preview.diff.view()
		if view == nil {
			return true
		}
		return view.ScrollAtBoundary(delta, view.Height())
	default:
		return true
	}
}

// WorkspacesFilterFocused reports that the inline filter owns the keyboard, so
// the app can report a text-input context and keep its own printable shortcuts
// off the query.
func (m *Model) WorkspacesFilterFocused() bool {
	return m.workspaces.Filter().Focused()
}

// WorkspacesFilterActive reports that a query is still narrowing the list, even
// after enter handed navigation back to it. The app asks so that esc clears the
// query the user can see before it means "leave the global space".
func (m *Model) WorkspacesFilterActive() bool {
	return m.workspaces.Filter().Active()
}

// WorkspacesPaste appends pasted text to a focused filter.
func (m *Model) WorkspacesPaste(text string) bool {
	if !m.WorkspacesFilterFocused() {
		return false
	}
	m.workspaces.Filter().Insert(text)
	m.workspaces.Reproject()
	return true
}

// WorkspacesPreviewCmd is the command a paste or another caller-side selection
// change owes the preview: the cursor may have moved to a different item, and
// the preview follows the cursor.
func (m *Model) WorkspacesPreviewCmd() tea.Cmd { return m.previewSync() }

// WorkspacesKey handles one key for the global Workspaces tab and reports
// whether it consumed it. While the filter has focus it consumes everything
// except live navigation, which is what keeps app shortcuts from stealing
// printable characters mid-query.
func (m *Model) WorkspacesKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	key := msg.String()
	// A live pane owns the keyboard outright, before the filter and before any
	// of the browser's own keys: while the user is typing into a terminal, "/"
	// is a slash, "q" is a q, and ctrl+c interrupts what is running there.
	if m.PreviewInteractive() {
		return m.previewKey(msg)
	}
	if m.renameOpen {
		return m.handleRenameShellKey(msg)
	}
	if m.createOpen {
		return m.handleCreateShellKey(msg)
	}
	if m.deleteOpen {
		return m.handleDeleteKey(msg)
	}
	// The fly-out is an overlay, not a third browse mode. Esc / backdrop close
	// it and leave the list as the rest state. "/" still focuses the filter.
	if m.viewFlyoutOpen {
		if key == "/" {
			m.closeViewFlyout()
			cmd := m.focusList()
			m.workspaces.FocusFilter()
			return true, cmd
		}
		return m.handleViewFlyoutKey(msg)
	}
	// Tab cycles the windows on screen. It sits above the filter so focus moves
	// even mid-query — the query text survives, the filter merely stops owning
	// the keyboard — and below the live-pane check above, which is the one
	// exception: a terminal being typed into keeps its own tab.
	if key == "tab" || key == "shift+tab" {
		if m.workspaces.Filter().Focused() {
			m.workspaces.Filter().Blur()
		}
		return true, m.cyclePaneFocus(key == "shift+tab")
	}
	if m.workspaces.Filter().Focused() {
		// ctrl+c is the host's, even mid-query. It is one of sidecar's two ways
		// out, and every other text-input surface hands it back (internal/app's
		// precedence level 2 intercepts it before forwarding); a focused filter
		// must not be the one place the quit confirmation is unreachable.
		if key == "ctrl+c" {
			return false, nil
		}
		// Keys the filter ignores are navigation, which stays live while
		// filtering. Anything else is swallowed: a focused query is a text
		// input, and a stray key must not reach the app's global switch.
		if m.workspaces.FilterKey(key, msg.Text) == workspacelist.KeyIgnored {
			m.navigateWorkspaces(key)
		}
		return true, m.previewSync()
	}
	if key == "\\" {
		return true, m.toggleWorkspaceSidebar()
	}
	// While the preview has focus its keys come first, so list navigation
	// cannot move the cursor out from under the output being read.
	if m.PreviewFocused() {
		if handled, cmd := m.previewKey(msg); handled {
			return true, cmd
		}
	}

	switch key {
	case "D":
		if workspace, ok := m.SelectedWorkspace(); ok && workspace.Kind == workspaceinventory.KindShell {
			return true, m.OpenDeleteSelectedShell()
		}
		return false, nil
	case "m":
		if workspace, ok := m.SelectedWorkspace(); ok && mergeRefusal(workspace) == "" {
			return true, m.StartSelectedMerge()
		}
		return false, nil
	case "n":
		return true, m.OpenCreateWorktree("")
	case "ctrl+n":
		return true, m.OpenCreateShell("")
	case "enter", interactiveEnterKeyAlt:
		// Enter (and E) start typing in the selected live pane. A row with no
		// live pane refuses and stays on the list — it does not navigate.
		// Double-click is the remaining jump-to-project path. Diff/Task are
		// views of the row, so typing always happens on Output.
		return true, m.enterPreviewInteractive()
	case "/":
		cmd := m.focusList()
		m.workspaces.FocusFilter()
		return true, cmd
	case "v", "s":
		// v is the shared key for View, matching the project sidebar. s stays
		// as an alias because it is what this surface has always answered to.
		m.openViewFlyout()
		return true, nil
	case "p":
		return true, m.toggleWorkspacePin()
	case "R":
		if workspace, ok := m.SelectedWorkspace(); ok {
			switch workspace.Kind {
			case workspaceinventory.KindShell:
				return true, m.OpenRenameShell()
			case workspaceinventory.KindWorktree:
				return true, m.OpenRenameWorktree()
			}
		}
		return false, nil
	case "O":
		return true, m.OpenSelectedInGit()
	case "r":
		return true, tea.Batch(m.Start(m.projects), m.previewSelect())
	case "esc":
		if m.workspaces.Filter().Active() {
			m.workspaces.Filter().Reset()
			m.workspaces.Reproject()
			return true, m.previewSync()
		}
		return false, nil
	}
	if m.navigateWorkspaces(key) {
		return true, m.previewSync()
	}
	return false, nil
}

// WorkspaceFocusContext is the keymap, footer, and help context for this tab.
// List, filter, rename, typing, and a focused document or issue leaf each have
// their own. There is no watched-preview context: hiding the sidebar is layout
// only, and the keyboard stays on the list until the user types or focuses a
// content leaf.
func (m *Model) WorkspaceFocusContext() string {
	if m.PreviewInteractive() {
		return "global-workspaces-terminal"
	}
	if m.renameOpen {
		return "global-workspaces-rename"
	}
	if m.createOpen {
		return "global-workspaces-create"
	}
	if m.deleteOpen {
		return ctxGlobalWorkspacesDelete
	}
	if m.WorkspacesFilterFocused() {
		return "global-workspaces-filter"
	}
	if m.issuePaneFocused() {
		return "global-workspaces-issue"
	}
	if m.diffPaneFocused() {
		return ctxGlobalWorkspacesDiff
	}
	if m.docPaneFocused() {
		return "global-workspaces-doc"
	}
	return "global-workspaces"
}

func (m *Model) issuePaneFocused() bool {
	return m.PreviewFocused() && !m.PreviewInteractive() &&
		m.preview.issue != nil && m.preview.issue.focused
}

func (m *Model) docPaneFocused() bool {
	return m.PreviewFocused() && !m.PreviewInteractive() &&
		m.preview.doc != nil && m.preview.doc.focused
}

func (m *Model) diffPaneFocused() bool {
	return m.PreviewFocused() && !m.PreviewInteractive() &&
		m.preview.diff != nil && m.preview.diff.focused
}

func (m *Model) WorkspaceSidebarVisible() bool { return m.sidebarVisible }

func (m *Model) toggleWorkspacePin() tea.Cmd {
	item, ok := m.workspaces.Selected()
	if !ok {
		return nil
	}
	ids := m.workspaces.TogglePin(item.ID)
	if !m.loading {
		ids = m.knownPinnedIDs(ids)
		m.workspaces.SetPinned(ids)
	}
	_ = savePinnedWorkspaceIDs(ids)
	return nil
}

func (m *Model) knownPinnedIDs(ids []string) []string {
	if len(m.catalog) == 0 {
		return ids
	}
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := m.catalog[id]; ok {
			kept = append(kept, id)
		}
	}
	return kept
}

// toggleWorkspaceSidebar shows or hides the list. It is a layout toggle only:
// hiding fills the tab with the preview and leaves the keyboard on the list, so
// j/k still browse, enter still types, and esc still leaves global. Restoring
// the sidebar also ends interactive mode if a pane was being typed into.
func (m *Model) toggleWorkspaceSidebar() tea.Cmd {
	m.sidebarVisible = !m.sidebarVisible
	if m.sidebarVisible {
		return m.focusList()
	}
	m.preview.full = true
	return tea.Batch(m.syncTerminalGeometry(), appmsg.ShowToast("Sidebar hidden (\\ to restore)", 2*time.Second))
}

// WorkspacesResize adopts a new tab size ahead of the frame that will use it,
// so a live pane is resized when the window is, rather than the next time a
// message happens to reach the terminal.
func (m *Model) WorkspacesResize(width, height int) tea.Cmd {
	m.width, m.height = width, height
	// A window narrow enough to drop the preview would leave the keyboard
	// pointed at a box that is no longer drawn, so an interactive preview
	// takes the whole tab instead.
	if m.PreviewFocused() && m.previewNarrow() {
		m.preview.full = true
	}
	return m.syncTerminalGeometry()
}

func (m *Model) navigateWorkspaces(key string) bool {
	switch key {
	case "j", "down", "ctrl+n":
		m.workspaces.Move(1)
	case "k", "up", "ctrl+p":
		m.workspaces.Move(-1)
	case "g", "home":
		m.workspaces.Top()
	case "G", "end":
		m.workspaces.Bottom()
	default:
		return false
	}
	return true
}

// WorkspacesMouse routes a mouse event to the list using the regions the last
// render registered.
func (m *Model) WorkspacesMouse(msg tea.Msg) tea.Cmd {
	mouseMsg, ok := msg.(tea.MouseMsg)
	if !ok {
		return nil
	}
	if m.renameOpen {
		return m.handleRenameShellMouse(mouseMsg)
	}
	if m.createOpen {
		return m.handleCreateShellMouse(mouseMsg)
	}
	if m.deleteOpen {
		return m.handleDeleteMouse(mouseMsg)
	}
	if m.viewFlyoutOpen {
		return m.handleViewFlyoutMouse(mouseMsg)
	}
	if m.workspacesMouse == nil {
		return nil
	}
	// Every mouse event counts as mouse activity for the live pane, whether or
	// not it is routed there: the terminal component's bare-"[" gate reads a
	// host-wide last-mouse time, and a split SGR sequence would otherwise leak
	// into the pane as a literal bracket.
	if m.preview.terminal != nil {
		m.preview.terminal.NoteMouseActivity()
	}

	wasDragging := m.workspacesMouse.IsDragging()
	dragSourceBefore := m.workspacesMouse.DragRegion()
	action := m.workspacesMouse.HandleMouse(mouseMsg)
	if action.Type == mouse.ActionHover {
		// Record before the terminal gesture machine: a press that opened the
		// pane can still look like a lost release, and that path must not
		// swallow the X's hover.
		m.setPreviewCloseHover(action)
		m.setHandleHover(action)
	}
	// What a pointer action over a terminal means is the shared layer's; what
	// this surface does about it is its own.
	switch m.previewPointerIntent(action, wasDragging, dragSourceBefore) {
	case tty.PointerAbandon:
		return m.abandonPreviewGesture()
	case tty.PointerDrag:
		return m.dragPreview(action)
	case tty.PointerFinish:
		return m.finishPreviewGesture()
	}
	// A drag moves the preview box, and a live pane is sized against that box.
	if action.Type == mouse.ActionDrag && m.workspacesMouse.DragRegion() == workspacesDividerRegion {
		m.sidebarWidth = workspacelist.ResizePercent(m.workspacesMouse.DragStartValue(), action.DragDX, m.width)
		return m.syncTerminalGeometry()
	}
	if action.Type == mouse.ActionDrag && m.workspacesMouse.DragRegion() == previewDiffDividerKind {
		view := m.previewDiffDragView()
		view.SetListWidth(m.workspacesMouse.DragStartValue())
		view.ApplyListWidthDelta(action.DragDX, m.previewDiffDragWidth())
		return nil
	}
	if action.Type == mouse.ActionDrag && m.workspacesMouse.DragRegion() == previewPaneDividerKind {
		split := panelayout.Find(m.preview.paneRoot, m.preview.paneDragSplitID)
		// The ratio is a fraction of the OUTER peer, because that is the box the
		// tree was laid out in.
		peer, ok := m.previewPeerBox()
		if !ok || split == nil || split.Split == nil {
			return nil
		}
		ratio := m.workspacesMouse.DragStartValue()
		if split.Split.Axis == panelayout.Rows && peer.H > 0 {
			ratio += action.DragDY * 100 / peer.H
		} else if split.Split.Axis == panelayout.Columns && peer.W > 0 {
			ratio += action.DragDX * 100 / peer.W
		}
		panelayout.SetRatio(m.preview.paneRoot, m.preview.paneDragSplitID, ratio)
		return m.syncTerminalGeometry()
	}
	if action.Type == mouse.ActionDragEnd && action.DragStartID == workspacesDividerRegion {
		_ = saveWorkspaceSidebarWidth(m.sidebarWidth)
		return m.syncTerminalGeometry()
	}
	if action.Type == mouse.ActionDragEnd && action.DragStartID == previewDiffDividerKind {
		_ = state.SetDiffTabFileListWidth(m.previewDiffDragView().ListWidth())
		return nil
	}
	if action.Type == mouse.ActionDragEnd && action.DragStartID == previewPaneDividerKind {
		m.preview.paneDragSplitID = 0
		return m.syncTerminalGeometry()
	}
	// Whether a notch is placed by region or stays with the pointer is the
	// shared rule's answer, argued there.
	switch action.Type {
	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		if tty.WheelStaysWithPointer(m.PreviewInteractive()) {
			return m.wheelPreview(action)
		}
	}
	if action.Region == nil {
		return nil
	}

	// A press anywhere but the terminal is a click away from it: it ends the
	// gesture the press armed and hands the keyboard back to the list. That
	// includes a list row, which is what the project surface answers — a click
	// on the left pane is how the user gets the arrow keys back, and a row that
	// kept the keyboard in the pane would send j/k into the shell instead.
	kind, _ := action.Region.Data.(string)
	if region, ok := action.Region.Data.(workspacelist.Region); ok && region.Kind == workspacelist.RegionRow {
		kind = string(region.Kind)
	}
	_, docTab := action.Region.Data.(previewDocTabHit)
	_, issueTab := action.Region.Data.(previewIssueTabHit)
	_, closeHit := action.Region.Data.(previewPaneCloseHit)
	secondaryClick := isPreviewDocRegion(kind) || isPreviewIssueRegion(kind) || docTab || issueTab || closeHit
	pressAway := tty.PressesTerminal(action.Type) && tty.PressLeavesTerminal(kind, previewRegionKind)
	if pressAway {
		m.preview.pointer.Abandon()
	}
	// Focus follows the pointer's LEAF before any region handler runs, so the
	// ring lands on what was pressed whether or not that leaf's kind happens to
	// own a click-to-focus region. The terminal leaf owns none — its presses are
	// the live pane's and are forwarded to tmux — which is why hanging focus off
	// the region handlers left the ring behind on the neighbour. This moves
	// focus only; the press still reaches the region that claimed it.
	if tty.PressesTerminal(action.Type) {
		paneframe.FocusLeafAt(paneHost{m}, action.X, action.Y)
	}
	cmd := m.workspacesRegionMouse(action)
	if !pressAway || secondaryClick {
		return cmd
	}
	// Last, so a region that hands the keyboard back itself — the sidebar,
	// sort header, chrome — reconciles the producer to the selection this same
	// event moved rather than to the one it is leaving.
	return tea.Batch(cmd, m.focusList())
}

// WorkspacesWheelAtBoundary mirrors WorkspacesMouse's wheel routing without
// mutating visible state. It is called before Bubble Tea Update/View so an
// inertial tail at a real boundary can be discarded cheaply.
func (m *Model) WorkspacesWheelAtBoundary(msg tea.MouseWheelMsg) bool {
	if m != nil && m.createOpen {
		return m.createWheelAtBoundary(msg)
	}
	if m == nil || m.renameOpen || m.viewFlyoutOpen || m.workspacesMouse == nil {
		return false
	}
	action := m.workspacesMouse.HandleMouse(msg)
	if action.Type != mouse.ActionScrollUp && action.Type != mouse.ActionScrollDown {
		return false
	}
	if tty.WheelStaysWithPointer(m.PreviewInteractive()) {
		return m.previewWheelAtBoundary(action)
	}
	if action.Region == nil {
		return false
	}
	if hit, ok := action.Region.Data.(previewPaneCloseHit); ok {
		return m.previewCloseWheelAtBoundary(hit.Kind, action.Delta)
	}
	if _, ok := action.Region.Data.(previewDocTabHit); ok {
		view := m.preview.doc.view()
		return view == nil || view.ScrollAtBoundary(action.Delta)
	}
	if _, ok := action.Region.Data.(previewIssueTabHit); ok {
		view := m.preview.issue.view()
		return view == nil || view.ScrollAtBoundary(action.Delta)
	}
	if kind, ok := action.Region.Data.(string); ok {
		switch {
		case kind == workspacesSidebarRegion:
			return m.workspaces.ScrollAtBoundary(action.Delta)
		case isPreviewDocRegion(kind):
			view := m.preview.doc.view()
			return view == nil || view.ScrollAtBoundary(action.Delta)
		case isPreviewIssueRegion(kind):
			view := m.preview.issue.view()
			return view == nil || view.ScrollAtBoundary(action.Delta)
		case kind == previewDiffRegionKind:
			view := m.preview.diff.view()
			if view == nil {
				return true
			}
			return view.ScrollAtBoundary(action.Delta, view.Height())
		case kind == previewRegionKind:
			return m.previewWheelAtBoundary(action)
		default:
			return false
		}
	}
	if region, ok := action.Region.Data.(workspacelist.Region); ok {
		switch region.Kind {
		case workspacelist.RegionRow, workspacelist.RegionSort, workspacelist.RegionFilter:
			return m.workspaces.ScrollAtBoundary(action.Delta)
		}
	}
	return false
}

// previewPointerIntent asks the shared layer what a pointer action over the
// preview means. A drag and its release are answered by the region they started
// in, which is what keeps a selection dragged off the box that box's.
func (m *Model) previewPointerIntent(action mouse.MouseAction, wasDragging bool, dragSourceBefore string) tty.PointerIntent {
	kind, _ := regionKind(action.Region)
	return tty.PointerIntentFor(tty.PointerIntentInput{
		Action:       action.Type,
		OverTerminal: kind == previewRegionKind,
		FromTerminal: action.DragStartID == previewRegionKind || dragSourceBefore == previewRegionKind,
		LostRelease:  action.Type == mouse.ActionHover && wasDragging && !m.workspacesMouse.IsDragging(),
	})
}

// regionKind names the region an action landed on, if it landed on one this
// surface named itself.
func regionKind(region *mouse.Region) (string, bool) {
	if region == nil {
		return "", false
	}
	kind, ok := region.Data.(string)
	return kind, ok
}

// workspacesRegionMouse routes a mouse event to the region it landed on, after
// the gesture-level decisions above have been made.
func (m *Model) workspacesRegionMouse(action mouse.MouseAction) tea.Cmd {
	// The preview owns its own wheel: scrolling over terminal output moves that
	// output, not the list underneath it.
	if hit, ok := action.Region.Data.(previewPaneCloseHit); ok {
		switch action.Type {
		case mouse.ActionClick, mouse.ActionDoubleClick:
			return m.closePreviewPane(hit.Kind)
		case mouse.ActionScrollUp, mouse.ActionScrollDown:
			return m.scrollPreviewClose(hit.Kind, action.Delta)
		}
		return nil
	}
	if _, ok := action.Region.Data.(previewDiffTabHit); ok {
		return m.handlePreviewDiffMouse(action)
	}
	if kind, _ := regionKind(action.Region); kind == previewDiffRegionKind {
		return m.handlePreviewDiffMouse(action)
	}
	if hit, ok := action.Region.Data.(previewActionHit); ok {
		if action.Type == mouse.ActionClick || action.Type == mouse.ActionDoubleClick {
			return m.clickPreviewAction(hit)
		}
		return nil
	}
	if _, ok := action.Region.Data.(previewGitHit); ok {
		if action.Type == mouse.ActionClick || action.Type == mouse.ActionDoubleClick {
			return m.OpenSelectedInGit()
		}
		return nil
	}
	if hit, ok := action.Region.Data.(previewPaneDividerHit); ok {
		if action.Type == mouse.ActionClick {
			split := panelayout.Find(m.preview.paneRoot, int(hit))
			if split != nil && split.Split != nil {
				m.preview.paneDragSplitID = int(hit)
				m.workspacesMouse.StartDrag(action.X, action.Y, previewPaneDividerKind, split.Split.Ratio)
			}
		}
		return nil
	}
	if _, ok := action.Region.Data.(previewDiffDividerHit); ok {
		if action.Type == mouse.ActionClick {
			view := m.previewDiffDragView()
			w := m.previewDiffDragWidth()
			start := view.EffectiveListWidth(w)
			view.SetListWidth(start)
			m.workspacesMouse.StartDrag(action.X, action.Y, previewDiffDividerKind, start)
		}
		return nil
	}
	if _, ok := action.Region.Data.(previewDocTabHit); ok {
		return m.handlePreviewDocMouse(action)
	}
	if _, ok := action.Region.Data.(previewIssueTabHit); ok {
		return m.handlePreviewIssueMouse(action)
	}
	if kind, ok := action.Region.Data.(string); ok && isPreviewDocRegion(kind) {
		return m.handlePreviewDocMouse(action)
	}
	if kind, ok := action.Region.Data.(string); ok && isPreviewIssueRegion(kind) {
		return m.handlePreviewIssueMouse(action)
	}
	if kind, ok := action.Region.Data.(string); ok {
		switch kind {
		case workspacesDividerRegion:
			if action.Type == mouse.ActionClick {
				m.workspacesMouse.StartDrag(action.X, action.Y, workspacesDividerRegion, m.sidebarWidth)
			}
			return nil
		case workspacesSidebarRegion:
			switch action.Type {
			case mouse.ActionClick, mouse.ActionDoubleClick:
				return m.focusList()
			case mouse.ActionScrollUp, mouse.ActionScrollDown:
				m.workspaces.Move(action.Delta)
				return m.previewSync()
			}
			return nil
		case previewRegionKind:
			// The terminal box answers the pointer the way a terminal does: a
			// press arms a gesture the release resolves, a drag selects, a double
			// or triple click takes the word or the line, and the wheel belongs
			// to the application only while it has asked for mouse reports.
			switch m.previewPointerIntent(action, false, "") {
			case tty.PointerPress:
				return m.pressPreview(action)
			case tty.PointerSelectWord:
				return m.selectPreviewUnit(action, tty.SelectUnitWord)
			case tty.PointerSelectLine:
				return m.selectPreviewUnit(action, tty.SelectUnitLine)
			case tty.PointerWheel:
				return m.wheelPreview(action)
			}
			return nil
		}
	}

	region, ok := action.Region.Data.(workspacelist.Region)
	if !ok {
		return nil
	}
	var focus tea.Cmd
	switch action.Type {
	case mouse.ActionClick, mouse.ActionDoubleClick:
		switch region.Kind {
		case workspacelist.RegionRow:
			// The selection moves before focus does: giving the keyboard back
			// rebinds the terminal producer, and it has to bind to the row the
			// click landed on rather than to the one it is leaving.
			wasTyping := m.PreviewInteractive()
			m.workspaces.SelectID(region.ID)
			// A single click only selects. The second click opens the row the
			// first one selected, so a double click can never activate a
			// neighbour: the identity is resolved from the selection this same
			// event just moved.
			if action.Type == mouse.ActionDoubleClick {
				return tea.Batch(m.focusList(), m.previewSync(), m.activateWorkspace())
			}
			if wasTyping {
				// Tabbed-terminal: switch session and keep typing when the new
				// row has a live pane; otherwise land on the list.
				return m.switchPreviewInteractive()
			}
			focus = m.focusList()
		case workspacelist.RegionSort:
			m.openViewFlyout()
		case workspacelist.RegionHeaderAction:
			if region.ID == globalCreateActionID {
				return m.OpenCreate("")
			}
		case workspacelist.RegionSectionAction:
			if strings.HasPrefix(region.ID, globalCreateActionID+":") {
				return m.OpenCreate(createProjectKeyFromAction(region.ID))
			}
		case workspacelist.RegionFilter:
			focus = m.focusList()
			m.workspaces.FocusFilter()
		}
	case mouse.ActionScrollUp:
		m.workspaces.Move(action.Delta)
	case mouse.ActionScrollDown:
		m.workspaces.Move(action.Delta)
	}
	return tea.Batch(focus, m.previewSync())
}

// activateWorkspace opens the selected row through the same app-owned
// validated navigation the Agents board uses.
//
// The identity travels as the catalog record the row was built from — a stable
// ProjectKey + Kind + Key — never as a row number, a display name, a branch, or
// a tmux title. That is what makes duplicate names in different projects, a
// list that re-sorted under the cursor, and a row that disappeared between the
// keypress and the validation all resolve to the right answer or to none.
//
// Nothing is opened, attached to, created, or typed into here: the command only
// asks the app to validate an identity.
func (m *Model) activateWorkspace() tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return nil
	}
	return m.RequestNavigation(workspace)
}

// SelectedWorkspace resolves the list cursor back to its catalog record.
func (m *Model) SelectedWorkspace() (workspaceinventory.Workspace, bool) {
	item, ok := m.workspaces.Selected()
	if !ok {
		return workspaceinventory.Workspace{}, false
	}
	workspace, ok := m.catalog[item.ID]
	return workspace, ok
}

// WorkspacesSummary is the header-right text for the tab.
func (m *Model) WorkspacesSummary() string {
	matched, total := m.workspaces.Counts()
	if matched != total {
		return fmt.Sprintf("%d of %d", matched, total)
	}
	return fmt.Sprintf("%d workspaces", total)
}

// The browser's command set is declared in Commands() and registered in
// internal/keymap under each WorkspaceFocusContext. Help, the palette, and
// the host footer all read that pair, so a focused document or issue leaf
// cannot advertise the list's keys. Creation delegates to workspaceops;
// rename-shell and rename-worktree are display-name writes. Typing into a
// live pane is Enter / click / E.
