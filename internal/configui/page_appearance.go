package configui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/theme"
)

// Appearance is the theme library plus the handful of interface settings that
// change how Sidecar looks. Moving through the theme list previews across the
// whole surface; Enter saves at the chosen scope; Escape puts back the theme
// that was in force when the page opened.

const (
	regionScopeGlobal  = "config-theme-scope-global"
	regionScopeProject = "config-theme-scope-project"
	regionNerdFont     = "config-nerd-font"
	regionClock        = "config-clock"
	regionTitle        = "config-terminal-title"

	appearancePickerRows = 8
)

// appearanceState is the page's own state: which scope a save writes to, the
// picker, and the terminal-title editor.
type appearanceState struct {
	picker *themePicker
	// projectScope marks that Enter saves a project override rather than the
	// global theme. The project is always an explicit choice, never an implied
	// "wherever I happen to be".
	projectScope bool
	// projectPath is the project a project-scoped save writes to.
	projectPath string
	title       textinput.Model
}

// appearance lazily builds the page state against the running configuration.
func (m *Model) appearance() *appearanceState {
	if m.appearanceState == nil {
		title := textinput.New()
		title.Prompt = ""
		title.Placeholder = "{project}{worktree}"
		title.CharLimit = 120
		state := &appearanceState{
			picker: newThemePicker(false, appearancePickerRows),
			title:  title,
		}
		if project := m.activeProject(); project != nil {
			state.projectPath = project.Path
		}
		m.appearanceState = state
		m.resetAppearance()
	}
	return m.appearanceState
}

func (m *Model) appearancePicker() *themePicker { return m.appearance().picker }

// resetAppearance points the page at the configuration as it stands now: the
// title template it would save, the theme it calls current, and the theme it
// restores if the user leaves without saving.
func (m *Model) resetAppearance() {
	state := m.appearanceState
	if state == nil {
		return
	}
	cfg := m.Config()
	state.title.SetValue(cfg.UI.TerminalTitle)
	state.picker.selectEntry = func(m *Model, entry theme.Entry) tea.Cmd { return m.saveAppearanceTheme(entry) }
	state.picker.open(m.appearanceCurrentEntry(), theme.ResolveTheme(cfg, m.host.ProjectDir))
}

// appearanceCurrentEntry is the theme the list badges as current: the project
// override while project scope is chosen, the global theme otherwise.
func (m *Model) appearanceCurrentEntry() theme.Entry {
	cfg := m.Config()
	state := m.appearanceState
	if state != nil && state.projectScope {
		for _, project := range cfg.Projects.List {
			if project.Path == state.projectPath && project.Theme != nil {
				return theme.EntryForConfig(*project.Theme)
			}
		}
		return theme.Entry{}
	}
	return theme.EntryForConfig(cfg.UI.Theme)
}

// scopeProjects are the projects a theme override can be saved to. The scope
// selector only appears when the project the user is working in is one of them:
// an override for a project Sidecar does not know about is not a setting.
func (m *Model) scopeProjects() []config.ProjectConfig {
	if m.activeProject() == nil {
		return nil
	}
	return m.projects()
}

func (m *Model) buildAppearance(b *paneBuilder) {
	state := m.appearance()
	b.text(PaneTitle(PageTitle(PageAppearance)), "")
	b.text(Body("Choose how Sidecar looks in your terminal."))

	b.text(SectionHeader("Theme"))
	m.buildThemeScope(b, state)
	b.blank()

	m.buildThemePicker(b, state.picker, RowIndent)

	b.blank()
	b.text(Muted("↑/↓ previews   Enter saves   Esc restores the previous theme"))

	b.text(SectionHeader("Interface"))
	cfg := m.Config()

	b.toggleRow(regionNerdFont, "Nerd Font icons", cfg.UI.NerdFontsEnabled, func(m *Model) tea.Cmd {
		enabled := !m.Config().UI.NerdFontsEnabled
		return SaveCmd(nerdFontNotice(enabled), func() error {
			return config.SaveUI(func(ui *config.UIConfig) { ui.NerdFontsEnabled = enabled })
		})
	})

	b.toggleRow(regionClock, "Header clock", cfg.UI.ShowClock, func(m *Model) tea.Cmd {
		enabled := !m.Config().UI.ShowClock
		notice := "Header clock off"
		if enabled {
			notice = "Header clock on"
		}
		return SaveCmd(notice, func() error {
			return config.SaveUI(func(ui *config.UIConfig) { ui.ShowClock = enabled })
		})
	})

	b.row(regionTitle, "", func(m *Model) tea.Cmd {
		m.editTerminalTitle()
		return nil
	}, func(s State) string {
		if m.editingID() == regionTitle {
			s.Focused = true
			return FormRow("Terminal title", Field(&state.title, 40, s), s)
		}
		value := cfg.UI.TerminalTitle
		if value == "" {
			value = "(no title)"
		}
		return FormRow("Terminal title", StaticField(value, b.controlWidth(40), s), s)
	})
	b.help("Variables: {project} {worktree} {plugin} {dir}. Empty leaves the title alone.")
}

func nerdFontNotice(enabled bool) string {
	if enabled {
		return "Nerd Font icons on"
	}
	return "Nerd Font icons off"
}

// buildThemeScope paints the Global/project scope selector. With no configured
// project to override, Global is the only scope there is, and the row says so
// instead of offering a control that cannot do anything.
func (m *Model) buildThemeScope(b *paneBuilder, state *appearanceState) {
	projects := m.scopeProjects()

	globalState := b.declare(regionScopeGlobal, "", true, func(m *Model) tea.Cmd {
		m.setThemeScope(false, "")
		return nil
	})
	globalPill := Button("Global", !state.projectScope, globalState)

	line := strings.Repeat(" ", RowIndent) + globalPill
	pills := []string{globalPill}
	ids := []string{regionScopeGlobal}

	if len(projects) > 0 {
		options := make([]dropdownOption, 0, len(projects))
		for _, project := range projects {
			options = append(options, dropdownOption{id: project.Path, label: project.Name})
		}
		label := state.projectPath
		for _, project := range projects {
			if project.Path == state.projectPath {
				label = project.Name
			}
		}
		if label == "" && len(projects) > 0 {
			label = projects[0].Name
		}
		projectState := b.declare(regionScopeProject, "", true, func(m *Model) tea.Cmd {
			return m.openDropdown(regionScopeProject, options, m.appearance().projectPath, selectThemeScopeProject)
		})
		projectState, arrow := m.dropdownControlState(regionScopeProject, projectState)
		pill := SelectorArrow(label, arrow, projectState)
		line += "  " + pill
		pills = append(pills, pill)
		ids = append(ids, regionScopeProject)
		hint := "Select a project to create an override"
		if state.projectScope {
			hint = "Saving to " + label + " overrides the global theme"
		}
		line += "  " + mutedStyle().Render(hint)
	} else {
		line += "  " + mutedStyle().Render("Add a project to create a per-project override")
	}

	y := len(b.lines)
	b.lines = append(b.lines, line)
	// The pills are laid out along one line, so the project list hangs from the
	// column its own pill starts at rather than from the row's left edge.
	b.pillRegions(y, ids, pills, themeScopeListWidth)
}

// themeScopeListWidth is how wide the project list opens: project names are
// longer than the pill that names the selected one, and a list clipped to the
// pill would hide the very thing it is there to show.
const themeScopeListWidth = 34

// selectThemeScopeProject moves theme saves to a chosen project. Choosing one is
// also what enters project scope: a project the user picked is a statement that
// the override is what they want.
func selectThemeScopeProject(m *Model, option dropdownOption) tea.Cmd {
	m.setThemeScope(true, option.id)
	return nil
}

// setThemeScope switches which scope a save writes to and re-points the list at
// the theme that scope currently has.
func (m *Model) setThemeScope(project bool, path string) {
	state := m.appearance()
	state.projectScope = project
	if path != "" {
		state.projectPath = path
	}
	if project && state.projectPath == "" {
		if projects := m.scopeProjects(); len(projects) > 0 {
			state.projectPath = projects[0].Path
		}
	}
	state.picker.current = m.appearanceCurrentEntry()
}

// cycleThemeScopeProject chooses the project a project-scoped save writes to.
// The first press enters project scope; further presses walk the list, so the
// project is always something the user picked.
func (m *Model) cycleThemeScopeProject() {
	projects := m.scopeProjects()
	if len(projects) == 0 {
		return
	}
	state := m.appearance()
	if !state.projectScope {
		if state.projectPath == "" {
			state.projectPath = projects[0].Path
		}
		m.setThemeScope(true, state.projectPath)
		return
	}
	index := 0
	for i, project := range projects {
		if project.Path == state.projectPath {
			index = i
		}
	}
	next := projects[(index+1)%len(projects)]
	m.setThemeScope(true, next.Path)
}

// saveAppearanceTheme writes the selected theme at the chosen scope.
func (m *Model) saveAppearanceTheme(entry theme.Entry) tea.Cmd {
	if entry.IsZero() {
		return nil
	}
	state := m.appearance()
	tc := theme.ThemeConfig(entry)
	if state.projectScope && state.projectPath != "" {
		path := state.projectPath
		name := entry.Name
		return SaveCmd("Theme: "+name+" (project)", func() error {
			return config.SaveProjectTheme(path, &tc)
		})
	}
	return SaveCmd("Theme: "+entry.Name+" (global)", func() error {
		return config.SaveGlobalTheme(tc)
	})
}

// editTerminalTitle opens the template for editing. Saving hands the new
// template to the host, which is what re-titles the window on the next tick.
func (m *Model) editTerminalTitle() {
	state := m.appearance()
	state.title.SetValue(m.Config().UI.TerminalTitle)
	m.openEditor(&editorState{
		id:    regionTitle,
		input: &state.title,
		submit: func(m *Model) (tea.Cmd, bool) {
			value := strings.TrimSpace(m.appearance().title.Value())
			return SaveCmd("Terminal title saved", func() error {
				return config.SaveUI(func(ui *config.UIConfig) { ui.TerminalTitle = value })
			}), false
		},
		cancel: func(m *Model) {
			m.appearance().title.SetValue(m.Config().UI.TerminalTitle)
		},
	})
	m.focusControlByID(regionTitle)
}
