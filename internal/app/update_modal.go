package app

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/scroll"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/version"
)

// updateModalWidthFor derives the one width for the whole journey from the
// library's own sizing: a content-sized box that leaves the roomy margin ring
// on each side, capped at the library maximum. Responsive within the
// library's clamps — no fixed constant, no per-size caching.
func updateModalWidthFor(screenW int) int {
	return min(modal.ContentBoxWidth(screenW), modal.MaxModalWidth)
}

// updateNotesToggleID and updateChangelogRetryID are the notes section's
// focusable affordances.
const (
	updateNotesToggleID    = "update-notes-toggle"
	updateChangelogRetryID = "update-changelog-retry"
)

// notesCollapsedRows is the notes window before expansion.
const notesCollapsedRows = 6

// updateUIState is the heap-allocated snapshot the single update modal's
// section closures read. The model is copied through bubbletea updates, so a
// closure that captured the model would go stale after the first Update;
// closures capture this pointer instead, and syncUpdateUI refreshes its
// contents wherever the app is about to touch the modal.
type updateUIState struct {
	phase     UpdateModalState
	plan      []version.Target
	results   []version.Result
	settled   []version.Result
	activeIdx int
	running   bool
	start     time.Time

	products []version.Target
	// notesTarget is whose release notes the section shows: the first planned
	// product in display order (Sidecar, td, Tasks).
	notesTarget     version.Target
	anyManaged      bool
	restartRequired bool
	retryCount      int
	retryBatch      bool

	// Notes-section view state. notesScroll/notesExpanded are written by
	// gestures, keys, and the toggle action; notesTotal/notesVisible/
	// notesPresent are refreshed by the section's render so read-only wheel-
	// boundary queries can answer from the geometry that is on screen.
	notesScroll   int
	notesExpanded bool
	notesTotal    int
	notesVisible  int
	notesPresent  bool

	// Full-changelog expansion fetch, keyed by the offered release's tag.
	// Lives on this shared struct because the modal's section closures are
	// bound to whichever model copy built them; per-copy fields would go
	// stale after the next Update.
	changelogState   updateChangelogState
	changelogBody    string
	changelogErr     error
	changelogTag     string
	changelogProduct version.ProductID

	// presentedPhase records which phase the modal's presentation currently
	// reflects so re-presenting is skipped between phase changes. It lives on
	// the shared heap struct because separate model copies cannot see each
	// other's writes.
	presentedPhase UpdateModalState
	presentedValid bool
	presentedWidth int
}

func (u *updateUIState) hasNotesTarget() bool {
	return u != nil && u.notesTarget.Product != ""
}

func (u *updateUIState) includesTasks() bool {
	for _, t := range version.SelectPlan(u.products) {
		if t.Product == version.ProductTasks {
			return true
		}
	}
	return false
}

// syncUpdateUI copies the live update-flow state into the snapshot the modal
// reads. Called from every path that renders or feeds input to the modal.
func (m *Model) syncUpdateUI() {
	if m.updateUI == nil {
		m.updateUI = &updateUIState{}
	}
	u := m.updateUI
	u.phase = m.updateModalState
	u.plan = m.updatePlan
	u.results = m.updateResults
	u.settled = m.settledResults()
	u.activeIdx = m.updateActiveIdx
	u.running = m.updateInProgress
	u.start = m.updateStartTime
	u.products = m.products
	if plan := version.SelectPlan(u.products); len(plan) > 0 {
		u.notesTarget = plan[0]
		for _, t := range plan {
			if strings.TrimSpace(t.Notes) != "" {
				u.notesTarget = t
				break
			}
		}
	} else if len(u.products) > 0 {
		u.notesTarget = u.products[0]
	}
	if u.changelogProduct != u.notesTarget.Product {
		u.changelogProduct = u.notesTarget.Product
		u.changelogState = changelogIdle
		u.changelogBody = ""
		u.changelogErr = nil
		u.changelogTag = ""
	}
	u.anyManaged = false
	for _, t := range version.SelectPlan(u.products) {
		if t.Install.Managed {
			u.anyManaged = true
		}
	}
	u.restartRequired = version.RestartRequired(u.settled)
	u.retryCount = len(version.RetryTargets(u.settled))
	u.retryBatch = m.updateBatchRetry
}

// ensureUpdateModal builds the single update modal once and keeps its
// presentation in step with the current phase. Sections are When-gated on the
// phase and read the shared UI state, so the same modal object, mouse
// handler, and focus list carry across Overview → Installing → Done/Failed.
func (m *Model) ensureUpdateModal() {
	m.syncUpdateUI()
	if m.updateModal != nil {
		m.applyUpdatePresentation()
		return
	}

	u := m.updateUI
	inPhase := func(p UpdateModalState) func() bool {
		return func() bool { return u.phase == p }
	}

	mdl := modal.New(updateTitle(u.phase),
		modal.WithWidth(updateModalWidthFor(m.width)),
		modal.WithVariant(updateVariant(u.phase)),
		modal.WithHints(false),
		modal.WithPrimaryAction(updatePrimaryAction(m.updateUIState())),
	)

	mdl.AddSection(modal.When(inPhase(UpdateModalPreview), modal.Custom(func(cw int, _, _ string) modal.RenderedSection {
		return modal.RenderedSection{Content: updateOverviewContent(u, cw)}
	}, nil)))

	mdl.AddSection(modal.When(inPhase(UpdateModalPreview), m.updateNotesSection()))
	mdl.AddSection(modal.When(inPhase(UpdateModalPreview), m.updateNotesToggleSection()))

	mdl.AddSection(modal.When(inPhase(UpdateModalProgress), modal.Custom(func(cw int, _, _ string) modal.RenderedSection {
		return modal.RenderedSection{Content: updateProgressContent(u, cw)}
	}, nil)))

	mdl.AddSection(modal.When(inPhase(UpdateModalComplete), modal.Custom(func(cw int, _, _ string) modal.RenderedSection {
		return modal.RenderedSection{Content: updateResultContent(u, cw)}
	}, nil)))

	mdl.AddSection(modal.When(inPhase(UpdateModalError), modal.Custom(func(cw int, _, _ string) modal.RenderedSection {
		return modal.RenderedSection{Content: updateErrorContent(u, cw)}
	}, nil)))

	mdl.AddSection(modal.Spacer())
	mdl.AddSection(modal.Custom(func(cw int, focusID, hoverID string) modal.RenderedSection {
		return m.renderUpdateChips(cw, focusID, hoverID)
	}, m.updateChipsSectionUpdate))

	m.updateModal = mdl
	if m.updateMouseHandler == nil {
		m.updateMouseHandler = mouse.NewHandler()
	}
	m.applyUpdatePresentation()
}

// applyUpdatePresentation restates what changes between phases — and on
// resize — on the one persistent modal: title, border variant, hint line,
// primary action, and width. Skipped while nothing changed: Apply invalidates
// layout, and ensure runs from View as well as Update on every frame.
func (m *Model) applyUpdatePresentation() {
	width := updateModalWidthFor(m.width)
	if u := m.updateUI; u != nil && u.presentedValid &&
		u.presentedPhase == m.updateModalState && u.presentedWidth == width {
		return
	}
	m.updateModal.Apply(
		modal.WithTitle(updateTitle(m.updateModalState)),
		modal.WithVariant(updateVariant(m.updateModalState)),
		modal.WithPrimaryAction(updatePrimaryAction(m.updateUIState())),
		modal.WithWidth(width),
	)
	if u := m.updateUI; u != nil {
		u.presentedPhase = m.updateModalState
		u.presentedWidth = width
		u.presentedValid = true
	}
}

func (m *Model) updateUIState() *updateUIState { return m.updateUI }

func updateTitle(p UpdateModalState) string {
	switch p {
	case UpdateModalProgress:
		return "Updating…"
	case UpdateModalComplete:
		return "Update complete"
	case UpdateModalError:
		return "Update incomplete"
	default:
		return "Update available"
	}
}

func updateVariant(p UpdateModalState) modal.Variant {
	switch p {
	case UpdateModalProgress:
		return modal.VariantWarning
	case UpdateModalComplete:
		return modal.VariantInfo
	case UpdateModalError:
		return modal.VariantDanger
	default:
		return modal.VariantDefault
	}
}

// updateChips is every phase's one inline action line: key chips in exactly
// the footer hint style. Preview confirms with enter or u and dismisses with
// esc; Installing keeps the honest no-cancel line; Complete/Error pair their
// primary with Close.
func updateChips(u *updateUIState) ([]ui.KeyChip, string) {
	switch u.phase {
	case UpdateModalProgress:
		return []ui.KeyChip{{Keys: "[esc]", Label: "hide", ID: "cancel"}},
			" · update continues"
	case UpdateModalComplete:
		if u.restartRequired {
			return []ui.KeyChip{
				{Keys: "[enter]", Label: "Quit & Restart", ID: "quit"},
				{Keys: "[esc]", Label: "Close", ID: "cancel"},
			}, ""
		}
		return []ui.KeyChip{{Keys: "[esc]", Label: "Close", ID: "cancel"}}, ""
	case UpdateModalError:
		if u.retryCount == 0 {
			return []ui.KeyChip{{Keys: "[esc]", Label: "Close", ID: "cancel"}}, ""
		}
		return []ui.KeyChip{
			{Keys: "[enter]", Label: "Retry", ID: "retry"},
			{Keys: "[esc]", Label: "Close", ID: "cancel"},
		}, ""
	default:
		if !u.anyManaged {
			return []ui.KeyChip{{Keys: "[esc]", Label: "Close", ID: "cancel"}}, ""
		}
		return []ui.KeyChip{
			{Keys: "[enter/u]", Label: "Update", ID: "update"},
			{Keys: "[esc]", Label: "Close", ID: "cancel"},
		}, ""
	}
}

// renderUpdateChips paints the action line, registering each chip as a real
// focusable control so a click and Enter both fire its action.
func (m *Model) renderUpdateChips(contentW int, focusID, hoverID string) modal.RenderedSection {
	u := m.updateUIState()
	chips, suffix := updateChips(u)
	line, regions := ui.RenderKeyChips(chips, contentW)
	if line == "" {
		return modal.RenderedSection{}
	}
	content := line
	if suffix != "" {
		content += styles.Muted.Render(suffix)
	}
	focusables := make([]modal.FocusableInfo, 0, len(regions))
	for _, r := range regions {
		focusables = append(focusables, modal.FocusableInfo{
			ID:      r.ID,
			OffsetX: r.OffsetX,
			Width:   r.Width,
			Height:  1,
		})
	}
	return modal.RenderedSection{Content: content, Focusables: focusables}
}

// updateChipsSectionUpdate fires a focused chip's action on Enter or space.
func (m *Model) updateChipsSectionUpdate(msg tea.Msg, focusID string) (string, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || focusID == "" {
		return "", nil
	}
	switch key.String() {
	case "enter", " ", "space":
		return focusID, nil
	}
	return "", nil
}

func updatePrimaryAction(u *updateUIState) string {
	switch u.phase {
	case UpdateModalPreview:
		return "update"
	case UpdateModalError:
		return "retry"
	case UpdateModalComplete:
		if u.restartRequired {
			return "quit"
		}
		return "cancel"
	default:
		return ""
	}
}

// renderUpdateModalOverlay overlays the single update modal on the background.
func (m *Model) renderUpdateModalOverlay(background string) string {
	if m.updateModalState == UpdateModalClosed {
		return background
	}
	m.ensureUpdateModal()
	rendered := m.updateModal.Render(m.width, m.height, m.updateMouseHandler)
	return ui.OverlayModal(background, rendered, m.width, m.height)
}

// twoColumnRow renders primary-left / secondary-right on one line with the
// secondary flush right — modal-redesign.md's column behaviour. When the two
// no longer fit, the left side truncates before the right is sacrificed;
// below the minimum legible left width the row degrades to stacked lines so
// the primary value is never hard-clipped.
func twoColumnRow(left, right string, width int) string {
	const gap = 2
	const minLeft = 12
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	switch {
	case lw+gap+rw <= width:
		return left + strings.Repeat(" ", width-lw-rw) + right
	case width-rw-gap >= minLeft:
		trimmed := ansi.Truncate(left, width-rw-gap, "…")
		return trimmed + strings.Repeat(" ", width-lipgloss.Width(trimmed)-rw) + right
	default:
		return left + "\n" + strings.Repeat(" ", max(0, width-rw)) + right
	}
}

// updateOverviewContent renders the confirmation view: what changes and the
// Tasks note. Release notes live in their own scrollable section beside it.
func updateOverviewContent(u *updateUIState, contentW int) string {
	plan := version.SelectPlan(u.products)
	if len(plan) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(styles.Muted.Render("Updating will change:") + "\n")
	rows := make([]string, 0, len(plan))
	for _, t := range plan {
		rows = append(rows, targetRow(t, contentW))
	}
	b.WriteString(strings.Join(rows, "\n"))

	if u.includesTasks() {
		b.WriteString("\n\n" + styles.Muted.Render(
			"Tasks here is the standalone tasks/tasks-tui/tasks-api commands.\nSidecar's embedded Tasks tab updates with Sidecar itself."))
	}
	return b.String()
}

// targetRow renders one product row: what changes, from which version to
// which, and how Sidecar would do it.
func targetRow(t version.Target, width int) string {
	left := fmt.Sprintf("%s %s%s%s", t.DisplayName, t.CurrentVersion,
		lipgloss.NewStyle().Foreground(styles.Success).Render(" → "), t.LatestVersion)
	right := styles.Muted.Render(t.Install.Method.String())
	if !t.Install.Managed {
		right = styles.Muted.Render("manual")
	}
	row := twoColumnRow(left, right, width)
	if !t.Install.Managed && t.Install.ManualCommand != "" {
		// Blank padding keeps every target row two lines tall regardless of
		// provenance, so columns line up down the list.
		row += "\n"
	}
	return row
}

// updateProgressContent shows which product is being changed right now, plus
// the settled rows for targets that already finished.
func updateProgressContent(u *updateUIState, contentW int) string {
	muted := lipgloss.NewStyle().Foreground(styles.TextMuted)
	rows := make([]string, 0, len(u.plan))
	for i, t := range u.plan {
		switch {
		case i < len(u.results):
			r := u.results[i]
			rows = append(rows, twoColumnRow(
				resultIcon(r.Status)+" "+t.DisplayName,
				muted.Render(resultLabel(r)), contentW))
		case i == u.activeIdx:
			action := "installing " + t.LatestVersion
			if t.Install.Managed {
				action += " via " + t.Install.Method.String()
			}
			rows = append(rows, twoColumnRow(
				lipgloss.NewStyle().Foreground(styles.Warning).Render("●")+" "+
					lipgloss.NewStyle().Bold(true).Render(t.DisplayName),
				muted.Render(action), contentW))
		default:
			rows = append(rows, twoColumnRow(muted.Render("○ "+t.DisplayName), "", contentW))
		}
	}
	return strings.Join(rows, "\n") + "\n\n" + muted.Render(
		fmt.Sprintf("Elapsed: %s", formatElapsed(time.Since(u.start))))
}

// updateResultContent renders the settled results of a fully successful batch.
// It reads the whole settled set, not just this batch: after retrying one
// product, an upgrade that succeeded earlier is still part of the outcome.
func updateResultContent(u *updateUIState, contentW int) string {
	rows := make([]string, 0, len(u.settled))
	for _, r := range u.settled {
		row := twoColumnRow(
			resultIcon(r.Status)+" "+r.Target.DisplayName,
			styles.Muted.Render(resultLabel(r)), contentW)
		if r.Status == version.StatusManual && r.Target.Install.ManualCommand != "" {
			row += "\n"
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		rows = append(rows, styles.Muted.Render("Nothing to update."))
	}
	var b strings.Builder
	b.WriteString(strings.Join(rows, "\n"))
	if u.restartRequired {
		b.WriteString("\n\n" + styles.Muted.Render("Restart sidecar to use the new version."))
	} else {
		b.WriteString("\n\n" + styles.Muted.Render("Sidecar itself did not change; no restart needed."))
	}
	return b.String()
}

// updateErrorContent renders a batch that settled with failures. Earlier
// successes are retained and each failed target gets its own manual command.
func updateErrorContent(u *updateUIState, contentW int) string {
	errorStyle := lipgloss.NewStyle().Foreground(styles.Error)
	var b strings.Builder
	var rows []string
	for _, r := range u.settled {
		row := twoColumnRow(
			resultIcon(r.Status)+" "+r.Target.DisplayName,
			styles.Muted.Render(resultLabel(r)), contentW)
		if r.Status == version.StatusFailed {
			for _, detail := range version.FailureDetail(r, 6) {
				for _, wrapped := range wrapDetailLine(detail, contentW-4) {
					row += "\n" + errorStyle.Render("    "+wrapped)
				}
			}
			if cmd := r.Target.Install.ManualCommand; cmd != "" {
				row += "\n" + styles.Muted.Render("    manual fix: "+cmd)
			}
		}
		if r.Status == version.StatusManual && r.Target.Install.ManualCommand != "" {
			row += "\n"
		}
		rows = append(rows, row)
	}
	b.WriteString(strings.Join(rows, "\n"))
	b.WriteString("\n\n" + styles.Muted.Render("Retry runs only the failed products.") +
		"\n" + styles.Muted.Render("Report: github.com/marcus/sidecar/issues"))
	return b.String()
}

// closeUpdateModal dismisses the overlay. During an install this hides the
// modal while the batch keeps running; results the user has not seen yet stay
// unacknowledged so reopening lands on the Done/Failed phase.
func (m *Model) closeUpdateModal() {
	switch m.updateModalState {
	case UpdateModalComplete, UpdateModalError:
		m.updateResultsAcked = true
	}
	m.updateModalState = UpdateModalClosed
	m.ensureUpdateModal()
}

// openUpdateModal converges every updater entry point onto one rule: open the
// modal in whatever phase the flow is actually in. A batch in flight reopens
// as Installing; settled results reopen as Done/Failed until the user has
// seen them; once acknowledged, they yield to a fresh confirmation whenever
// an update is still pending — a dismissed failure must never lock Retry
// away. It reports false only when there is genuinely nothing to show.
func (m *Model) openUpdateModal() bool {
	m.showDiagnostics = false
	switch {
	case m.updateInProgress:
		m.updateModalState = UpdateModalProgress
	case !m.updateResultsAcked && len(m.settledResults()) > 0:
		if len(version.RetryTargets(m.settledResults())) > 0 {
			m.updateModalState = UpdateModalError
		} else {
			m.updateModalState = UpdateModalComplete
		}
	case m.hasUpdatesAvailable() && !m.needsRestart:
		m.updateModalState = UpdateModalPreview
	default:
		return false
	}
	m.ensureUpdateModal()
	return true
}

// applyUpdateAction routes an action from the modal's key or mouse handling.
// Actions can only arrive from elements rendered by their own phase, so no
// per-phase switch lives here.
func (m *Model) applyUpdateAction(action string, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	switch action {
	case "update":
		m.updateBatchRetry = false
		return m, m.startUpdateBatch(version.SelectPlan(m.products))
	case "retry":
		m.updateBatchRetry = true
		return m, m.startUpdateBatch(version.RetryTargets(m.settledResults()))
	case "quit":
		m.shutdown()
		return m, tea.Quit
	case "cancel", "close":
		m.closeUpdateModal()
		return m, nil
	case "toggle-notes", updateNotesToggleID:
		var fetch tea.Cmd
		if u := m.updateUIState(); u != nil {
			u.notesExpanded = !u.notesExpanded
			u.notesScroll = 0
			if u.notesExpanded && u.changelogState == changelogIdle && u.hasNotesTarget() {
				fetch = m.fetchChangelogCmd()
			}
		}
		return m, fetch
	case "retry-changelog", updateChangelogRetryID:
		if u := m.updateUIState(); u != nil && u.hasNotesTarget() {
			return m, m.fetchChangelogCmd()
		}
		return m, nil
	}
	return m, cmd
}

// handleUpdateModalKey handles keyboard input for the update modal. In the
// Overview phase the scroll keys drive the notes section (the phase's own
// scroller); everywhere else they ride the modal body's viewport. Everything
// else goes through the modal's action routing.
func (m *Model) handleUpdateModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == tea.KeyEsc {
		m.closeUpdateModal()
		return m, nil
	}

	m.ensureUpdateModal()
	u := m.updateUIState()
	switch msg.String() {
	case "u":
		if u != nil && u.phase == UpdateModalPreview && u.anyManaged {
			return m.applyUpdateAction("update", nil)
		}
	case "r":
		if u != nil && u.phase == UpdateModalError && u.retryCount > 0 {
			return m.applyUpdateAction("retry", nil)
		}
	case "up", "k":
		m.updatePreviewScroll(-3)
		return m, nil
	case "down", "j":
		m.updatePreviewScroll(3)
		return m, nil
	case "pgup", "ctrl+u":
		m.updatePreviewScroll(-10)
		return m, nil
	case "pgdown", "ctrl+d":
		m.updatePreviewScroll(10)
		return m, nil
	}

	action, cmd := m.updateModal.HandleKey(msg)
	return m.applyUpdateAction(action, cmd)
}

// handleUpdateModalMouse handles mouse events for the update modal. The
// notes bar claims its gestures before anything else sees them (see
// modal_scrollbar.go for why this cannot route through the modal); in the
// Overview phase the wheel drives the notes section; the rest is action
// routing.
func (m *Model) handleUpdateModalMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.ensureUpdateModal()
	if handled, _ := m.updateNotesBarEvent(msg); handled {
		return m, nil
	}
	if m.updateNotesOwnsScroll() {
		switch msg.Mouse().Button {
		case tea.MouseWheelUp:
			m.updatePreviewScroll(-3)
			return m, nil
		case tea.MouseWheelDown:
			m.updatePreviewScroll(3)
			return m, nil
		}
	}
	action := m.updateModal.HandleMouse(msg, m.updateMouseHandler)
	return m.applyUpdateAction(action, nil)
}

// updateNotesSection renders Sidecar's release notes as the Overview phase's
// own scrollable section: a windowed slice of the rendered markdown with an
// interactive, draggable bar, and a "View full changelog" element that
// expands the window in place — the modal grows within its clamp; no second
// overlay and no width change.
func (m *Model) updateNotesSection() modal.Section {
	return modal.ScrollingCustom(
		m.renderUpdateNotes,
		m.updateNotesSectionUpdate,
		func(regionID string) bool {
			return regionID == modal.RegionScrollbarThumb || regionID == modal.RegionScrollbarTrack
		},
		func(delta int) bool { return m.updateNotesAtBoundary(delta) },
	)
}

// notesWindowRows is how many note lines the section shows: collapsed it is
// a teaser, expanded it grows toward the library's preferred list height for
// the terminal — inside the modal's clamp either way.
func (u *updateUIState) notesWindowRows(screenH int) int {
	if !u.notesExpanded {
		return notesCollapsedRows
	}
	return max(modal.PreferredListRows(screenH), 3)
}

func (m *Model) renderUpdateNotes(contentW int, focusID, hoverID string) modal.RenderedSection {
	u := m.updateUIState()
	wrapW := max(10, contentW-1) // reserve the bar's column
	lines := m.updateActiveLines(u, wrapW)
	total := len(lines)

	window := u.notesWindowRows(m.height)
	visible := min(window, total)
	maxOff := max(0, total-visible)
	scroll := min(max(u.notesScroll, 0), maxOff)
	u.notesScroll = scroll
	u.notesTotal, u.notesVisible, u.notesPresent = total, visible, true

	meta := fmt.Sprintf("%s %s · %d lines",
		u.notesTarget.DisplayName, u.notesTarget.LatestVersion, total)
	var b strings.Builder
	b.WriteString(updateNotesHeader(meta, contentW))
	b.WriteString("\n")

	padded := make([]string, visible)
	for i, line := range lines[scroll : scroll+visible] {
		if w := lipgloss.Width(line); w < wrapW {
			line += strings.Repeat(" ", wrapW-w)
		}
		padded[i] = line
	}
	bar, _ := ui.RenderScrollbarWithState(ui.ScrollbarParams{
		TotalItems:   total,
		ScrollOffset: scroll,
		VisibleItems: visible,
		TrackHeight:  visible,
	}, m.updateNotesBar.style(m.updateMouseHandler))
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(padded, "\n"), bar))

	return modal.RenderedSection{
		Content: b.String(),
		Scrollbar: &modal.SectionScrollbar{
			TotalItems:   total,
			ScrollOffset: scroll,
			VisibleItems: visible,
			TrackHeight:  visible,
			LocalX:       contentW - 1,
		},
	}
}

// updateNotesToggleSection hosts the "View full changelog" element below the
// notes window — outside the modal's scrollable body, so the affordance can
// never scroll out of reach.
func (m *Model) updateNotesToggleSection() modal.Section {
	return modal.Custom(func(contentW int, focusID, hoverID string) modal.RenderedSection {
		u := m.updateUIState()
		if u == nil || !u.notesPresent || (u.notesTotal <= notesCollapsedRows && !u.notesExpanded) {
			return modal.RenderedSection{}
		}
		label := "[ View full changelog ]"
		if u.notesExpanded {
			label = "[ Collapse changelog ]"
		}
		style := styles.Muted
		switch {
		case focusID == updateNotesToggleID:
			style = lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)
		case hoverID == updateNotesToggleID:
			style = lipgloss.NewStyle().Foreground(styles.Primary)
		}
		rendered := style.Render(label)
		focusables := []modal.FocusableInfo{{
			ID:     updateNotesToggleID,
			Width:  lipgloss.Width(rendered),
			Height: 1,
		}}
		content := rendered

		if u.notesExpanded && u.hasNotesTarget() {
			switch u.changelogState {
			case changelogLoading:
				content += "\n" + styles.Muted.Render("Loading full changelog…")
			case changelogFailed:
				errLine := lipgloss.NewStyle().Foreground(styles.Error).
					Render("Couldn't load the full changelog: " + updateChangelogErrText(u.changelogErr))
				retry := styles.Muted.Render("[ Retry ]")
				if focusID == updateChangelogRetryID {
					retry = lipgloss.NewStyle().Foreground(styles.Primary).Bold(true).Render("[ Retry ]")
				} else if hoverID == updateChangelogRetryID {
					retry = lipgloss.NewStyle().Foreground(styles.Primary).Render("[ Retry ]")
				}
				content += "\n" + errLine + "\n" + retry
				focusables = append(focusables, modal.FocusableInfo{
					ID:      updateChangelogRetryID,
					OffsetY: 2,
					Width:   lipgloss.Width(retry),
					Height:  1,
				})
			}
		}

		return modal.RenderedSection{Content: content, Focusables: focusables}
	}, m.updateNotesSectionUpdate)
}

// updateChangelogErrText words a fetch failure for the styled error line.
func updateChangelogErrText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

// updateNotesHeader draws the section rule per modal-redesign.md's Header
// shape: small-caps label, rule to the edge, right-aligned meta.
func updateNotesHeader(meta string, width int) string {
	label := styles.Muted.Render("WHAT'S NEW")
	metaText := styles.Muted.Render(meta)
	lw, mw := lipgloss.Width(label), lipgloss.Width(metaText)
	ruleW := width - lw - mw - 2
	if ruleW < 3 {
		ruleW = 3
	}
	rule := styles.Muted.Render(strings.Repeat("─", ruleW))
	return label + " " + rule + " " + metaText
}

func (m *Model) updateNotesSectionUpdate(msg tea.Msg, focusID string) (string, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || focusID != updateNotesToggleID {
		return "", nil
	}
	switch key.String() {
	case "enter", " ", "space":
		return "toggle-notes", nil
	}
	return "", nil
}

// markdownLines renders markdown to wrapped lines, trailing blanks trimmed so
// the window math counts what will actually draw.
func markdownLines(md string, wrapW int) []string {
	lines := strings.Split(strings.TrimRight(renderReleaseNotes(parseReleaseNotes(md), wrapW), "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// updateActiveLines picks what the section windows right now: the offered
// release's body normally; once expanded and loaded, that release's full
// tag-pinned changelog.
func (m *Model) updateActiveLines(u *updateUIState, wrapW int) []string {
	if u.notesExpanded && u.changelogState == changelogLoaded && u.changelogBody != "" {
		return markdownLines(u.changelogBody, wrapW)
	}
	body := u.notesTarget.Notes
	if strings.TrimSpace(body) == "" {
		body = "No release notes were published for this release."
	}
	return markdownLines(body, wrapW)
}

// fetchChangelogCmd starts the tag-pinned fetch for the current notes target,
// marking the request tag so a late response for another release is dropped.
func (m *Model) fetchChangelogCmd() tea.Cmd {
	u := m.updateUI
	if u == nil {
		return nil
	}
	t := u.notesTarget
	d, ok := version.DescriptorFor(t.Product)
	if !ok || t.LatestVersion == "" {
		return nil
	}
	u.changelogTag = t.LatestVersion
	u.changelogState = changelogLoading
	return version.FetchChangelogCmd(d.RepoOwner, d.RepoName, t.LatestVersion)
}

// handleUpdateChangelogMsg settles a changelog fetch. Responses for anything
// other than the request still in flight are stale and dropped.
func (m *Model) handleUpdateChangelogMsg(msg version.ChangelogMsg) {
	u := m.updateUI
	if u == nil {
		return
	}
	d, ok := version.DescriptorFor(u.changelogProduct)
	if !ok || msg.Tag != u.changelogTag || msg.Repo != d.RepoName {
		return
	}
	if msg.Err != nil {
		u.changelogErr = msg.Err
		u.changelogState = changelogFailed
	} else {
		u.changelogBody = msg.Body
		u.changelogErr = nil
		u.changelogState = changelogLoaded
	}
	if m.updateModal != nil {
		m.updateModal.Invalidate()
	}
}

// updateNotesOwnsScroll reports that the Overview phase's scroll keys and
// wheel belong to the notes section. It reads only render-refreshed state.
func (m *Model) updateNotesOwnsScroll() bool {
	u := m.updateUIState()
	return u != nil && u.phase == UpdateModalPreview && u.notesPresent
}

// updatePreviewScroll scrolls the Preview phase's content by delta: the notes
// window first, spilling into the modal body once the notes hit their edge —
// on a tiny terminal both scrollers have something to move.
func (m *Model) updatePreviewScroll(delta int) {
	if m.updateNotesOwnsScroll() && !m.updateNotesAtBoundary(delta) {
		m.scrollUpdateNotes(delta)
		return
	}
	if m.updateModal != nil && m.updateModal.CanScroll(delta) {
		m.updateModal.ScrollBy(delta)
	}
}

func (m *Model) scrollUpdateNotes(delta int) {
	u := m.updateUIState()
	if u == nil {
		return
	}
	m.scrollUpdateNotesTo(u.notesScroll + delta)
}

func (m *Model) scrollUpdateNotesTo(off int) {
	u := m.updateUIState()
	if u == nil {
		return
	}
	maxOff := max(0, u.notesTotal-u.notesVisible)
	next := min(max(off, 0), maxOff)
	if next != u.notesScroll {
		u.notesScroll = next
		m.updateNotesBar.moved = true
	}
}

func (m *Model) updateNotesAtBoundary(delta int) bool {
	u := m.updateUIState()
	if u == nil {
		return false
	}
	maximum := max(0, u.notesTotal-u.notesVisible)
	return scroll.Bounds{Position: u.notesScroll, Maximum: maximum}.AtBoundary(delta)
}

// updateNotesBarEvent answers the notes bar's gestures through the shared
// switcher core: thumb grabs, jump-to-spot track presses, drag motions, and
// lost-release settling.
func (m *Model) updateNotesBarEvent(msg tea.MouseMsg) (bool, tea.Cmd) {
	return switcherBarMouseEvent(&m.updateNotesBar, m.updateModal, m.updateMouseHandler, msg,
		switcherBarOps{
			current: func() int {
				if u := m.updateUIState(); u != nil {
					return u.notesScroll
				}
				return 0
			},
			apply:     m.scrollUpdateNotesTo,
			onRelease: func(bool) tea.Cmd { return nil },
		})
}

// parseReleaseNotes cleans up release notes by removing duplicate headers
// and excessive whitespace. The modal already shows "What's New" as a header,
// so we strip any leading "What's New" headers from the content.
func parseReleaseNotes(notes string) string {
	if notes == "" {
		return notes
	}

	headerPatterns := regexp.MustCompile(`(?im)^#+\s*(what'?s?\s*new|release\s*notes)\s*\n*`)

	result := strings.TrimSpace(notes)

	for {
		loc := headerPatterns.FindStringIndex(result)
		if loc == nil || loc[0] != 0 {
			break
		}
		result = result[loc[1]:]
		result = strings.TrimSpace(result)
	}

	multiNewlines := regexp.MustCompile(`\n{3,}`)
	result = multiNewlines.ReplaceAllString(result, "\n\n")

	if strings.TrimSpace(result) == "" {
		return strings.TrimSpace(notes)
	}

	return result
}

// renderReleaseNotes renders markdown release notes.
//
// The modal box already insets its content by border + padding, so the notes
// render with a compact-document renderer: Glamour's default 2-column
// document margin would compound with the modal's own inset and wrap the
// text well before the box's right edge (td-65095b).
func renderReleaseNotes(notes string, width int) string {
	renderer, err := markdown.NewRenderer(markdown.CompactDocument)
	if err != nil {
		return notes
	}

	lines := renderer.RenderContent(notes, width)
	return strings.Join(lines, "\n")
}

// resultIcon renders the settled status of one target.
func resultIcon(status version.ResultStatus) string {
	switch status {
	case version.StatusUpdated:
		return lipgloss.NewStyle().Foreground(styles.Success).Render("✓")
	case version.StatusFailed:
		return lipgloss.NewStyle().Foreground(styles.Error).Render("✗")
	default:
		return lipgloss.NewStyle().Foreground(styles.TextMuted).Render("•")
	}
}

// resultLabel words a settled outcome for the user.
func resultLabel(r version.Result) string {
	switch r.Status {
	case version.StatusUpdated:
		return "updated to " + r.Version
	case version.StatusManual:
		return "needs a manual update"
	default:
		return "failed"
	}
}

// formatElapsed formats a duration as M:SS.
func formatElapsed(d time.Duration) string {
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

// truncateLine keeps long package-manager errors inside the modal width.
func truncateLine(s string, width int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if width < 10 {
		width = 10
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	if len(runes) > width-1 {
		runes = runes[:width-1]
	}
	return string(runes) + "…"
}

// wrapDetailLine wraps one diagnostic line to the modal width instead of
// truncating it. A toolchain error puts the useful part at the end of the line,
// so cutting it off is the same as not showing it. Capped at maxDetailWrapLines
// so one pathological line cannot push the modal past the viewport.
func wrapDetailLine(s string, width int) []string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\t", " ")
	if width < 20 {
		width = 20
	}
	var out []string
	for s != "" {
		if lipgloss.Width(s) <= width {
			out = append(out, s)
			break
		}
		if len(out) == maxDetailWrapLines-1 {
			out = append(out, truncateLine(s, width))
			break
		}
		runes := []rune(s)
		cut := width
		// Prefer a space so paths and flags stay readable.
		for i := cut; i > width/2; i-- {
			if runes[i] == ' ' {
				cut = i
				break
			}
		}
		out = append(out, strings.TrimSpace(string(runes[:cut])))
		s = strings.TrimSpace(string(runes[cut:]))
	}
	return out
}

// maxDetailWrapLines bounds how many lines a single diagnostic line may occupy.
const maxDetailWrapLines = 3
