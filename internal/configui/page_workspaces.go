package configui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/config"
)

// Workspaces is the set of defaults Sidecar applies when a user creates a shell
// or a worktree. Everything here is read at creation time, so the page says so
// once and then stays out of the way.

const (
	regionDefaultAgent    = "config-workspace-default-agent"
	regionAutoShell       = "config-workspace-auto-shell"
	regionDirPrefix       = "config-workspace-dir-prefix"
	regionOverviewScope   = "config-workspace-overview-scope"
	regionSidebarPrefix   = "config-workspace-sidebar-prefix"
	regionSidebarAgent    = "config-workspace-sidebar-agent"
	regionSidebarTask     = "config-workspace-sidebar-task"
	regionSidebarStats    = "config-workspace-sidebar-stats"
	controlWidth          = 48
	noneAgentLabel        = "None (plain shell)"
	overviewProjectLabel  = "Project root"
	overviewWorktreeLabel = "Worktree"
)

// offeredAgentFamilies is what creating work would offer right now: the
// allowlist resolved by the same rule the workspace pickers use.
func (m *Model) offeredAgentFamilies() []agentcatalog.Family {
	return agentcatalog.Resolve(m.Config().Plugins.Workspace.Agents)
}

// defaultAgentLabel names the configured default agent. An unset default is a
// plain shell, which is what creation does with it.
func (m *Model) defaultAgentLabel() string {
	id := strings.TrimSpace(m.Config().Plugins.Workspace.DefaultAgentType)
	if id == "" {
		return noneAgentLabel
	}
	if family, ok := agentcatalog.Find(id); ok {
		return family.Short
	}
	// A default naming something Sidecar does not know is reported as it is
	// stored rather than quietly renamed.
	return id
}

// defaultAgentOptions are the choices creation would offer, with "no agent"
// first: a plain shell is a real answer, not the absence of one.
func (m *Model) defaultAgentOptions() []dropdownOption {
	families := m.offeredAgentFamilies()
	options := make([]dropdownOption, 0, len(families)+1)
	options = append(options, dropdownOption{id: "", label: noneAgentLabel})
	for _, family := range families {
		options = append(options, dropdownOption{id: family.ID, label: family.Short})
	}
	return options
}

// saveDefaultAgent writes the agent creation starts a new workspace with.
func saveDefaultAgent(m *Model, option dropdownOption) tea.Cmd {
	id := option.id
	return SaveCmd("Default agent: "+option.label, func() error {
		return config.SaveWorkspace(func(ws *config.WorkspacePluginConfig) { ws.DefaultAgentType = id })
	})
}

func (m *Model) buildWorkspaces(b *paneBuilder) {
	cfg := m.Config()
	ws := cfg.Plugins.Workspace

	b.text(PaneTitle(PageTitle(PageWorkspaces)), "")
	b.lead("Defaults used when you create a new workspace.")

	b.text(SectionHeader("New workspaces"))

	b.selectRowValue(regionDefaultAgent, "Default agent", m.defaultAgentLabel(), controlWidth,
		m.defaultAgentOptions(), strings.TrimSpace(ws.DefaultAgentType), saveDefaultAgent)

	b.toggleRow(regionAutoShell, "Start with a shell", ws.AutoCreateShell, func(m *Model) tea.Cmd {
		enabled := !m.Config().Plugins.Workspace.AutoCreateShell
		return SaveCmd(toggleNotice("Start with a shell", enabled), func() error {
			return config.SaveWorkspace(func(ws *config.WorkspacePluginConfig) { ws.AutoCreateShell = enabled })
		})
	})

	b.text(SectionHeader("Worktree defaults"))

	b.toggleRow(regionDirPrefix, "Repository prefix", ws.DirPrefix, func(m *Model) tea.Cmd {
		enabled := !m.Config().Plugins.Workspace.DirPrefix
		return SaveCmd(toggleNotice("Repository prefix", enabled), func() error {
			return config.SaveWorkspace(func(ws *config.WorkspacePluginConfig) { ws.DirPrefix = enabled })
		})
	})
	b.help("Names a new worktree directory after its repository, so it stays identifiable later.")

	b.selectRow(regionOverviewScope, "Overview location", controlWidth, overviewScopeOptions(),
		m.overviewScope(), saveOverviewScope)
	if m.overviewScope() == config.OverviewWorktreeScopeWorktree {
		b.help("When you select a worktree in Activity, scope Sidecar to that worktree")
		b.help("instead of its project's main checkout.")
	} else {
		b.help("When you select a worktree in Activity, open its project's main")
		b.help("checkout instead of that worktree.")
	}

	b.text(SectionHeader("What the workspace sidebar displays"))
	display := ws.SidebarDisplay
	m.sidebarToggle(b, regionSidebarPrefix, "Repo name prefix", !display.HideRepoPrefix,
		func(d *config.SidebarDisplayConfig, on bool) { d.HideRepoPrefix = !on })
	m.sidebarToggle(b, regionSidebarAgent, "Agent", !display.HideAgent,
		func(d *config.SidebarDisplayConfig, on bool) { d.HideAgent = !on })
	m.sidebarToggle(b, regionSidebarTask, "Linked task", !display.HideTask,
		func(d *config.SidebarDisplayConfig, on bool) { d.HideTask = !on })
	m.sidebarToggle(b, regionSidebarStats, "Change stats", !display.HideStats,
		func(d *config.SidebarDisplayConfig, on bool) { d.HideStats = !on })

	b.text(SectionHeader("Worktree setup"))
	b.note("Not configurable here yet. Edit plugins.workspace.worktreeSetup in your Sidecar config.")
	b.note("Use copyEnvFiles and envFiles to copy startup files into each new worktree.")
	b.note("Use runHook and hookPath to run a setup script after it is created.")
	b.blank()
	b.note(worktreeSetupSummary(ws.WorktreeSetup))
	b.note("A setup hook is a script from the repository, so running one executes repository-provided code.")

	b.blank()
	b.lead("These are creation defaults: they apply to the next shell or worktree, not to existing ones.")
}

// overviewScopeOptions are the two places selecting a worktree in Activity can
// take Sidecar.
func overviewScopeOptions() []dropdownOption {
	return []dropdownOption{
		{id: config.OverviewWorktreeScopeProject, label: overviewProjectLabel},
		{id: config.OverviewWorktreeScopeWorktree, label: overviewWorktreeLabel},
	}
}

// saveOverviewScope writes where activating a worktree scopes Sidecar.
func saveOverviewScope(m *Model, option dropdownOption) tea.Cmd {
	scope := option.id
	return SaveCmd("Overview location: "+option.label, func() error {
		return config.SaveWorkspace(func(ws *config.WorkspacePluginConfig) { ws.OverviewWorktreeScope = scope })
	})
}

// overviewScope is the configured Overview location, defaulted the way the
// workspace plugin defaults it.
func (m *Model) overviewScope() string {
	if m.Config().Plugins.Workspace.OverviewWorktreeScope == config.OverviewWorktreeScopeWorktree {
		return config.OverviewWorktreeScopeWorktree
	}
	return config.OverviewWorktreeScopeProject
}

// sidebarToggle declares one sidebar-display control. The stored fields are
// negative (hide*), the controls are positive: a user turns on what they want
// to see.
func (m *Model) sidebarToggle(b *paneBuilder, id, label string, on bool, set func(*config.SidebarDisplayConfig, bool)) {
	b.toggleRow(id, label, on, func(m *Model) tea.Cmd {
		enabled := !on
		return SaveCmd(toggleNotice(label, enabled), func() error {
			return config.SaveWorkspace(func(ws *config.WorkspacePluginConfig) {
				set(&ws.SidebarDisplay, enabled)
			})
		})
	})
}

// worktreeSetupSummary states what the current configuration would actually do,
// so the static explanation above is not the only thing the user has to go on.
func worktreeSetupSummary(setup config.WorktreeSetupConfig) string {
	var parts []string
	if setup.CopyEnvFiles {
		if len(setup.EnvFiles) > 0 {
			parts = append(parts, "copies "+strings.Join(setup.EnvFiles, ", "))
		} else {
			parts = append(parts, "copies startup files")
		}
	}
	if setup.RunHook {
		hook := setup.HookPath
		if hook == "" {
			hook = "the repository setup hook"
		}
		parts = append(parts, "offers to run "+hook)
	}
	if len(parts) == 0 {
		return "Currently: nothing is copied and no hook runs."
	}
	return "Currently: " + strings.Join(parts, "; ") + "."
}

func toggleNotice(label string, on bool) string {
	if on {
		return label + " on"
	}
	return label + " off"
}
