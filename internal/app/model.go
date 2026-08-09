package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/marcus/sidecar/internal/community"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/palette"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/theme"
	"github.com/marcus/sidecar/internal/version"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// ModalKind identifies an app-level modal with explicit priority ordering.
// Lower values = higher priority (checked first for rendering and input routing).
type ModalKind int

const (
	ModalNone             ModalKind = iota // No modal open
	ModalPalette                           // Command palette (highest priority)
	ModalHelp                              // Help overlay
	ModalUpdate                            // Update modal
	ModalDiagnostics                       // Diagnostics/version info
	ModalQuitConfirm                       // Quit confirmation dialog
	ModalProjectSwitcher                   // Project switcher
	ModalWorktreeSwitcher                  // Worktree switcher
	ModalThemeSwitcher                     // Theme switcher
	ModalOpenIn                            // Open In IDE picker
	ModalIssueInput                        // Issue ID text input
	ModalIssuePreview                      // Issue preview display (lowest priority)
)

// activeModal returns the highest-priority open modal.
// This is the single source of truth for which modal is currently active.
func (m *Model) activeModal() ModalKind {
	switch {
	case m.showPalette:
		return ModalPalette
	case m.showHelp:
		return ModalHelp
	case m.updateModalState != UpdateModalClosed:
		return ModalUpdate
	case m.showDiagnostics:
		return ModalDiagnostics
	case m.showQuitConfirm:
		return ModalQuitConfirm
	case m.showProjectSwitcher:
		return ModalProjectSwitcher
	case m.showWorktreeSwitcher:
		return ModalWorktreeSwitcher
	case m.showThemeSwitcher:
		return ModalThemeSwitcher
	case m.showOpenIn:
		return ModalOpenIn
	case m.showIssueInput:
		return ModalIssueInput
	case m.showIssuePreview:
		return ModalIssuePreview
	default:
		return ModalNone
	}
}

// hasModal returns true if any app-level modal is open.
func (m *Model) hasModal() bool {
	return m.activeModal() != ModalNone
}

// modalFocusContext returns the keymap context a modal owns while it is open.
// An open modal holds keyboard focus, so its context must survive plugin
// activity underneath it (an interactive tmux pane, for example, keeps emitting
// messages that would otherwise recompute the context back to the plugin's).
// Modals that intercept keys before context-based routing (quit confirm, the
// update modal) have no context of their own and report false, leaving the
// underlying plugin context in place.
func modalFocusContext(kind ModalKind) (string, bool) {
	switch kind {
	case ModalPalette:
		return "palette", true
	case ModalHelp:
		return "help", true
	case ModalDiagnostics:
		return "diagnostics", true
	case ModalProjectSwitcher:
		return "project-switcher", true
	case ModalWorktreeSwitcher:
		return "worktree-switcher", true
	case ModalThemeSwitcher:
		return "theme-switcher", true
	case ModalOpenIn:
		return "open-in", true
	case ModalIssueInput:
		return "issue-input", true
	case ModalIssuePreview:
		return "issue-preview", true
	}
	return "", false
}

// TabBounds represents the X position range of a tab for mouse hit testing.
type TabBounds struct {
	Start, End int
}

type projectAddState struct {
	nameInput     textinput.Model
	pathInput     textinput.Model
	errorMessage  string
	themeSelected string
}

type projectSwitcherDestination struct {
	Kind    string
	Name    string
	Path    string
	Project *config.ProjectConfig
}

const (
	destinationProject  = "project"
	destinationOverview = "overview"
	workspacePluginID   = "workspace-manager"
)

// Model is the root Bubble Tea model for the sidecar application.
type Model struct {
	// Configuration
	cfg *config.Config

	// Plugin management
	registry     *plugin.Registry
	activePlugin int

	// Keymap
	keymap        *keymap.Registry
	activeContext string

	// UI state
	width, height           int
	showHelp                bool
	helpModal               *modal.Modal
	helpModalWidth          int
	helpMouseHandler        *mouse.Handler
	showDiagnostics         bool
	diagnosticsModal        *modal.Modal
	diagnosticsModalWidth   int
	diagnosticsMouseHandler *mouse.Handler
	showClock               bool
	titleTemplate           string // ui.terminalTitle; empty leaves the terminal title alone
	lastTitle               string // last title emitted, so OSC 1 only goes out on change
	titleResyncCounter      int    // ticks since the icon name was last re-asserted
	showPalette             bool
	showQuitConfirm         bool
	quitModal               *modal.Modal
	quitMouseHandler        *mouse.Handler
	palette                 palette.Model

	// Project switcher modal
	showProjectSwitcher         bool
	projectSwitcherCursor       int
	projectSwitcherScroll       int // scroll offset for list
	projectSwitcherInput        textinput.Model
	projectSwitcherFiltered     []projectSwitcherDestination
	projectSwitcherModal        *modal.Modal
	projectSwitcherModalWidth   int
	projectSwitcherMouseHandler *mouse.Handler

	// Project add sub-mode (within project switcher)
	projectAddMode         bool
	projectAdd             *projectAddState
	projectAddModal        *modal.Modal
	projectAddModalWidth   int
	projectAddMouseHandler *mouse.Handler

	// Theme picker within add-project flow
	projectAddThemeMode       bool // is theme picker sub-modal open?
	projectAddThemeCursor     int
	projectAddThemeScroll     int
	projectAddThemeInput      textinput.Model
	projectAddThemeFiltered   []string // filtered built-in theme list
	projectAddCommunityMode   bool     // in community sub-browser?
	projectAddCommunityList   []string // filtered community scheme names
	projectAddCommunityCursor int
	projectAddCommunityScroll int

	// Worktree switcher modal
	showWorktreeSwitcher         bool
	worktreeSwitcherCursor       int
	worktreeSwitcherScroll       int
	worktreeSwitcherInput        textinput.Model
	worktreeSwitcherFiltered     []WorktreeInfo
	worktreeSwitcherAll          []WorktreeInfo
	worktreeSwitcherModal        *modal.Modal
	worktreeSwitcherModalWidth   int
	worktreeSwitcherMouseHandler *mouse.Handler
	worktreeCheckCounter         int // Counter for periodic worktree existence check

	// Worktree info cache (avoids git subprocess forks on every View render)
	cachedWorktreeInfo      *WorktreeInfo
	cachedWorktreeInventory []WorktreeInfo

	// Open In modal
	showOpenIn         bool
	openInCursor       int
	openInScroll       int
	openInApps         []openInApp
	openInLastID       string
	openInModal        *modal.Modal
	openInModalWidth   int
	openInMouseHandler *mouse.Handler

	// Theme switcher modal
	showThemeSwitcher         bool
	themeSwitcherModal        *modal.Modal
	themeSwitcherModalWidth   int
	themeSwitcherMouseHandler *mouse.Handler
	themeSwitcherSelectedIdx  int
	themeSwitcherInput        textinput.Model
	themeSwitcherFiltered     []themeEntry
	themeSwitcherOriginal     themeEntry // original theme to restore on cancel
	themeSwitcherScope        string     // "global" or "project"

	// Issue preview - input phase
	showIssueInput         bool
	issueInputInput        textinput.Model
	issueInputModal        *modal.Modal
	issueInputModalWidth   int
	issueInputMouseHandler *mouse.Handler

	// Issue input auto-complete
	issueSearchResults       []IssueSearchResult
	issueSearchQuery         string // last query sent to td search
	issueSearchLoading       bool
	issueSearchCursor        int  // selected result index (-1 = none/input focused)
	issueSearchScrollOffset  int  // viewport scroll offset for search results
	issueSearchIncludeClosed bool // whether to include closed issues in search

	// Issue preview - preview phase
	showIssuePreview         bool
	issuePreviewData         *IssuePreviewData
	issuePreviewLoading      bool
	issuePreviewError        error
	issuePreviewModal        *modal.Modal
	issuePreviewModalWidth   int
	issuePreviewMouseHandler *mouse.Handler

	// Header/footer
	ui *UIState

	// Status/toast messages
	statusMsg     string
	statusExpiry  time.Time
	statusIsError bool

	// Error handling
	lastError error

	// Ready state
	ready              bool
	applicationFocused bool

	// Version info
	currentVersion  string
	updateAvailable *version.UpdateAvailableMsg
	tdVersionInfo   *version.TdVersionMsg

	// Update feature state
	updateInProgress    bool
	updateError         string
	needsRestart        bool
	updateSpinnerFrame  int
	updateInstallMethod version.InstallMethod

	// Update modal state
	updateModalState      UpdateModalState
	updatePhase           UpdatePhase
	updatePhaseStatus     map[UpdatePhase]string
	updateStartTime       time.Time
	updateReleaseNotes    string // Release notes for current update
	updateChangelog       string // Full changelog content
	changelogVisible      bool
	changelogScrollOffset int
	changelogScrollState  *changelogViewState // Shared state for modal closure

	// Update modal (declarative)
	updatePreviewModal         *modal.Modal
	updatePreviewModalWidth    int
	updatePreviewMouseHandler  *mouse.Handler
	updateCompleteModal        *modal.Modal
	updateCompleteModalWidth   int
	updateCompleteMouseHandler *mouse.Handler
	updateErrorModal           *modal.Modal
	updateErrorModalWidth      int
	updateErrorMouseHandler    *mouse.Handler
	changelogModal             *modal.Modal
	changelogModalWidth        int
	changelogMouseHandler      *mouse.Handler
	changelogRenderedLines     []string // Cached rendered changelog lines
	changelogMaxVisibleLines   int      // Max lines visible in viewport

	// Intro animation
	intro IntroModel

	// Overview is an app-owned destination, not a project plugin. It is only
	// constructed while the default-off feature is enabled.
	overview       *overview.Model
	overviewActive bool
}

// New creates a new application model.
// initialPluginID optionally specifies which plugin to focus on startup (empty = first plugin).
func New(reg *plugin.Registry, km *keymap.Registry, cfg *config.Config, currentVersion, workDir, projectRoot, initialPluginID string) Model {
	repoName := GetRepoName(workDir)
	ui := NewUIState()
	ui.WorkDir = workDir
	ui.ProjectRoot = projectRoot

	// Determine initial active plugin index
	activeIdx := 0
	if initialPluginID != "" {
		for i, p := range reg.Plugins() {
			if p.ID() == initialPluginID {
				activeIdx = i
				break
			}
		}
	}

	m := Model{
		cfg:                cfg,
		registry:           reg,
		keymap:             km,
		activePlugin:       activeIdx,
		activeContext:      "global",
		showClock:          cfg.UI.ShowClock,
		titleTemplate:      cfg.UI.TerminalTitle,
		palette:            palette.New(),
		ui:                 ui,
		ready:              false,
		applicationFocused: true,
		intro:              NewIntroModel(repoName),
		currentVersion:     currentVersion,
		updatePhaseStatus:  make(map[UpdatePhase]string),
	}
	if features.IsEnabled(features.CrossProjectOverview.Name) {
		m.overview = overview.New(workspaceinventory.Collector{})
	}
	return m
}

// Init initializes the model and returns initial commands.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tickCmd(),
		IntroTick(),
		version.CheckAsync(m.currentVersion),
		version.CheckTdAsync(),
	}

	// Mark the startup plugin focused. SetActivePlugin only runs when the user
	// switches tabs, so without this the initial tab reports itself unfocused —
	// which, among other things, makes it poll at the slower unfocused interval.
	// Deliberately no PluginFocused() broadcast: several plugins refresh on that
	// message without checking their own focus flag, which would duplicate their
	// Start() work on the startup path.
	if p := m.ActivePlugin(); p != nil {
		p.SetFocused(true)
	}

	// Start all registered plugins
	for _, cmd := range m.registry.Start() {
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return tea.Batch(cmds...)
}

// initQuitModal initializes the quit confirmation modal.
func (m *Model) initQuitModal() {
	m.quitModal = modal.New("Quit Sidecar?",
		modal.WithWidth(50),
		modal.WithVariant(modal.VariantDefault),
		modal.WithPrimaryAction("quit"),
	).
		AddSection(modal.Text("Are you sure you want to quit?")).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Quit ", "quit"),
			modal.Btn(" Cancel ", "cancel"),
		))
	m.quitMouseHandler = mouse.NewHandler()
}

// ActivePlugin returns the currently active plugin.
func (m Model) ActivePlugin() plugin.Plugin {
	plugins := m.registry.Plugins()
	if len(plugins) == 0 {
		return nil
	}
	if m.activePlugin >= len(plugins) {
		m.activePlugin = 0
	}
	return plugins[m.activePlugin]
}

// SetActivePlugin sets the active plugin by index and returns a command
// to notify the plugin it has been focused.
func (m *Model) SetActivePlugin(idx int) tea.Cmd {
	plugins := m.registry.Plugins()
	if idx >= 0 && idx < len(plugins) {
		// Unfocus current
		if current := m.ActivePlugin(); current != nil {
			current.SetFocused(false)
		}
		m.activePlugin = idx
		// Focus new
		if next := m.ActivePlugin(); next != nil {
			next.SetFocused(true)
			m.updateContext()
			return PluginFocused()
		}
	}
	return nil
}

// NextPlugin switches to the next plugin.
func (m *Model) NextPlugin() tea.Cmd {
	plugins := m.registry.Plugins()
	if len(plugins) == 0 {
		return nil
	}
	return m.SetActivePlugin((m.activePlugin + 1) % len(plugins))
}

// PrevPlugin switches to the previous plugin.
func (m *Model) PrevPlugin() tea.Cmd {
	plugins := m.registry.Plugins()
	if len(plugins) == 0 {
		return nil
	}
	idx := m.activePlugin - 1
	if idx < 0 {
		idx = len(plugins) - 1
	}
	return m.SetActivePlugin(idx)
}

// FocusPluginByID switches to a plugin by its ID.
func (m *Model) FocusPluginByID(id string) tea.Cmd {
	plugins := m.registry.Plugins()
	for i, p := range plugins {
		if p.ID() == id {
			return m.SetActivePlugin(i)
		}
	}
	return nil
}

// ShowToast displays a temporary status message.
func (m *Model) ShowToast(msg string, duration time.Duration) {
	m.statusMsg = msg
	m.statusExpiry = time.Now().Add(duration)
}

// ClearToast clears any expired toast message.
func (m *Model) ClearToast() {
	if m.statusMsg != "" && time.Now().After(m.statusExpiry) {
		m.statusMsg = ""
		m.statusIsError = false
	}
}

// hasUpdatesAvailable returns true if either sidecar or td has an update available.
func (m *Model) hasUpdatesAvailable() bool {
	if m.updateAvailable != nil {
		return true
	}
	if m.tdVersionInfo != nil && m.tdVersionInfo.HasUpdate && m.tdVersionInfo.Installed {
		return true
	}
	return false
}

// initPhaseStatus initializes all update phases to pending status.
func (m *Model) initPhaseStatus() {
	m.updatePhaseStatus = map[UpdatePhase]string{
		PhaseCheckPrereqs: "pending",
		PhaseInstalling:   "pending",
		PhaseVerifying:    "pending",
	}
}

// startUpdateWithPhases starts the update process with phase tracking.
// This replaces the old doUpdate for the new modal-based update flow.
func (m *Model) startUpdateWithPhases() tea.Cmd {
	return tea.Batch(
		// Start elapsed timer
		m.startElapsedTimer(),
		// Start spinner animation
		updateSpinnerTick(),
		// Start the first phase (check prerequisites)
		m.runCheckPrerequisites(),
	)
}

// startElapsedTimer starts the elapsed time ticker.
func (m *Model) startElapsedTimer() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return UpdateElapsedTickMsg{}
	})
}

// UpdatePrereqsPassedMsg signals prerequisites check passed, triggers install phase.
type UpdatePrereqsPassedMsg struct{}

// UpdateInstallDoneMsg signals install completed, triggers verify phase.
type UpdateInstallDoneMsg struct {
	SidecarUpdated    bool
	TdUpdated         bool
	NewSidecarVersion string
	NewTdVersion      string
}

// runCheckPrerequisites runs the prerequisites check phase.
func (m *Model) runCheckPrerequisites() tea.Cmd {
	method := m.updateInstallMethod
	return func() tea.Msg {
		switch method {
		case version.InstallMethodHomebrew:
			if _, err := exec.LookPath("brew"); err != nil {
				return UpdateErrorMsg{Step: "check", Err: fmt.Errorf("brew not found in PATH")}
			}
		case version.InstallMethodBinary:
			// No prerequisites for binary download — just show the URL
		default:
			if _, err := exec.LookPath("go"); err != nil {
				return UpdateErrorMsg{Step: "check", Err: fmt.Errorf("go not found in PATH")}
			}
		}
		return UpdatePrereqsPassedMsg{}
	}
}

// runInstallPhase runs the install phase using the detected install method.
func (m *Model) runInstallPhase() tea.Cmd {
	sidecarUpdate := m.updateAvailable
	tdUpdate := m.tdVersionInfo
	method := m.updateInstallMethod

	return func() tea.Msg {
		var sidecarUpdated, tdUpdated bool
		var newSidecarVersion, newTdVersion string

		// Refresh Homebrew tap so brew knows about new versions
		if method == version.InstallMethodHomebrew {
			_ = exec.Command("brew", "update").Run() // best-effort
		}

		// Update sidecar
		if sidecarUpdate != nil {
			switch method {
			case version.InstallMethodHomebrew:
				cmd := exec.Command("brew", "upgrade", "sidecar")
				output, err := cmd.CombinedOutput()
				if err != nil {
					return UpdateErrorMsg{Step: "sidecar", Err: fmt.Errorf("%v: %s", err, output)}
				}
				outLower := strings.ToLower(string(output))
				if strings.Contains(outLower, "already installed") || strings.Contains(outLower, "already up-to-date") {
					return UpdateErrorMsg{Step: "sidecar", Err: fmt.Errorf("brew reports sidecar is already at latest version — tap may be out of date. Try: brew update && brew upgrade sidecar")}
				}
				sidecarUpdated = true
				newSidecarVersion = sidecarUpdate.LatestVersion
			case version.InstallMethodBinary:
				// Binary installs cannot be auto-updated; the preview modal
				// shows the download URL instead. The user must download manually.
			default: // Go install
				args := []string{
					"install",
					"-ldflags", fmt.Sprintf("-X main.Version=%s", sidecarUpdate.LatestVersion),
					fmt.Sprintf("github.com/marcus/sidecar/cmd/sidecar@%s", sidecarUpdate.LatestVersion),
				}
				cmd := exec.Command("go", args...)
				if output, err := cmd.CombinedOutput(); err != nil {
					return UpdateErrorMsg{Step: "sidecar", Err: fmt.Errorf("%v: %s", err, output)}
				}
				sidecarUpdated = true
				newSidecarVersion = sidecarUpdate.LatestVersion
			}
		}

		// Update td
		if tdUpdate != nil && tdUpdate.HasUpdate && tdUpdate.Installed {
			switch method {
			case version.InstallMethodHomebrew:
				cmd := exec.Command("brew", "upgrade", "td")
				output, err := cmd.CombinedOutput()
				if err != nil {
					return UpdateErrorMsg{Step: "td", Err: fmt.Errorf("%v: %s", err, output)}
				}
				outLower := strings.ToLower(string(output))
				if strings.Contains(outLower, "already installed") || strings.Contains(outLower, "already up-to-date") {
					return UpdateErrorMsg{Step: "td", Err: fmt.Errorf("brew reports td is already at latest version — tap may be out of date. Try: brew update && brew upgrade td")}
				}
			default: // Go install (binary users of td still use go install)
				cmd := exec.Command("go", "install",
					fmt.Sprintf("github.com/marcus/td@%s", tdUpdate.LatestVersion))
				if output, err := cmd.CombinedOutput(); err != nil {
					return UpdateErrorMsg{Step: "td", Err: fmt.Errorf("%v: %s", err, output)}
				}
			}
			tdUpdated = true
			newTdVersion = tdUpdate.LatestVersion
		}

		return UpdateInstallDoneMsg{
			SidecarUpdated:    sidecarUpdated,
			TdUpdated:         tdUpdated,
			NewSidecarVersion: newSidecarVersion,
			NewTdVersion:      newTdVersion,
		}
	}
}

// runVerifyPhase runs the verification phase (check installed binaries).
func (m *Model) runVerifyPhase(installResult UpdateInstallDoneMsg) tea.Cmd {
	return func() tea.Msg {
		// Verify sidecar binary if it was updated
		if installResult.SidecarUpdated {
			sidecarPath, err := exec.LookPath("sidecar")
			if err != nil {
				return UpdateErrorMsg{Step: "verify", Err: fmt.Errorf("sidecar not found in PATH after install")}
			}
			// Verify the binary is executable by running --version
			cmd := exec.Command(sidecarPath, "--version")
			output, err := cmd.Output()
			if err != nil {
				return UpdateErrorMsg{Step: "verify", Err: fmt.Errorf("sidecar binary not executable: %v", err)}
			}
			// Compare installed version against expected version
			if installResult.NewSidecarVersion != "" {
				got := strings.TrimSpace(string(output))
				expected := strings.TrimPrefix(installResult.NewSidecarVersion, "v")
				if !strings.Contains(got, expected) {
					return UpdateErrorMsg{Step: "verify", Err: fmt.Errorf(
						"version mismatch after update: expected %s, got %s — the update may not have taken effect, try updating manually",
						installResult.NewSidecarVersion, got)}
				}
			}
		}

		// Verify td binary if it was updated
		if installResult.TdUpdated {
			tdPath, err := exec.LookPath("td")
			if err != nil {
				return UpdateErrorMsg{Step: "verify", Err: fmt.Errorf("td not found in PATH after install")}
			}
			// Verify the binary is executable
			cmd := exec.Command(tdPath, "version", "--short")
			if err := cmd.Run(); err != nil {
				return UpdateErrorMsg{Step: "verify", Err: fmt.Errorf("td binary not executable: %v", err)}
			}
		}

		return UpdateSuccessMsg(installResult)
	}
}

// resetProjectSwitcher resets the project switcher modal state.
func (m *Model) resetProjectSwitcher() {
	m.showProjectSwitcher = false
	m.projectSwitcherCursor = 0
	m.projectSwitcherScroll = 0
	m.projectSwitcherFiltered = nil
	m.clearProjectSwitcherModal()
	m.resetProjectAdd()
	// Restore current project's theme (undo any live preview)
	resolved := theme.ResolveTheme(m.cfg, m.ui.WorkDir)
	theme.ApplyResolved(resolved)
}

// clearProjectSwitcherModal clears the modal cache.
func (m *Model) clearProjectSwitcherModal() {
	m.projectSwitcherModal = nil
	m.projectSwitcherModalWidth = 0
	m.projectSwitcherMouseHandler = nil
}

// initProjectSwitcher initializes the project switcher modal.
func (m *Model) initProjectSwitcher() {
	m.clearProjectSwitcherModal()
	ti := textinput.New()
	ti.Placeholder = "Filter projects..."
	ti.Focus()
	ti.CharLimit = 50
	ti.SetWidth(40)
	m.projectSwitcherInput = ti
	m.projectSwitcherFiltered = m.projectSwitcherDestinations("")
	m.projectSwitcherCursor = 0
	m.projectSwitcherScroll = 0

	// Set cursor to current project if found
	for i, destination := range m.projectSwitcherFiltered {
		if m.overviewActive && destination.Kind == destinationOverview {
			m.projectSwitcherCursor = i
			break
		}
		matchesCurrent := destination.Path == m.ui.WorkDir
		if m.overview != nil {
			matchesCurrent = matchesCurrent || destination.Path == m.ui.ProjectRoot
		}
		if !m.overviewActive && destination.Kind == destinationProject && matchesCurrent {
			m.projectSwitcherCursor = i
			break
		}
	}
	// Preview the initially-selected project's theme
	m.previewProjectTheme()
}

func (m *Model) projectSwitcherDestinations(query string) []projectSwitcherDestination {
	projects := filterProjects(m.cfg.Projects.List, query)
	result := make([]projectSwitcherDestination, 0, len(projects)+1)
	if m.overview != nil {
		result = append(result, projectSwitcherDestination{Kind: destinationOverview, Name: "Overview"})
	}
	for i := range projects {
		project := projects[i]
		result = append(result, projectSwitcherDestination{Kind: destinationProject, Name: project.Name, Path: project.Path, Project: &project})
	}
	return result
}

// filterProjects filters projects by name or path using a case-insensitive substring match.
func filterProjects(all []config.ProjectConfig, query string) []config.ProjectConfig {
	if query == "" {
		return all
	}
	q := strings.ToLower(query)
	var matches []config.ProjectConfig
	for _, p := range all {
		if strings.Contains(strings.ToLower(p.Name), q) ||
			strings.Contains(strings.ToLower(p.Path), q) {
			matches = append(matches, p)
		}
	}
	return matches
}

// updateProjectSwitcherFilter sends text input to the project filter and
// refreshes the derived list state. Mouse-driven modal transitions can leave
// the bubbles input blurred, so filtering always explicitly reclaims focus.
func (m *Model) updateProjectSwitcherFilter(msg tea.Msg) tea.Cmd {
	m.projectSwitcherInput.Focus()

	var cmd tea.Cmd
	m.projectSwitcherInput, cmd = m.projectSwitcherInput.Update(msg)
	m.projectSwitcherFiltered = m.projectSwitcherDestinations(m.projectSwitcherInput.Value())
	m.clearProjectSwitcherModal()

	if m.projectSwitcherCursor >= len(m.projectSwitcherFiltered) {
		m.projectSwitcherCursor = len(m.projectSwitcherFiltered) - 1
	}
	if m.projectSwitcherCursor < 0 {
		m.projectSwitcherCursor = 0
	}
	m.projectSwitcherScroll = projectSwitcherEnsureCursorVisible(m.projectSwitcherCursor, 0, 8)
	m.previewProjectTheme()

	return cmd
}

// projectSwitcherEnsureCursorVisible adjusts scroll to keep cursor in view.
// Returns the new scroll offset.
func projectSwitcherEnsureCursorVisible(cursor, scroll, maxVisible int) int {
	if cursor < scroll {
		return cursor
	}
	if cursor >= scroll+maxVisible {
		return cursor - maxVisible + 1
	}
	return scroll
}

// switchProject switches all plugins to a new project directory.
func (m *Model) switchProject(projectPath string) tea.Cmd {
	return m.switchProjectWithInventory(projectPath, nil)
}

func (m *Model) activateProjectSwitcherDestination(destination projectSwitcherDestination) tea.Cmd {
	m.resetProjectSwitcher()
	if destination.Kind == destinationOverview && m.overview != nil {
		m.overviewActive = true
		m.updateContext()
		return m.overview.Start(m.overviewProjects())
	}
	m.exitOverview()
	m.updateContext()
	return m.switchProject(destination.Path)
}

func (m *Model) overviewProjects() []overview.Project {
	if m.cfg == nil {
		return nil
	}
	var selected []overview.Project
	currentRoot := workspaceinventory.CanonicalPath(m.ui.ProjectRoot)
	for _, project := range m.cfg.Projects.List {
		if workspaceinventory.CanonicalPath(project.Path) == currentRoot {
			selected = append(selected, overview.Project{Name: project.Name, Path: project.Path})
			break
		}
	}
	for _, project := range m.cfg.Projects.List {
		if len(selected) > 0 && workspaceinventory.CanonicalPath(project.Path) == workspaceinventory.CanonicalPath(selected[0].Path) {
			continue
		}
		selected = append(selected, overview.Project{Name: project.Name, Path: project.Path})
		if len(selected) == 2 {
			break
		}
	}
	return selected
}

func (m *Model) exitOverview() {
	if m.overviewActive && m.overview != nil {
		m.overview.Stop()
	}
	m.overviewActive = false
}

func (m *Model) switchProjectWithInventory(projectPath string, inventory []WorktreeInfo) tea.Cmd {
	return m.switchProjectWithSelection(projectPath, inventory, nil)
}

func (m *Model) switchProjectWithSelection(projectPath string, inventory []WorktreeInfo, pending *plugin.PendingWorkspaceSelection) tea.Cmd {
	// Skip if already on this project
	if projectPath == m.ui.WorkDir {
		return func() tea.Msg {
			return ToastMsg{Message: "Already on this project", Duration: 2 * time.Second}
		}
	}

	// Save the active plugin state for the old project root
	oldWorkDir := m.ui.WorkDir
	if activePlugin := m.ActivePlugin(); activePlugin != nil {
		_ = state.SetActivePlugin(oldWorkDir, activePlugin.ID())
	}

	// Normalize old workdir for comparisons
	normalizedOldWorkDir, _ := normalizePath(oldWorkDir)

	// Check if target project has a saved worktree we should restore.
	// Only restore if projectPath is the main repo - if user explicitly chose a
	// specific worktree path (via worktree switcher), respect that choice.
	targetPath := projectPath
	targetMainRepo := mainWorktreePath(inventory)
	if targetMainRepo == "" {
		targetMainRepo = GetMainWorktreePath(projectPath)
	}
	if targetMainRepo != "" {
		normalizedProject, _ := normalizePath(projectPath)
		normalizedTargetMain, _ := normalizePath(targetMainRepo)

		// Only restore saved worktree if switching to the main repo path
		if normalizedProject == normalizedTargetMain {
			if savedWorktree := state.GetLastWorktreePath(normalizedTargetMain); savedWorktree != "" {
				// Don't restore if the saved worktree is where we're coming FROM
				// (user is explicitly leaving that worktree)
				if savedWorktree != normalizedOldWorkDir {
					// Verify saved worktree still exists
					if WorktreeExists(savedWorktree) {
						targetPath = savedWorktree
					} else {
						// Stale entry - clear it
						_ = state.ClearLastWorktreePath(normalizedTargetMain)
					}
				}
			}
		}

		// Save the final target as last active worktree for this repo
		normalizedTarget, _ := normalizePath(targetPath)
		_ = state.SetLastWorktreePath(normalizedTargetMain, normalizedTarget)
	}

	// Update the UI state
	m.ui.WorkDir = targetPath
	m.intro.RepoName = GetRepoName(targetPath)
	if len(inventory) > 0 {
		m.setWorktreeInventory(inventory, targetPath)
	} else {
		m.refreshWorktreeCache()
	}

	// Resolve project root (main worktree for linked worktrees, same as targetPath otherwise)
	newProjectRoot := mainWorktreePath(inventory)
	if newProjectRoot == "" {
		newProjectRoot = GetMainWorktreePath(targetPath)
	}
	if newProjectRoot == "" {
		newProjectRoot = targetPath
	}
	m.ui.ProjectRoot = newProjectRoot

	// Apply project-specific theme (or global fallback)
	resolved := theme.ResolveTheme(m.cfg, targetPath)
	theme.ApplyResolved(resolved)

	// Reinitialize all plugins with the new working directory and project root
	// This stops all plugins, updates the context, and starts them again
	startCmds := m.registry.Reinit(targetPath, newProjectRoot)
	if pending != nil {
		if selector, ok := m.registry.Get(workspacePluginID).(plugin.PendingWorkspaceSelector); ok {
			selector.SetPendingWorkspaceSelection(*pending)
		}
	}

	// Send WindowSizeMsg to all plugins so they recalculate layout/bounds.
	// Without this, plugins like td-monitor lose mouse interactivity because
	// their panel bounds are only calculated on WindowSizeMsg receipt.
	adjustedHeight := m.height - headerHeight - footerHeight
	sizeMsg := tea.WindowSizeMsg{Width: m.width, Height: adjustedHeight}
	plugins := m.registry.Plugins()
	for i, p := range plugins {
		newPlugin, cmd := p.Update(sizeMsg)
		plugins[i] = newPlugin
		if cmd != nil {
			startCmds = append(startCmds, cmd)
		}
	}

	// Restore active plugin for the new project root if saved, otherwise keep current
	newActivePluginID := state.GetActivePluginForWorkDir(targetPath, newProjectRoot)
	if newActivePluginID != "" {
		m.FocusPluginByID(newActivePluginID)
	}
	var overviewFocusCmd tea.Cmd
	if pending != nil {
		overviewFocusCmd = m.FocusPluginByID(workspacePluginID)
	}

	// Retitle the terminal now rather than waiting for the next tick, so the
	// tab label changes at the same moment the UI does.
	titleCmd := m.syncTerminalTitle(false)
	var inventoryRefresh tea.Cmd
	if len(inventory) > 0 {
		inventoryRefresh = refreshWorktreeInventoryCmd(targetPath)
	}

	// Return batch of start commands plus a toast notification
	return tea.Batch(
		tea.Batch(startCmds...),
		titleCmd,
		inventoryRefresh,
		overviewFocusCmd,
		func() tea.Msg {
			return ToastMsg{
				Message:  fmt.Sprintf("Switched to %s", GetRepoName(targetPath)),
				Duration: 3 * time.Second,
			}
		},
	)
}

func (m *Model) navigateFromOverview(workspace workspaceinventory.Workspace) tea.Cmd {
	m.exitOverview()
	kind := plugin.WorkspaceSelectionWorktree
	target := workspace.Path
	key := workspace.Key
	if workspace.Kind == workspaceinventory.KindShell {
		kind = plugin.WorkspaceSelectionShell
		target = workspace.ProjectRoot
		key = workspace.TmuxName
	}
	pending := plugin.PendingWorkspaceSelection{Kind: kind, Key: key, Path: workspace.Path}
	if workspaceinventory.CanonicalPath(target) == workspaceinventory.CanonicalPath(m.ui.WorkDir) ||
		(workspace.Kind == workspaceinventory.KindShell && workspaceinventory.CanonicalPath(target) == workspaceinventory.CanonicalPath(m.ui.ProjectRoot)) {
		if selector, ok := m.registry.Get(workspacePluginID).(plugin.PendingWorkspaceSelector); ok {
			selector.SetPendingWorkspaceSelection(pending)
		}
		m.updateContext()
		return m.FocusPluginByID(workspacePluginID)
	}
	return m.switchProjectWithSelection(target, nil, &pending)
}

// previewProjectTheme applies the theme for the currently selected project in the switcher.
func (m *Model) previewProjectTheme() {
	destinations := m.projectSwitcherFiltered
	if m.projectSwitcherCursor >= 0 && m.projectSwitcherCursor < len(destinations) {
		destination := destinations[m.projectSwitcherCursor]
		if destination.Kind == destinationOverview {
			theme.ApplyResolved(theme.ResolveTheme(m.cfg, ""))
			return
		}
		theme.ApplyResolved(theme.ResolveTheme(m.cfg, destination.Path))
	}
}

// currentProjectConfig returns the ProjectConfig for the current workdir, or nil.
// If the current workdir is a worktree, it also checks if the main worktree path
// matches a registered project (so theme scope selector works from worktrees).
func (m *Model) currentProjectConfig() *config.ProjectConfig {
	if m.cfg == nil {
		return nil
	}
	// First, check direct match
	for i := range m.cfg.Projects.List {
		if m.cfg.Projects.List[i].Path == m.ui.WorkDir {
			return &m.cfg.Projects.List[i]
		}
	}

	// If not found, check if we're in a worktree and the main repo is registered
	mainPath := GetMainWorktreePath(m.ui.WorkDir)
	if mainPath != "" && mainPath != m.ui.WorkDir {
		for i := range m.cfg.Projects.List {
			if m.cfg.Projects.List[i].Path == mainPath {
				return &m.cfg.Projects.List[i]
			}
		}
	}

	return nil
}

// confirmThemeSelection saves the theme, reloads config, resets all theme
// switcher state, and returns a toast command. displayName is used in the toast.
func (m *Model) confirmThemeSelection(tc config.ThemeConfig, displayName string) tea.Cmd {
	scope := m.themeSwitcherScope

	// Save before reset clears scope
	if err := m.saveTheme(tc, scope); err != nil {
		m.resetThemeSwitcher()
		m.updateContext()
		return func() tea.Msg {
			return ToastMsg{Message: "Theme applied (save failed)", Duration: 3 * time.Second, IsError: true}
		}
	}
	if cfg, err := config.Load(); err == nil {
		m.cfg = cfg
	}

	m.resetThemeSwitcher()
	m.updateContext()

	toastMsg := "Theme: " + displayName
	if scope == "project" {
		toastMsg += " (project)"
	} else {
		toastMsg += " (global)"
	}
	return func() tea.Msg {
		return ToastMsg{Message: toastMsg, Duration: 2 * time.Second}
	}
}

// saveTheme persists a ThemeConfig based on scope.
func (m *Model) saveTheme(tc config.ThemeConfig, scope string) error {
	if scope == "project" {
		projectPath := m.ui.WorkDir
		if pc := m.currentProjectConfig(); pc != nil {
			projectPath = pc.Path
		}
		return config.SaveProjectTheme(projectPath, &tc)
	}
	return config.SaveGlobalTheme(tc)
}

// copyProjectSetupPrompt copies an LLM-friendly prompt for configuring projects.
func (m *Model) copyProjectSetupPrompt() tea.Cmd {
	prompt := `Configure sidecar projects for me.

Add my code projects to ~/.config/sidecar/config.json using this format:

{
  "projects": {
    "list": [
      {"name": "short-name", "path": "~/code/project-path"}
    ]
  }
}

Rules:
- Use short, memorable names (1-2 words, lowercase, hyphens ok)
- Expand ~ to full home path if needed
- Only add directories that exist and contain code
- Merge with existing config if present

My code is located at: [TELL ME WHERE YOUR CODE DIRECTORIES ARE]`

	if err := clipboard.WriteAll(prompt); err != nil {
		return func() tea.Msg {
			return ToastMsg{Message: "Copy failed: " + err.Error(), Duration: 2 * time.Second}
		}
	}
	return func() tea.Msg {
		return ToastMsg{Message: "Copied LLM setup prompt", Duration: 2 * time.Second}
	}
}

// initProjectAdd initializes the project add sub-mode.
func (m *Model) initProjectAdd() {
	m.projectAddMode = true
	m.clearProjectAddModal()

	if m.projectAdd == nil {
		m.projectAdd = &projectAddState{}
	}
	m.projectAdd.errorMessage = ""
	m.projectAdd.themeSelected = ""

	nameInput := textinput.New()
	nameInput.Placeholder = "project-name"
	nameInput.CharLimit = 40
	nameInput.SetWidth(36)
	nameInput.Focus()
	m.projectAdd.nameInput = nameInput

	pathInput := textinput.New()
	pathInput.Placeholder = "~/code/project-path"
	pathInput.CharLimit = 200
	pathInput.SetWidth(36)
	m.projectAdd.pathInput = pathInput
}

// resetProjectAdd resets the project add sub-mode state.
func (m *Model) resetProjectAdd() {
	m.projectAddMode = false
	if m.projectAdd != nil {
		m.projectAdd.errorMessage = ""
		m.projectAdd.themeSelected = ""
	}
	m.clearProjectAddModal()
	m.resetProjectAddThemePicker()
}

// initProjectAddThemePicker opens the theme picker sub-modal.
func (m *Model) initProjectAddThemePicker() {
	m.projectAddThemeMode = true
	ti := textinput.New()
	ti.Placeholder = "Filter themes..."
	ti.Focus()
	ti.CharLimit = 50
	ti.SetWidth(36)
	m.projectAddThemeInput = ti
	m.projectAddThemeFiltered = append([]string{"(use global)"}, styles.ListThemes()...)
	m.projectAddThemeCursor = 0
	m.projectAddThemeScroll = 0
	m.projectAddCommunityMode = false
}

// resetProjectAddThemePicker closes the theme picker sub-modal.
func (m *Model) resetProjectAddThemePicker() {
	m.projectAddThemeMode = false
	m.projectAddCommunityMode = false
	m.projectAddThemeCursor = 0
	m.projectAddThemeScroll = 0
	m.projectAddCommunityCursor = 0
	m.projectAddCommunityScroll = 0
}

// previewProjectAddTheme previews the currently-selected built-in theme.
func (m *Model) previewProjectAddTheme() {
	if m.projectAddThemeCursor >= 0 && m.projectAddThemeCursor < len(m.projectAddThemeFiltered) {
		name := m.projectAddThemeFiltered[m.projectAddThemeCursor]
		if name == "(use global)" {
			resolved := theme.ResolveTheme(m.cfg, m.ui.WorkDir)
			theme.ApplyResolved(resolved)
		} else {
			theme.ApplyResolved(theme.ResolvedTheme{BaseName: name})
		}
	}
}

// previewProjectAddCommunity previews the currently-selected community theme.
func (m *Model) previewProjectAddCommunity() {
	if m.projectAddCommunityCursor >= 0 && m.projectAddCommunityCursor < len(m.projectAddCommunityList) {
		name := m.projectAddCommunityList[m.projectAddCommunityCursor]
		theme.ApplyResolved(theme.ResolvedTheme{BaseName: "default", CommunityName: name})
	}
}

// validateProjectAdd validates the project add form inputs.
// Returns an error message or empty string if valid.
func (m *Model) validateProjectAdd() string {
	if m.projectAdd == nil {
		return "Name is required"
	}

	name := strings.TrimSpace(m.projectAdd.nameInput.Value())
	path := strings.TrimSpace(m.projectAdd.pathInput.Value())

	if name == "" {
		return "Name is required"
	}
	if path == "" {
		return "Path is required"
	}

	// Expand path for validation
	expanded := config.ExpandPath(path)

	// Check path exists and is a directory
	info, err := os.Stat(expanded)
	if err != nil {
		if os.IsNotExist(err) {
			return "Path does not exist"
		}
		return "Cannot access path"
	}
	if !info.IsDir() {
		return "Path is not a directory"
	}

	// Check for duplicate name or path
	for _, proj := range m.cfg.Projects.List {
		if strings.EqualFold(proj.Name, name) {
			return "Project name already exists"
		}
		if proj.Path == expanded {
			return "Project path already configured"
		}
	}

	return ""
}

// saveProjectAdd saves the new project to config and refreshes the list.
func (m *Model) saveProjectAdd() tea.Cmd {
	if m.projectAdd == nil {
		return func() tea.Msg {
			return ToastMsg{Message: "Project add state missing", Duration: 3 * time.Second, IsError: true}
		}
	}

	name := strings.TrimSpace(m.projectAdd.nameInput.Value())
	path := strings.TrimSpace(m.projectAdd.pathInput.Value())

	// Build project config
	proj := config.ProjectConfig{
		Name: name,
		Path: config.ExpandPath(path),
	}

	// Add theme if user selected one
	if m.projectAdd.themeSelected != "" && m.projectAdd.themeSelected != "(use global)" {
		if community.GetScheme(m.projectAdd.themeSelected) != nil {
			proj.Theme = &config.ThemeConfig{
				Name:      "default",
				Community: m.projectAdd.themeSelected,
			}
		} else {
			proj.Theme = &config.ThemeConfig{
				Name: m.projectAdd.themeSelected,
			}
		}
	}

	// Reload config from disk to avoid overwriting external changes
	cfg, err := config.Load()
	if err != nil {
		return func() tea.Msg {
			return ToastMsg{Message: "Failed to load config: " + err.Error(), Duration: 3 * time.Second, IsError: true}
		}
	}

	// Add project to fresh config
	cfg.Projects.List = append(cfg.Projects.List, proj)

	// Save to disk
	if err := config.Save(cfg); err != nil {
		return func() tea.Msg {
			return ToastMsg{Message: "Added project (save failed: " + err.Error() + ")", Duration: 3 * time.Second, IsError: true}
		}
	}

	// Update in-memory config
	m.cfg.Projects.List = cfg.Projects.List

	// Refresh the filtered list
	m.projectSwitcherFiltered = m.projectSwitcherDestinations("")

	return func() tea.Msg {
		return ToastMsg{Message: fmt.Sprintf("Added project: %s", name), Duration: 3 * time.Second}
	}
}

// resetThemeSwitcher resets the theme switcher modal state.
func (m *Model) resetThemeSwitcher() {
	m.showThemeSwitcher = false
	m.themeSwitcherSelectedIdx = 0
	m.themeSwitcherFiltered = nil
	m.themeSwitcherScope = ""
	m.themeSwitcherOriginal = themeEntry{}
	m.clearThemeSwitcherModal()
}

// clearThemeSwitcherModal clears the theme switcher modal state.
func (m *Model) clearThemeSwitcherModal() {
	m.themeSwitcherModal = nil
	m.themeSwitcherModalWidth = 0
	m.themeSwitcherMouseHandler = nil
}

// initIssueInput initializes the issue input modal.
func (m *Model) initIssueInput() {
	ti := textinput.New()
	ti.Placeholder = "Issue ID or search text"
	ti.Focus()
	ti.CharLimit = 50
	ti.SetWidth(50)
	m.issueInputInput = ti
	m.issueInputModal = nil
	m.issueInputModalWidth = 0
	m.issueInputMouseHandler = mouse.NewHandler()
	m.issueSearchResults = nil
	m.issueSearchQuery = ""
	m.issueSearchLoading = false
	m.issueSearchCursor = -1
	m.issueSearchScrollOffset = 0
	m.issueSearchIncludeClosed = false
}

// resetIssueInput resets the issue input modal state.
func (m *Model) resetIssueInput() {
	m.showIssueInput = false
	m.issueInputModal = nil
	m.issueInputModalWidth = 0
	m.issueInputMouseHandler = nil
	m.issueSearchResults = nil
	m.issueSearchQuery = ""
	m.issueSearchLoading = false
	m.issueSearchCursor = -1
	m.issueSearchScrollOffset = 0
	m.issueSearchIncludeClosed = false
}

// resetIssuePreview resets the issue preview modal state.
func (m *Model) resetIssuePreview() {
	m.showIssuePreview = false
	m.issuePreviewData = nil
	m.issuePreviewLoading = false
	m.issuePreviewError = nil
	m.issuePreviewModal = nil
	m.issuePreviewModalWidth = 0
	m.issuePreviewMouseHandler = nil
}

// backToIssueInput closes the preview and returns to the search modal
// with the previous query, results, and cursor intact.
func (m *Model) backToIssueInput() {
	m.resetIssuePreview()
	m.showIssueInput = true
	m.activeContext = "issue-input"
	m.issueInputInput.Focus()
	m.issueInputModal = nil
	m.issueInputModalWidth = 0
	m.issueInputMouseHandler = mouse.NewHandler()
}

// initThemeSwitcher initializes the theme switcher modal.
func (m *Model) initThemeSwitcher() {
	ti := textinput.New()
	ti.Placeholder = "Filter themes..."
	ti.Focus()
	ti.CharLimit = 50
	ti.SetWidth(54)
	m.themeSwitcherInput = ti

	allEntries := buildUnifiedThemeList()
	m.themeSwitcherFiltered = allEntries
	m.themeSwitcherSelectedIdx = 0
	if m.currentProjectConfig() != nil {
		m.themeSwitcherScope = "project"
	} else {
		m.themeSwitcherScope = "global"
	}
	m.clearThemeSwitcherModal()

	// Determine original theme from config
	m.themeSwitcherOriginal = themeEntry{Name: "default", IsBuiltIn: true, ThemeKey: "default"}
	if freshCfg, err := config.Load(); err == nil {
		if freshCfg.UI.Theme.Community != "" {
			// Current theme is a community theme
			communityName := freshCfg.UI.Theme.Community
			m.themeSwitcherOriginal = themeEntry{Name: communityName, IsBuiltIn: false, ThemeKey: communityName}
		} else if freshCfg.UI.Theme.Name != "" {
			name := freshCfg.UI.Theme.Name
			displayName := name
			if t := styles.GetTheme(name); t.DisplayName != "" {
				displayName = t.DisplayName
			}
			m.themeSwitcherOriginal = themeEntry{Name: displayName, IsBuiltIn: true, ThemeKey: name}
		}
	}

	// Set cursor to current theme
	for i, entry := range m.themeSwitcherFiltered {
		if entry.IsBuiltIn == m.themeSwitcherOriginal.IsBuiltIn && entry.ThemeKey == m.themeSwitcherOriginal.ThemeKey {
			m.themeSwitcherSelectedIdx = i
			break
		}
	}
}

// previewThemeEntry applies the given theme entry for live preview.
func (m *Model) previewThemeEntry(entry themeEntry) {
	if entry.IsBuiltIn {
		m.applyThemeFromConfig(entry.ThemeKey)
	} else {
		theme.ApplyResolved(theme.ResolvedTheme{
			BaseName:      "default",
			CommunityName: entry.ThemeKey,
		})
	}
}

// applyThemeFromConfig applies a theme, using config overrides only if the
// saved config has that theme selected. This means live preview of other themes
// won't include user customizations (which is intentional - you want to see the
// base theme, not your customizations for a different theme).
func (m *Model) applyThemeFromConfig(themeName string) {
	freshCfg, err := config.Load()
	if err == nil && freshCfg.UI.Theme.Name == themeName {
		// Apply the saved theme with its full config (community + overrides)
		theme.ApplyResolved(theme.ResolvedTheme{
			BaseName:      themeName,
			CommunityName: freshCfg.UI.Theme.Community,
			Overrides:     freshCfg.UI.Theme.Overrides,
		})
	} else {
		styles.ApplyTheme(themeName)
	}
}
