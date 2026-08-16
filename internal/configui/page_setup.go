package configui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

	// Recheck is a real control, not a hidden key: it sits pinned to the right of
	// a line that says what it looks at, so it can be seen, clicked, or run with
	// R. It is declared before anything can return early, so it works while the
	// first run is still pending — which is exactly when a check that never
	// started needs it. Like Diagnostics' Recheck it is not a cursor stop: the
	// row cursor belongs on the work the page is about.
	b.rightControl(setupRecheckHint(b.inner, m.checking), "setup-recheck", "r", setupRecheckLabel, func(m *Model) tea.Cmd {
		return m.Recheck()
	})
	b.blank()

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
		// The Recheck control at the top already says what R does, so an
		// all-healthy page does not repeat it.
		b.lead("Recheck whenever your machine or your projects change.")
	}
}

// setupRecheckLabel is the pill's visible label; the shortcut it advertises and
// the key registered with it are the same letter.
const setupRecheckLabel = "R  Recheck"

// setupRecheckHint is the explanatory text beside the Recheck pill, in the
// longest form the pane can hold. The button is the point of the line, so the
// sentence gives way to it rather than the other way round: a truncated hint is
// a smaller loss than a Recheck control clipped off the right edge, which is the
// state this control existed to get out of.
//
// The shortest candidate is sized to survive the app's 60-column minimum width,
// because a bare right-aligned pill with nothing beside it is the unexplained
// control this page was fixed to stop showing — a smaller window is not a reason
// to go back to it.
func setupRecheckHint(inner int, checking bool) string {
	long := []string{
		"Looks again at tmux, terminal colors, projects, and agent instructions.",
		"Looks again at tmux, colors, projects, and instructions.",
		"Re-runs every setup check.",
		"Rechecks your setup.",
	}
	if checking {
		long = []string{
			"Rechecking tmux, terminal colors, projects, and agent instructions…",
			"Rechecking tmux, colors, projects, and instructions…",
			"Rechecking…",
		}
	}
	// The gap the pill needs: its own width plus two spaces of breathing room.
	room := inner - ansi.StringWidth(Button(setupRecheckLabel, false, State{})) - 2
	for _, text := range long {
		if ansi.StringWidth(text) <= room {
			return Muted(text)
		}
	}
	return ""
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
