package app

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/community"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/palette"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/theme"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/version"
)

// isMouseEscapeSequence returns true if the key message appears to be
// an unparsed mouse escape sequence (SGR format: [<...M or [<...m)
func isMouseEscapeSequence(msg tea.KeyPressMsg) bool {
	s := msg.String()
	// SGR mouse sequences contain [< and end with M or m
	if strings.Contains(s, "[<") && (strings.HasSuffix(s, "M") || strings.HasSuffix(s, "m")) {
		return true
	}
	// Check for semicolon-separated coordinate patterns typical of mouse sequences
	if strings.Contains(s, ";") && strings.ContainsAny(s, "0123456789") {
		if strings.HasSuffix(s, "M") || strings.HasSuffix(s, "m") {
			return true
		}
	}
	return false
}

// offsetMouseY returns a copy of the mouse message with its Y coordinate
// shifted by dy, preserving the concrete message type. In bubbletea v2 mouse
// messages are interfaces (no struct rebuild), so we reconstruct the matching
// concrete type from the offset Mouse value.
func offsetMouseY(msg tea.MouseMsg, dy int) tea.MouseMsg {
	mm := msg.Mouse()
	mm.Y += dy
	switch msg.(type) {
	case tea.MouseClickMsg:
		return tea.MouseClickMsg(mm)
	case tea.MouseReleaseMsg:
		return tea.MouseReleaseMsg(mm)
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg(mm)
	case tea.MouseMotionMsg:
		return tea.MouseMotionMsg(mm)
	}
	return msg
}

// handlePaste routes a bracketed-paste message into the active text-input modal
// (mirroring the per-modal key routing in handleKeyMsg), or forwards it to the
// active plugin when no app-level text-input modal is open. textinput.Update handles
// tea.PasteMsg natively in v2.
func (m *Model) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	switch m.activeModal() {
	case ModalPalette:
		var cmd tea.Cmd
		m.palette, cmd = m.palette.Update(msg)
		return m, cmd

	case ModalWorktreeSwitcher:
		var cmd tea.Cmd
		m.worktreeSwitcherInput, cmd = m.worktreeSwitcherInput.Update(msg)
		m.worktreeSwitcherFiltered = filterWorktrees(m.worktreeSwitcherAll, m.worktreeSwitcherInput.Value())
		m.clearWorktreeSwitcherModal()
		return m, cmd

	case ModalProjectSwitcher:
		// The project-add sub-flow has multiple focus-dependent inputs; leave it
		// to the plugin-forward fallback rather than guess the focused field.
		if !m.projectAddMode {
			return m, m.updateProjectSwitcherFilter(msg)
		}

	case ModalThemeSwitcher:
		var cmd tea.Cmd
		m.themeSwitcherInput, cmd = m.themeSwitcherInput.Update(msg)
		m.themeSwitcherFiltered = filterThemeEntries(buildUnifiedThemeList(), m.themeSwitcherInput.Value())
		m.clearThemeSwitcherModal()
		return m, cmd

	case ModalIssueInput:
		var cmd tea.Cmd
		m.issueInputInput, cmd = m.issueInputInput.Update(msg)
		m.issueInputModal = nil
		m.issueInputModalWidth = 0
		newValue := strings.TrimSpace(m.issueInputInput.Value())
		if newValue != m.issueSearchQuery && len(newValue) >= 2 {
			m.issueSearchQuery = newValue
			m.issueSearchLoading = true
			m.issueSearchCursor = -1
			return m, tea.Batch(cmd, issueSearchCmd(m.ui.WorkDir, newValue, m.issueSearchIncludeClosed))
		}
		if len(newValue) < 2 {
			m.issueSearchResults = nil
			m.issueSearchQuery = ""
			m.issueSearchCursor = -1
		}
		return m, cmd
	}

	if m.globalWorkspacesVisible() && m.overview.RenameShellOpen() && m.overview.RenameShellPaste(msg.Content) {
		return m, nil
	}

	// A focused global filter is a text input and takes the paste, exactly as
	// it takes typed characters.
	if m.globalWorkspacesFilterFocused() && m.overview.WorkspacesPaste(msg.Content) {
		// A paste can change what the filter matches, and therefore what is
		// selected; the preview follows the selection.
		return m, m.overview.WorkspacesPreviewCmd()
	}

	// A pane the global browser is typing into is a real terminal and takes the
	// paste, exactly as it takes typed characters.
	if m.globalWorkspacesVisible() {
		if handled, cmd := m.overview.WorkspacesTerminalPaste(msg.Content); handled {
			return m, cmd
		}
	}

	// A global view that sidecar draws itself owns keyboard focus, so a paste
	// must not reach a hidden project plugin (an interactive tmux pane would
	// run it). The hosted Tasks tab is a real surface and gets its own pastes,
	// routed to the focused surface by forwardKeyToPlugin.
	if m.globalOverlayOwnsKeys() {
		return m, nil
	}

	// No app-level text-input modal active: hand the paste to the active plugin
	// only, exactly as keys are routed. Broadcasting it instead dropped the same
	// text into every background plugin's text input — a paste into a workspace
	// terminal also landed in the Tasks prompt.
	return m.forwardKeyToPlugin(msg)
}

// Update handles all messages and returns the updated model and commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if m.overview != nil && overview.IsAsyncMessage(msg) {
		return m, m.overview.Update(msg)
	}

	switch msg := msg.(type) {
	case tea.FocusMsg:
		m.applicationFocused = true
		return m, m.forwardApplicationFocus(msg)

	case tea.BlurMsg:
		m.applicationFocused = false
		return m, m.forwardApplicationFocus(msg)

	case tea.KeyPressMsg:
		// Input is what geometry arbitration uses to tell two focused instances
		// apart: the machine the user walked away from never blurs (td-ee222a).
		tty.NoteUserInput()
		return (&m).handleKeyMsg(msg)

	case tea.PasteMsg:
		// Pasting is user input like any other; without this a session driven
		// entirely by pastes would look unattended to arbitration (td-ee222a).
		tty.NoteUserInput()
		// v2: bracketed paste arrives as a dedicated message (not a KeyMsg).
		// Route it into the active text-input modal so paste-into-filter works
		// like v1; otherwise forward to plugins (notes editor handles it natively).
		return (&m).handlePaste(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			// Prime worktree cache before first render
			m.refreshWorktreeCache()
		}
		m.ready = true
		// Reset diagnostics modal on resize (will be rebuilt on next render)
		if m.showDiagnostics {
			m.diagnosticsModalWidth = 0
		}
		// Forward adjusted WindowSizeMsg to all plugins
		// Plugins receive the content area size (minus header and footer)
		// Must match the height passed to Plugin.View() in view.go
		adjustedHeight := msg.Height - headerHeight - footerHeight
		adjustedMsg := tea.WindowSizeMsg{
			Width:  msg.Width,
			Height: adjustedHeight,
		}
		plugins := m.registry.Plugins()
		var cmds []tea.Cmd
		for i, p := range plugins {
			newPlugin, cmd := p.Update(adjustedMsg)
			plugins[i] = newPlugin
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// The global Tasks host lays out against the same content box.
		if cmd := m.globalTasks.update(adjustedMsg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// So does the Workspaces browser, whose live pane is sized against the
		// box the new geometry gives it.
		if m.overview != nil {
			if cmd := m.overview.WorkspacesResize(msg.Width, adjustedHeight); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// First real frame: name the terminal after the project.
		if cmd := (&m).syncTerminalTitle(false); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case tea.MouseMsg:
		tty.NoteUserInput()
		// Route mouse events to active modal (priority order)
		switch m.activeModal() {
		case ModalPalette:
			var cmd tea.Cmd
			m.palette, cmd = m.palette.Update(msg)
			return m, cmd
		case ModalHelp:
			return m.handleHelpModalMouse(msg)
		case ModalUpdate:
			return m.handleUpdateModalMouse(msg)
		case ModalDiagnostics:
			return m.handleDiagnosticsModalMouse(msg)
		case ModalQuitConfirm:
			return m.handleQuitConfirmMouse(msg)
		case ModalProjectSwitcher:
			if m.projectAddMode {
				return m.handleProjectAddModalMouse(msg)
			}
			return m.handleProjectSwitcherMouse(msg)
		case ModalWorktreeSwitcher:
			return m.handleWorktreeSwitcherMouse(msg)
		case ModalThemeSwitcher:
			return m.handleThemeSwitcherMouse(msg)
		case ModalOpenIn:
			return m.handleOpenInMouse(msg)
		case ModalIssueInput:
			return m.handleIssueInputMouse(msg)
		case ModalIssuePreview:
			return m.handleIssuePreviewMouse(msg)
		}

		// Handle header tab clicks (Y < 2 means header area)
		mi := msg.Mouse()
		_, isClickPress := msg.(tea.MouseClickMsg)
		if mi.Y < headerHeight && isClickPress && mi.Button == tea.MouseLeft {
			// Brand logo opens the Overview (when the feature is enabled).
			if start, end, ok := m.getLogoBounds(); ok && !m.intro.Active && mi.X >= start && mi.X < end {
				return m, m.toggleOverview()
			}

			if start, end, ok := m.getScopeBounds(); ok && !m.intro.Active && mi.X >= start && mi.X < end {
				return m, m.toggleOverview()
			}

			if start, end, ok := m.getRepoNameBounds(); ok && !m.intro.Active && mi.X >= start && mi.X < end {
				m.showProjectSwitcher = true
				m.activeContext = "project-switcher"
				m.initProjectSwitcher()
				return m, nil
			}

			// Check if click is on worktree indicator
			if start, end, ok := m.getWorktreeIndicatorBounds(); ok && !m.intro.Active && mi.X >= start && mi.X < end {
				worktrees := m.worktreeInventory()
				if len(worktrees) > 1 {
					m.showWorktreeSwitcher = true
					m.activeContext = "worktree-switcher"
					m.initWorktreeSwitcher()
					return m, nil
				}
			}

			// Check if click is on a tab. The bounds carry the typed tab of the
			// scope that painted them, so a click activates that tab and only
			// that tab.
			tabBounds := m.getTabBounds()
			for _, bounds := range tabBounds {
				if mi.X >= bounds.Start && mi.X < bounds.End {
					return m, m.activateTab(bounds.Tab)
				}
			}
			return m, nil
		}

		if m.inGlobalScope() {
			cmd := m.globalMouse(offsetMouseY(msg, -headerHeight))
			m.updateContext()
			return m, cmd
		}

		// Forward mouse events to active plugin with Y offset for app header (2 lines)
		if p := m.ActivePlugin(); p != nil {
			adjusted := offsetMouseY(msg, -headerHeight) // Offset for app header
			newPlugin, cmd := p.Update(adjusted)
			plugins := m.registry.Plugins()
			if m.activePlugin < len(plugins) {
				plugins[m.activePlugin] = newPlugin
			}
			m.updateContext()
			return m, cmd
		}
		return m, nil

	case IntroTickMsg:
		if m.intro.Active {
			m.intro.Update(16 * time.Millisecond)
			// Keep ticking until logo done AND repo name fully faded in
			if !m.intro.Done || m.intro.RepoOpacity < 1.0 {
				return m, IntroTick()
			}
			// All animations complete - mark intro as inactive so header clicks work
			m.intro.Active = false
			return m, Refresh()
		}
		return m, nil

	case TickMsg:
		m.ui.UpdateClock()
		m.ui.ClearExpiredToast()
		m.ClearToast()
		// Eagerly refresh worktree cache (must happen in Update, not View, due to value receiver)
		m.refreshWorktreeCache()
		// Resync the tab title against the freshly refreshed worktree cache, so
		// a branch switched outside sidecar shows up within a second. Every
		// titleResyncTicks the title is re-asserted even when unchanged, to take
		// the tab label back from anything run through tea.ExecProcess.
		m.titleResyncCounter++
		forceTitle := m.titleResyncCounter >= titleResyncTicks
		if forceTitle {
			m.titleResyncCounter = 0
		}
		titleCmd := (&m).syncTerminalTitle(forceTitle)
		// Periodically check if current worktree still exists (every 10 seconds)
		m.worktreeCheckCounter++
		if m.worktreeCheckCounter >= 10 {
			m.worktreeCheckCounter = 0
			return m, tea.Batch(tickCmd(), checkWorktreeExists(m.ui.WorkDir), titleCmd)
		}
		return m, tea.Batch(tickCmd(), titleCmd)

	case worktreeInventoryRefreshedMsg:
		current, _ := normalizePath(m.ui.WorkDir)
		requested, _ := normalizePath(msg.WorkDir)
		if current == requested {
			m.setWorktreeInventory(msg.Inventory, m.ui.WorkDir)
		}
		return m, nil

	case ToastMsg:
		m.ShowToast(msg.Message, msg.Duration)
		m.statusIsError = msg.IsError
		return m, nil

	case RefreshMsg:
		m.ui.MarkRefresh()
		if m.inGlobalScope() {
			if m.globalTasksFocused() {
				return m, m.globalTasks.update(msg)
			}
			return m, (&m).startVisibleGlobalTab()
		}
		// Refresh active plugin
		if p := m.ActivePlugin(); p != nil {
			_, cmd := p.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	case ErrorMsg:
		m.lastError = msg.Err
		m.ShowToast("Error: "+msg.Err.Error(), 5*time.Second)
		return m, nil

	case UpdateBatchReadyMsg:
		return m, m.handleUpdateBatchReady(msg)

	case UpdateTargetResultMsg:
		return m, m.handleUpdateTargetResult(msg)

	case UpdateElapsedTickMsg:
		// Continue timer if update is in progress
		if m.updateInProgress && m.updateModalState == UpdateModalProgress {
			return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
				return UpdateElapsedTickMsg{}
			})
		}
		return m, nil

	case ChangelogLoadedMsg:
		if msg.Err != nil {
			m.updateChangelog = "Failed to load changelog: " + msg.Err.Error()
		} else {
			m.updateChangelog = msg.Content
		}
		m.clearChangelogModal() // Force rebuild with new content
		return m, nil

	case FocusPluginByIDMsg:
		// Switch to requested plugin
		m.leaveOverview(false)
		return m, m.FocusPluginByID(msg.PluginID)

	case overview.OpenInGitMsg:
		return m, m.openInGitFromOverview(msg.Path)

	case openInGitSwitchMsg:
		// Nil inventory, same as navigateFromOverview: resolve ProjectRoot
		// from the target checkout, not the current project's worktree cache.
		pending := plugin.PendingWorkspaceSelection{
			Kind: plugin.WorkspaceSelectionWorktree,
			Key:  msg.Path,
			Path: msg.Path,
		}
		return m, m.switchProjectWithSelection(msg.Path, nil, &pending, false)

	case overview.NavigateMsg:
		if !m.globalCatalogNavigable() || !m.overview.IsCurrentNavigation(msg.Generation, msg.RequestID) {
			return m, nil
		}
		return m, m.overview.Validate(msg)

	case overview.ValidationMsg:
		if !m.globalCatalogNavigable() || !m.overview.ConsumeValidation(msg.Generation, msg.RequestID) {
			return m, nil
		}
		if msg.Err != nil {
			return m, func() tea.Msg {
				return ToastMsg{Message: "Overview item is stale: " + msg.Err.Error(), Duration: 4 * time.Second, IsError: true}
			}
		}
		return m, m.navigateFromOverview(msg.Workspace)

	case SwitchWorktreeMsg:
		// Switch to the requested worktree
		return m, m.switchWorktree(msg.WorktreePath)

	case WorktreeDeletedMsg:
		// Current worktree was deleted (detected by periodic check) - switch to main
		return m, tea.Batch(
			m.switchWorktree(msg.MainPath),
			ShowToast("Worktree deleted, switched to main", 3*time.Second),
		)

	case SwitchToMainWorktreeMsg:
		// Current worktree was deleted (detected by workspace plugin) - switch to main
		if msg.MainWorktreePath != "" && msg.MainWorktreePath != m.ui.WorkDir {
			return m, tea.Batch(
				m.switchProject(msg.MainWorktreePath),
				func() tea.Msg {
					return ToastMsg{
						Message:  "Worktree deleted, switched to main repo",
						Duration: 3 * time.Second,
					}
				},
			)
		}
		return m, nil

	case plugin.OpenFileMsg:
		// Open file in editor using tea.ExecProcess
		// Most editors support +lineNo syntax for opening at a line
		args := []string{}
		if msg.LineNo > 0 {
			args = append(args, fmt.Sprintf("+%d", msg.LineNo))
		}
		args = append(args, msg.Path)
		c := exec.Command(msg.Editor, args...)
		termState, _ := term.GetState(int(os.Stdout.Fd()))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			if termState != nil {
				_ = term.Restore(int(os.Stdout.Fd()), termState)
			}
			return EditorReturnedMsg{Err: err}
		})

	case EditorReturnedMsg:
		// After editor exits, trigger refresh. In v2 mouse mode is declared on
		// tea.View and the renderer re-asserts it on the next frame after
		// tea.ExecProcess returns, so no manual mouse re-enable is needed.
		var cmds []tea.Cmd
		if msg.Err != nil {
			cmds = append(cmds, func() tea.Msg { return ErrorMsg(msg) })
		} else {
			cmds = append(cmds, func() tea.Msg { return RefreshMsg{} })
		}
		// The editor set its own terminal title; take it back now rather than
		// leaving the tab mislabelled until the next forced resync.
		cmds = append(cmds, (&m).syncTerminalTitle(true))
		return m, tea.Batch(cmds...)

	case palette.CommandSelectedMsg:
		// Execute the selected command from the palette
		m.showPalette = false
		m.updateContext()
		// Look up and execute the command
		if cmd, ok := m.keymap.GetCommand(msg.CommandID); ok && cmd.Handler != nil {
			return m, cmd.Handler()
		}
		// Plugins may carry the handler on the command itself rather than
		// registering a keymap command. Prefer an exact context match: one
		// command ID can mean different things in different plugin contexts.
		if handler := m.pluginCommandHandler(msg.CommandID, msg.Context); handler != nil {
			return m, handler()
		}
		// Sidecar's own globals are answered inside handleKeyMsg rather than by a
		// registered keymap handler, so the palette resolves them here. They must
		// not be registered with the keymap instead: findCommand falls back to the
		// global context whenever the focused context's binding has no handler, so
		// a registered global would fire for every context that rebinds its key.
		if (&m).runHostCommand(msg.CommandID) {
			return m, nil
		}
		if cmd := m.runGlobalWorkspacesCommand(msg.CommandID); cmd != nil {
			return m, cmd
		}
		return m, nil

	case version.ProductStatusMsg:
		m.setProductStatus(msg)
		m.clearDiagnosticsModal() // rebuild so the modal picks up new state
		// Never rebuild the preview underneath an open confirmation: the user
		// is deciding about the plan they were shown, and a rebuilt modal has
		// no focus until the next frame.
		if m.updateModalState == UpdateModalClosed {
			m.clearUpdatePreviewModal()
		}
		// Summarize rather than emitting one toast per product: the checks are
		// asynchronous, so per-product toasts would overwrite one another.
		if summary := m.updateToastSummary(); summary != "" && !m.updateInProgress && !m.needsRestart {
			m.ShowToast(summary, 15*time.Second)
		}
		return m, nil

	case IssuePreviewResultMsg:
		m.applyIssuePreviewData(msg.Data, msg.Error)
		return m, nil

	case issueview.LoadedMsg:
		// The modal is one host of issueview. A workspace issue pane is
		// another. Claiming every LoadedMsg here left those panes stuck on
		// "Loading issue…" because the plugin never saw its own result.
		if m.claimIssuePreviewLoad(msg) {
			return m, nil
		}

	case IssueSearchResultMsg:
		// Discard stale results
		if msg.Query != m.issueSearchQuery || !m.showIssueInput {
			return m, nil
		}
		m.issueSearchLoading = false
		if msg.Error == nil {
			m.issueSearchResults = msg.Results
			// Auto-select the sole hit so it is highlighted and Enter is consistent.
			if len(m.issueSearchResults) == 1 {
				m.issueSearchCursor = 0
			}
		}
		m.issueSearchScrollOffset = 0
		m.issueInputModal = nil
		m.issueInputModalWidth = 0
		return m, nil

	case uirequest.RequestMsg:
		if m.uiRequestWatcher != nil {
			cmds = append(cmds, listenForUIRequests(m.uiRequestWatcher.Messages()))
		}
		if m.overview != nil {
			if cmd := m.overview.Update(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	// Unparsed terminal input (CSI u / modifyOtherKeys sequences) is keyboard
	// input in disguise: while a global view sidecar draws itself holds focus,
	// it must not reach a hidden interactive pane, same as a regular key press.
	if m.globalOverlayOwnsKeys() && tty.ExtractUnknownCSIBytes(msg) != nil {
		// Unless a global surface is itself driving a terminal, in which case the
		// sequence is that pane's input and is delivered as the key it encodes.
		if m.globalWorkspacesVisible() {
			if handled, cmd := m.overview.WorkspacesTerminalKeySequence(msg); handled {
				return m, cmd
			}
		}
		return m, nil
	}

	// An embedded terminal's own messages are scope-tagged, so the global
	// Workspaces browser is offered every one of them alongside the plugins:
	// whichever activation owns the scope acts on it and the rest ignore it.
	if m.overview != nil && tty.IsTerminalMessage(msg) {
		if cmd := m.overview.WorkspacesTerminalMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	// Forward other messages to ALL plugins (not just active)
	// This ensures plugin-specific messages (like SessionsLoadedMsg) reach
	// their target plugin even when another plugin is focused
	plugins := m.registry.Plugins()
	for i, p := range plugins {
		newPlugin, cmd := p.Update(msg)
		plugins[i] = newPlugin
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	// The global Tasks host is not in the registry, so it is forwarded here.
	// This is what keeps its file watch, ticks, and agent queue running while
	// any other tab — global or project — is visible.
	if cmd := m.globalTasks.update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.updateContext()

	return m, tea.Batch(cmds...)
}

func (m *Model) forwardApplicationFocus(msg tea.Msg) tea.Cmd {
	// Geometry arbitration is process-wide and must see focus even if no plugin
	// with a control manager is loaded (td-ee222a).
	tty.SetAppFocused(m.applicationFocused)

	var cmds []tea.Cmd
	for _, p := range m.registry.Plugins() {
		_, cmd := p.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if cmd := m.globalTasks.update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// handleKeyMsg processes keyboard input.
func (m *Model) handleKeyMsg(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Close modals with escape (priority order via activeModal)
	if msg.Code == tea.KeyEsc {
		switch m.activeModal() {
		case ModalPalette:
			m.showPalette = false
			m.updateContext()
			return m, nil
		case ModalHelp:
			m.showHelp = false
			m.clearHelpModal()
			return m, nil
		case ModalUpdate:
			// Handle Esc in update modal
			if m.changelogVisible {
				// Close changelog overlay, return to preview
				m.changelogVisible = false
				m.changelogScrollOffset = 0
				m.clearChangelogModal()
				return m, nil
			}
			// Close update modal
			m.updateModalState = UpdateModalClosed
			return m, nil
		case ModalDiagnostics:
			m.showDiagnostics = false
			return m, nil
		case ModalQuitConfirm:
			m.showQuitConfirm = false
			return m, nil
		case ModalProjectSwitcher:
			// If in add mode, Esc exits back to list
			if m.projectAddMode {
				m.resetProjectAdd()
				return m, nil
			}
			// Esc: clear filter if set, otherwise close
			if m.projectSwitcherInput.Value() != "" {
				m.projectSwitcherInput.SetValue("")
				m.projectSwitcherFiltered = m.projectSwitcherDestinations("")
				m.projectSwitcherCursor = 0
				m.projectSwitcherScroll = 0
				return m, nil
			}
			m.resetProjectSwitcher()
			m.updateContext()
			return m, nil
		case ModalWorktreeSwitcher:
			// Esc: clear filter if set, otherwise close
			if m.worktreeSwitcherInput.Value() != "" {
				m.worktreeSwitcherInput.SetValue("")
				m.worktreeSwitcherFiltered = m.worktreeSwitcherAll
				m.worktreeSwitcherCursor = 0
				m.worktreeSwitcherScroll = 0
				return m, nil
			}
			m.resetWorktreeSwitcher()
			m.updateContext()
			return m, nil
		case ModalIssueInput:
			m.resetIssueInput()
			m.updateContext()
			return m, nil
		case ModalIssuePreview:
			if m.issuePreviewView != nil && m.issuePreviewView.Active() {
				m.issuePreviewView.SetActive(false)
				return m, nil
			}
			m.resetIssuePreview()
			m.resetIssueInput()
			m.updateContext()
			return m, nil
		case ModalThemeSwitcher:
			// Esc: clear filter if set, otherwise close (restore original)
			if m.themeSwitcherInput.Value() != "" {
				m.themeSwitcherInput.SetValue("")
				m.themeSwitcherFiltered = buildUnifiedThemeList()
				m.themeSwitcherSelectedIdx = 0
				return m, nil
			}
			m.previewThemeEntry(m.themeSwitcherOriginal)
			m.resetThemeSwitcher()
			m.updateContext()
			return m, nil
		case ModalNone:
			// No modal: Esc leaves the global space and returns to the project
			// plugin underneath — unless the focused global surface wants esc
			// itself. The hosted Tasks tab is a real surface whose overlays,
			// pickers, and prompts close on esc through precedence level 2; this
			// branch runs before that level, so without the guard esc would yank
			// the user out of the global space and leave the overlay open.
			if m.inGlobalScope() && !m.globalSurfaceWantsEsc() {
				return m, m.exitOverview()
			}
		}
	}

	if m.showQuitConfirm {
		action, cmd := m.quitModal.HandleKey(msg)
		switch action {
		case "quit":
			// Save active plugin before quitting
			m.shutdown()
			return m, tea.Quit
		case "cancel":
			m.showQuitConfirm = false
			return m, nil
		}
		return m, cmd
	}

	// Handle update modal keys
	if m.updateModalState != UpdateModalClosed {
		return m.handleUpdateModalKey(msg)
	}

	// The global Workspaces browser answers for its own keys before sidecar's
	// global switch runs. It has to be here rather than beside the Agents board
	// below: while its filter has focus every printable key is query text, and
	// the tab/number/quit switches further down would otherwise take "q", "1",
	// and "`" out of the middle of a search.
	if !m.hasModal() && m.globalWorkspacesVisible() {
		if handled, cmd := m.overview.WorkspacesKey(msg); handled {
			m.updateContext()
			return m, cmd
		}
	}

	// Interactive/inline edit mode: forward ALL keys to plugin including ctrl+c
	// This ensures characters like `, ~, ?, !, @, q, 1-5 reach tmux instead of triggering app shortcuts
	// Ctrl+C is forwarded to tmux (to interrupt running processes) instead of showing quit dialog
	// User can exit interactive mode with Ctrl+\ first, then quit normally
	// An open modal takes keyboard focus away from the pane; the plugin keeps its
	// mode, so focus returns to it when the modal closes.
	// A global view covers the plugin pane and owns keyboard focus, so a plugin
	// left in interactive/text-input mode underneath it must not swallow keys.
	if !m.hasModal() && !m.globalOverlayOwnsKeys() &&
		(m.activeContext == "workspace-interactive" || m.activeContext == "file-browser-inline-edit" || m.activeContext == "notes-inline-edit") {
		// Forward ALL keys to plugin (exit keys and ctrl+c handled by plugin)
		return m.forwardKeyToPlugin(msg)
	}

	// Precedence level 2: the active plugin's text-input or blocking-overlay
	// context. Forward all keys to the plugin except ctrl+c.
	// Uses plugin runtime capability first, then app-level fallback contexts.
	// Skipped while a modal is open so the modal's own input gets the keys.
	if !m.hasModal() && !m.globalOverlayOwnsKeys() && (m.consumesTextInput() || m.pluginBlocksGlobalKeys()) {
		// ctrl+c shows quit confirmation
		if msg.String() == "ctrl+c" {
			if !m.hasModal() {
				m.initQuitModal()
				m.showQuitConfirm = true
			}
			return m, nil
		}
		// Forward everything else to plugin (esc, alt+enter handled by plugin)
		return m.forwardKeyToPlugin(msg)
	}

	// Precedence level 3: an active plugin contextual binding beats sidecar's
	// global bindings. Only plugins that implement plugin.KeyRouter take part,
	// and pluginClaimsKey refuses the host's reserved keys (keymap.HostReservedKeys),
	// so this cannot capture ctrl+c, the quit flow, or merged help.
	if m.pluginClaimsKey(msg.String()) {
		// A user keymap override outranks a plugin claim. Plan §1.4 offers the
		// override as the way to change a claimed mapping ("through Sidecar's
		// keymap override rather than forking the Tasks registry"), and level 3
		// runs before keymap.Handle, so without this the documented escape
		// hatch would be unreachable for exactly the keys that need it.
		//
		// Deliberately scoped to keys a plugin actually claims: consulting
		// overrides for every key here would move them ahead of sidecar's own
		// global switch too, which is a different change to a different level
		// of the ladder.
		if cmd, ok := m.keymap.UserOverride(msg); ok {
			return m, cmd
		}
		return m.forwardKeyToPlugin(msg)
	}

	// Precedence level 4: sidecar global bindings, starting with quit. ctrl+c
	// always takes precedence; 'q' quits from root plugin contexts.
	switch msg.String() {
	case "ctrl+c":
		if !m.hasModal() {
			m.initQuitModal()
			m.showQuitConfirm = true
			return m, nil
		}
	case "q":
		if !m.hasModal() && m.quitKeyExits() {
			m.initQuitModal()
			m.showQuitConfirm = true
			return m, nil
		}
		// Fall through to forward to plugin for navigation (back/escape)
	}

	// Handle palette input when open (Esc handled above)
	if m.showPalette {
		var cmd tea.Cmd
		m.palette, cmd = m.palette.Update(msg)
		return m, cmd
	}

	// Handle diagnostics modal keys
	if m.showDiagnostics {
		m.ensureDiagnosticsModal()
		if m.diagnosticsModal != nil {
			action, cmd := m.diagnosticsModal.HandleKey(msg)
			if cmd != nil {
				return m, cmd
			}
			switch action {
			case "update":
				// Open update modal instead of starting update directly
				if m.hasUpdatesAvailable() && !m.updateInProgress && !m.needsRestart {
					m.openUpdatePreview()
					return m, nil
				}
			}
		}
		// Handle 'u' shortcut for update - open update modal
		if msg.String() == "u" && m.hasUpdatesAvailable() && !m.updateInProgress && !m.needsRestart {
			m.openUpdatePreview()
			return m, nil
		}
		return m, nil
	}

	// Handle worktree switcher modal keys (Esc handled above)
	if m.showWorktreeSwitcher {
		worktrees := m.worktreeSwitcherFiltered

		switch msg.Code {
		case tea.KeyEnter:
			// Select worktree and switch to it
			if m.worktreeSwitcherCursor >= 0 && m.worktreeSwitcherCursor < len(worktrees) {
				selectedPath := worktrees[m.worktreeSwitcherCursor].Path
				m.resetWorktreeSwitcher()
				m.updateContext()
				return m, m.switchWorktree(selectedPath)
			}
			return m, nil

		case tea.KeyUp:
			m.worktreeSwitcherCursor--
			if m.worktreeSwitcherCursor < 0 {
				m.worktreeSwitcherCursor = 0
			}
			m.worktreeSwitcherScroll = worktreeSwitcherEnsureCursorVisible(m.worktreeSwitcherCursor, m.worktreeSwitcherScroll, 8)
			return m, nil

		case tea.KeyDown:
			m.worktreeSwitcherCursor++
			if m.worktreeSwitcherCursor >= len(worktrees) {
				m.worktreeSwitcherCursor = len(worktrees) - 1
			}
			if m.worktreeSwitcherCursor < 0 {
				m.worktreeSwitcherCursor = 0
			}
			m.worktreeSwitcherScroll = worktreeSwitcherEnsureCursorVisible(m.worktreeSwitcherCursor, m.worktreeSwitcherScroll, 8)
			return m, nil
		}

		// Handle non-text shortcuts
		switch msg.String() {
		case "ctrl+n":
			m.worktreeSwitcherCursor++
			if m.worktreeSwitcherCursor >= len(worktrees) {
				m.worktreeSwitcherCursor = len(worktrees) - 1
			}
			if m.worktreeSwitcherCursor < 0 {
				m.worktreeSwitcherCursor = 0
			}
			m.worktreeSwitcherScroll = worktreeSwitcherEnsureCursorVisible(m.worktreeSwitcherCursor, m.worktreeSwitcherScroll, 8)
			return m, nil

		case "ctrl+p":
			m.worktreeSwitcherCursor--
			if m.worktreeSwitcherCursor < 0 {
				m.worktreeSwitcherCursor = 0
			}
			m.worktreeSwitcherScroll = worktreeSwitcherEnsureCursorVisible(m.worktreeSwitcherCursor, m.worktreeSwitcherScroll, 8)
			return m, nil

		case "W":
			// Close modal with same key
			m.resetWorktreeSwitcher()
			m.updateContext()
			return m, nil
		}

		// Filter out unparsed mouse escape sequences
		if isMouseEscapeSequence(msg) {
			return m, nil
		}

		// Forward other keys to text input for filtering
		var cmd tea.Cmd
		m.worktreeSwitcherInput, cmd = m.worktreeSwitcherInput.Update(msg)

		// Re-filter on input change
		m.worktreeSwitcherFiltered = filterWorktrees(m.worktreeSwitcherAll, m.worktreeSwitcherInput.Value())
		m.clearWorktreeSwitcherModal() // Clear modal cache on filter change
		// Reset cursor if it's beyond filtered list
		if m.worktreeSwitcherCursor >= len(m.worktreeSwitcherFiltered) {
			m.worktreeSwitcherCursor = len(m.worktreeSwitcherFiltered) - 1
		}
		if m.worktreeSwitcherCursor < 0 {
			m.worktreeSwitcherCursor = 0
		}
		m.worktreeSwitcherScroll = 0
		m.worktreeSwitcherScroll = worktreeSwitcherEnsureCursorVisible(m.worktreeSwitcherCursor, m.worktreeSwitcherScroll, 8)

		return m, cmd
	}

	// Handle project switcher modal keys (Esc handled above)
	if m.showProjectSwitcher {
		// Handle project add sub-mode keys
		if m.projectAddMode {
			return m.handleProjectAddModalKeys(msg)
		}

		allProjects := m.cfg.Projects.List
		if len(allProjects) == 0 && !m.globalScopeAvailable() {
			// No projects configured - handle y for LLM prompt, ctrl+a for add, close on q/@
			switch msg.String() {
			case "y":
				return m, m.copyProjectSetupPrompt()
			case "ctrl+a":
				m.initProjectAdd()
				return m, nil
			case "q", "@":
				m.resetProjectSwitcher()
				m.updateContext()
			}
			return m, nil
		}

		projects := m.projectSwitcherFiltered

		// The + button takes focus from the filter input via tab or right
		// arrow; enter or space then opens add-project.
		if m.projectSwitcherAddFocused {
			switch msg.String() {
			case "enter", " ", "space":
				m.projectSwitcherAddFocused = false
				m.initProjectAdd()
				return m, nil
			case "tab", "shift+tab", "left", "backtab":
				m.projectSwitcherAddFocused = false
				return m, nil
			case "up", "down", "ctrl+n", "ctrl+p":
				m.projectSwitcherAddFocused = false
				// fall through to the normal handling below
			default:
				// Typing returns to the filter input.
				m.projectSwitcherAddFocused = false
			}
		} else {
			switch msg.String() {
			case "tab":
				m.projectSwitcherAddFocused = true
				return m, nil
			case "right":
				// Only when the caret is already at the end, so right arrow
				// still moves through filter text.
				if m.projectSwitcherInput.Position() >= len(m.projectSwitcherInput.Value()) {
					m.projectSwitcherAddFocused = true
					return m, nil
				}
			}
		}

		switch msg.Code {
		case tea.KeyEnter:
			// Select project and switch to it
			if m.projectSwitcherCursor >= 0 && m.projectSwitcherCursor < len(projects) {
				return m, m.activateProjectSwitcherDestination(projects[m.projectSwitcherCursor])
			}
			return m, nil

		case tea.KeyUp:
			m.projectSwitcherCursor--
			if m.projectSwitcherCursor < 0 {
				m.projectSwitcherCursor = 0
			}
			m.projectSwitcherScroll = projectSwitcherEnsureCursorVisible(m.projectSwitcherCursor, m.projectSwitcherScroll, 8)
			m.previewProjectTheme()
			return m, nil

		case tea.KeyDown:
			m.projectSwitcherCursor++
			if m.projectSwitcherCursor >= len(projects) {
				m.projectSwitcherCursor = len(projects) - 1
			}
			if m.projectSwitcherCursor < 0 {
				m.projectSwitcherCursor = 0
			}
			m.projectSwitcherScroll = projectSwitcherEnsureCursorVisible(m.projectSwitcherCursor, m.projectSwitcherScroll, 8)
			m.previewProjectTheme()
			return m, nil
		}

		// Handle non-text shortcuts
		switch msg.String() {
		case "ctrl+n":
			m.projectSwitcherCursor++
			if m.projectSwitcherCursor >= len(projects) {
				m.projectSwitcherCursor = len(projects) - 1
			}
			if m.projectSwitcherCursor < 0 {
				m.projectSwitcherCursor = 0
			}
			m.projectSwitcherScroll = projectSwitcherEnsureCursorVisible(m.projectSwitcherCursor, m.projectSwitcherScroll, 8)
			m.previewProjectTheme()
			return m, nil

		case "ctrl+p":
			m.projectSwitcherCursor--
			if m.projectSwitcherCursor < 0 {
				m.projectSwitcherCursor = 0
			}
			m.projectSwitcherScroll = projectSwitcherEnsureCursorVisible(m.projectSwitcherCursor, m.projectSwitcherScroll, 8)
			m.previewProjectTheme()
			return m, nil

		case "ctrl+a":
			m.initProjectAdd()
			return m, nil

		case "@":
			// Close modal
			m.resetProjectSwitcher()
			m.updateContext()
			return m, nil
		}

		// Filter out unparsed mouse escape sequences
		if isMouseEscapeSequence(msg) {
			return m, nil
		}

		return m, m.updateProjectSwitcherFilter(msg)
	}

	// Handle theme switcher modal keys (Esc handled above)
	if m.showThemeSwitcher {
		// ctrl+s or left/right toggles scope between global and project
		if m.currentProjectConfig() != nil {
			switch msg.String() {
			case "ctrl+s", "left", "right":
				if m.themeSwitcherScope == "global" {
					m.themeSwitcherScope = "project"
				} else {
					m.themeSwitcherScope = "global"
				}
				return m, nil
			}
		}

		themes := m.themeSwitcherFiltered

		switch msg.Code {
		case tea.KeyEnter:
			// Confirm selection and close (ignore separators)
			if m.themeSwitcherSelectedIdx >= 0 && m.themeSwitcherSelectedIdx < len(themes) && !themes[m.themeSwitcherSelectedIdx].IsSeparator {
				entry := themes[m.themeSwitcherSelectedIdx]
				var tc config.ThemeConfig
				if entry.IsBuiltIn {
					tc = config.ThemeConfig{Name: entry.ThemeKey}
				} else {
					tc = config.ThemeConfig{Name: "default", Community: entry.ThemeKey}
				}
				m.previewThemeEntry(entry)
				return m, m.confirmThemeSelection(tc, entry.Name)
			}
			return m, nil

		case tea.KeyUp:
			m.themeSwitcherSelectedIdx--
			if m.themeSwitcherSelectedIdx < 0 {
				m.themeSwitcherSelectedIdx = 0
			}
			// Skip separators
			for m.themeSwitcherSelectedIdx > 0 && themes[m.themeSwitcherSelectedIdx].IsSeparator {
				m.themeSwitcherSelectedIdx--
			}
			if m.themeSwitcherSelectedIdx < len(themes) && !themes[m.themeSwitcherSelectedIdx].IsSeparator {
				m.previewThemeEntry(themes[m.themeSwitcherSelectedIdx])
			}
			return m, nil

		case tea.KeyDown:
			m.themeSwitcherSelectedIdx++
			if m.themeSwitcherSelectedIdx >= len(themes) {
				m.themeSwitcherSelectedIdx = len(themes) - 1
			}
			if m.themeSwitcherSelectedIdx < 0 {
				m.themeSwitcherSelectedIdx = 0
			}
			// Skip separators
			for m.themeSwitcherSelectedIdx < len(themes)-1 && themes[m.themeSwitcherSelectedIdx].IsSeparator {
				m.themeSwitcherSelectedIdx++
			}
			if m.themeSwitcherSelectedIdx < len(themes) && !themes[m.themeSwitcherSelectedIdx].IsSeparator {
				m.previewThemeEntry(themes[m.themeSwitcherSelectedIdx])
			}
			return m, nil
		}

		// Handle non-text shortcuts
		switch msg.String() {
		case "ctrl+n":
			m.themeSwitcherSelectedIdx++
			if m.themeSwitcherSelectedIdx >= len(themes) {
				m.themeSwitcherSelectedIdx = len(themes) - 1
			}
			if m.themeSwitcherSelectedIdx < 0 {
				m.themeSwitcherSelectedIdx = 0
			}
			for m.themeSwitcherSelectedIdx < len(themes)-1 && themes[m.themeSwitcherSelectedIdx].IsSeparator {
				m.themeSwitcherSelectedIdx++
			}
			if m.themeSwitcherSelectedIdx < len(themes) && !themes[m.themeSwitcherSelectedIdx].IsSeparator {
				m.previewThemeEntry(themes[m.themeSwitcherSelectedIdx])
			}
			return m, nil

		case "ctrl+p":
			m.themeSwitcherSelectedIdx--
			if m.themeSwitcherSelectedIdx < 0 {
				m.themeSwitcherSelectedIdx = 0
			}
			for m.themeSwitcherSelectedIdx > 0 && themes[m.themeSwitcherSelectedIdx].IsSeparator {
				m.themeSwitcherSelectedIdx--
			}
			if m.themeSwitcherSelectedIdx < len(themes) && !themes[m.themeSwitcherSelectedIdx].IsSeparator {
				m.previewThemeEntry(themes[m.themeSwitcherSelectedIdx])
			}
			return m, nil

		case "#":
			// Close modal and restore original
			m.previewThemeEntry(m.themeSwitcherOriginal)
			m.resetThemeSwitcher()
			m.updateContext()
			return m, nil
		}

		// Filter out unparsed mouse escape sequences
		if isMouseEscapeSequence(msg) {
			return m, nil
		}

		// Forward other keys to text input for filtering
		var cmd tea.Cmd
		m.themeSwitcherInput, cmd = m.themeSwitcherInput.Update(msg)

		// Re-filter on input change
		m.themeSwitcherFiltered = filterThemeEntries(buildUnifiedThemeList(), m.themeSwitcherInput.Value())
		m.clearThemeSwitcherModal() // Force modal rebuild
		if m.themeSwitcherSelectedIdx >= len(m.themeSwitcherFiltered) {
			m.themeSwitcherSelectedIdx = len(m.themeSwitcherFiltered) - 1
		}
		if m.themeSwitcherSelectedIdx < 0 {
			m.themeSwitcherSelectedIdx = 0
		}

		// Live preview current selection (skip separators)
		if m.themeSwitcherSelectedIdx >= 0 && m.themeSwitcherSelectedIdx < len(m.themeSwitcherFiltered) && !m.themeSwitcherFiltered[m.themeSwitcherSelectedIdx].IsSeparator {
			m.previewThemeEntry(m.themeSwitcherFiltered[m.themeSwitcherSelectedIdx])
		}

		return m, cmd
	}

	// Handle Open In modal keys (Esc handled above)
	if m.showOpenIn {
		m.ensureOpenInModal()
		if m.openInModal != nil {
			action, cmd := m.openInModal.HandleKey(msg)
			switch action {
			case "cancel":
				m.resetOpenIn()
				m.updateContext()
				return m, nil
			case "select":
				return m, m.confirmOpenIn()
			}
			if cmd != nil {
				return m, cmd
			}
		}
		return m, nil
	}

	// Handle issue input modal keys
	if m.showIssueInput {
		// ctrl+x toggles closed issue visibility (before type switch)
		if msg.String() == "ctrl+x" {
			m.issueSearchIncludeClosed = !m.issueSearchIncludeClosed
			m.issueSearchScrollOffset = 0
			m.issueSearchCursor = -1
			m.issueInputModal = nil
			m.issueInputModalWidth = 0
			if len(strings.TrimSpace(m.issueInputInput.Value())) >= 2 {
				m.issueSearchLoading = true
				return m, issueSearchCmd(m.ui.WorkDir, strings.TrimSpace(m.issueInputInput.Value()), m.issueSearchIncludeClosed)
			}
			return m, nil
		}

		switch msg.Code {
		case tea.KeyEnter:
			return m.issueInputSubmit()
		case tea.KeyUp:
			if len(m.issueSearchResults) > 0 {
				m.issueSearchCursor--
				if m.issueSearchCursor < -1 {
					m.issueSearchCursor = -1
				}
				// Keep cursor visible in viewport
				if m.issueSearchCursor >= 0 && m.issueSearchCursor < m.issueSearchScrollOffset {
					m.issueSearchScrollOffset = m.issueSearchCursor
				}
				m.issueInputModal = nil
				m.issueInputModalWidth = 0
				return m, nil
			}
		case tea.KeyDown:
			if len(m.issueSearchResults) > 0 {
				m.issueSearchCursor++
				if m.issueSearchCursor >= len(m.issueSearchResults) {
					m.issueSearchCursor = len(m.issueSearchResults) - 1
				}
				// Keep cursor visible in viewport
				const maxVisible = 10
				if m.issueSearchCursor >= m.issueSearchScrollOffset+maxVisible {
					m.issueSearchScrollOffset = m.issueSearchCursor - maxVisible + 1
				}
				m.issueInputModal = nil
				m.issueInputModalWidth = 0
				return m, nil
			}
		case tea.KeyTab:
			if m.issueSearchCursor >= 0 && m.issueSearchCursor < len(m.issueSearchResults) {
				m.issueInputInput.SetValue(m.issueSearchResults[m.issueSearchCursor].ID)
				m.issueInputInput.CursorEnd()
				m.issueInputModal = nil
				m.issueInputModalWidth = 0
			}
			// Tab is consumed (fill-in or no-op) — don't forward to textinput
			return m, nil
		}

		if isMouseEscapeSequence(msg) {
			return m, nil
		}

		// Forward key to text input, then clear modal cache so it rebuilds
		var cmd tea.Cmd
		m.issueInputInput, cmd = m.issueInputInput.Update(msg)
		m.issueInputModal = nil
		m.issueInputModalWidth = 0

		// Trigger search if input changed (min 2 chars)
		newValue := strings.TrimSpace(m.issueInputInput.Value())
		if newValue != m.issueSearchQuery && len(newValue) >= 2 {
			m.issueSearchQuery = newValue
			m.issueSearchLoading = true
			// Keep previous results visible while loading to avoid modal shrink/grow flicker.
			// Results are replaced when the new IssueSearchResultMsg arrives.
			m.issueSearchCursor = -1
			return m, tea.Batch(cmd, issueSearchCmd(m.ui.WorkDir, newValue, m.issueSearchIncludeClosed))
		}
		if len(newValue) < 2 {
			m.issueSearchResults = nil
			m.issueSearchQuery = ""
			m.issueSearchCursor = -1
		}
		return m, cmd
	}

	// Handle issue preview modal keys
	if m.showIssuePreview {
		m.ensureIssuePreviewModal()
		if m.issuePreviewModal == nil {
			return m, nil
		}
		view := m.ensureIssuePreviewView()
		key := msg.String()

		// Enter on the card (or before buttons take it) activates rather than
		// firing Open in TD. After that, arrows belong to the epic.
		if key == "enter" && view != nil && !view.Active() &&
			(m.issuePreviewModal.FocusedID() == "" || m.issuePreviewModal.FocusedID() == issueViewFocusID) {
			view.SetActive(true)
			view.SetFocused(true)
			m.issuePreviewModal.SetFocus(issueViewFocusID)
			return m, nil
		}

		if view != nil && view.Active() && m.issuePreviewModal.FocusedID() == issueViewFocusID {
			handled, cmd := view.HandleKey(msg)
			if handled {
				return m, cmd
			}
		}

		// Inactive (or unhandled): j/k/arrows scroll the card, not the modal
		// chrome — the card owns its own viewport.
		if view != nil && !view.Active() {
			switch key {
			case "j", "down":
				view.Scroll(1)
				return m, nil
			case "k", "up":
				view.Scroll(-1)
				return m, nil
			case "ctrl+d":
				view.Scroll(10)
				return m, nil
			case "ctrl+u":
				view.Scroll(-10)
				return m, nil
			case "g":
				view.Scroll(-10000)
				return m, nil
			case "G":
				view.Scroll(10000)
				return m, nil
			}
		}

		switch key {
		case "o":
			if d := m.previewIssueData(); d != nil {
				issueID := d.ID
				m.resetIssuePreview()
				m.resetIssueInput()
				m.updateContext()
				return m, tea.Batch(
					FocusPlugin("td-monitor"),
					func() tea.Msg { return OpenFullIssueMsg{IssueID: issueID} },
				)
			}
		case "b":
			m.backToIssueInput()
			return m, nil
		case "y":
			if d := m.previewIssueData(); d != nil {
				return m, issueview.CopyMarkdown(d)
			}
		case "Y", "shift+y":
			if d := m.previewIssueData(); d != nil {
				return m, issueview.CopyID(d)
			}
		}

		action, cmd := m.issuePreviewModal.HandleKey(msg)
		if key == "tab" || key == "shift+tab" {
			if view != nil && m.issuePreviewModal.FocusedID() != issueViewFocusID {
				view.SetActive(false)
				view.SetFocused(false)
			} else if view != nil {
				view.SetFocused(true)
			}
		}
		switch action {
		case "open-in-td":
			issueID := ""
			if d := m.previewIssueData(); d != nil {
				issueID = d.ID
			}
			m.resetIssuePreview()
			m.resetIssueInput()
			m.updateContext()
			if issueID != "" {
				return m, tea.Batch(
					FocusPlugin("td-monitor"),
					func() tea.Msg { return OpenFullIssueMsg{IssueID: issueID} },
				)
			}
			return m, nil
		case "back":
			m.backToIssueInput()
			return m, nil
		case "cancel":
			m.resetIssuePreview()
			m.resetIssueInput()
			m.updateContext()
			return m, nil
		}
		return m, cmd
	}

	// If any modal is open, don't process plugin/toggle keys
	if m.hasModal() {
		return m, nil
	}

	if m.agentsBoardVisible() && m.overview != nil {
		switch msg.String() {
		case "left", "h", "right", "l", "up", "k", "down", "j", "enter", "r":
			return m, m.overview.Update(msg)
		case "K":
			// Same key that opened it closes it.
			return m, m.toggleOverview()
		}
	}

	// Tab switching. Cycling and the number row move between the tabs of the
	// active scope only: in the global space they step across Agents /
	// Workspaces / Tasks, in project space across the plugin tabs. Neither
	// silently crosses the boundary — K, q, and the brand are the toggle.
	switch msg.String() {
	case "`", "]":
		// Backtick cycles to the next tab (except in text input contexts)
		if m.consumesTextInput() {
			break
		}
		return m, m.cycleTabs(1)
	case "~", "[":
		// Tilde cycles to the previous tab (except in text input contexts)
		if m.consumesTextInput() {
			break
		}
		return m, m.cycleTabs(-1)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// Number keys for direct tab switching.
		// Block in text input contexts (user is typing numbers)
		if m.consumesTextInput() {
			break
		}
		return m, m.selectTabByNumber(int([]rune(msg.Text)[0] - '1'))
	}

	// Toggles
	switch msg.String() {
	case "?":
		m.showPalette = !m.showPalette
		if m.showPalette {
			// Open palette with current context
			pluginCtx := "global"
			if p := m.focusedSurface(); p != nil {
				pluginCtx = p.ID()
			}
			m.palette.SetSize(m.width, m.height)
			// surfacePlugins includes the global Tasks host, so its commands
			// stay reachable now that it is not a registry plugin.
			m.palette.Open(m.keymap, m.surfacePlugins(), m.activeContext, pluginCtx)
			m.activeContext = "palette"
		} else {
			m.updateContext()
		}
		return m, nil
	case "!":
		m.showDiagnostics = !m.showDiagnostics
		if m.showDiagnostics {
			m.activeContext = "diagnostics"
			// Force version check in background (bypasses cache)
			return m, tea.Batch(m.productCheckCmds(true)...)
		}
		m.clearDiagnosticsModal()
		m.updateContext()
		return m, nil
	case "@":
		// Toggle project switcher modal
		m.showProjectSwitcher = !m.showProjectSwitcher
		if m.showProjectSwitcher {
			m.activeContext = "project-switcher"
			m.initProjectSwitcher()
		} else {
			m.resetProjectSwitcher()
			m.updateContext()
		}
		return m, nil
	case "K":
		// Toggle cross-project Overview (Kanban). Blocked in text-input contexts
		// above. Workspace shell delete is D (with confirm); this stays global.
		if m.consumesTextInput() {
			break
		}
		return m, m.toggleOverview()
	case "W":
		// Toggle worktree switcher modal (capital W)
		// Only enable if we're in a git repo with worktrees
		worktrees := m.worktreeInventory()
		if len(worktrees) <= 1 {
			// No worktrees or only main repo - show toast
			return m, func() tea.Msg {
				return ToastMsg{Message: "No worktrees found", Duration: 2 * time.Second}
			}
		}
		m.showWorktreeSwitcher = !m.showWorktreeSwitcher
		if m.showWorktreeSwitcher {
			m.activeContext = "worktree-switcher"
			m.initWorktreeSwitcher()
		} else {
			m.resetWorktreeSwitcher()
			m.updateContext()
		}
		return m, nil
	case "#":
		// Toggle theme switcher modal
		m.showThemeSwitcher = !m.showThemeSwitcher
		if m.showThemeSwitcher {
			m.activeContext = "theme-switcher"
			m.initThemeSwitcher()
		} else {
			m.previewThemeEntry(m.themeSwitcherOriginal)
			m.resetThemeSwitcher()
			m.updateContext()
		}
		return m, nil
	case "^":
		// Toggle Open In modal
		if !m.hasModal() {
			m.showOpenIn = true
			m.activeContext = "open-in"
			m.initOpenIn()
		}
		return m, nil
	case "i":
		// A context that binds "i" for itself answers before the issue modal,
		// or the binding help advertises could never fire. Workspaces no longer
		// takes the key — Enter / E / click start typing — so find-TD-task
		// stays reachable on those lists.
		if _, bound := m.keymap.CommandForContextKey(m.activeContext, "i"); bound {
			break
		}
		if m.openIssueInput() {
			return m, nil
		}
	case "r":
		// Forward 'r' to plugin in contexts where it's used for specific actions
		// or where the user is typing text input
		if !isGlobalRefreshContext(m.activeContext) {
			// Fall through to forward to plugin
			break
		}
		return m, Refresh()
	}

	// Try keymap for context-specific bindings
	if cmd := m.keymap.Handle(msg, m.activeContext); cmd != nil {
		return m, cmd
	}

	// A global view sidecar draws itself covers the plugin pane: unhandled keys
	// stop here instead of reaching a plugin the user cannot see.
	if m.globalOverlayOwnsKeys() {
		return m, nil
	}

	// Precedence level 5: unbound input is forwarded to the active plugin.
	return m.forwardKeyToPlugin(msg)
}

// updateContext sets activeContext based on current state.
// An open modal owns the context; only when none is open does the active
// plugin decide it. Closing a modal therefore restores the plugin's context
// (including "workspace-interactive") on the next call, with no per-modal
// bookkeeping.
func (m *Model) updateContext() {
	if ctx, ok := modalFocusContext(m.activeModal()); ok {
		m.activeContext = ctx
		return
	}
	if m.inGlobalScope() {
		// The visible global tab owns the context. Tasks reports its own, so a
		// Tasks overlay keeps sidecar's globals off its keyboard.
		if host := m.globalTasksPlugin(); m.globalTasksFocused() && host != nil {
			m.activeContext = host.FocusContext()
			return
		}
		if m.globalWorkspacesVisible() && m.overview != nil {
			// Includes the filter, rename prompt, a focused document or
			// issue leaf, and typing. Those contexts are not the list's.
			m.activeContext = m.overview.WorkspaceFocusContext()
			return
		}
		m.activeContext = m.globalTab.context()
		return
	}
	if p := m.ActivePlugin(); p != nil {
		m.activeContext = p.FocusContext()
	} else {
		m.activeContext = "global"
	}
}

// pluginCommandHandler finds a plugin command handler for a palette selection.
// A handler declared for the selected context wins over one declared elsewhere.
func (m *Model) pluginCommandHandler(commandID, context string) func() tea.Cmd {
	var fallback func() tea.Cmd
	for _, p := range m.surfacePlugins() {
		for _, cmd := range p.Commands() {
			if cmd.ID != commandID || cmd.Handler == nil {
				continue
			}
			if cmd.Context == context {
				return cmd.Handler
			}
			if fallback == nil {
				fallback = cmd.Handler
			}
		}
	}
	return fallback
}

// activeKeyRouter returns the active plugin's explicit key-routing capability,
// or nil when it has none.
//
// Nil is the answer for six of the seven plugins today, and that is the point:
// levels 2 (overlay) and 3 (contextual binding) of the precedence order are
// opt-in, so adding them changed nothing for git-status, file-browser,
// conversations, workspace, notes, or td-monitor. Their keys still reach the
// global switch first and fall through to the plugin exactly as before.
func (m *Model) activeKeyRouter() plugin.KeyRouter {
	if m.hasModal() {
		return nil
	}
	p := m.focusedSurface()
	if p == nil {
		return nil
	}
	router, ok := p.(plugin.KeyRouter)
	if !ok {
		return nil
	}
	return router
}

// pluginBlocksGlobalKeys reports that the active plugin has an overlay owning
// the keyboard (precedence level 2).
func (m *Model) pluginBlocksGlobalKeys() bool {
	if m.hasModal() {
		return false
	}
	p := m.focusedSurface()
	blocker, ok := p.(plugin.GlobalKeyBlocker)
	return ok && blocker.BlocksGlobalKeys()
}

// pluginClaimsKey reports that the active plugin has a live contextual binding
// for a key (precedence level 3).
//
// The host refuses keymap.HostReservedKeys here, whatever the router says. A
// plugin filtering them on its own side is welcome defence in depth, but that
// is the plugin's goodwill; this is the guarantee.
func (m *Model) pluginClaimsKey(key string) bool {
	if keymap.HostReservedKeys[key] {
		return false
	}
	router := m.activeKeyRouter()
	return router != nil && router.ClaimsKey(key)
}

// quitKeyExits reports whether `q` should open sidecar's quit flow. A plugin
// that routes its own keys answers for itself; everything else keeps the
// host's context list.
func (m *Model) quitKeyExits() bool {
	if router := m.activeKeyRouter(); router != nil {
		return router.QuitKeyExits()
	}
	return isRootContext(m.activeContext)
}

// forwardKeyToPlugin hands a key to the focused surface (precedence level 5,
// and the delivery mechanism for levels 2 and 3). That is the global Tasks host
// while its tab is visible, and the active project plugin otherwise.
func (m *Model) forwardKeyToPlugin(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.globalTasksFocused() {
		cmd := m.globalTasks.update(msg)
		m.updateContext()
		return m, cmd
	}
	p := m.ActivePlugin()
	if p == nil {
		return m, nil
	}
	newPlugin, cmd := p.Update(msg)
	plugins := m.registry.Plugins()
	if m.activePlugin < len(plugins) {
		plugins[m.activePlugin] = newPlugin
	}
	m.updateContext()
	return m, cmd
}

// consumesTextInput returns true when the active context should treat printable
// keys as text input (block app-level navigation shortcuts).
func (m *Model) consumesTextInput() bool {
	return Model(*m).textInputFocused()
}

// textInputFocused is consumesTextInput for the value receivers — the footer,
// above all, which has to know whether the keys it is about to advertise are
// already spoken for.
func (m Model) textInputFocused() bool {
	// A global view overlays the plugin pane and takes keyboard focus, so a
	// plugin sitting in a text-input mode underneath it does not consume keys.
	// focusedSurface answers nil for exactly that case.
	if p := m.focusedSurface(); p != nil {
		if c, ok := p.(plugin.TextInputConsumer); ok && c.ConsumesTextInput() {
			return true
		}
	}
	return isTextInputContext(m.activeContext)
}

// isRootContext returns true if the context is a root view where 'q' should quit.
// Root contexts are plugin top-level views (not sub-views like detail/diff/commit).
func isRootContext(ctx string) bool {
	switch ctx {
	case "global", "", "overview", "global-workspaces":
		return true
	// Plugin root contexts where 'q' is not used for navigation
	case "conversations", "conversations-sidebar", "conversations-main":
		return true
	case "git-status", "git-status-commits", "git-status-diff", "git-commit-preview":
		return true
	case "file-browser-tree", "file-browser-preview":
		return true
	case "workspace-list", "workspace-preview":
		return true
	case "td-monitor", "td-board":
		return true
	case "notes-list":
		return true
	default:
		return false
	}
}

// isTextInputContext returns true if the context is a text input mode
// where alphanumeric keys should be forwarded to the plugin for typing.
func isTextInputContext(ctx string) bool {
	switch ctx {
	case "td-search", "td-form", "td-board-editor", "td-confirm", "td-close-confirm",
		"theme-switcher",
		"global-workspaces-filter",
		"global-workspaces-rename",
		"issue-input":
		return true
	default:
		return false
	}
}

// isGlobalRefreshContext returns true if 'r' should trigger a global refresh.
// Returns false for contexts where 'r' should be forwarded to the plugin
// (text input modes or plugin-specific 'r' bindings).
func isGlobalRefreshContext(ctx string) bool {
	switch ctx {
	// Global context - 'r' refreshes
	case "global", "":
		return true

	// Git status contexts - 'r' refreshes (no text input, no 'r' binding)
	case "git-status", "git-history", "git-commit-detail", "git-diff":
		return true

	// Conversations list - 'r' refreshes (no text input, no 'r' binding)
	case "conversations", "conversation-detail", "message-detail":
		return true

	// File browser preview - 'r' refreshes (no text input)
	case "file-browser-preview":
		return true

	// Contexts where 'r' should be forwarded to plugin:
	// - td-monitor: 'r' is mark-review
	// - file-browser-tree: 'r' is rename
	// - file-browser-search: text input mode
	// - file-browser-content-search: text input mode
	// - file-browser-quick-open: text input mode
	// - file-browser-file-op: text input mode
	// - conversations-search: text input mode
	// - conversations-filter: text input mode
	// - git-commit: text input mode (commit message)
	// - td-modal: modal view
	// - palette: command palette
	// - diagnostics: diagnostics view
	default:
		return false
	}
}

// handleProjectSwitcherMouse handles mouse events for the project switcher modal.
func (m *Model) handleProjectSwitcherMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.ensureProjectSwitcherModal()
	if m.projectSwitcherModal == nil {
		return m, nil
	}
	if m.projectSwitcherMouseHandler == nil {
		m.projectSwitcherMouseHandler = mouse.NewHandler()
	}

	// Handle scroll wheel for project list navigation
	switch msg.Mouse().Button {
	case tea.MouseWheelUp:
		m.projectSwitcherCursor--
		if m.projectSwitcherCursor < 0 {
			m.projectSwitcherCursor = 0
		}
		m.projectSwitcherScroll = projectSwitcherEnsureCursorVisible(
			m.projectSwitcherCursor, m.projectSwitcherScroll, 8)
		m.clearProjectSwitcherModal()
		m.previewProjectTheme()
		return m, nil
	case tea.MouseWheelDown:
		projects := m.projectSwitcherFiltered
		m.projectSwitcherCursor++
		if m.projectSwitcherCursor >= len(projects) {
			m.projectSwitcherCursor = len(projects) - 1
		}
		if m.projectSwitcherCursor < 0 {
			m.projectSwitcherCursor = 0
		}
		m.projectSwitcherScroll = projectSwitcherEnsureCursorVisible(
			m.projectSwitcherCursor, m.projectSwitcherScroll, 8)
		m.clearProjectSwitcherModal()
		m.previewProjectTheme()
		return m, nil
	}

	action := m.projectSwitcherModal.HandleMouse(msg, m.projectSwitcherMouseHandler)

	// Check if action is a project item click
	if strings.HasPrefix(action, projectSwitcherItemPrefix) {
		// Extract index from item ID
		var idx int
		if _, err := fmt.Sscanf(action, projectSwitcherItemPrefix+"%d", &idx); err == nil {
			projects := m.projectSwitcherFiltered
			if idx >= 0 && idx < len(projects) {
				return m, m.activateProjectSwitcherDestination(projects[idx])
			}
		}
		return m, nil
	}

	switch action {
	case projectSwitcherAddButtonID:
		m.projectSwitcherAddFocused = false
		m.initProjectAdd()
		return m, nil
	case "cancel":
		m.resetProjectSwitcher()
		m.updateContext()
		return m, nil
	case "select":
		projects := m.projectSwitcherFiltered
		if m.projectSwitcherCursor >= 0 && m.projectSwitcherCursor < len(projects) {
			return m, m.activateProjectSwitcherDestination(projects[m.projectSwitcherCursor])
		}
		return m, nil
	}

	return m, nil
}

// handleThemeSwitcherMouse handles mouse events for the theme switcher modal.
func (m *Model) handleThemeSwitcherMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.ensureThemeSwitcherModal()
	if m.themeSwitcherModal == nil {
		return m, nil
	}
	if m.themeSwitcherMouseHandler == nil {
		m.themeSwitcherMouseHandler = mouse.NewHandler()
	}

	action := m.themeSwitcherModal.HandleMouse(msg, m.themeSwitcherMouseHandler)
	switch action {
	case "select":
		themes := m.themeSwitcherFiltered
		if m.themeSwitcherSelectedIdx >= 0 && m.themeSwitcherSelectedIdx < len(themes) {
			entry := themes[m.themeSwitcherSelectedIdx]
			m.previewThemeEntry(entry)
			var tc config.ThemeConfig
			if entry.IsBuiltIn {
				tc = config.ThemeConfig{Name: entry.ThemeKey}
			} else {
				tc = config.ThemeConfig{Name: "default", Community: entry.ThemeKey}
			}
			return m, m.confirmThemeSelection(tc, entry.Name)
		}
	}
	return m, nil
}

// handleQuitConfirmMouse handles mouse events for the quit confirmation modal.
func (m *Model) handleHelpModalMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.ensureHelpModal()
	if m.helpModal == nil {
		return m, nil
	}
	// Info-only modal - no mouse interaction needed beyond ensuring modal exists
	return m, nil
}

// handleUpdateModalKey handles keyboard input for the update modal.
func (m *Model) handleUpdateModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Handle changelog overlay first if visible
	if m.changelogVisible {
		switch key {
		case "j", "down":
			m.changelogScrollOffset++
			m.syncChangelogScroll()
			return m, nil
		case "k", "up":
			if m.changelogScrollOffset > 0 {
				m.changelogScrollOffset--
				m.syncChangelogScroll()
			}
			return m, nil
		case "ctrl+d", "pgdown":
			m.changelogScrollOffset += 10
			m.syncChangelogScroll()
			return m, nil
		case "ctrl+u", "pgup":
			m.changelogScrollOffset -= 10
			if m.changelogScrollOffset < 0 {
				m.changelogScrollOffset = 0
			}
			m.syncChangelogScroll()
			return m, nil
		case "g":
			m.changelogScrollOffset = 0
			m.syncChangelogScroll()
			return m, nil
		case "G":
			m.changelogScrollOffset = 999999 // Will be clamped during render
			m.syncChangelogScroll()
			return m, nil
		case "esc", "c", "q":
			m.changelogVisible = false
			m.changelogScrollOffset = 0
			m.clearChangelogModal()
			return m, nil
		}
		// Route to modal for Enter (close button)
		m.ensureChangelogModal()
		if m.changelogModal != nil {
			action, _ := m.changelogModal.HandleKey(msg)
			if action == "cancel" {
				m.changelogVisible = false
				m.changelogScrollOffset = 0
				m.clearChangelogModal()
				return m, nil
			}
		}
		return m, nil
	}

	// Handle keys based on modal state
	switch m.updateModalState {
	case UpdateModalPreview:
		// Handle special keys first
		switch key {
		case "c":
			// Show changelog
			m.changelogScrollOffset = 0
			if m.updateChangelog == "" {
				m.changelogVisible = true
				return m, fetchChangelog()
			}
			m.changelogVisible = true
			return m, nil
		case "q":
			m.updateModalState = UpdateModalClosed
			return m, nil
		}
		// Route to modal for Tab/Shift+Tab/Enter/Esc
		m.ensureUpdatePreviewModal()
		m.primeUpdateModalFocus()
		if m.updatePreviewModal != nil {
			action, cmd := m.updatePreviewModal.HandleKey(msg)
			switch action {
			case "update":
				return m, m.startUpdateBatch(version.SelectPlan(m.products))
			case "cancel":
				m.updateModalState = UpdateModalClosed
				return m, nil
			}
			if cmd != nil {
				return m, cmd
			}
		}

	case UpdateModalProgress:
		// No keys during progress (except Esc handled earlier)
		return m, nil

	case UpdateModalComplete:
		// Handle 'q' specially for quit
		if key == "q" {
			m.shutdown()
			return m, tea.Quit
		}
		// Route to modal for Tab/Shift+Tab/Enter/Esc
		m.ensureUpdateCompleteModal()
		m.primeUpdateModalFocus()
		if m.updateCompleteModal != nil {
			action, cmd := m.updateCompleteModal.HandleKey(msg)
			switch action {
			case "quit":
				m.shutdown()
				return m, tea.Quit
			case "cancel":
				m.updateModalState = UpdateModalClosed
				return m, nil
			}
			if cmd != nil {
				return m, cmd
			}
		}

	case UpdateModalError:
		// Handle 'r' for retry and 'q' for close
		switch key {
		case "r":
			return m, m.startUpdateBatch(version.RetryTargets(m.updateResults))
		case "q":
			m.updateModalState = UpdateModalClosed
			return m, nil
		}
		// Route to modal for Tab/Shift+Tab/Enter/Esc
		m.ensureUpdateErrorModal()
		m.primeUpdateModalFocus()
		if m.updateErrorModal != nil {
			action, cmd := m.updateErrorModal.HandleKey(msg)
			switch action {
			case "retry":
				return m, m.startUpdateBatch(version.RetryTargets(m.updateResults))
			case "cancel":
				m.updateModalState = UpdateModalClosed
				return m, nil
			}
			if cmd != nil {
				return m, cmd
			}
		}
	}

	return m, nil
}

// handleUpdateModalMouse handles mouse events for the update modal.
func (m *Model) handleUpdateModalMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Handle changelog overlay first if visible
	if m.changelogVisible {
		m.ensureChangelogModal()
		if m.changelogMouseHandler == nil {
			m.changelogMouseHandler = mouse.NewHandler()
		}
		// Handle scroll events via shared state pointer (no modal rebuild needed)
		switch msg.Mouse().Button {
		case tea.MouseWheelUp:
			if m.changelogScrollOffset > 0 {
				m.changelogScrollOffset -= 3
				if m.changelogScrollOffset < 0 {
					m.changelogScrollOffset = 0
				}
				m.syncChangelogScroll()
			}
			return m, nil
		case tea.MouseWheelDown:
			m.changelogScrollOffset += 3
			m.syncChangelogScroll()
			return m, nil
		}
		// Handle modal interaction (close button, backdrop)
		if m.changelogModal != nil {
			action := m.changelogModal.HandleMouse(msg, m.changelogMouseHandler)
			if action == "cancel" {
				m.changelogVisible = false
				m.changelogScrollOffset = 0
				m.clearChangelogModal()
				return m, nil
			}
		}
		return m, nil
	}

	switch m.updateModalState {
	case UpdateModalPreview:
		m.ensureUpdatePreviewModal()
		if m.updatePreviewModal == nil {
			return m, nil
		}
		if m.updatePreviewMouseHandler == nil {
			m.updatePreviewMouseHandler = mouse.NewHandler()
		}
		action := m.updatePreviewModal.HandleMouse(msg, m.updatePreviewMouseHandler)
		switch action {
		case "update":
			return m, m.startUpdateBatch(version.SelectPlan(m.products))
		case "cancel":
			m.updateModalState = UpdateModalClosed
			return m, nil
		}

	case UpdateModalComplete:
		m.ensureUpdateCompleteModal()
		if m.updateCompleteModal == nil {
			return m, nil
		}
		if m.updateCompleteMouseHandler == nil {
			m.updateCompleteMouseHandler = mouse.NewHandler()
		}
		action := m.updateCompleteModal.HandleMouse(msg, m.updateCompleteMouseHandler)
		switch action {
		case "quit":
			m.shutdown()
			return m, tea.Quit
		case "cancel":
			m.updateModalState = UpdateModalClosed
			return m, nil
		}

	case UpdateModalError:
		m.ensureUpdateErrorModal()
		if m.updateErrorModal == nil {
			return m, nil
		}
		if m.updateErrorMouseHandler == nil {
			m.updateErrorMouseHandler = mouse.NewHandler()
		}
		action := m.updateErrorModal.HandleMouse(msg, m.updateErrorMouseHandler)
		switch action {
		case "retry":
			return m, m.startUpdateBatch(version.RetryTargets(m.updateResults))
		case "cancel":
			m.updateModalState = UpdateModalClosed
			return m, nil
		}
	}

	return m, nil
}

func (m *Model) handleQuitConfirmMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	action := m.quitModal.HandleMouse(msg, m.quitMouseHandler)
	switch action {
	case "quit":
		// Save active plugin before quitting
		m.shutdown()
		return m, tea.Quit
	case "cancel":
		m.showQuitConfirm = false
		return m, nil
	}
	return m, nil
}

// handleProjectAddThemePickerKeys handles keys within the theme picker sub-modal.
func (m *Model) handleProjectAddThemePickerKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.projectAddCommunityMode {
		return m.handleProjectAddCommunityKeys(msg)
	}

	maxVisible := 6
	switch msg.String() {
	case "esc":
		m.resetProjectAddThemePicker()
		// Restore theme
		resolved := theme.ResolveTheme(m.cfg, m.ui.WorkDir)
		theme.ApplyResolved(resolved)
		return m, nil

	case "tab":
		// Switch to community themes
		m.projectAddCommunityMode = true
		m.projectAddCommunityList = community.ListSchemes()
		m.projectAddCommunityCursor = 0
		m.projectAddCommunityScroll = 0
		return m, nil

	case "up", "k":
		if m.projectAddThemeCursor > 0 {
			m.projectAddThemeCursor--
			if m.projectAddThemeCursor < m.projectAddThemeScroll {
				m.projectAddThemeScroll = m.projectAddThemeCursor
			}
			m.previewProjectAddTheme()
		}
		return m, nil

	case "down", "j":
		if m.projectAddThemeCursor < len(m.projectAddThemeFiltered)-1 {
			m.projectAddThemeCursor++
			if m.projectAddThemeCursor >= m.projectAddThemeScroll+maxVisible {
				m.projectAddThemeScroll = m.projectAddThemeCursor - maxVisible + 1
			}
			m.previewProjectAddTheme()
		}
		return m, nil

	case "enter":
		if m.projectAddThemeCursor >= 0 && m.projectAddThemeCursor < len(m.projectAddThemeFiltered) {
			if m.projectAdd != nil {
				m.projectAdd.themeSelected = m.projectAddThemeFiltered[m.projectAddThemeCursor]
			}
		}
		m.projectAddModalWidth = 0 // Force modal rebuild to show new theme
		m.resetProjectAddThemePicker()
		// Restore theme
		resolved := theme.ResolveTheme(m.cfg, m.ui.WorkDir)
		theme.ApplyResolved(resolved)
		return m, nil
	}

	// Filter out unparsed mouse escape sequences
	if isMouseEscapeSequence(msg) {
		return m, nil
	}

	// Forward to filter input
	var cmd tea.Cmd
	m.projectAddThemeInput, cmd = m.projectAddThemeInput.Update(msg)
	// Re-filter
	query := m.projectAddThemeInput.Value()
	all := append([]string{"(use global)"}, styles.ListThemes()...)
	if query == "" {
		m.projectAddThemeFiltered = all
	} else {
		var filtered []string
		q := strings.ToLower(query)
		for _, name := range all {
			if strings.Contains(strings.ToLower(name), q) {
				filtered = append(filtered, name)
			}
		}
		m.projectAddThemeFiltered = filtered
	}
	m.projectAddThemeCursor = 0
	m.projectAddThemeScroll = 0
	return m, cmd
}

// handleProjectAddCommunityKeys handles keys in the community sub-browser within add-project.
func (m *Model) handleProjectAddCommunityKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	maxVisible := 6
	switch msg.String() {
	case "esc", "tab":
		// Back to built-in themes
		m.projectAddCommunityMode = false
		// Restore theme
		resolved := theme.ResolveTheme(m.cfg, m.ui.WorkDir)
		theme.ApplyResolved(resolved)
		return m, nil

	case "up", "k":
		if m.projectAddCommunityCursor > 0 {
			m.projectAddCommunityCursor--
			if m.projectAddCommunityCursor < m.projectAddCommunityScroll {
				m.projectAddCommunityScroll = m.projectAddCommunityCursor
			}
			m.previewProjectAddCommunity()
		}
		return m, nil

	case "down", "j":
		if m.projectAddCommunityCursor < len(m.projectAddCommunityList)-1 {
			m.projectAddCommunityCursor++
			if m.projectAddCommunityCursor >= m.projectAddCommunityScroll+maxVisible {
				m.projectAddCommunityScroll = m.projectAddCommunityCursor - maxVisible + 1
			}
			m.previewProjectAddCommunity()
		}
		return m, nil

	case "enter":
		if m.projectAddCommunityCursor >= 0 && m.projectAddCommunityCursor < len(m.projectAddCommunityList) {
			if m.projectAdd != nil {
				m.projectAdd.themeSelected = m.projectAddCommunityList[m.projectAddCommunityCursor]
			}
		}
		m.projectAddModalWidth = 0 // Force modal rebuild to show new theme
		m.resetProjectAddThemePicker()
		// Restore theme
		resolved := theme.ResolveTheme(m.cfg, m.ui.WorkDir)
		theme.ApplyResolved(resolved)
		return m, nil
	}

	return m, nil
}

// resolveIssueOpenID picks which issue ID to open from the issue input modal.
// Priority: selected result (cursor ≥ 0) → sole visible search result when
// cursor is unset → typed value (direct ID open / multi-result fallback).
func resolveIssueOpenID(cursor int, results []IssueSearchResult, typed string) string {
	if cursor >= 0 && cursor < len(results) {
		return results[cursor].ID
	}
	if cursor < 0 && len(results) == 1 {
		return results[0].ID
	}
	return strings.TrimSpace(typed)
}

// issueInputSubmit resolves the current issue input (selected result or typed ID)
// and either opens the full issue in TD monitor or shows a lightweight preview.
func (m *Model) issueInputSubmit() (tea.Model, tea.Cmd) {
	issueID := resolveIssueOpenID(m.issueSearchCursor, m.issueSearchResults, m.issueInputInput.Value())
	if issueID == "" {
		return m, nil
	}
	// Check if active plugin is TD monitor — go directly to rich modal
	if p := m.ActivePlugin(); p != nil && p.ID() == "td-monitor" {
		m.resetIssueInput()
		m.updateContext()
		return m, tea.Batch(
			func() tea.Msg { return OpenFullIssueMsg{IssueID: issueID} },
		)
	}
	// Hide input modal but preserve search state so "back" can restore it.
	m.showIssueInput = false
	// Show lightweight preview
	m.showIssuePreview = true
	m.activeContext = "issue-preview"
	m.issuePreviewLoading = true
	m.issuePreviewData = nil
	m.issuePreviewError = nil
	m.issuePreviewModal = nil
	m.issuePreviewModalWidth = 0
	m.issuePreviewModalHeight = 0
	m.issuePreviewMouseHandler = mouse.NewHandler()
	workDir := ""
	if m.ui != nil {
		workDir = m.ui.WorkDir
	}
	m.issuePreviewView = issueview.New(nil)
	return m, m.issuePreviewView.Load(issuePreviewModelID, workDir, issueID, 0)
}

// handleIssueInputMouse handles mouse events for the issue input modal.
func (m *Model) handleIssueInputMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.ensureIssueInputModal()
	if m.issueInputModal == nil {
		return m, nil
	}
	if m.issueInputMouseHandler == nil {
		m.issueInputMouseHandler = mouse.NewHandler()
	}
	// Pre-render to sync hit regions and focusIDs on the (potentially rebuilt) modal.
	// The issue input modal is nilled on every keystroke to fix a stale text-input
	// pointer, so the modal object seen here may lack focusIDs from a prior Render.
	m.issueInputModal.Render(m.width, m.height, m.issueInputMouseHandler)
	action := m.issueInputModal.HandleMouse(msg, m.issueInputMouseHandler)
	switch {
	case action == "cancel":
		m.resetIssueInput()
		m.updateContext()
	case action == "open":
		return m.issueInputSubmit()
	case strings.HasPrefix(action, issueSearchResultPrefix):
		// Click on a search result — select it and submit
		idxStr := strings.TrimPrefix(action, issueSearchResultPrefix)
		if idx, err := strconv.Atoi(idxStr); err == nil && idx >= 0 && idx < len(m.issueSearchResults) {
			m.issueSearchCursor = idx
			return m.issueInputSubmit()
		}
	}
	return m, nil
}

// handleIssuePreviewMouse handles mouse events for the issue preview modal.
func (m *Model) handleIssuePreviewMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.ensureIssuePreviewModal()
	if m.issuePreviewModal == nil {
		return m, nil
	}
	if m.issuePreviewMouseHandler == nil {
		m.issuePreviewMouseHandler = mouse.NewHandler()
	}
	// Pre-render to sync hit regions and focusIDs on the modal, which may have
	// been rebuilt (e.g. after data/error arrival cleared the cache).
	m.issuePreviewModal.Render(m.width, m.height, m.issuePreviewMouseHandler)

	view := m.ensureIssuePreviewView()
	if view != nil {
		switch ev := msg.(type) {
		case tea.MouseWheelMsg:
			if ev.Button == tea.MouseWheelDown {
				view.Scroll(3)
			} else {
				view.Scroll(-3)
			}
			return m, nil
		}
	}

	action := m.issuePreviewModal.HandleMouse(msg, m.issuePreviewMouseHandler)
	if action == issueViewFocusID && view != nil {
		view.SetActive(true)
		view.SetFocused(true)
		if r := findMouseRegion(m.issuePreviewMouseHandler, issueViewFocusID); r != nil {
			mi := msg.Mouse()
			_, cmd := view.HandleClick(mi.X-r.Rect.X, mi.Y-r.Rect.Y)
			return m, cmd
		}
		return m, nil
	}
	switch action {
	case "cancel":
		m.resetIssuePreview()
		m.resetIssueInput()
		m.updateContext()
	case "back":
		m.backToIssueInput()
		return m, nil
	case "open-in-td":
		issueID := ""
		if d := m.previewIssueData(); d != nil {
			issueID = d.ID
		}
		m.resetIssuePreview()
		m.resetIssueInput()
		m.updateContext()
		if issueID != "" {
			return m, tea.Batch(
				FocusPlugin("td-monitor"),
				func() tea.Msg { return OpenFullIssueMsg{IssueID: issueID} },
			)
		}
	}
	return m, nil
}

func findMouseRegion(h *mouse.Handler, id string) *mouse.Region {
	if h == nil || h.HitMap == nil {
		return nil
	}
	for _, r := range h.HitMap.Regions() {
		if r.ID == id {
			reg := r
			return &reg
		}
	}
	return nil
}
