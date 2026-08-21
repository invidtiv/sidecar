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
	// The integration probe rides along with the readiness run: both answer
	// questions about the machine, both must be off the render path, and both
	// are stale for exactly the same reasons.
	return tea.Batch(
		func() tea.Msg { return ChecksMsg{Results: configchecks.Run(in)} },
		m.ProbeCmd(),
		// Install provenance is the same kind of fact — resolved by running
		// `brew --cellar` — so it rides along and About paints from the cache.
		m.detectInstallationCmd(),
	)
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
	// Whether the check was already passing when the route opened is what makes
	// a later Recheck meaningful: a route opened on a healthy check — the agent
	// instructions the Agents page opens to read — is where the user asked to be,
	// and only a problem that has just become OK closes itself.
	m.repairOpenedOK = false
	if id, ok := repairCheckID(child); ok {
		if found, known := m.checks.Get(id); known && found.OK {
			m.repairOpenedOK = true
		}
	}
	m.rowCursor = 0
	m.detailFocus = false
	return true
}

// repairCheckID is the check a repair route is about.
func repairCheckID(child ChildID) (configchecks.ID, bool) {
	switch child {
	case ChildRepairTmux:
		return configchecks.CheckTmux, true
	case ChildRepairTerminalColors:
		return configchecks.CheckTerminalColors, true
	case ChildRepairAgentInstructions:
		return configchecks.CheckAgentInstructions, true
	case ChildRepairConfiguration:
		return configchecks.CheckConfiguration, true
	}
	return "", false
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
		// The deep link lands on the work itself: Projects' Add Project route,
		// with Location focused, rather than on the page that hosts it.
		m.OpenAddProject()
		return m.drain(nil)
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
	// A route the user opened on an already-healthy check has nothing to resolve.
	// Closing it on the next Recheck would take the screen away from a user who
	// asked for it and then pressed R.
	if m.repairOpenedOK {
		return
	}
	id, ok := repairCheckID(route.Child)
	if !ok {
		return
	}
	if found, known := m.checks.Get(id); known && found.OK {
		m.Back()
		m.rowCursor = 0
	}
}
