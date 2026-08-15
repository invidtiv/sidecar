package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/workspacelist"
)

func TestViewFlyoutClickAppliesTheClickedSort(t *testing.T) {
	var persisted string
	origSave := saveWorkspaceListSort
	saveWorkspaceListSort = func(label string) error { persisted = label; return nil }
	t.Cleanup(func() { saveWorkspaceListSort = origSave })

	m := catalogModel(t)
	if m.workspaces.Sort() != workspacelist.SortActivity {
		t.Fatalf("precondition: sort = %s, want Activity", m.workspaces.Sort().Label())
	}
	m.openViewFlyout()
	clickViewFlyoutSort(t, m, workspacelist.SortProject)
	if m.workspaces.Sort() != workspacelist.SortProject {
		t.Fatalf("click applied %s, want Project", m.workspaces.Sort().Label())
	}
	if m.ViewFlyoutOpen() {
		t.Fatal("View stayed open after a click")
	}
	if persisted != workspacelist.SortProject.Label() {
		t.Fatalf("persisted %q, want Project", persisted)
	}
}

func TestViewFlyoutClickOnSelectedSortCloses(t *testing.T) {
	m := catalogModel(t)
	before := m.workspaces.Sort()
	m.openViewFlyout()
	clickViewFlyoutSort(t, m, before)
	if m.workspaces.Sort() != before {
		t.Fatalf("click changed sort to %s", m.workspaces.Sort().Label())
	}
	if m.ViewFlyoutOpen() {
		t.Fatal("clicking the current sort left View open")
	}
}

func TestViewFlyoutEnterAppliesTheHighlightedSort(t *testing.T) {
	m := catalogModel(t)
	m.openViewFlyout()
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 'j', Text: "j"}); !handled {
		t.Fatal("j was not handled in the fly-out")
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter}); !handled {
		t.Fatal("enter was not handled in the fly-out")
	}
	if m.workspaces.Sort() != workspacelist.SortProject {
		t.Fatalf("enter applied %s, want Project", m.workspaces.Sort().Label())
	}
	if m.ViewFlyoutOpen() {
		t.Fatal("enter left View open")
	}
}

func clickViewFlyoutSort(t *testing.T, m *Model, mode workspacelist.Sort) {
	t.Helper()
	_ = m.WorkspacesView(80, 24)
	if m.viewFlyoutMouse == nil {
		t.Fatal("view flyout has no mouse handler")
	}
	id := workspacelist.SortActionID(mode)
	var region *mouse.Region
	for _, r := range m.viewFlyoutMouse.HitMap.Regions() {
		if r.ID == id {
			copied := r
			region = &copied
			break
		}
	}
	if region == nil {
		t.Fatalf("no hit region for %s", id)
	}
	hit := m.viewFlyoutMouse.HitMap.Test(region.Rect.X, region.Rect.Y)
	if hit == nil || hit.ID != id {
		t.Fatalf("hit-test on %s = %v, want that row (not the list)", id, hit)
	}
	if cmd := m.WorkspacesMouse(tea.MouseClickMsg{
		X: region.Rect.X, Y: region.Rect.Y, Button: tea.MouseLeft,
	}); cmd != nil {
		_ = cmd()
	}
}
