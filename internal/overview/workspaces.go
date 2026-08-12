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
// It is also read-only. Creation, deletion, attach, interactive input, Git
// lifecycle, and Task actions stay in the owning project's Workspaces plugin,
// where their validation and refusal rules live.

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
	detail := item.TaskID
	if detail == "" {
		detail = item.Branch
	}
	if item.Kind == workspaceinventory.KindShell {
		detail = item.TmuxName
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

// WorkspacesView renders the global Workspaces tab: the list on the left and
// exactly one read-only terminal box on the right.
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
	if !m.sidebarVisible {
		m.addPreviewRegion(0, width, height)
		return styles.RenderPanel(m.renderPreview(width-globalPanelOverhead, height-2), width, height, true)
	}

	if m.previewNarrow() {
		if m.preview.full {
			m.addPreviewRegion(0, width, height)
			return styles.RenderPanel(m.renderPreview(width-globalPanelOverhead, height-2), width, height, true)
		}
		m.addSidebarRegion(0, width, height)
		return styles.RenderPanel(m.renderWorkspaceList(globalContentInset, 1, width-globalPanelOverhead, height-2), width, height, true)
	}

	split := m.previewSplit(width)
	m.addSidebarRegion(0, split.SidebarWidth, height)
	m.addPreviewRegion(split.PreviewX, split.PreviewWidth, height)
	list := m.renderWorkspaceList(globalContentInset, 1, split.SidebarContentWidth, height-2)
	preview := m.renderPreview(split.ContentWidth, height-2)

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

// previewRegionKind is the hit region covering the read-only preview box.
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
	// cannot move the cursor out from under the output being read. Every key it
	// answers scrolls or moves focus; none reaches a terminal.
	if m.PreviewFocused() {
		if handled, cmd := m.previewKey(key); handled {
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
		m.focusList()
		m.workspaces.FocusFilter()
		return true, nil
	case "s":
		m.workspaces.CycleSort()
		return true, m.previewSync()
	case "r":
		return true, tea.Batch(m.Start(m.projects), m.previewSelect())
	case "right", "l":
		return true, m.focusPreviewPane()
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

// WorkspaceFocusContext distinguishes list and read-only preview so binding,
// footer and help discovery follow mouse/keyboard focus. A hidden sidebar is
// necessarily preview-focused until backslash restores it.
func (m *Model) WorkspaceFocusContext() string {
	if m.PreviewFocused() || !m.sidebarVisible {
		return "global-workspaces-preview"
	}
	return "global-workspaces"
}

func (m *Model) WorkspaceSidebarVisible() bool { return m.sidebarVisible }

func (m *Model) toggleWorkspaceSidebar() tea.Cmd {
	m.sidebarVisible = !m.sidebarVisible
	if m.sidebarVisible {
		m.focusList()
		return nil
	}
	m.preview.full = true
	cmd := m.focusPreviewPane()
	return tea.Batch(cmd, appmsg.ShowToast("Sidebar hidden (\\ to restore)", 2*time.Second))
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
	action := m.workspacesMouse.HandleMouse(mouseMsg)
	if action.Type == mouse.ActionDrag && m.workspacesMouse.DragRegion() == workspacesDividerRegion {
		m.sidebarWidth = workspacelist.ResizePercent(m.workspacesMouse.DragStartValue(), action.DragDX, m.width)
		return nil
	}
	if action.Type == mouse.ActionDragEnd && action.DragStartID == workspacesDividerRegion {
		_ = saveWorkspaceSidebarWidth(m.sidebarWidth)
		return nil
	}
	if action.Region == nil {
		return nil
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
				m.focusList()
			case mouse.ActionScrollUp, mouse.ActionScrollDown:
				m.workspaces.Move(action.Delta)
				return m.previewSync()
			}
			return nil
		case previewRegionKind:
			switch action.Type {
			case mouse.ActionClick, mouse.ActionDoubleClick:
				return m.focusPreviewPane()
			case mouse.ActionScrollUp, mouse.ActionScrollDown:
				m.scrollPreview(-action.Delta)
			}
			return nil
		}
	}

	region, ok := action.Region.Data.(workspacelist.Region)
	if !ok {
		return nil
	}
	switch action.Type {
	case mouse.ActionClick, mouse.ActionDoubleClick:
		switch region.Kind {
		case workspacelist.RegionRow:
			m.focusList()
			m.workspaces.SelectID(region.ID)
			// A single click only selects. The second click opens the row the
			// first one selected, so a double click can never activate a
			// neighbour: the identity is resolved from the selection this same
			// event just moved.
			if action.Type == mouse.ActionDoubleClick {
				return tea.Batch(m.previewSync(), m.activateWorkspace())
			}
		case workspacelist.RegionSort:
			m.workspaces.CycleSort()
		case workspacelist.RegionFilter:
			m.focusList()
			m.workspaces.FocusFilter()
		}
	case mouse.ActionScrollUp:
		m.workspaces.Move(action.Delta)
	case mouse.ActionScrollDown:
		m.workspaces.Move(action.Delta)
	}
	return m.previewSync()
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
// internal/keymap under the "global-workspaces" and "global-workspaces-filter"
// contexts, which is what makes it discoverable in help and the palette rather
// than only in footer hints — and what makes the read-only boundary (no create,
// delete, attach, or interactive command) a single documented fact instead of a
// second list beside the keys this file actually answers.
