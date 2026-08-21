package configui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/gitinit"
	"github.com/marcus/sidecar/internal/pathcomplete"
	"github.com/marcus/sidecar/internal/theme"
)

// Add Project is a focused child route of Projects, not a modal: the header,
// the sidebar, the footer, and the Projects destination all stay exactly where
// they were. Edit Project is the same form with the values already in it.
//
// Nothing here is written until Save. Escape returns to Projects with the
// configuration untouched — and with the theme the user was looking at before
// they started previewing.

const (
	ChildAddProject  ChildID = "add-project"
	ChildEditProject ChildID = "edit-project"
)

const (
	regionFormName        = "config-form-name"
	regionFormLocation    = "config-form-location"
	regionFormTheme       = "config-form-theme"
	regionFormOpenIn      = "config-form-open-in"
	regionFormSave        = "config-form-save"
	regionFormCancel      = "config-form-cancel"
	regionFormInitRepo    = "config-form-init-repo"
	regionFormCompletion  = "config-form-completion-"
	inlinePickerRows      = 6
	completionCandidates  = 6
	projectFormFieldWidth = 46
	// openInRememberLabel is what an unset preference is called: the host still
	// opens the project, in whatever the user reached for last.
	openInRememberLabel = "Remember the last app used"
)

// projectForm is the draft. It is deliberately separate from the configuration:
// a draft that is abandoned changes nothing.
type projectForm struct {
	edit bool
	// originalPath is the project being edited, empty when adding.
	originalPath string

	name     textinput.Model
	location textinput.Model

	// themeEntry is the draft's theme override; the zero entry means the
	// project inherits the global theme.
	themeEntry theme.Entry
	openIn     string

	// picker is the inline theme disclosure. Non-nil only while it is open.
	picker *themePicker
	// restore is the resolved theme to put back if the draft is abandoned.
	restore theme.ResolvedTheme

	completions []string
	// completionFor is the input the candidates were computed for, so a stale
	// list is never shown against a different prefix.
	completionFor string
	completion    int

	message string

	// cwdGit tracks whether the host's working directory is already a repo,
	// filled by a command so the form never probes git while rendering.
	cwdProbed      bool
	cwdIsRepo      bool
	initInProgress bool
}

// isProjectFormRoute reports that the visible route is the project form, which
// is what makes an open draft the thing the keyboard and the picker answer for.
func isProjectFormRoute(route Route) bool {
	return route.IsChild() && (route.Child == ChildAddProject || route.Child == ChildEditProject)
}

// OpenAddProject opens the Add Project route with Location focused. The
// diagnostics "no projects" repair and Setup's "Add a project" row both land
// here, which is why focusing the field is part of opening it.
func (m *Model) OpenAddProject() {
	m.Navigate(PageProjects)
	m.startProjectForm(nil)
	m.PushChild(ChildAddProject, "Add project")
	m.editLocationField()
}

// OpenEditProject opens the same form over an existing project.
func (m *Model) OpenEditProject(path string) {
	for _, project := range m.projects() {
		if project.Path != path {
			continue
		}
		p := project
		m.startProjectForm(&p)
		m.PushChild(ChildEditProject, "Edit project")
		return
	}
}

// startProjectForm builds the draft. An edit starts from the saved project; an
// add starts empty.
func (m *Model) startProjectForm(project *config.ProjectConfig) {
	name := textinput.New()
	name.Prompt = ""
	name.Placeholder = "project-name"
	name.CharLimit = 40

	location := textinput.New()
	location.Prompt = ""
	location.Placeholder = "~/code/project-path"
	location.CharLimit = 200

	form := &projectForm{
		name:     name,
		location: location,
		restore:  theme.ResolveTheme(m.Config(), m.host.ProjectDir),
	}
	if project != nil {
		form.edit = true
		form.originalPath = project.Path
		form.name.SetValue(project.Name)
		form.location.SetValue(project.Path)
		form.openIn = project.OpenIn
		if project.Theme != nil {
			form.themeEntry = theme.EntryForConfig(*project.Theme)
		}
	}
	m.addProject = form
	if project == nil {
		m.queueCwdGitProbe()
	}
}

// PrefillAddProjectFromDir puts the current working directory in Location
// (and Name, if empty) so a first-run user can add this folder without
// hunting for it. Completions stay user-initiated: a prefilled path still
// asks for candidates.
// AddProjectLocation is the Location field on the open Add/Edit Project form.
func (m *Model) AddProjectLocation() string {
	if m.addProject == nil {
		return ""
	}
	return m.addProject.location.Value()
}

func (m *Model) PrefillAddProjectFromDir(dir string) {
	form := m.addProject
	if form == nil || strings.TrimSpace(dir) == "" {
		return
	}
	form.location.SetValue(dir)
	if strings.TrimSpace(form.name.Value()) == "" {
		form.name.SetValue(filepath.Base(dir))
	}
	if m.editingID() == regionFormLocation {
		m.requestCompletions()
	}
}

type cwdGitMsg struct {
	Dir    string
	IsRepo bool
}

func (cwdGitMsg) configMsg() {}

type repoInitMsg struct {
	Root string
	Err  error
}

func (repoInitMsg) configMsg() {}

func (m *Model) queueCwdGitProbe() {
	dir := m.host.ProjectDir
	if strings.TrimSpace(dir) == "" {
		return
	}
	m.pending = append(m.pending, func() tea.Msg {
		return cwdGitMsg{Dir: dir, IsRepo: gitinit.IsRepository(dir)}
	})
}

func (m *Model) applyCwdGit(msg cwdGitMsg) tea.Cmd {
	form := m.addProject
	if form == nil || form.edit {
		return nil
	}
	if msg.Dir != m.host.ProjectDir {
		return nil
	}
	form.cwdProbed = true
	form.cwdIsRepo = msg.IsRepo
	return nil
}

func (form *projectForm) offersInit() bool {
	return form != nil && !form.edit && form.cwdProbed && !form.cwdIsRepo && !form.initInProgress
}

func (m *Model) buildInitThisDirectory(b *paneBuilder, form *projectForm) {
	if form.edit || !form.cwdProbed || form.cwdIsRepo {
		return
	}
	if form.initInProgress {
		b.text(IndentedRaw(mutedStyle().Render("Initializing git repository…")))
		b.blank()
		return
	}
	b.buttons(buttonSpec{
		id:    regionFormInitRepo,
		key:   "i",
		label: "I  Initialize this directory",
		run: func(m *Model) tea.Cmd {
			return m.initCwdRepo()
		},
	})
	b.blank()
}

func (m *Model) initCwdRepo() tea.Cmd {
	form := m.addProject
	if form == nil || form.initInProgress {
		return nil
	}
	dir := m.host.ProjectDir
	if strings.TrimSpace(dir) == "" {
		form.message = "No working directory to initialize"
		return nil
	}
	form.initInProgress = true
	form.message = ""
	return func() tea.Msg {
		root, err := gitinit.Init(dir)
		return repoInitMsg{Root: root, Err: err}
	}
}

func (m *Model) applyRepoInit(msg repoInitMsg) tea.Cmd {
	form := m.addProject
	if form == nil {
		if msg.Root != "" {
			return func() tea.Msg { return gitinit.ReadyMsg{Root: msg.Root} }
		}
		return nil
	}
	form.initInProgress = false
	if msg.Root == "" {
		if msg.Err != nil {
			form.message = msg.Err.Error()
		} else {
			form.message = "Failed to initialize repository"
		}
		return nil
	}
	form.cwdIsRepo = true
	form.cwdProbed = true
	if strings.TrimSpace(form.location.Value()) == "" || form.location.Value() == m.host.ProjectDir {
		form.location.SetValue(msg.Root)
	}
	if strings.TrimSpace(form.name.Value()) == "" {
		form.name.SetValue(filepath.Base(msg.Root))
	}
	if msg.Err != nil {
		form.message = "Repository initialized on main; failed to update .gitignore"
	} else {
		form.message = ""
	}
	return func() tea.Msg { return gitinit.ReadyMsg{Root: msg.Root} }
}

// closeProjectForm abandons the draft, putting back the theme the user was
// looking at before the picker started previewing.
func (m *Model) closeProjectForm() {
	if m.addProject == nil {
		return
	}
	if m.addProject.picker != nil {
		m.addProject.picker.restoreTheme()
	} else {
		theme.ApplyResolved(m.addProject.restore)
	}
	m.addProject = nil
	m.closeEditor()
}

// --- rendering ----------------------------------------------------------

func (m *Model) buildProjectForm(b *paneBuilder) {
	form := m.addProject
	if form == nil {
		b.lead("This form is no longer open.")
		return
	}

	b.text(PaneTitle("Project details"))
	b.blank()
	if !form.edit {
		b.lead("Sidecar uses Git repositories for worktrees, status, and diffs.")
		b.lead("Add an existing repo by Location, or initialize this directory and save it as a project.")
		b.blank()
	}

	// Name.
	b.row(regionFormName, "", func(m *Model) tea.Cmd {
		m.editNameField()
		return nil
	}, func(s State) string {
		if m.editingID() == regionFormName {
			s.Focused = true
			return FormRow("Name", Field(&form.name, b.controlWidth(projectFormFieldWidth), s), s)
		}
		return FormRow("Name", StaticField(form.name.Value(), b.controlWidth(projectFormFieldWidth), s), s)
	})
	b.blank()

	// Location, with its completions aligned under the input rather than under
	// the pane edge.
	b.row(regionFormLocation, "", func(m *Model) tea.Cmd {
		m.editLocationField()
		return nil
	}, func(s State) string {
		if m.editingID() == regionFormLocation {
			s.Focused = true
			return FormRow("Location", Field(&form.location, b.controlWidth(projectFormFieldWidth), s), s)
		}
		return FormRow("Location", StaticField(form.location.Value(), b.controlWidth(projectFormFieldWidth), s), s)
	})
	m.buildCompletions(b, form)
	b.blank()
	m.buildInitThisDirectory(b, form)

	// Theme: a selector that expands the shared picker inline beneath itself.
	b.row(regionFormTheme, "", func(m *Model) tea.Cmd {
		m.toggleInlineThemePicker()
		return nil
	}, func(s State) string {
		label := "Use global theme"
		if !form.themeEntry.IsZero() {
			label = form.themeEntry.Name
		}
		if form.picker != nil {
			return FormRow("Theme", SelectorArrow(label, "▴", s), s)
		}
		return FormRow("Theme", Selector(label, s), s)
	})

	if form.picker != nil {
		b.blank()
		b.text(strings.Repeat(" ", RowIndent+4) + mutedStyle().Render("Find a theme"))
		m.buildThemePicker(b, form.picker, RowIndent+4)
		b.note("↑/↓ preview   Enter selects   Esc closes picker")
	}
	b.blank()

	// Preferred "open in" application. Last-used memory stays the fallback, so
	// leaving this unset is a real answer rather than a missing one.
	if len(m.host.OpenInApps) > 0 {
		label := openInRememberLabel
		if form.openIn != "" {
			label = m.openInName(form.openIn)
		}
		b.selectRowValue(regionFormOpenIn, "Open in", label, projectFormFieldWidth, m.openInOptions(), form.openIn,
			func(m *Model, option dropdownOption) tea.Cmd {
				if form := m.addProject; form != nil {
					form.openIn = option.id
				}
				return nil
			})
		b.blank()
	}

	if form.message != "" {
		b.text(IndentedRaw(Warning(form.message)))
		b.blank()
	}

	saveLabel := "Enter  Add project"
	if form.edit {
		saveLabel = "Enter  Save changes"
	}
	b.buttons(
		buttonSpec{id: regionFormSave, key: "", label: saveLabel, primary: true, run: func(m *Model) tea.Cmd {
			return m.saveProjectForm()
		}},
		buttonSpec{id: regionFormCancel, key: "", label: "Esc  Back to Projects", run: func(m *Model) tea.Cmd {
			m.closeProjectForm()
			m.Back()
			return nil
		}},
	)
}

// buildCompletions lists the matching folders under the Location input. The
// list only ever exists because the user typed a prefix: nothing enumerates a
// directory before that.
func (m *Model) buildCompletions(b *paneBuilder, form *projectForm) {
	if len(form.completions) == 0 || m.editingID() != regionFormLocation {
		return
	}
	b.text(strings.Repeat(" ", ControlColumn) + mutedStyle().Render("Matching folders"))
	for i, candidate := range form.completions {
		index := i
		value := candidate
		id := fmt.Sprintf("%s%d", regionFormCompletion, index)
		state := b.declare(id, "", false, func(m *Model) tea.Cmd {
			m.acceptCompletion(index)
			return nil
		})
		if index == form.completion {
			state.Focused = true
		}
		text := bodyStyle().Render(value)
		right := ""
		if state.Focused {
			text = titleStyle().Render(value)
			right = mutedStyle().Render("Tab completes")
		}
		line := strings.Repeat(" ", ControlColumn) + text
		if right != "" {
			line = padRight(line, right, b.inner)
		}
		y := len(b.lines)
		b.lines = append(b.lines, line)
		b.m.mouse.HitMap.AddRect(id, b.originX+ControlColumn, 1+y, b.inner-ControlColumn, 1, nil)
	}
}

// --- fields -------------------------------------------------------------

func (m *Model) editNameField() {
	form := m.addProject
	if form == nil {
		return
	}
	m.openEditor(&editorState{
		id:    regionFormName,
		input: &form.name,
		submit: func(m *Model) (tea.Cmd, bool) {
			m.editLocationField()
			return nil, true
		},
		keys: func(m *Model, msg tea.KeyPressMsg) (bool, tea.Cmd) {
			if msg.String() == "tab" {
				m.editLocationField()
				return true, nil
			}
			return false, nil
		},
	})
	m.focusControlByID(regionFormName)
}

// editLocationField gives Location the keyboard and starts user-initiated
// completion: every keystroke asks for candidates for what has actually been
// typed, and an empty field asks for nothing.
func (m *Model) editLocationField() {
	form := m.addProject
	if form == nil {
		return
	}
	m.openEditor(&editorState{
		id:     regionFormLocation,
		input:  &form.location,
		change: func(m *Model) { m.requestCompletions() },
		submit: func(m *Model) (tea.Cmd, bool) {
			m.closeEditor()
			m.focusControlByID(regionFormSave)
			return nil, true
		},
		cancel: func(m *Model) { m.clearCompletions() },
		keys: func(m *Model, msg tea.KeyPressMsg) (bool, tea.Cmd) {
			form := m.addProject
			if form == nil {
				return false, nil
			}
			switch msg.String() {
			case "tab":
				// Tab accepts the highlighted completion without submitting the
				// form: completing a path and adding a project are two decisions.
				if len(form.completions) > 0 {
					m.acceptCompletion(form.completion)
					return true, nil
				}
				// With nothing to complete, Tab walks to Initialize when this
				// directory can become a repo, otherwise to Save. It must not
				// jump to Search: first-run users need a keyboard path to init.
				m.closeEditor()
				if form.offersInit() {
					m.focusControlByID(regionFormInitRepo)
				} else {
					m.focusControlByID(regionFormSave)
				}
				return true, nil
			case "down", "ctrl+n":
				if len(form.completions) > 0 {
					form.completion = min(form.completion+1, len(form.completions)-1)
					return true, nil
				}
			case "up", "ctrl+p":
				if len(form.completions) > 0 {
					form.completion = max(form.completion-1, 0)
					return true, nil
				}
			}
			return false, nil
		},
	})
	m.focusControlByID(regionFormLocation)
	m.requestCompletions()
}

// completionsMsg carries a finished directory listing back to the form.
type completionsMsg struct {
	// For is the input the listing answers, so a slow result cannot land
	// against a prefix the user has since changed.
	For        string
	Candidates []string
}

func (completionsMsg) configMsg() {}

// requestCompletions asks for candidates in a command. Reading a directory is
// filesystem work and never happens while rendering.
func (m *Model) requestCompletions() {
	form := m.addProject
	if form == nil {
		return
	}
	typed := form.location.Value()
	if strings.TrimSpace(typed) == "" {
		m.clearCompletions()
		return
	}
	if typed == form.completionFor {
		return
	}
	form.completionFor = typed
	m.pending = append(m.pending, func() tea.Msg {
		return completionsMsg{For: typed, Candidates: pathcomplete.Directories(typed, completionCandidates)}
	})
}

// applyCompletions accepts a listing that still matches what is typed.
func (m *Model) applyCompletions(msg completionsMsg) {
	form := m.addProject
	if form == nil || form.location.Value() != msg.For {
		return
	}
	form.completions = msg.Candidates
	if form.completion >= len(form.completions) {
		form.completion = max(0, len(form.completions)-1)
	}
}

func (m *Model) clearCompletions() {
	if form := m.addProject; form != nil {
		form.completions = nil
		form.completion = 0
		form.completionFor = ""
	}
}

// acceptCompletion puts a candidate in the field. It does not submit: the user
// may keep typing deeper into the path.
func (m *Model) acceptCompletion(index int) {
	form := m.addProject
	if form == nil || index < 0 || index >= len(form.completions) {
		return
	}
	form.location.SetValue(form.completions[index])
	form.location.CursorEnd()
	form.completions = nil
	form.completion = 0
	form.completionFor = form.location.Value()
	if m.editingID() != regionFormLocation {
		m.editLocationField()
	}
	m.requestCompletions()
}

// openInOptions are the applications the host can open a project in, with "no
// preference" first: last-used memory stays the fallback, so leaving this unset
// is a real answer rather than a missing one.
func (m *Model) openInOptions() []dropdownOption {
	options := make([]dropdownOption, 0, len(m.host.OpenInApps)+1)
	options = append(options, dropdownOption{id: "", label: openInRememberLabel})
	for _, app := range m.host.OpenInApps {
		options = append(options, dropdownOption{id: app.ID, label: app.Name})
	}
	return options
}

// --- inline theme picker (mockup 03b) -----------------------------------

// toggleInlineThemePicker expands the shared picker under the Theme field. It
// is a disclosure: the draft, the route, and the Projects sidebar selection all
// stay exactly as they are.
func (m *Model) toggleInlineThemePicker() {
	form := m.addProject
	if form == nil {
		return
	}
	if form.picker != nil {
		m.collapseInlineThemePicker()
		return
	}
	picker := newThemePicker(true, inlinePickerRows)
	picker.selectEntry = func(m *Model, entry theme.Entry) tea.Cmd {
		form := m.addProject
		if form == nil || entry.IsZero() {
			return nil
		}
		// The draft holds the choice; only Save writes it.
		form.themeEntry = entry
		if form.picker != nil {
			form.picker.current = entry
		}
		return nil
	}
	picker.useGlobal = func(m *Model) tea.Cmd {
		form := m.addProject
		if form == nil {
			return nil
		}
		form.themeEntry = theme.Entry{}
		if form.picker != nil {
			form.picker.current = theme.Entry{}
			form.picker.restoreTheme()
		}
		m.collapseInlineThemePicker()
		return nil
	}
	current := form.themeEntry
	if current.IsZero() {
		// The project has no theme of its own, so the picker opens on what the
		// project inherits — the global choice. GlobalEntry, not
		// EntryForConfig: a user who has never opened Configuration has no
		// recorded global theme, and EntryForConfig would answer "inherits
		// from the level above" (the zero entry) for a scope that has no level
		// above it, leaving the cursor on nothing instead of on the theme
		// actually on screen.
		current = theme.GlobalEntry(m.Config().UI.Theme)
	}
	picker.open(current, form.restore)
	form.picker = picker
	m.focusPickerList()
}

// collapseInlineThemePicker closes the disclosure and returns focus to the
// Theme field. It never leaves the route and never discards the draft.
func (m *Model) collapseInlineThemePicker() bool {
	form := m.addProject
	if form == nil || form.picker == nil {
		return false
	}
	form.picker.restoreTheme()
	form.picker = nil
	m.closeEditor()
	m.focusControlByID(regionFormTheme)
	return true
}

// --- saving -------------------------------------------------------------

// saveProjectForm validates the draft and writes it. Validation is the same
// function the project switcher's add flow uses, so the two journeys accept
// exactly the same things.
func (m *Model) saveProjectForm() tea.Cmd {
	form := m.addProject
	if form == nil {
		return nil
	}
	m.closeEditor()

	name := strings.TrimSpace(form.name.Value())
	location := strings.TrimSpace(form.location.Value())

	skip := -1
	if form.edit {
		for i, project := range m.projects() {
			if project.Path == form.originalPath {
				skip = i
			}
		}
	}
	if message := config.ValidateProject(m.projects(), name, location, skip); message != "" {
		form.message = message
		return nil
	}
	form.message = ""

	expanded := config.ExpandPath(location)
	var themeConfig *config.ThemeConfig
	if !form.themeEntry.IsZero() {
		tc := theme.ThemeConfig(form.themeEntry)
		themeConfig = &tc
	}
	openIn := form.openIn
	editing := form.edit
	original := form.originalPath

	// Select the saved project when Projects comes back into view.
	m.projectsPage().selectPath = expanded
	if form.picker != nil {
		form.picker.restoreTheme()
	} else {
		theme.ApplyResolved(form.restore)
	}
	m.addProject = nil
	m.Back()

	if editing {
		return SaveCmd("Saved project: "+name, func() error {
			return config.UpdateProject(original, func(project *config.ProjectConfig) {
				project.Name = name
				project.Path = expanded
				project.Theme = themeConfig
				project.OpenIn = openIn
			})
		})
	}
	return SaveCmd("Added project: "+name, func() error {
		_, err := config.AddProject(config.ProjectConfig{
			Name:   name,
			Path:   expanded,
			Theme:  themeConfig,
			OpenIn: openIn,
		})
		return err
	})
}
