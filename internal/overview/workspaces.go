package overview

import (
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
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
// The list itself is a reader. Creation, deletion, attach, Git lifecycle, and
// Task actions stay in the owning project's Workspaces plugin, where their
// validation and refusal rules live. The one thing the browser does drive is an
// existing live pane: the focused preview hands its keyboard to the pane behind
// the selected row (internal/overview/interactive.go), which creates nothing and
// destroys nothing — it types into a session that is already there.

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
			items = append(items, listItem(workspace.Item(), project.Name, order, m.stale[key]))
		}
	}
	sort.SliceStable(failures, func(a, b int) bool { return failures[a] < failures[b] })
	m.workspaces.SetItems(items)
	m.workspaces.SetFailures(failures)
	m.workspaces.SetLoading(m.loading)
	m.workspaces.SetEmptyText("No shells or worktrees found in the configured projects")
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

// laneGroup is the vertical projection of the shared Kanban lanes. The order
// the list renders them in is workspacelist's, not a second opinion about what
// each lane means.
func laneGroup(lane agentstatus.LaneID) workspacelist.Group {
	switch lane {
	case agentstatus.LaneBlocked:
		return workspacelist.GroupNeedsAttention
	case agentstatus.LaneWorking:
		return workspacelist.GroupWorking
	case agentstatus.LaneDone:
		return workspacelist.GroupDone
	case agentstatus.LaneIdle:
		return workspacelist.GroupIdle
	default:
		return workspacelist.GroupPaused
	}
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
	box          termpreview.Box
	split        termpreview.Split
}

func (m *Model) workspacesLayout() workspacesLayout {
	// The panel's top and bottom border rows are not content.
	height := m.height - 2
	drawable := m.width >= 1 && height >= 1
	if !m.sidebarVisible || (m.previewNarrow() && m.preview.full) {
		layout := workspacesLayout{previewOnly: true, previewDrawn: drawable}
		if drawable {
			layout.box = termpreview.Box{X: globalContentInset, Y: 1, W: m.width - globalPanelOverhead, H: height}
		}
		return layout
	}
	if m.previewNarrow() {
		return workspacesLayout{listOnly: true}
	}
	split := m.previewSplit(m.width)
	layout := workspacesLayout{previewDrawn: drawable, split: split}
	if drawable {
		layout.box = termpreview.Box{X: split.ContentX, Y: 1, W: split.ContentWidth, H: height}
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
	if m.workspacesMouse == nil {
		m.workspacesMouse = mouse.NewHandler()
	}
	m.workspacesMouse.Clear()
	layout := m.workspacesLayout()
	if layout.previewOnly {
		m.addPreviewRegion(0, width, height)
		return styles.RenderPanel(m.renderPreview(layout.box.W, layout.box.H), width, height, true)
	}
	if layout.listOnly {
		m.addSidebarRegion(0, width, height)
		return styles.RenderPanel(m.renderWorkspaceList(globalContentInset, 1, width-globalPanelOverhead, height-2), width, height, true)
	}

	split := layout.split
	m.addSidebarRegion(0, split.SidebarWidth, height)
	m.addPreviewRegion(split.PreviewX, split.PreviewWidth, height)
	list := m.renderWorkspaceList(globalContentInset, 1, split.SidebarContentWidth, height-2)
	preview := m.renderPreview(layout.box.W, layout.box.H)

	leftPane := styles.RenderPanel(list, split.SidebarWidth, height, !m.PreviewFocused())
	rightPane := styles.RenderPanel(preview, split.PreviewWidth, height, m.PreviewFocused())
	divider := ui.RenderDivider(height)
	// Register the forgiving three-column divider target last, above both pane
	// regions and any list row that reaches the content edge.
	m.workspacesMouse.HitMap.AddRect(workspacesDividerRegion, split.SidebarWidth, 0, 3, height, workspacesDividerRegion)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, divider, rightPane)
}

// renderWorkspaceList draws the list and registers its regions at an x offset,
// so a click lands on the row the list actually drew there.
func (m *Model) renderWorkspaceList(x, y, width, height int) string {
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
)

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
	case "enter":
		// Enter means the same thing in both layouts: open the exact owning
		// project workspace through the app's validated navigation. The narrow
		// full-width preview is reached with right/l, so Enter never has to
		// mean two different things depending on how wide the terminal is.
		return true, m.activateWorkspace()
	case "/":
		cmd := m.focusList()
		m.workspaces.FocusFilter()
		return true, cmd
	case "s":
		m.workspaces.CycleSort()
		return true, m.previewSync()
	case "r":
		return true, tea.Batch(m.Start(m.projects), m.previewSelect())
	case "right", "l":
		return true, m.focusPreviewPane()
	case interactiveEnterKey, interactiveEnterKeyAlt:
		// The keyboard entry point is the list on both surfaces: the project
		// sidebar answers i/E without a focus move first, so one press from the
		// list here both focuses the pane and hands it the keyboard. A selection
		// with no live pane refuses, and focus stays where the user left it.
		return true, m.enterPreviewInteractive()
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

// WorkspaceFocusContext distinguishes the list, the watched preview, and a
// preview whose pane is being typed into, so binding, footer and help discovery
// follow mouse/keyboard focus. Focus alone decides it: hiding the sidebar moves
// focus to the preview and focusing the list brings the sidebar back, so the
// advertised keys are always the focused surface's.
func (m *Model) WorkspaceFocusContext() string {
	if m.PreviewInteractive() {
		return "global-workspaces-terminal"
	}
	if m.PreviewFocused() {
		return "global-workspaces-preview"
	}
	return "global-workspaces"
}

func (m *Model) WorkspaceSidebarVisible() bool { return m.sidebarVisible }

// toggleWorkspaceSidebar shows or hides the list. Restoring it moves focus back
// to the list, which ends interactive mode. Hiding it leaves focus on the
// preview, so any live pane is resized to the wider box it will be drawn in.
func (m *Model) toggleWorkspaceSidebar() tea.Cmd {
	m.sidebarVisible = !m.sidebarVisible
	if m.sidebarVisible {
		return m.focusList()
	}
	m.preview.full = true
	cmd := m.focusPreviewPane()
	return tea.Batch(cmd, m.syncTerminalGeometry(), appmsg.ShowToast("Sidebar hidden (\\ to restore)", 2*time.Second))
}

// WorkspacesResize adopts a new tab size ahead of the frame that will use it,
// so a live pane is resized when the window is, rather than the next time a
// message happens to reach the terminal.
func (m *Model) WorkspacesResize(width, height int) tea.Cmd {
	m.width, m.height = width, height
	// A window narrow enough to drop the preview would leave the keyboard
	// pointed at a box that is no longer drawn, so a focused preview takes the
	// whole tab instead — the arrangement focusPreviewPane makes when narrow.
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
	if m.workspacesMouse == nil {
		return nil
	}
	mouseMsg, ok := msg.(tea.MouseMsg)
	if !ok {
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
	// A release can be lost when the pointer leaves the window or focus changes.
	// The mouse handler cancels that stale drag on the next button-less motion;
	// end the paired terminal gesture at the same boundary.
	if action.Type == mouse.ActionHover && wasDragging && !m.workspacesMouse.IsDragging() &&
		dragSourceBefore == previewRegionKind {
		return m.abandonPreviewGesture()
	}
	// A drag over the terminal is a selection, and its release resolves the
	// gesture the press armed. Both carry the region they started in, so neither
	// depends on where the pointer has since travelled.
	if action.Type == mouse.ActionDrag && action.DragStartID == previewRegionKind {
		return m.dragPreview(action)
	}
	if action.Type == mouse.ActionDragEnd && action.DragStartID == previewRegionKind {
		return m.finishPreviewGesture()
	}
	// A drag moves the preview box, and a live pane is sized against that box.
	if action.Type == mouse.ActionDrag && m.workspacesMouse.DragRegion() == workspacesDividerRegion {
		m.sidebarWidth = workspacelist.ResizePercent(m.workspacesMouse.DragStartValue(), action.DragDX, m.width)
		return m.syncTerminalGeometry()
	}
	if action.Type == mouse.ActionDragEnd && action.DragStartID == workspacesDividerRegion {
		_ = saveWorkspaceSidebarWidth(m.sidebarWidth)
		return m.syncTerminalGeometry()
	}
	if action.Region == nil {
		return nil
	}

	// A press anywhere but the terminal is a click away from it, and clicking away
	// is not a release of the gesture the terminal armed: the divider, a row and
	// the sidebar all abandon it identically.
	if kind, _ := action.Region.Data.(string); kind != previewRegionKind {
		switch action.Type {
		case mouse.ActionClick, mouse.ActionDoubleClick, mouse.ActionTripleClick:
			m.preview.pointer.Abandon()
		}
	}

	// The preview owns its own wheel: scrolling over captured output moves that
	// output, not the list underneath it.
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
			switch action.Type {
			case mouse.ActionClick:
				return m.pressPreview(action)
			case mouse.ActionDoubleClick:
				return m.selectPreviewUnit(action, tty.SelectUnitWord)
			case mouse.ActionTripleClick:
				return m.selectPreviewUnit(action, tty.SelectUnitLine)
			case mouse.ActionScrollUp, mouse.ActionScrollDown:
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
			// rebinds the capture cadence, and it has to bind to the row the
			// click landed on rather than to the one it is leaving.
			m.workspaces.SelectID(region.ID)
			focus = m.focusList()
			// A single click only selects. The second click opens the row the
			// first one selected, so a double click can never activate a
			// neighbour: the identity is resolved from the selection this same
			// event just moved.
			if action.Type == mouse.ActionDoubleClick {
				return tea.Batch(focus, m.previewSync(), m.activateWorkspace())
			}
		case workspacelist.RegionSort:
			m.workspaces.CycleSort()
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

// The browser's command set is not declared here. It is registered in
// internal/keymap under the "global-workspaces", "global-workspaces-preview",
// "global-workspaces-terminal" and "global-workspaces-filter" contexts, which is
// what makes it discoverable in help and the palette rather than only in footer
// hints — and what makes the boundary (no create, delete or attach anywhere; the
// pane's keyboard only from the preview) a single documented fact instead of a
// second list beside the keys this file actually answers.
