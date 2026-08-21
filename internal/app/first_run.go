package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/styles"
)

// firstRunProbeMsg is the result of the launch-time "no projects, is this a
// Git repo?" check. It is produced by a tea.Cmd so Init itself never walks
// the filesystem or spawns git.
type firstRunProbeMsg struct {
	NeedsSetup bool
}

func (m *Model) handleFirstRunProbe(msg firstRunProbeMsg) tea.Cmd {
	m.firstRunProbePending = false
	if !msg.NeedsSetup {
		return nil
	}
	return m.handleOpenConfiguration(OpenConfigurationMsg{
		Page:       configui.PageProjects,
		AddProject: true,
	})
}

func (m *Model) handleOpenConfiguration(msg OpenConfigurationMsg) tea.Cmd {
	page := msg.Page
	if msg.AddProject && page == "" {
		page = configui.PageProjects
	}
	cmd := m.openConfiguration(page)
	if !msg.AddProject || m.config == nil {
		return cmd
	}
	m.config.OpenAddProject()
	if dir := m.ui.WorkDir; dir != "" {
		m.config.PrefillAddProjectFromDir(dir)
	}
	m.updateContext()
	return tea.Batch(cmd, m.config.TakePending())
}

func (m Model) renderFirstRunProbe(width, height int) string {
	var sb strings.Builder
	sb.WriteString(styles.Title.Render("Set up a project"))
	sb.WriteString("\n\n")
	sb.WriteString(styles.Muted.Render("Sidecar uses Git repositories for worktrees, status, and diffs."))
	sb.WriteString("\n")
	sb.WriteString(styles.Muted.Render("Checking this directory…"))
	return styles.RenderPanel(sb.String(), width, height, true)
}
