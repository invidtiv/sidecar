package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	worktreeSwitcherFilterID   = "worktree-switcher-filter"
	worktreeSwitcherItemPrefix = "worktree-switcher-item-"
)

var listWorktreesForSwitcher = GetWorktrees

const localWorktreeNameCachePrefix = "name\x1f"

// worktreeSwitcherRow is one W entry: a local WorktreeInfo or a remote
// Destination. Local display stays branch + [main]; remote display uses
// FormatDestination.
type worktreeSwitcherRow struct {
	Local          WorktreeInfo
	Destination    Destination
	DisabledReason string
}

func (r worktreeSwitcherRow) isRemote() bool {
	return r.Destination.HostID != ""
}

func (r worktreeSwitcherRow) identityKey() string {
	if r.isRemote() {
		return "host\x1f" + r.Destination.HostID + "\x1f" + r.Destination.ProjectKey + "\x1f" + r.Destination.WorktreeKey
	}
	return "local\x1f" + r.Local.Path
}

// worktreeSwitcherItemID returns the ID for a worktree item at the given index.
func worktreeSwitcherItemID(idx int) string {
	return fmt.Sprintf("%s%d", worktreeSwitcherItemPrefix, idx)
}

// initWorktreeSwitcher initializes the worktree switcher modal.
func (m *Model) initWorktreeSwitcher() {
	m.clearWorktreeSwitcherModal()

	ti := textinput.New()
	ti.Placeholder = "Filter worktrees..."
	ti.Focus()
	ti.CharLimit = 50
	ti.SetWidth(40)
	m.worktreeSwitcherInput = ti

	// Reuse the immutable inventory already captured for the current repository.
	// Opening the switcher must not synchronously list worktrees a second time.
	m.worktreeSwitcherAll = m.worktreeSwitcherRows()
	m.worktreeSwitcherFiltered = m.worktreeSwitcherAll
	m.worktreeSwitcherCursor = 0
	m.worktreeSwitcherScroll = 0

	for i, row := range m.worktreeSwitcherFiltered {
		if m.isCurrentWorktreeRow(row) {
			m.worktreeSwitcherCursor = i
			break
		}
	}
}

func (m *Model) openWorktreeSwitcher() tea.Cmd {
	if !m.worktreeSwitcherHasChoices() {
		return ShowFlash("No worktrees found")
	}
	m.showWorktreeSwitcher = true
	m.initWorktreeSwitcher()
	m.activeContext = "worktree-switcher"
	return nil
}

func (m *Model) worktreeSwitcherHasChoices() bool {
	var local, remote int
	for _, row := range m.worktreeSwitcherRows() {
		if row.isRemote() {
			remote++
		} else {
			local++
		}
	}
	if remote > 0 {
		return true
	}
	return local > 1
}

func (m *Model) worktreeSwitcherRows() []worktreeSwitcherRow {
	rows := m.localWorktreeSwitcherRows()
	return append(rows, m.remoteWorktreeSwitcherRows()...)
}

func (m *Model) localWorktreeSwitcherRows() []worktreeSwitcherRow {
	var inventory []WorktreeInfo
	if m.boundDestination.HostID == "" {
		inventory = m.worktreeInventory()
	} else {
		inventory = m.localInventoryForBoundProject()
	}
	if len(inventory) == 0 {
		return nil
	}
	rows := make([]worktreeSwitcherRow, 0, len(inventory))
	for _, wt := range inventory {
		rows = append(rows, worktreeSwitcherRow{Local: wt})
	}
	return rows
}

func (m *Model) localInventoryForBoundProject() []WorktreeInfo {
	if m.localWorktreeCache == nil {
		return nil
	}
	name := m.boundDestination.ProjectName
	if inv := m.localWorktreeCache[localWorktreeNameCachePrefix+strings.ToLower(strings.TrimSpace(name))]; len(inv) > 0 {
		return append([]WorktreeInfo(nil), inv...)
	}
	if m.cfg == nil {
		return nil
	}
	for _, p := range m.cfg.Projects.List {
		if !projectNamesMatch(p.Name, name) {
			continue
		}
		path, _ := normalizePath(p.Path)
		if inv := m.localWorktreeCache[path]; len(inv) > 0 {
			return append([]WorktreeInfo(nil), inv...)
		}
	}
	return nil
}

func (m *Model) remoteWorktreeSwitcherRows() []worktreeSwitcherRow {
	name := m.currentProjectNameForSwitcher()
	if name == "" {
		return nil
	}
	catalog := m.currentHostCatalog()
	if len(catalog) == 0 {
		return nil
	}
	var rows []worktreeSwitcherRow
	for _, entry := range catalog {
		reason := destinationDisabledReason(entry.Health)
		for _, project := range entry.Projects {
			if !projectNamesMatch(project.Name, name) {
				continue
			}
			for _, ws := range project.Workspaces {
				if ws.Kind != workspaceinventory.KindWorktree {
					continue
				}
				key := unscopedWorktreeKey(ws)
				if key == "" {
					continue
				}
				if ws.IsMain || key == project.Key || (project.Root != "" && key == project.Root) {
					continue
				}
				dest := Destination{
					HostID:          entry.ID,
					HostIncarnation: entry.Incarnation,
					ProjectKey:      project.Key,
					ProjectName:     project.Name,
					WorktreeKey:     key,
					WorktreeName:    catalogWorktreeDisplayName(ws),
					Root:            project.Root,
				}
				rows = append(rows, worktreeSwitcherRow{
					Destination:    dest,
					DisabledReason: reason,
				})
			}
		}
	}
	return rows
}

func (m *Model) currentProjectNameForSwitcher() string {
	if m.boundDestination.HostID != "" {
		return m.boundDestination.ProjectName
	}
	if m.cfg != nil && m.ui != nil {
		workDir, _ := normalizePath(m.ui.WorkDir)
		projectRoot, _ := normalizePath(m.ui.ProjectRoot)
		for _, p := range m.cfg.Projects.List {
			path, _ := normalizePath(p.Path)
			if path != "" && (path == workDir || path == projectRoot) {
				return p.Name
			}
		}
	}
	return m.intro.RepoName
}

func unscopedWorktreeKey(ws workspaceinventory.Workspace) string {
	key := ws.Key
	if key == "" {
		key = ws.Path
	}
	if _, rest, ok := hosts.SplitScopedKey(key); ok {
		return rest
	}
	return key
}

func catalogWorktreeDisplayName(ws workspaceinventory.Workspace) string {
	if name := strings.TrimSpace(ws.Branch); name != "" {
		return name
	}
	if name := strings.TrimSpace(ws.Name); name != "" {
		return name
	}
	return filepath.Base(unscopedWorktreeKey(ws))
}

func (m *Model) isCurrentWorktreeRow(row worktreeSwitcherRow) bool {
	if row.isRemote() {
		return m.boundDestination.HostID == row.Destination.HostID &&
			m.boundDestination.ProjectKey == row.Destination.ProjectKey &&
			m.boundDestination.WorktreeKey == row.Destination.WorktreeKey
	}
	if m.boundDestination.HostID != "" {
		return false
	}
	if m.ui == nil {
		return false
	}
	normalizedPath, _ := normalizePath(row.Local.Path)
	normalizedWorkDir, _ := normalizePath(m.ui.WorkDir)
	return normalizedPath != "" && normalizedPath == normalizedWorkDir
}

func (m *Model) refreshOpenWorktreeSwitcher() {
	if !m.showWorktreeSwitcher {
		return
	}
	var highlighted worktreeSwitcherRow
	if m.worktreeSwitcherCursor >= 0 && m.worktreeSwitcherCursor < len(m.worktreeSwitcherFiltered) {
		highlighted = m.worktreeSwitcherFiltered[m.worktreeSwitcherCursor]
	}
	m.worktreeSwitcherAll = m.worktreeSwitcherRows()
	m.worktreeSwitcherFiltered = filterWorktreeRows(m.worktreeSwitcherAll, m.worktreeSwitcherInput.Value())
	m.worktreeSwitcherCursor = indexOfWorktreeRow(m.worktreeSwitcherFiltered, highlighted)
	if m.worktreeSwitcherCursor < 0 {
		m.worktreeSwitcherCursor = 0
	}
	m.worktreeSwitcherScroll = worktreeSwitcherEnsureCursorVisible(m.worktreeSwitcherCursor, m.worktreeSwitcherScroll, 8)
	m.clearWorktreeSwitcherModal()
}

func indexOfWorktreeRow(rows []worktreeSwitcherRow, target worktreeSwitcherRow) int {
	if target.identityKey() == "local\x1f" && !target.isRemote() && target.Local.Path == "" {
		return 0
	}
	key := target.identityKey()
	for i, row := range rows {
		if row.identityKey() == key {
			return i
		}
	}
	return 0
}

func (m *Model) activateWorktreeSwitcherRow(row worktreeSwitcherRow) tea.Cmd {
	if row.DisabledReason != "" {
		return func() tea.Msg {
			return ToastMsg{Message: row.DisabledReason, Duration: 4 * time.Second, IsError: true}
		}
	}
	if row.isRemote() {
		return m.bindRemoteDestination(row.Destination)
	}
	return m.switchWorktree(row.Local.Path)
}

// resetWorktreeSwitcher resets the worktree switcher modal state.
func (m *Model) resetWorktreeSwitcher() {
	m.showWorktreeSwitcher = false
	m.worktreeSwitcherCursor = 0
	m.worktreeSwitcherScroll = 0
	m.worktreeSwitcherFiltered = nil
	m.worktreeSwitcherAll = nil
	m.clearWorktreeSwitcherModal()
}

// clearWorktreeSwitcherModal clears the modal cache. A scrollbar gesture live
// on this modal's handler ends here — closing mid-drag must not leave a dead
// anchor behind (the td-f63097 boundary, from the switcher's side).
func (m *Model) clearWorktreeSwitcherModal() {
	if m.worktreeSwitcherMouseHandler != nil && m.worktreeSwitcherMouseHandler.IsDragging() {
		m.worktreeSwitcherMouseHandler.EndDrag()
	}
	m.worktreeSwitcherBar = switcherBarState{}
	m.worktreeSwitcherModal = nil
	m.worktreeSwitcherModalWidth = 0
	m.worktreeSwitcherMouseHandler = nil
}

// filterWorktrees filters worktrees by branch name or path.
func filterWorktrees(all []WorktreeInfo, query string) []WorktreeInfo {
	if query == "" {
		return all
	}
	q := strings.ToLower(query)
	var matches []WorktreeInfo
	for _, wt := range all {
		if strings.Contains(strings.ToLower(wt.Branch), q) ||
			strings.Contains(strings.ToLower(filepath.Base(wt.Path)), q) {
			matches = append(matches, wt)
		}
	}
	return matches
}

func filterWorktreeRows(all []worktreeSwitcherRow, query string) []worktreeSwitcherRow {
	if query == "" {
		return all
	}
	q := strings.ToLower(query)
	var matches []worktreeSwitcherRow
	for _, row := range all {
		if row.isRemote() {
			if DestinationMatches(row.Destination, query) {
				matches = append(matches, row)
			}
			continue
		}
		wt := row.Local
		if strings.Contains(strings.ToLower(wt.Branch), q) ||
			strings.Contains(strings.ToLower(filepath.Base(wt.Path)), q) {
			matches = append(matches, row)
		}
	}
	return matches
}

// worktreeSwitcherEnsureCursorVisible adjusts scroll to keep cursor in view.
func worktreeSwitcherEnsureCursorVisible(cursor, scroll, maxVisible int) int {
	if cursor < scroll {
		return cursor
	}
	if cursor >= scroll+maxVisible {
		return cursor - maxVisible + 1
	}
	return scroll
}

// ensureWorktreeSwitcherModal builds/rebuilds the worktree switcher modal.
func (m *Model) ensureWorktreeSwitcherModal() {
	modalW := 60
	if modalW > m.width-4 {
		modalW = m.width - 4
	}
	if modalW < 30 {
		modalW = 30
	}

	// Only rebuild if modal doesn't exist or width changed
	if m.worktreeSwitcherModal != nil && m.worktreeSwitcherModalWidth == modalW {
		return
	}
	m.worktreeSwitcherModalWidth = modalW

	m.worktreeSwitcherModal = modal.New("Switch Worktree",
		modal.WithWidth(modalW),
		modal.WithHints(false),
	).
		AddSection(modal.Input(worktreeSwitcherFilterID, &m.worktreeSwitcherInput, modal.WithSubmitOnEnter(false))).
		AddSection(m.worktreeSwitcherCountSection()).
		AddSection(modal.Spacer()).
		AddSection(m.worktreeSwitcherListSection()).
		AddSection(m.worktreeSwitcherHintsSection())
}

// worktreeSwitcherCountSection renders the worktree count.
func (m *Model) worktreeSwitcherCountSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		worktrees := m.worktreeSwitcherFiltered
		allWorktrees := m.worktreeSwitcherAll

		var countText string
		if m.worktreeSwitcherInput.Value() != "" {
			countText = fmt.Sprintf("%d of %d worktrees", len(worktrees), len(allWorktrees))
		} else if len(allWorktrees) > 0 {
			countText = fmt.Sprintf("%d worktrees", len(allWorktrees))
		}
		return modal.RenderedSection{Content: styles.Muted.Render(countText)}
	}, nil)
}

// worktreeSwitcherListSection renders the worktree list with selection and scrollbar.
func (m *Model) worktreeSwitcherListSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		worktrees := m.worktreeSwitcherFiltered

		// No worktrees
		if len(worktrees) == 0 {
			return modal.RenderedSection{Content: styles.Muted.Render("No worktrees found")}
		}

		// Styles
		cursorStyle := lipgloss.NewStyle().Foreground(styles.Primary)
		nameNormalStyle := lipgloss.NewStyle().Foreground(styles.Secondary)
		nameSelectedStyle := lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)
		nameCurrentStyle := lipgloss.NewStyle().Foreground(styles.Success).Bold(true)
		nameCurrentSelectedStyle := lipgloss.NewStyle().Foreground(styles.Success).Bold(true)
		mainBadgeStyle := lipgloss.NewStyle().Foreground(styles.Warning)

		maxVisible := 8
		visibleCount := len(worktrees)
		if visibleCount > maxVisible {
			visibleCount = maxVisible
		}
		scrollOffset := m.worktreeSwitcherScroll

		rowWidth := max(10, contentWidth-2) // Reserve 2 cols for space + scrollbar
		lines := make([]string, 0, visibleCount*2)
		focusables := make([]modal.FocusableInfo, 0, visibleCount)

		for i := 0; i < visibleCount; i++ {
			entryIdx := scrollOffset + i
			row := worktrees[entryIdx]
			isCursor := entryIdx == m.worktreeSwitcherCursor
			itemID := worktreeSwitcherItemID(entryIdx)
			isHovered := itemID == hoverID
			isCurrent := m.isCurrentWorktreeRow(row)
			disabled := row.DisabledReason != ""

			var nameRow strings.Builder
			// Cursor indicator
			if isCursor {
				nameRow.WriteString(cursorStyle.Render("> "))
			} else {
				nameRow.WriteString("  ")
			}

			displayName := row.Local.Branch
			isMain := row.Local.IsMain
			if row.isRemote() {
				displayName = FormatDestination(row.Destination)
				isMain = false
			} else if displayName == "" {
				displayName = filepath.Base(row.Local.Path)
			}

			// Name styling
			var nameStyle lipgloss.Style
			if disabled {
				nameStyle = styles.Muted
			} else if isCurrent {
				if isCursor || isHovered {
					nameStyle = nameCurrentSelectedStyle
				} else {
					nameStyle = nameCurrentStyle
				}
			} else if isCursor || isHovered {
				nameStyle = nameSelectedStyle
			} else {
				nameStyle = nameNormalStyle
			}

			nameRow.WriteString(nameStyle.Render(displayName))

			// Main badge (local rows only)
			if isMain {
				nameRow.WriteString(" ")
				nameRow.WriteString(mainBadgeStyle.Render("[main]"))
			}

			// Current indicator
			if isCurrent {
				nameRow.WriteString(styles.Muted.Render(" (current)"))
			}

			line1 := nameRow.String()
			lineWidth1 := lipgloss.Width(line1)
			if lineWidth1 < rowWidth {
				line1 += strings.Repeat(" ", rowWidth-lineWidth1)
			}
			lines = append(lines, line1)

			pathDisplay := row.Local.Path
			if row.isRemote() {
				if row.DisabledReason != "" {
					pathDisplay = row.DisabledReason
				} else if row.Destination.WorktreeKey != "" {
					pathDisplay = row.Destination.WorktreeKey
				} else {
					pathDisplay = row.Destination.Root
				}
			}
			maxPathLen := rowWidth - 4
			if maxPathLen < 4 {
				maxPathLen = 4
			}
			if len(pathDisplay) > maxPathLen {
				pathDisplay = "..." + pathDisplay[len(pathDisplay)-maxPathLen+3:]
			}
			line2 := styles.Muted.Render("  " + pathDisplay)
			lineWidth2 := lipgloss.Width(line2)
			if lineWidth2 < rowWidth {
				line2 += strings.Repeat(" ", rowWidth-lineWidth2)
			}
			lines = append(lines, line2)

			// Each worktree takes 2 lines (name + path)
			focusables = append(focusables, modal.FocusableInfo{
				ID:      itemID,
				OffsetX: 0,
				OffsetY: i * 2,
				Width:   contentWidth,
				Height:  2,
			})
		}

		barParams := ui.ScrollbarParams{
			TotalItems:   len(worktrees) * 2,
			ScrollOffset: scrollOffset * 2,
			VisibleItems: visibleCount * 2,
			TrackHeight:  visibleCount * 2,
		}
		scrollbar, _ := ui.RenderScrollbarWithState(barParams, m.worktreeSwitcherBar.style(m.worktreeSwitcherMouseHandler))

		bodyContent := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(lines, "\n")+" ", scrollbar)

		// Declaring the bar lets the modal library place its hit regions and
		// route presses/drags back through this section's Update.
		return modal.RenderedSection{
			Content:    bodyContent,
			Focusables: focusables,
			Scrollbar: &modal.SectionScrollbar{
				TotalItems:   barParams.TotalItems,
				ScrollOffset: barParams.ScrollOffset,
				VisibleItems: barParams.VisibleItems,
				TrackHeight:  barParams.TrackHeight,
				LocalX:       rowWidth + 1,
			},
		}
	}, m.worktreeSwitcherListUpdate)
}

// worktreeSwitcherListUpdate handles key events for the worktree list.
// Scrollbar gestures on the declared bar are answered by
// worktreeSwitcherBarEvent in the switcher's mouse handler.
func (m *Model) worktreeSwitcherListUpdate(msg tea.Msg, focusID string) (string, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return "", nil
	}

	worktrees := m.worktreeSwitcherFiltered
	if len(worktrees) == 0 {
		return "", nil
	}

	switch keyMsg.String() {
	case "up", "k", "ctrl+p":
		if m.worktreeSwitcherCursor > 0 {
			m.worktreeSwitcherCursor--
			m.worktreeSwitcherScroll = worktreeSwitcherEnsureCursorVisible(m.worktreeSwitcherCursor, m.worktreeSwitcherScroll, 8)
			m.worktreeSwitcherModalWidth = 0 // Force modal rebuild for scroll
		}
		return "", nil

	case "down", "j", "ctrl+n":
		if m.worktreeSwitcherCursor < len(worktrees)-1 {
			m.worktreeSwitcherCursor++
			m.worktreeSwitcherScroll = worktreeSwitcherEnsureCursorVisible(m.worktreeSwitcherCursor, m.worktreeSwitcherScroll, 8)
			m.worktreeSwitcherModalWidth = 0 // Force modal rebuild for scroll
		}
		return "", nil

	case "enter":
		if m.worktreeSwitcherCursor >= 0 && m.worktreeSwitcherCursor < len(worktrees) {
			return "select", nil
		}
		return "", nil
	}

	return "", nil
}

// worktreeSwitcherHintsSection renders the help text.
func (m *Model) worktreeSwitcherHintsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		worktrees := m.worktreeSwitcherFiltered

		var sb strings.Builder
		sb.WriteString("\n")

		if len(worktrees) == 0 {
			sb.WriteString(styles.KeyHint.Render("esc"))
			sb.WriteString(styles.Muted.Render(" clear filter  "))
			sb.WriteString(styles.KeyHint.Render("W"))
			sb.WriteString(styles.Muted.Render(" close"))
		} else {
			sb.WriteString(styles.KeyHint.Render("enter"))
			sb.WriteString(styles.Muted.Render(" switch  "))
			sb.WriteString(styles.KeyHint.Render("↑/↓"))
			sb.WriteString(styles.Muted.Render(" navigate  "))
			sb.WriteString(styles.KeyHint.Render("esc"))
			sb.WriteString(styles.Muted.Render(" cancel"))
		}

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// renderWorktreeSwitcherModal renders the worktree switcher modal.
func (m *Model) renderWorktreeSwitcherModal(content string) string {
	m.ensureWorktreeSwitcherModal()
	if m.worktreeSwitcherModal == nil {
		return content
	}

	if m.worktreeSwitcherMouseHandler == nil {
		m.worktreeSwitcherMouseHandler = mouse.NewHandler()
	}
	modalContent := m.worktreeSwitcherModal.Render(m.width, m.height, m.worktreeSwitcherMouseHandler)
	return ui.OverlayModal(content, modalContent, m.width, m.height)
}

// handleWorktreeSwitcherMouse handles mouse events for the worktree switcher modal.
func (m *Model) handleWorktreeSwitcherMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.ensureWorktreeSwitcherModal()
	if m.worktreeSwitcherModal == nil {
		return m, nil
	}
	if m.worktreeSwitcherMouseHandler == nil {
		m.worktreeSwitcherMouseHandler = mouse.NewHandler()
	}

	// The list's own scrollbar claims its events before anything else sees
	// them (see modal_scrollbar.go for why this cannot route via the modal).
	if handled, cmd := m.worktreeSwitcherBarEvent(msg); handled {
		return m, cmd
	}

	action := m.worktreeSwitcherModal.HandleMouse(msg, m.worktreeSwitcherMouseHandler)

	// Check if action is a worktree item click
	if strings.HasPrefix(action, worktreeSwitcherItemPrefix) {
		var idx int
		if _, err := fmt.Sscanf(action, worktreeSwitcherItemPrefix+"%d", &idx); err == nil {
			worktrees := m.worktreeSwitcherFiltered
			if idx >= 0 && idx < len(worktrees) {
				selected := worktrees[idx]
				m.resetWorktreeSwitcher()
				m.updateContext()
				return m, m.activateWorktreeSwitcherRow(selected)
			}
		}
		return m, nil
	}

	switch action {
	case "cancel":
		m.resetWorktreeSwitcher()
		m.updateContext()
		return m, nil
	case "select":
		worktrees := m.worktreeSwitcherFiltered
		if m.worktreeSwitcherCursor >= 0 && m.worktreeSwitcherCursor < len(worktrees) {
			selected := worktrees[m.worktreeSwitcherCursor]
			m.resetWorktreeSwitcher()
			m.updateContext()
			return m, m.activateWorktreeSwitcherRow(selected)
		}
		return m, nil
	}

	return m, nil
}

// switchWorktree switches all plugins to a new worktree directory.
func (m *Model) switchWorktree(worktreePath string) tea.Cmd {
	// Skip if already on this worktree
	normalizedPath, _ := normalizePath(worktreePath)
	normalizedWorkDir, _ := normalizePath(m.ui.WorkDir)
	if normalizedPath == normalizedWorkDir {
		// The user is already on it; saying so adds nothing (audit row 10).
		return nil
	}

	// Validate that the worktree still exists before switching
	if !WorktreeExists(worktreePath) {
		return func() tea.Msg {
			return ToastMsg{Message: "Worktree no longer exists", Duration: 3 * time.Second, IsError: true}
		}
	}

	// W is an explicit destination: do not restore LastWorktreePath. After a
	// remote bind WorkDir is empty, so the oldWorkDir coincidence that used to
	// skip restore on local main no longer holds.
	return m.switchProjectWithSelection(worktreePath, append([]WorktreeInfo(nil), m.cachedWorktreeInventory...), nil, false)
}

// refreshWorktreeCache calls GetWorktrees and caches the result for the current WorkDir.
func (m *Model) refreshWorktreeCache() {
	if m.ui == nil || m.ui.WorkDir == "" {
		return
	}
	worktrees := listWorktreesForSwitcher(m.ui.WorkDir)
	m.setWorktreeInventory(worktrees, m.ui.WorkDir)
}

func (m *Model) setWorktreeInventory(worktrees []WorktreeInfo, workDir string) {
	m.cachedWorktreeInventory = append([]WorktreeInfo(nil), worktrees...)
	normalizedWorkDir, _ := normalizePath(workDir)
	m.cachedWorktreeInfo = nil
	if workDir != "" && len(worktrees) > 0 {
		m.rememberLocalWorktreeInventory(worktrees, workDir)
	}
	for i, wt := range m.cachedWorktreeInventory {
		normalizedPath, _ := normalizePath(wt.Path)
		if normalizedPath == normalizedWorkDir {
			m.cachedWorktreeInfo = &m.cachedWorktreeInventory[i]
			return
		}
	}
}

func (m *Model) rememberLocalWorktreeInventory(worktrees []WorktreeInfo, workDir string) {
	if workDir == "" || len(worktrees) == 0 {
		return
	}
	if m.localWorktreeCache == nil {
		m.localWorktreeCache = make(map[string][]WorktreeInfo)
	}
	copy := append([]WorktreeInfo(nil), worktrees...)
	main := mainWorktreePath(worktrees)
	if main == "" {
		main = workDir
	}
	if normalized, err := normalizePath(main); err == nil && normalized != "" {
		m.localWorktreeCache[normalized] = copy
	}
	if name := m.localProjectNameForPath(workDir, main); name != "" {
		m.localWorktreeCache[localWorktreeNameCachePrefix+strings.ToLower(strings.TrimSpace(name))] = copy
	}
}

func (m *Model) localProjectNameForPath(workDir, main string) string {
	if m.cfg != nil {
		wd, _ := normalizePath(workDir)
		mn, _ := normalizePath(main)
		for _, p := range m.cfg.Projects.List {
			path, _ := normalizePath(p.Path)
			if path != "" && (path == wd || path == mn) {
				return p.Name
			}
		}
	}
	return m.intro.RepoName
}

func (m *Model) worktreeInventory() []WorktreeInfo {
	if len(m.cachedWorktreeInventory) == 0 {
		if m.ui == nil || m.ui.WorkDir == "" {
			return nil
		}
		m.refreshWorktreeCache()
	}
	return append([]WorktreeInfo(nil), m.cachedWorktreeInventory...)
}

func mainWorktreePath(inventory []WorktreeInfo) string {
	for _, wt := range inventory {
		if wt.IsMain {
			return wt.Path
		}
	}
	return ""
}

type worktreeInventoryRefreshedMsg struct {
	WorkDir   string
	Inventory []WorktreeInfo
}

func refreshWorktreeInventoryCmd(workDir string) tea.Cmd {
	return func() tea.Msg {
		return worktreeInventoryRefreshedMsg{WorkDir: workDir, Inventory: listWorktreesForSwitcher(workDir)}
	}
}

// currentWorktreeInfo returns the cached WorktreeInfo for the current WorkDir, or nil.
// Cache is populated eagerly in Update() (TickMsg, switchProject) — never in View().
func (m *Model) currentWorktreeInfo() *WorktreeInfo {
	return m.cachedWorktreeInfo
}
