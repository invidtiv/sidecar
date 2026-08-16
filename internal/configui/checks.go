package configui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/configchecks"
)

// Readiness checks run in commands and are cached here. Nothing on a render
// path may look at the filesystem, PATH, or a subprocess: Sidecar Setup and
// Diagnostics both paint from this cache, and Recheck is the only thing that
// refreshes it.

// ChecksMsg carries a completed run back to the surface. The host forwards it;
// the surface owns the cache.
type ChecksMsg struct {
	Results configchecks.Results
}

// CopyMsg asks the host to put text on the clipboard and say so. Configuration
// does not own the clipboard or the toast, so it asks.
type CopyMsg struct {
	Text string
	// Notice is what the toast should say on success.
	Notice string
}

// NoticeMsg asks the host for a toast.
type NoticeMsg struct {
	Message string
}

// OpenShellMsg asks the host to open an ordinary Sidecar shell with a command
// typed into it. The command is never executed by Sidecar — the user reads it
// and presses Enter, or does not.
type OpenShellMsg struct {
	Command string
}

// OpenFileMsg asks the host to put a file in front of the user.
type OpenFileMsg struct {
	Path string
}

// SetCheckInput tells the surface what to check. The host supplies the running
// configuration and the active project each time Configuration opens, so a
// project switch cannot leave stale answers behind.
func (m *Model) SetCheckInput(in configchecks.Input) { m.checkInput = in }

// Recheck runs every check in a command. It is explicit everywhere: no repair
// infers success from a shell closing or a file being saved.
func (m *Model) Recheck() tea.Cmd {
	in := m.checkInput
	m.checking = true
	return func() tea.Msg { return ChecksMsg{Results: configchecks.Run(in)} }
}

// ApplyChecks caches a completed run.
func (m *Model) ApplyChecks(msg ChecksMsg) {
	m.checks = msg.Results
	m.checked = true
	m.checking = false
	// A repair that resolved returns the user to the page that sent them, so a
	// finished job does not leave a stale problem screen on display.
	m.closeResolvedRepair()
}

// Checks are the cached results.
func (m *Model) Checks() configchecks.Results { return m.checks }

// ChecksReady reports whether a run has completed.
func (m *Model) ChecksReady() bool { return m.checked }

// result looks up one cached check.
func (m *Model) result(id configchecks.ID) configchecks.Result {
	found, _ := m.checks.Get(id)
	return found
}

// Child route IDs. They are exported because later phases route to the same
// repairs from their own pages — the Agents page opens the agent-instructions
// route rather than building a second one.
const (
	ChildRepairTmux              ChildID = "repair-tmux"
	ChildRepairTerminalColors    ChildID = "repair-terminal-colors"
	ChildRepairAgentInstructions ChildID = "repair-agent-instructions"
	ChildRepairConfiguration     ChildID = "repair-configuration"
)

// repairRoute maps a check's repair hint onto a child route and its title.
func repairRoute(repair configchecks.RepairID) (ChildID, string, bool) {
	switch repair {
	case configchecks.RepairTmux:
		return ChildRepairTmux, "Set up tmux", true
	case configchecks.RepairTerminalColors:
		return ChildRepairTerminalColors, "Terminal colors", true
	case configchecks.RepairAgentInstructions:
		return ChildRepairAgentInstructions, "Agent instructions", true
	case configchecks.RepairConfiguration:
		return ChildRepairConfiguration, "Configuration", true
	}
	return ChildNone, "", false
}

// OpenRepair opens the focused route for a repair hint from the current page.
// It reports false for a hint with no route of its own — today, adding a
// project, which is a page rather than a repair.
func (m *Model) OpenRepair(repair configchecks.RepairID) bool {
	child, title, ok := repairRoute(repair)
	if !ok {
		return false
	}
	m.PushChild(child, title)
	m.rowCursor = 0
	m.detailFocus = false
	return true
}

// OpenAgentInstructions opens the agent-instructions repair as a child of the
// current page. The Agents page uses this in a later phase; Diagnostics uses it
// now. Both get the same route, with the same parent-return.
func (m *Model) OpenAgentInstructions() { m.OpenRepair(configchecks.RepairAgentInstructions) }

// activateRepair is what a Setup or Diagnostics row does when chosen. A repair
// with no route navigates to the page that owns the work instead.
func (m *Model) activateRepair(repair configchecks.RepairID) tea.Cmd {
	if m.OpenRepair(repair) {
		return nil
	}
	if repair == configchecks.RepairAddProject {
		// TODO(phase 3): deep-link to Add Project with Location focused. The
		// Projects page does not host that route yet, so this lands on the page
		// that will own it rather than inventing a second add flow here.
		m.Navigate(PageProjects)
	}
	return nil
}

// closeResolvedRepair returns from a repair route whose problem is gone. It is
// only ever reached from a completed check run, so "resolved" means Sidecar
// looked again and found the thing fixed.
func (m *Model) closeResolvedRepair() {
	route := m.Route()
	if !route.IsChild() {
		return
	}
	var id configchecks.ID
	switch route.Child {
	case ChildRepairTmux:
		id = configchecks.CheckTmux
	case ChildRepairTerminalColors:
		id = configchecks.CheckTerminalColors
	case ChildRepairAgentInstructions:
		id = configchecks.CheckAgentInstructions
	case ChildRepairConfiguration:
		id = configchecks.CheckConfiguration
	default:
		return
	}
	if found, ok := m.checks.Get(id); ok && found.OK {
		m.Back()
		m.rowCursor = 0
	}
}
