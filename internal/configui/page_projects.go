package configui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
)

// Projects is action-first: how many there are, a prominent way to add one, and
// then the list. Each row is the project: name, path, and on hover the things
// that can be done to it. Sidecar does not discover projects, so the page never
// explains a scan it is not doing.

const (
	regionProjectAdd    = "config-project-add"
	regionProjectRow    = "config-project-row-"
	regionProjectEdit   = "config-project-edit"
	regionProjectRemove = "config-project-remove"
)

// projectsState is the page's selection.
type projectsState struct {
	cursor int
	// selectPath is a project to select once it exists — what returning from
	// Add Project with a new project means.
	selectPath string
}

func (m *Model) projectsPage() *projectsState {
	if m.projectsState == nil {
		m.projectsState = &projectsState{}
	}
	return m.projectsState
}

// clampProjectCursor keeps the selection inside the configured list, and
// honours a pending selection request from a child route. It reports whether it
// resolved such a request, which outranks where the row cursor happens to be:
// a project just added is the project the page should be on.
func (m *Model) clampProjectCursor() bool {
	state := m.projectsState
	if state == nil {
		return false
	}
	resolved := false
	list := m.projects()
	if state.selectPath != "" {
		for i, project := range list {
			if project.Path == state.selectPath {
				state.cursor = i
				state.selectPath = ""
				resolved = true
				break
			}
		}
	}
	if state.cursor >= len(list) {
		state.cursor = max(0, len(list)-1)
	}
	if state.cursor < 0 {
		state.cursor = 0
	}
	return resolved
}

// syncProjectCursor points the page's selection at the row the pane's cursor is
// on. The list has one row per project and no window of its own, so the row
// cursor is the selection — deciding it twice is how arrowing down the list
// lit up a second row and left the detail block below describing a project the
// user was no longer on.
func (m *Model) syncProjectCursor() {
	state := m.projectsState
	if state == nil || !strings.HasPrefix(m.focusedID, regionProjectRow) {
		return
	}
	index, err := strconv.Atoi(strings.TrimPrefix(m.focusedID, regionProjectRow))
	if err != nil || index < 0 || index >= len(m.projects()) {
		return
	}
	state.cursor = index
}

// selectedProject is the project the list cursor is on, or nil.
func (m *Model) selectedProject() *config.ProjectConfig {
	m.clampProjectCursor()
	list := m.projects()
	state := m.projectsPage()
	if state.cursor < 0 || state.cursor >= len(list) {
		return nil
	}
	return &list[state.cursor]
}

func (m *Model) buildProjects(b *paneBuilder) {
	state := m.projectsPage()
	if m.clampProjectCursor() {
		// A request from a child route wins, and takes the row cursor with it,
		// so the two selection sources never end up naming different projects.
		m.focusControlByID(fmt.Sprintf("%s%d", regionProjectRow, state.cursor))
	} else {
		m.syncProjectCursor()
	}
	list := m.projects()

	b.text(PaneTitle(PageTitle(PageProjects)), "")

	count := fmt.Sprintf("%d configured", len(list))
	if len(list) == 1 {
		count = "1 configured"
	}
	left := Body("Your projects") + "  " + Muted(count)
	b.rightControlPrimary(left, regionProjectAdd, "a", "A  Add project", func(m *Model) tea.Cmd {
		m.OpenAddProject()
		return nil
	})
	b.blank()

	if len(list) == 0 {
		b.lead("Sidecar does not know about any projects yet.")
		b.lead("Add one to switch between projects, create workspaces, and set per-project themes.")
		return
	}

	cols := measureProjectColumns(b.inner)
	for i := range list {
		index := i
		project := list[i]
		m.buildProjectTableRow(b, index, project, cols)
	}

	b.blank()
	b.lead("shift+↑/shift+↓ reorders the list. Removing a project leaves the directory untouched.")
}

// projectColumns is the shared name/path/action layout every row uses, so
// paths line up even when names differ.
type projectColumns struct {
	nameW    int
	pathW    int
	currentW int
	edit     string
	remove   string
	editW    int
	removeW  int
}

const (
	projectNameMax  = 22
	projectNameMin  = 10
	projectPathMin  = 14
	projectEdit     = "Edit Project"
	projectRemove   = "Remove Project"
	projectEditSm   = "Edit"
	projectRemoveSm = "Remove"
)

func measureProjectColumns(inner int) projectColumns {
	currentW := ansi.StringWidth(Badge("CURRENT", false))
	long := projectColumns{
		currentW: currentW,
		edit:     projectEdit,
		remove:   projectRemove,
		editW:    ansi.StringWidth(Button(projectEdit, false, State{})),
		removeW:  ansi.StringWidth(Button(projectRemove, false, State{})),
	}
	if leftover := inner - RowIndent - 2 - long.actionWidth() - 1; leftover >= projectNameMin+projectPathMin {
		long.nameW, long.pathW = splitProjectText(leftover)
		return long
	}
	short := long
	short.edit = projectEditSm
	short.remove = projectRemoveSm
	short.editW = ansi.StringWidth(Button(projectEditSm, false, State{}))
	short.removeW = ansi.StringWidth(Button(projectRemoveSm, false, State{}))
	leftover := inner - RowIndent - 2 - short.actionWidth() - 1
	if leftover < projectNameMin+projectPathMin {
		short.nameW = projectNameMin
		short.pathW = max(8, leftover-short.nameW)
		return short
	}
	short.nameW, short.pathW = splitProjectText(leftover)
	return short
}

func splitProjectText(leftover int) (nameW, pathW int) {
	nameW = min(projectNameMax, max(projectNameMin, leftover/3))
	pathW = leftover - nameW
	return nameW, pathW
}

func (c projectColumns) actionWidth() int {
	// CURRENT + gap + Edit + gap + Remove. Reserved even when the buttons
	// are hidden so the path column never jumps on hover.
	return c.currentW + 2 + c.editW + 2 + c.removeW
}

func (m *Model) buildProjectTableRow(b *paneBuilder, index int, project config.ProjectConfig, cols projectColumns) {
	id := fmt.Sprintf("%s%d", regionProjectRow, index)
	editID := fmt.Sprintf("%s-%d", regionProjectEdit, index)
	removeID := fmt.Sprintf("%s-%d", regionProjectRemove, index)
	path := project.Path
	name := project.Name
	current := project.Path == m.host.ProjectPath
	selected := m.projectsPage().cursor == index

	edit := func(m *Model) tea.Cmd {
		m.OpenEditProject(path)
		return nil
	}
	remove := func(m *Model) tea.Cmd {
		m.confirmRemoveProject(name, path)
		return nil
	}

	rowState := b.declareClickless(id, "", true, edit)
	if b.hovering(editID, removeID) {
		rowState.Hovered = true
	}
	removeKey := ""
	if selected {
		removeKey = "d"
	}
	editState := b.declare(editID, "", false, edit)
	removeState := b.declare(removeID, removeKey, false, remove)
	showActions := rowState.Focused || rowState.Hovered

	line := projectTableRow(project, current, cols, showActions, editState, removeState, rowState, b.inner)
	y := len(b.lines)
	b.lines = append(b.lines, line)
	b.m.mouse.HitMap.AddRect(id, b.originX, 1+y, b.inner, 1, nil)
	if showActions {
		editX, removeX := projectActionOrigins(b.originX, b.inner, cols)
		b.m.mouse.HitMap.AddRect(editID, editX, 1+y, cols.editW, 1, nil)
		b.m.mouse.HitMap.AddRect(removeID, removeX, 1+y, cols.removeW, 1, nil)
	}
}

func projectActionOrigins(originX, inner int, cols projectColumns) (editX, removeX int) {
	removeX = originX + inner - cols.removeW
	editX = removeX - 2 - cols.editW
	return editX, removeX
}

// projectTableRow is one configured project as a table row: name, a
// left-aligned path, a CURRENT badge, and hover/focus actions.
func projectTableRow(project config.ProjectConfig, current bool, cols projectColumns, showActions bool, editState, removeState, row State, width int) string {
	nameStyle := bodyStyle().Bold(true)
	switch {
	case row.Focused:
		nameStyle = titleStyle()
	case row.Hovered:
		nameStyle = bodyStyle().Bold(true)
	}
	name := padDisplay(nameStyle.Render(clampStart(project.Name, cols.nameW)), cols.nameW)
	path := padDisplay(mutedStyle().Render(clampEnd(project.Path, cols.pathW)), cols.pathW)
	left := strings.Repeat(" ", RowIndent) + name + "  " + path

	badge := strings.Repeat(" ", cols.currentW)
	if current {
		badge = padDisplay(Badge("CURRENT", false), cols.currentW)
	}
	actions := strings.Repeat(" ", cols.editW+2+cols.removeW)
	if showActions {
		actions = Button(cols.edit, false, editState) + "  " + Button(cols.remove, false, removeState)
	}
	return HighlightRow(padRight(left, badge+"  "+actions, width), width, row)
}

// openInName resolves an app ID to its display name.
func (m *Model) openInName(id string) string {
	for _, app := range m.host.OpenInApps {
		if app.ID == id {
			return app.Name
		}
	}
	return id
}

// confirmRemoveProject asks before dropping a project. Removing is a
// configuration change and nothing more: the directory on disk, and the
// per-directory UI state Sidecar remembers, are left exactly as they are.
func (m *Model) confirmRemoveProject(name, path string) {
	m.confirm = &confirmState{
		title: "Remove project",
		intro: []string{
			Body("Remove " + name + " from Sidecar's project list?"),
			IndentedMuted(path),
		},
		body: []string{
			IndentedMuted("The directory and its contents are not touched."),
			IndentedMuted("Any per-project theme or setup override in Sidecar's configuration is removed with it."),
		},
		apply: func(m *Model) tea.Cmd {
			return SaveCmd("Removed project: "+name, func() error { return config.RemoveProject(path) })
		},
	}
	m.rowCursor = 0
}

// moveSelectedProject reorders the list and saves it.
func (m *Model) moveSelectedProject(delta int) tea.Cmd {
	selected := m.selectedProject()
	if selected == nil {
		return nil
	}
	path := selected.Path
	state := m.projectsPage()
	next := state.cursor + delta
	if next < 0 || next >= len(m.projects()) {
		return nil
	}
	// The selection follows the project, not the row it used to be in: the
	// Remove action acts on the selection, so a reorder that leaves it
	// pointing at the neighbour is how a user removes the wrong project.
	// selectPath is deliberately not used here — the frame painted before the
	// save lands still holds the old order, and the request would resolve
	// against it and be consumed on the wrong index.
	state.cursor = next
	state.selectPath = ""
	// The row cursor follows the project too. It and the selection are the same
	// idea, and leaving the cursor on the row the project used to be in would
	// put the highlight back on the neighbour at the next render.
	m.focusControlByID(fmt.Sprintf("%s%d", regionProjectRow, next))
	return SaveCmd("Reordered projects", func() error { return config.MoveProject(path, delta) })
}
