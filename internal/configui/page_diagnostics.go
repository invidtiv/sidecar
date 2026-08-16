package configui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/configchecks"
)

// Diagnostics is the detailed form of the Setup checks and the durable home for
// them. It never hides a problem because Setup is not the current page, and it
// never turns a healthy row into something that looks clickable — except Agent
// instructions, which is always a way into its route because opening a
// project's instruction file is a reasonable thing to want when nothing is
// wrong.

var diagnosticsEnvironment = []configchecks.ID{
	configchecks.CheckTerminalColors,
	configchecks.CheckTmux,
}

var diagnosticsData = []configchecks.ID{
	configchecks.CheckConfiguration,
	configchecks.CheckProjects,
	configchecks.CheckAgentInstructions,
}

func (m *Model) buildDiagnostics(b *paneBuilder) {
	b.text(PaneTitle(PageTitle(PageDiagnostics)), "")

	header := Muted("Check the parts Sidecar depends on.")
	if m.checking {
		header = Muted("Rechecking…")
	}
	b.rightControl(header, "diagnostics-recheck", "r", "R  Recheck", func(m *Model) tea.Cmd {
		return m.Recheck()
	})

	if !m.checked {
		b.blank()
		b.lead("Running checks…")
		return
	}

	m.diagnosticsSection(b, "Environment", diagnosticsEnvironment)
	m.diagnosticsSection(b, "Data", diagnosticsData)

	b.blank()
	b.lead("Enter opens a focused repair with clear next steps.")
}

func (m *Model) diagnosticsSection(b *paneBuilder, title string, ids []configchecks.ID) {
	var shown []configchecks.Result
	for _, id := range ids {
		if result := m.result(id); result.ID != "" {
			shown = append(shown, result)
		}
	}
	if len(shown) == 0 {
		return
	}
	b.text(SectionHeader(title))
	for _, result := range shown {
		result := result
		if !result.Actionable() {
			// Quiet confirmation: one line, no badge, no cursor stop.
			b.text(StatusRow(result.OK, result.Title, result.Summary, "", b.inner, State{}))
			continue
		}
		b.row("diagnostics-"+string(result.ID), "", func(m *Model) tea.Cmd {
			return m.activateRepair(result.Repair)
		}, func(state State) string {
			return StatusRow(result.OK, result.Title, result.Summary, result.Badge, b.inner, state)
		})
	}
}
