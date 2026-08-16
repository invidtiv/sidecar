package configui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/theme"
)

// Projects is action-first: how many there are, a prominent way to add one, and
// then the list. Selecting a project reveals what Sidecar knows about it and
// the two things that can be done to it. Sidecar does not discover projects, so
// the page never explains a scan it is not doing.

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
// honours a pending selection request from a child route.
func (m *Model) clampProjectCursor() {
	state := m.projectsState
	if state == nil {
		return
	}
	list := m.projects()
	if state.selectPath != "" {
		for i, project := range list {
			if project.Path == state.selectPath {
				state.cursor = i
				state.selectPath = ""
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
}

// selectedProject is the project the page is showing detail for, or nil.
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
	m.clampProjectCursor()
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
		b.text(Muted("Sidecar does not know about any projects yet."))
		b.text(Muted("Add one to switch between projects, create workspaces, and set per-project themes."))
		return
	}

	for i := range list {
		index := i
		project := list[i]
		id := fmt.Sprintf("%s%d", regionProjectRow, index)
		b.row(id, "", func(m *Model) tea.Cmd {
			// Choosing the project already selected is choosing to edit it,
			// which is what the detail block below says Enter does.
			if m.projectsPage().cursor == index {
				m.OpenEditProject(project.Path)
				return nil
			}
			m.projectsPage().cursor = index
			return nil
		}, func(s State) string {
			if index == state.cursor && m.detailOwnsKeys() {
				s.Focused = true
			}
			badge := ""
			if project.Path == m.host.ProjectPath {
				badge = "CURRENT"
			}
			return projectRow(project, badge, b.inner, s)
		})
	}

	selected := m.selectedProject()
	if selected == nil {
		return
	}

	b.text(SectionHeader(selected.Name))
	b.text(FormRow("Location", Muted(selected.Path), State{}))
	b.text(FormRow("Theme", Body(projectThemeLabel(*selected)), State{}))
	b.text(FormRow("Worktree setup", Muted(worktreeSetupLabel(*selected)), State{}))
	b.text(FormRow("Open in", Muted(m.openInLabel(*selected)), State{}))
	b.blank()

	path := selected.Path
	name := selected.Name
	b.buttons(
		buttonSpec{id: regionProjectEdit, key: "enter", label: "Enter  Edit project", primary: true, run: func(m *Model) tea.Cmd {
			m.OpenEditProject(path)
			return nil
		}},
		buttonSpec{id: regionProjectRemove, key: "d", label: "D  Remove project", run: func(m *Model) tea.Cmd {
			m.confirmRemoveProject(name, path)
			return nil
		}},
	)
	b.blank()
	b.text(Muted("shift+↑/shift+↓ reorders the list. Removing a project leaves the directory untouched."))
}

// projectRow is one configured project: its name, its path quietly beside it,
// and a badge for the project the user is working in.
func projectRow(project config.ProjectConfig, badge string, width int, state State) string {
	name := project.Name
	nameStyle := bodyStyle().Bold(true)
	switch {
	case state.Focused:
		nameStyle = titleStyle()
	case state.Hovered:
		nameStyle = bodyStyle().Bold(true).Underline(true)
	}
	left := strings.Repeat(" ", RowIndent) + "  " + nameStyle.Render(name) + "  " + mutedStyle().Render(project.Path)
	if badge == "" {
		return left
	}
	return padRight(left, Badge(badge, false), width)
}

func projectThemeLabel(project config.ProjectConfig) string {
	if project.Theme == nil {
		return "Uses global"
	}
	entry := theme.EntryForConfig(*project.Theme)
	if entry.IsZero() {
		return "Uses global"
	}
	return entry.Name
}

func worktreeSetupLabel(project config.ProjectConfig) string {
	setup := project.WorktreeSetup
	if setup == nil {
		return "Uses Sidecar default"
	}
	var parts []string
	if setup.CopyEnvFiles {
		files := len(setup.EnvFiles)
		if files == 0 {
			parts = append(parts, "copies startup files")
		} else {
			parts = append(parts, fmt.Sprintf("copies %d startup file(s)", files))
		}
	}
	if setup.RunHook {
		hook := setup.HookPath
		if hook == "" {
			hook = "repository setup hook"
		}
		parts = append(parts, "runs "+hook)
	}
	if len(parts) == 0 {
		return "Override: nothing enabled"
	}
	return "Override: " + strings.Join(parts, ", ")
}

// openInLabel describes the project's preferred "open in" application.
func (m *Model) openInLabel(project config.ProjectConfig) string {
	if project.OpenIn == "" {
		if project.LastOpenInApp != "" {
			return "Remembers the last app used (" + m.openInName(project.LastOpenInApp) + ")"
		}
		return "Remembers the last app used"
	}
	return m.openInName(project.OpenIn)
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
	state.cursor = next
	state.selectPath = path
	return SaveCmd("Reordered projects", func() error { return config.MoveProject(path, delta) })
}
