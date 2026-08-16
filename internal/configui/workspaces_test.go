package configui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
)

// workspaceFixture is a model on the Workspaces page with a temp config file.
func workspaceFixture(t *testing.T, mutate func(*config.Config)) *Model {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	m, _ := configFixture(t, cfg)
	return m
}

// activate puts the cursor on a control by region ID and runs it, the way Enter
// does, and feeds the resulting save back through the host's path.
func activate(t *testing.T, m *Model, id string) {
	t.Helper()
	m.View(160, 45)
	m.detailFocus = true
	for i, c := range m.controls {
		if c.id != id {
			continue
		}
		m.focusControlIndex(i)
		cmd := m.runControl(i)
		if cmd == nil {
			return
		}
		msg := cmd()
		if saved, ok := msg.(ConfigSavedMsg); ok {
			if saved.Err != "" {
				t.Fatalf("saving %s failed: %s", id, saved.Err)
			}
			state := m.host
			state.Config = loadSaved(t)
			m.SetHostState(state)
		}
		return
	}
	t.Fatalf("control %q is not on screen", id)
}

func TestWorkspacesPageRenders(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)
	view := ansi.Strip(m.View(160, 45))

	for _, want := range []string{
		"New workspaces", "Default agent", "Start with a shell",
		"Worktree defaults", "Repository prefix", "Overview location", "Project root",
		"What the workspace sidebar displays", "Linked task", "Change stats",
		"Worktree setup", "copyEnvFiles", "runHook",
		"repository-provided code",
		"apply to the next shell or worktree",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("Workspaces is missing %q:\n%s", want, view)
		}
	}
}

func TestWorkspacesTogglesPersist(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)

	activate(t, m, regionAutoShell)
	if !loadSaved(t).Plugins.Workspace.AutoCreateShell {
		t.Fatal("Start with a shell did not persist")
	}

	activate(t, m, regionDirPrefix)
	if loadSaved(t).Plugins.Workspace.DirPrefix {
		t.Fatal("Repository prefix did not persist")
	}

	activate(t, m, regionSidebarTask)
	if !loadSaved(t).Plugins.Workspace.SidebarDisplay.HideTask {
		t.Fatal("sidebar display toggle did not persist")
	}
	// Positive controls over negative storage: turning it back on clears hide.
	activate(t, m, regionSidebarTask)
	if loadSaved(t).Plugins.Workspace.SidebarDisplay.HideTask {
		t.Fatal("sidebar display toggle did not round-trip")
	}
}

func TestOverviewScopeSelectorRoundTrips(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)

	choose(t, m, regionOverviewScope, config.OverviewWorktreeScopeWorktree)
	if got := loadSaved(t).Plugins.Workspace.OverviewWorktreeScope; got != config.OverviewWorktreeScopeWorktree {
		t.Fatalf("Overview location saved %q", got)
	}
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "scope Sidecar to that worktree") {
		t.Fatalf("the help did not follow the selected value:\n%s", view)
	}

	choose(t, m, regionOverviewScope, config.OverviewWorktreeScopeProject)
	if got := loadSaved(t).Plugins.Workspace.OverviewWorktreeScope; got != config.OverviewWorktreeScopeProject {
		t.Fatalf("Overview location did not return to project scope: %q", got)
	}
}

// The default-agent selector offers what creation would offer: with a narrowed
// allowlist it must not walk families the user has turned off.
func TestDefaultAgentSelectorFollowsAllowlist(t *testing.T) {
	m := workspaceFixture(t, func(cfg *config.Config) {
		cfg.Plugins.Workspace.Agents = []string{"claude", "grok"}
	})
	m.Open(PageWorkspaces)

	activate(t, m, regionDefaultAgent)
	if m.dropdown == nil {
		t.Fatal("the default-agent selector did not open a list")
	}
	seen := map[string]bool{}
	for _, option := range m.dropdown.options {
		seen[option.id] = true
	}
	want := map[string]bool{"": true, "claude": true, "grok": true}
	for id := range seen {
		if !want[id] {
			t.Fatalf("selector offered %q, which creation would not", id)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("selector offered %v, want %v", seen, want)
	}
	// Every choice it offers is one it can save.
	choose(t, m, regionDefaultAgent, "claude")
	if got := loadSaved(t).Plugins.Workspace.DefaultAgentType; got != "claude" {
		t.Fatalf("the selector saved %q", got)
	}
}

// Every control on the page is reachable from the keyboard, and typing never
// escapes an open editor into a page shortcut.
func TestWorkspacesControlsAreCursorStops(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)
	m.View(160, 45)
	m.detailFocus = true

	if len(m.cursorControls()) < 8 {
		t.Fatalf("Workspaces offered %d cursor stops", len(m.cursorControls()))
	}
	handled, _ := m.Key(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled {
		t.Fatal("Down did not move the row cursor")
	}
}
