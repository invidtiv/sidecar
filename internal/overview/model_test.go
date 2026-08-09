package overview

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestOverviewIncrementalPartialErrorAndCompactStates(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "one", Path: "/tmp/one"}, {Name: "two", Path: "/tmp/two"}}
	m.roots = []string{"/tmp/one", "/tmp/two"}
	m.generation = 7
	m.loading = true
	m.syncBoard()
	if view := m.View(120, 24); !strings.Contains(view, "Loading") {
		t.Fatalf("loading view missing state: %q", view)
	}
	workspace := workspaceinventory.Workspace{ID: "one:worktree:a", ProjectKey: workspaceinventory.CanonicalPath("/tmp/one"), ProjectName: "one", ProjectRoot: "/tmp/one", Kind: workspaceinventory.KindWorktree, Key: "a", Name: "agent", Provider: "codex", Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking, Label: "working", Freshness: agentstatus.FreshnessCurrent}}
	m.Update(projectMsg{Generation: 7, Result: workspaceinventory.ProjectResult{ProjectKey: workspaceinventory.CanonicalPath("/tmp/one"), ProjectName: "one", ProjectRoot: "/tmp/one", Workspaces: []workspaceinventory.Workspace{workspace}}})
	m.Update(projectMsg{Generation: 7, Result: workspaceinventory.ProjectResult{ProjectKey: workspaceinventory.CanonicalPath("/tmp/two"), ProjectName: "two", ProjectRoot: "/tmp/two", Err: errors.New("missing repo")}})
	view := m.View(150, 24)
	if !strings.Contains(view, "one / agent") || !strings.Contains(view, "project unavailable") {
		t.Fatalf("partial/error view = %q", view)
	}
	compact := m.View(60, 12)
	if !strings.Contains(compact, "Agent Overview") || !strings.Contains(compact, "one / agent") {
		t.Fatalf("compact view = %q", compact)
	}
	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not activate selected live card")
	}
	if got, ok := cmd().(NavigateMsg); !ok || got.Workspace.ID != workspace.ID {
		t.Fatalf("activation = %#v", cmd())
	}
}

func TestOverviewRejectsExitedGeneration(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.generation = 2
	m.Stop()
	m.Update(projectMsg{Generation: 2, Result: workspaceinventory.ProjectResult{ProjectKey: "stale"}})
	if len(m.results) != 0 {
		t.Fatal("stale project result applied after Overview exit")
	}
}

func TestOverviewDoubleClickActivatesExactCard(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	workspace := workspaceinventory.Workspace{
		ID:          "two:worktree:agent",
		ProjectKey:  workspaceinventory.CanonicalPath("/tmp/two"),
		ProjectName: "two",
		ProjectRoot: "/tmp/two",
		Kind:        workspaceinventory.KindWorktree,
		Key:         workspaceinventory.CanonicalPath("/tmp/two-agent"),
		Name:        "agent",
		Path:        "/tmp/two-agent",
		Provider:    "codex",
		Presentation: agentstatus.Presentation{
			Lane:      agentstatus.LaneWorking,
			Label:     "working",
			Freshness: agentstatus.FreshnessCurrent,
		},
	}
	m.projects = []Project{{Name: "two", Path: "/tmp/two"}}
	m.results[workspace.ProjectKey] = workspaceinventory.ProjectResult{
		ProjectKey:  workspace.ProjectKey,
		ProjectName: workspace.ProjectName,
		ProjectRoot: workspace.ProjectRoot,
		Workspaces:  []workspaceinventory.Workspace{workspace},
	}
	m.syncBoard()
	m.View(150, 24)
	regions := m.mouse.HitMap.Regions()
	var cardX, cardY int
	found := false
	for _, region := range regions {
		if hit, ok := region.Data.(kanban.HitRegion); ok && hit.CardID == workspace.ID {
			cardX, cardY, found = region.Rect.X, region.Rect.Y, true
			break
		}
	}
	if !found {
		t.Fatalf("card hit region missing: %#v", regions)
	}
	click := tea.MouseClickMsg{X: cardX, Y: cardY, Button: tea.MouseLeft}
	if cmd := m.Update(click); cmd != nil {
		t.Fatal("single click unexpectedly activated card")
	}
	cmd := m.Update(click)
	if cmd == nil {
		t.Fatal("double click did not activate card")
	}
	got, ok := cmd().(NavigateMsg)
	if !ok || got.Workspace.ID != workspace.ID || got.Workspace.Path != workspace.Path {
		t.Fatalf("double-click navigation = %#v", cmd())
	}
}
