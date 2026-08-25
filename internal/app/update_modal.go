package app

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/version"
)

// updateModalWidth is the one width for the whole journey. The library clamps
// it to the terminal at render time, so no per-size caching is needed.
const updateModalWidth = 60

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

	products        []version.Target
	notes           string
	anyManaged      bool
	restartRequired bool
	retryCount      int
	// presentedPhase records which phase the modal's presentation currently
	// reflects so re-presenting is skipped between phase changes. It lives on
	// the shared heap struct because separate model copies cannot see each
	// other's writes.
	presentedPhase UpdateModalState
	presentedValid bool
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
	u.notes = m.updateNotes
	u.anyManaged = false
	for _, t := range version.SelectPlan(u.products) {
		if t.Install.Managed {
			u.anyManaged = true
		}
	}
	u.restartRequired = version.RestartRequired(u.settled)
	u.retryCount = len(version.RetryTargets(u.settled))
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
		modal.WithWidth(updateModalWidth),
		modal.WithVariant(updateVariant(u.phase)),
		modal.WithHintText(updateHint(u.phase)),
		modal.WithPrimaryAction(updatePrimaryAction(m.updateUIState())),
	)

	mdl.AddSection(modal.When(inPhase(UpdateModalPreview), modal.Custom(func(cw int, _, _ string) modal.RenderedSection {
		return modal.RenderedSection{Content: updateOverviewContent(u, cw)}
	}, nil)))

	mdl.AddSection(modal.When(inPhase(UpdateModalProgress), modal.Custom(func(cw int, _, _ string) modal.RenderedSection {
		return modal.RenderedSection{Content: updateProgressContent(u)}
	}, nil)))

	mdl.AddSection(modal.When(inPhase(UpdateModalComplete), modal.Custom(func(cw int, _, _ string) modal.RenderedSection {
		return modal.RenderedSection{Content: updateResultContent(u)}
	}, nil)))

	mdl.AddSection(modal.When(inPhase(UpdateModalError), modal.Custom(func(cw int, _, _ string) modal.RenderedSection {
		return modal.RenderedSection{Content: updateErrorContent(u, cw)}
	}, nil)))

	mdl.AddSection(modal.Spacer())
	mdl.AddSection(modal.Custom(func(cw int, focusID, hoverID string) modal.RenderedSection {
		btns := updateButtons(u)
		if len(btns) == 0 {
			return modal.RenderedSection{}
		}
		return modal.Buttons(btns...).Render(cw, focusID, hoverID)
	}, func(msg tea.Msg, focusID string) (string, tea.Cmd) {
		btns := updateButtons(u)
		if len(btns) == 0 {
			return "", nil
		}
		return modal.Buttons(btns...).Update(msg, focusID)
	}))

	m.updateModal = mdl
	if m.updateMouseHandler == nil {
		m.updateMouseHandler = mouse.NewHandler()
	}
	m.applyUpdatePresentation()
}

// applyUpdatePresentation restates what changes between phases on the one
// persistent modal: title, border variant, hint line, and primary action.
// Skipped while the presented phase is unchanged — Apply invalidates layout,
// and ensure runs from View as well as Update on every frame.
func (m *Model) applyUpdatePresentation() {
	if u := m.updateUI; u != nil && u.presentedValid && u.presentedPhase == m.updateModalState {
		return
	}
	m.updateModal.Apply(
		modal.WithTitle(updateTitle(m.updateModalState)),
		modal.WithVariant(updateVariant(m.updateModalState)),
		modal.WithHintText(updateHint(m.updateModalState)),
		modal.WithPrimaryAction(updatePrimaryAction(m.updateUIState())),
	)
	if u := m.updateUI; u != nil {
		u.presentedPhase = m.updateModalState
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

// updateHint overrides the library default where it would lie. During an
// install the default "Esc to cancel" is exactly the false promise the
// honest-no-cancel stance forbids: Esc only hides the modal.
func updateHint(p UpdateModalState) string {
	if p == UpdateModalProgress {
		return "esc hides · update continues"
	}
	return ""
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

func updateButtons(u *updateUIState) []modal.ButtonDef {
	switch u.phase {
	case UpdateModalPreview:
		if !u.anyManaged {
			return []modal.ButtonDef{modal.Btn(" Close ", "cancel")}
		}
		return []modal.ButtonDef{
			modal.Btn(" Update Now ", "update"),
			modal.Btn(" Later ", "cancel"),
		}
	case UpdateModalComplete:
		if u.restartRequired {
			return []modal.ButtonDef{
				modal.Btn(" Quit & Restart ", "quit"),
				modal.Btn(" Later ", "cancel"),
			}
		}
		return []modal.ButtonDef{modal.Btn(" Close ", "cancel")}
	case UpdateModalError:
		return []modal.ButtonDef{
			modal.Btn(" Retry ", "retry"),
			modal.Btn(" Close ", "cancel"),
		}
	default:
		return nil
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

// updateOverviewContent renders the confirmation view: what changes, the
// Tasks note, and Sidecar's release notes inline. Notes longer than the box
// scroll in the modal's own viewport; nothing is truncated by hand here.
func updateOverviewContent(u *updateUIState, contentW int) string {
	plan := version.SelectPlan(u.products)
	if len(plan) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(styles.Muted.Render("Updating will change:") + "\n")
	rows := make([]string, 0, len(plan))
	for _, t := range plan {
		rows = append(rows, targetRow(t))
	}
	b.WriteString(strings.Join(rows, "\n"))

	if u.includesTasks() {
		b.WriteString("\n\n" + styles.Muted.Render(
			"Tasks here is the standalone tasks/tasks-tui/tasks-api commands.\nSidecar's embedded Tasks tab updates with Sidecar itself."))
	}

	b.WriteString("\n\n" + lipgloss.NewStyle().Bold(true).Render("What's New in Sidecar") + "\n\n")
	notes := u.notes
	if notes == "" {
		notes = "No release notes available."
	}
	b.WriteString(renderReleaseNotes(parseReleaseNotes(notes), contentW))
	return b.String()
}

// updateProgressContent shows which product is being changed right now, plus
// the settled rows for targets that already finished.
func updateProgressContent(u *updateUIState) string {
	var b strings.Builder
	for i, t := range u.plan {
		switch {
		case i < len(u.results):
			r := u.results[i]
			fmt.Fprintf(&b, "%s %s %s\n", resultIcon(r.Status), t.DisplayName,
				lipgloss.NewStyle().Foreground(styles.TextMuted).Render(resultLabel(r)))
		case i == u.activeIdx:
			action := "installing " + t.LatestVersion
			if t.Install.Managed {
				action += " via " + t.Install.Method.String()
			}
			fmt.Fprintf(&b, "%s %s %s\n",
				lipgloss.NewStyle().Foreground(styles.Warning).Render("●"),
				lipgloss.NewStyle().Bold(true).Render(t.DisplayName),
				lipgloss.NewStyle().Foreground(styles.TextMuted).Render(action))
		default:
			fmt.Fprintf(&b, "%s %s\n",
				lipgloss.NewStyle().Foreground(styles.TextMuted).Render("○"),
				lipgloss.NewStyle().Foreground(styles.TextMuted).Render(t.DisplayName))
		}
	}
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(styles.TextMuted).Render(
		fmt.Sprintf("Elapsed: %s", formatElapsed(time.Since(u.start)))))
	return b.String()
}

// updateResultContent renders the settled results of a fully successful batch.
// It reads the whole settled set, not just this batch: after retrying one
// product, an upgrade that succeeded earlier is still part of the outcome.
func updateResultContent(u *updateUIState) string {
	var b strings.Builder
	rows := make([]string, 0, len(u.settled))
	for _, r := range u.settled {
		row := fmt.Sprintf("%s %s %s", resultIcon(r.Status), r.Target.DisplayName,
			styles.Muted.Render(resultLabel(r)))
		if r.Status == version.StatusManual && r.Target.Install.ManualCommand != "" {
			row += "\n" + styles.Muted.Render("    update it yourself: "+r.Target.Install.ManualCommand)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		rows = append(rows, styles.Muted.Render("Nothing to update."))
	}
	b.WriteString(strings.Join(rows, "\n"))
	if u.restartRequired {
		b.WriteString("\n\n" + styles.Muted.Render("Restart sidecar to use the new version.") +
			"\n" + styles.Muted.Render("Tip: Press q to quit, then run 'sidecar' again."))
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
		row := fmt.Sprintf("%s %s %s", resultIcon(r.Status), r.Target.DisplayName,
			styles.Muted.Render(resultLabel(r)))
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
			row += "\n" + styles.Muted.Render("    update it yourself: "+r.Target.Install.ManualCommand)
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
// as Installing; unseen settled results reopen as Done/Failed; otherwise the
// confirmation opens when something is available. It reports false when there
// is nothing worth showing.
func (m *Model) openUpdateModal() bool {
	m.showDiagnostics = false
	switch {
	case m.updateInProgress:
		m.updateModalState = UpdateModalProgress
	case m.updateResultsAcked:
		return false
	case len(m.settledResults()) > 0:
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
		return m, m.startUpdateBatch(version.SelectPlan(m.products))
	case "retry":
		return m, m.startUpdateBatch(version.RetryTargets(m.settledResults()))
	case "quit":
		m.shutdown()
		return m, tea.Quit
	case "cancel", "close":
		m.closeUpdateModal()
		return m, nil
	}
	return m, cmd
}

// handleUpdateModalKey handles keyboard input for the update modal. Scrolling
// rides the modal body's own viewport; everything else goes through the
// modal's action routing.
func (m *Model) handleUpdateModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == tea.KeyEsc {
		m.closeUpdateModal()
		return m, nil
	}

	m.ensureUpdateModal()
	switch msg.String() {
	case "up", "k":
		m.updateModal.ScrollBy(-3)
		return m, nil
	case "down", "j":
		m.updateModal.ScrollBy(3)
		return m, nil
	case "pgup", "ctrl+u":
		m.updateModal.ScrollBy(-10)
		return m, nil
	case "pgdown", "ctrl+d":
		m.updateModal.ScrollBy(10)
		return m, nil
	}

	action, cmd := m.updateModal.HandleKey(msg)
	return m.applyUpdateAction(action, cmd)
}

// handleUpdateModalMouse handles mouse events for the update modal.
func (m *Model) handleUpdateModalMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.ensureUpdateModal()
	action := m.updateModal.HandleMouse(msg, m.updateMouseHandler)
	return m.applyUpdateAction(action, nil)
}

// targetRow renders one product row for the preview: what changes, from which
// version to which, and how Sidecar would do it.
func targetRow(t version.Target) string {
	arrow := lipgloss.NewStyle().Foreground(styles.Success).Render(" → ")
	line := fmt.Sprintf("%s %s%s%s", t.DisplayName, t.CurrentVersion, arrow, t.LatestVersion)
	if t.Install.Managed {
		return line + styles.Muted.Render("  · "+t.Install.Method.String())
	}
	return line + styles.Muted.Render("  · manual") +
		"\n" + styles.Muted.Render("    update it yourself: "+t.Install.ManualCommand)
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
