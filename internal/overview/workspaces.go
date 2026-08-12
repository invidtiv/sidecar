package overview

import (
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/mouse"
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
		if !item.Live && !item.Ambiguous {
			row.Group = workspacelist.GroupNoSession
		}
	case item.Ambiguous:
		row.Status, row.Group = "ambiguous panes", workspacelist.GroupPaused
	case item.Live:
		row.Status, row.Group = "live", workspacelist.GroupLive
		if item.Kind == workspaceinventory.KindShell {
			row.Provider = "shell"
		}
	default:
		row.Status, row.Group = "no session", workspacelist.GroupNoSession
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

// WorkspacesView renders the global Workspaces tab. Slice 2 ships the list;
// the read-only selected-pane preview beside it arrives in slice 3, so the
// list takes the full width for now rather than drawing an empty pane.
func (m *Model) WorkspacesView(width, height int) string {
	m.width, m.height = width, height
	if m.workspacesMouse == nil {
		m.workspacesMouse = mouse.NewHandler()
	}
	m.workspacesMouse.Clear()
	rendered := m.workspaces.Render(workspacelist.RenderOptions{
		Width:   width,
		Height:  height,
		Title:   "Workspaces",
		Focused: true,
		Now:     time.Now(),
	})
	for _, region := range rendered.Regions {
		m.workspacesMouse.HitMap.AddRect(string(region.Kind), region.X, region.Y, region.W, region.H, region)
	}
	return rendered.View
}

// WorkspacesFilterFocused reports that the inline filter owns the keyboard, so
// the app can report a text-input context and keep its own printable shortcuts
// off the query.
func (m *Model) WorkspacesFilterFocused() bool {
	return m.workspaces.Filter().Focused()
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
		return true, nil
	}
	switch key {
	case "/":
		m.workspaces.FocusFilter()
		return true, nil
	case "s":
		m.workspaces.CycleSort()
		return true, nil
	case "r":
		return true, m.Start(m.projects)
	case "esc":
		if m.workspaces.Filter().Active() {
			m.workspaces.Filter().Reset()
			m.workspaces.Reproject()
			return true, nil
		}
		return false, nil
	}
	if m.navigateWorkspaces(key) {
		return true, nil
	}
	return false, nil
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
	if action.Region == nil {
		if action.Type == mouse.ActionScrollUp {
			m.workspaces.Scroll(-1)
		} else if action.Type == mouse.ActionScrollDown {
			m.workspaces.Scroll(1)
		}
		return nil
	}
	region, ok := action.Region.Data.(workspacelist.Region)
	if !ok {
		return nil
	}
	switch action.Type {
	case mouse.ActionClick, mouse.ActionDoubleClick:
		switch region.Kind {
		case workspacelist.RegionRow:
			m.workspaces.SelectID(region.ID)
		case workspacelist.RegionSort:
			m.workspaces.CycleSort()
		case workspacelist.RegionFilter:
			m.workspaces.FocusFilter()
		}
	case mouse.ActionScrollUp:
		m.workspaces.Scroll(-1)
	case mouse.ActionScrollDown:
		m.workspaces.Scroll(1)
	}
	return nil
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

// WorkspacesCommands lists the read-only command set of the global browser.
// There is deliberately no create, delete, attach, or interactive entry here:
// the global browser must not become a second implementation of destructive
// workspace behaviour.
func (m *Model) WorkspacesCommands() []struct{ Key, Name string } {
	return []struct{ Key, Name string }{{"/", "Filter"}, {"s", "Sort"}, {"r", "Refresh"}}
}
