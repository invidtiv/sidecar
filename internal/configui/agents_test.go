package configui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configchecks"
)

func TestAgentsPageListsFamiliesAndCommands(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageAgents)
	view := ansi.Strip(m.View(160, 45))

	for _, want := range []string{"Available agents", "Claude", "Codex", "OpenCode", "Grok", "claude", "opencode", "Instructions"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Agents is missing %q:\n%s", want, view)
		}
	}
	// With no allowlist the page must not imply the user chose this state.
	if !strings.Contains(view, "No allowlist is set") {
		t.Fatalf("Agents did not explain an unset allowlist:\n%s", view)
	}
}

func TestAgentToggleMaintainsAllowlist(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageAgents)

	activate(t, m, regionAgentToggle+"codex")
	saved := loadSaved(t).Plugins.Workspace.Agents
	if len(saved) != len(agentcatalog.Families())-1 {
		t.Fatalf("turning Codex off stored %v", saved)
	}
	for _, id := range saved {
		if id == "codex" {
			t.Fatalf("Codex survived the toggle: %v", saved)
		}
	}
	if m.agentEnabled("codex") {
		t.Fatal("Codex still reports as offered")
	}

	// Turning it back on restores the unset (offer everything) form.
	activate(t, m, regionAgentToggle+"codex")
	if saved := loadSaved(t).Plugins.Workspace.Agents; len(saved) != 0 {
		t.Fatalf("re-enabling every agent stored %v, want the unset form", saved)
	}
}

// Turning off the last family cannot express "offer nothing" — an empty
// allowlist is the unset state — so the page must say what actually happens.
func TestAgentsEmptyAllowlistIsHonest(t *testing.T) {
	m := workspaceFixture(t, func(cfg *config.Config) {
		cfg.Plugins.Workspace.Agents = []string{"claude"}
	})
	m.Open(PageAgents)
	view := ansi.Strip(m.View(160, 45))
	if strings.Contains(view, "No allowlist is set") {
		t.Fatalf("a narrowed allowlist was described as unset:\n%s", view)
	}

	m.View(160, 45)
	var notice string
	for i, c := range m.controls {
		if c.id != regionAgentToggle+"claude" {
			continue
		}
		cmd := m.runControl(i)
		msg, ok := cmd().(ConfigSavedMsg)
		if !ok {
			t.Fatalf("toggle produced %#v", msg)
		}
		notice = msg.Notice
	}
	if !strings.Contains(notice, "every agent") {
		t.Fatalf("clearing the allowlist said %q", notice)
	}
	state := m.host
	state.Config = loadSaved(t)
	m.SetHostState(state)
	if len(loadSaved(t).Plugins.Workspace.Agents) != 0 {
		t.Fatal("the allowlist was not cleared")
	}
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "No allowlist is set") {
		t.Fatalf("the cleared allowlist was not explained:\n%s", view)
	}
}

func TestAgentLaunchCommandEditAndReset(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageAgents)
	m.View(160, 45)
	m.detailFocus = true

	m.editAgentCommand("claude")
	if m.FocusContext() != ContextConfigEdit {
		t.Fatalf("editing a launch command reported context %q", m.FocusContext())
	}
	// A typed "r" is an r, not the page's Recheck shortcut.
	for _, r := range "claude --resume" {
		m.Key(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, cmd := m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not save the launch command")
	}
	reload(t, m, cmd())
	if got := loadSaved(t).Plugins.Workspace.AgentStart["claude"]; got != "claude --resume" {
		t.Fatalf("launch command saved as %q", got)
	}
	if command, overridden := m.agentCommand("claude"); !overridden || command != "claude --resume" {
		t.Fatalf("page shows %q (overridden=%v)", command, overridden)
	}

	// Emptying the field resets the family to its catalog default.
	m.View(160, 45)
	m.editAgentCommand("claude")
	m.keyFieldClear(t)
	_, cmd = m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not reset the launch command")
	}
	reload(t, m, cmd())
	if _, ok := loadSaved(t).Plugins.Workspace.AgentStart["claude"]; ok {
		t.Fatal("the override survived a reset")
	}
	if command, overridden := m.agentCommand("claude"); overridden || command != "claude" {
		t.Fatalf("after reset the page shows %q (overridden=%v)", command, overridden)
	}
}

// keyFieldClear empties whichever field is being edited.
func (m *Model) keyFieldClear(t *testing.T) {
	t.Helper()
	if m.editor == nil || m.editor.input == nil {
		t.Fatal("no editor is open")
	}
	m.editor.input.SetValue("")
}

// The agent-instructions route is the same one Diagnostics opens, and it
// returns to whichever page sent the user there.
func TestAgentInstructionsReturnsToAgents(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.ApplyChecks(ChecksMsg{Results: configchecks.Results{{
		ID:      configchecks.CheckAgentInstructions,
		Title:   "Agent instructions",
		Summary: "AGENTS.md needs Sidecar guidance",
		Repair:  configchecks.RepairAgentInstructions,
	}}})

	m.Open(PageAgents)
	activate(t, m, regionAgentDocs)
	route := m.Route()
	if route.Child != ChildRepairAgentInstructions {
		t.Fatalf("Agents opened route %#v", route)
	}
	if route.Page != PageAgents {
		t.Fatalf("the repair claimed page %q", route.Page)
	}
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "Back to Agents") {
		t.Fatalf("the child route did not offer a return to Agents:\n%s", view)
	}
	if !m.Escape() {
		t.Fatal("Escape did not leave the repair")
	}
	if m.Page() != PageAgents || m.Route().IsChild() {
		t.Fatalf("returning landed on %#v", m.Route())
	}

	// The same route from Diagnostics returns to Diagnostics.
	m.Navigate(PageDiagnostics)
	m.OpenAgentInstructions()
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "Back to Diagnostics") {
		t.Fatalf("the repair from Diagnostics offered the wrong parent:\n%s", view)
	}
	m.Escape()
	if m.Page() != PageDiagnostics {
		t.Fatalf("returning from Diagnostics landed on %q", m.Page())
	}
}

// A repair route opened on a check that is already passing is where the user
// asked to be. Rechecking from it must not close it: only a problem that has
// just become OK is a resolved repair.
func TestHealthyRepairRouteSurvivesRecheck(t *testing.T) {
	healthy := configchecks.Results{{
		ID:      configchecks.CheckAgentInstructions,
		Title:   "Agent instructions",
		OK:      true,
		Summary: "AGENTS.md connected",
		Repair:  configchecks.RepairAgentInstructions,
	}}

	m := workspaceFixture(t, nil)
	m.ApplyChecks(ChecksMsg{Results: healthy})
	m.Open(PageAgents)
	m.OpenAgentInstructions()
	if m.Route().Child != ChildRepairAgentInstructions {
		t.Fatalf("the healthy route did not open: %#v", m.Route())
	}

	m.ApplyChecks(ChecksMsg{Results: healthy})
	if m.Route().Child != ChildRepairAgentInstructions {
		t.Fatalf("Recheck closed a route the user opened while healthy: %#v", m.Route())
	}

	// A route opened on a real problem still closes itself once the problem is
	// gone, which is the behavior this must not cost.
	failing := configchecks.Results{{
		ID:      configchecks.CheckAgentInstructions,
		Title:   "Agent instructions",
		Summary: "AGENTS.md needs Sidecar guidance",
		Repair:  configchecks.RepairAgentInstructions,
	}}
	m2 := workspaceFixture(t, nil)
	m2.ApplyChecks(ChecksMsg{Results: failing})
	m2.Open(PageAgents)
	m2.OpenAgentInstructions()
	if m2.Route().Child != ChildRepairAgentInstructions {
		t.Fatalf("the failing route did not open: %#v", m2.Route())
	}
	m2.ApplyChecks(ChecksMsg{Results: healthy})
	if m2.Route().IsChild() {
		t.Fatalf("a resolved repair stayed open: %#v", m2.Route())
	}
	if m2.Page() != PageAgents {
		t.Fatalf("a resolved repair returned to %q", m2.Page())
	}
}
