package configui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/configchecks"
)

// Sidecar Setup is an action-oriented readiness home, not a status page. It
// lists only work the user can do something about, and every listed item opens
// the focused repair that does it. When there is nothing to do it says so
// plainly rather than manufacturing reassurance.

// setupChecks is the order Setup considers readiness in: the two things that
// block work first, then the two that degrade it.
var setupChecks = []configchecks.ID{
	configchecks.CheckProjects,
	configchecks.CheckTmux,
	configchecks.CheckTerminalColors,
	configchecks.CheckAgentInstructions,
}

func (m *Model) buildSetup(b *paneBuilder) {
	b.text(PaneTitle(PageTitle(PageSetup)), "")

	// Recheck stays reachable from the page without occupying a visible row:
	// Setup's visible controls are the work, not the diagnostics. It is declared
	// before anything can return early, so R works while the first run is still
	// pending — which is exactly when a check that never started needs it.
	b.declare("setup-recheck", "r", false, func(m *Model) tea.Cmd { return m.Recheck() })

	if !m.checked {
		b.lead("Checking your setup…")
		return
	}

	var problems, healthy []configchecks.Result
	for _, id := range setupChecks {
		result := m.result(id)
		if result.ID == "" {
			continue
		}
		if result.OK {
			healthy = append(healthy, result)
			continue
		}
		problems = append(problems, result)
	}

	if len(problems) == 0 {
		b.text(Body("Sidecar is ready to work."))
		b.lead("Nothing needs attention right now. Use the sidebar to adjust anything else.")
	} else {
		b.text(Body("A few things will make Sidecar ready to work for you."))
		b.lead("Choose an item to fix it now, or return whenever your setup changes.")
	}

	if len(problems) > 0 {
		b.text(SectionHeader("Needs attention"))
		for i, result := range problems {
			result := result
			id := fmt.Sprintf("setup-repair-%d", i)
			// Setup speaks in work, not in status codes: every actionable item
			// here reads FIX, whatever badge Diagnostics gives the same check.
			b.row(id, "", func(m *Model) tea.Cmd {
				return m.activateRepair(result.Repair)
			}, func(state State) string {
				return RepairRow(configchecks.BadgeFix, setupTitle(result), setupDetail(result), b.inner, state)
			})
		}
	}

	if len(healthy) > 0 {
		b.text(SectionHeader("Looking good"))
		for _, result := range healthy {
			// Healthy items are quiet confirmation. They are not controls here;
			// Diagnostics is where a healthy check can still be opened.
			b.text(StatusRow(true, result.Title, result.Summary, "", b.inner, State{}))
		}
	}

	b.blank()
	if len(problems) > 0 {
		b.lead("Enter opens a focused repair that explains a change before making it.")
	} else {
		b.lead("R rechecks whenever your machine or your projects change.")
	}
}

func setupTitle(result configchecks.Result) string {
	if result.Action != "" {
		return result.Action
	}
	return result.Title
}

func setupDetail(result configchecks.Result) string {
	if result.ActionDetail != "" {
		return result.ActionDetail
	}
	return result.Summary
}
