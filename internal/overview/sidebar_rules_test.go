package overview

import (
	"reflect"
	"testing"
)

func TestSidebarPinPruningUsesCatalogBeforeViewFilters(t *testing.T) {
	original := savePinnedWorkspaceIDs
	var saved []string
	savePinnedWorkspaceIDs = func(ids []string) error {
		saved = append([]string(nil), ids...)
		return nil
	}
	t.Cleanup(func() { savePinnedWorkspaceIDs = original })
	m := catalogModel(t)
	m.loading = false
	m.showIdleWorktrees = false
	m.workspaces.SetPinned([]string{"gone", "s3", "s1"})
	m.workspaces.Filter().SetQuery("pipeline")
	m.syncWorkspaces()
	if visible := m.workspaces.Visible(); len(visible) != 1 || visible[0].ID != "b1" {
		t.Fatalf("query did not narrow the view: %+v", visible)
	}
	if got := m.workspaces.PinnedIDs(); !reflect.DeepEqual(got, []string{"s3", "s1"}) {
		t.Fatalf("view filters deleted catalog pins: %v", got)
	}
	if !reflect.DeepEqual(saved, []string{"s3", "s1"}) {
		t.Fatalf("persisted pins = %v, want hidden and query-excluded identities retained", saved)
	}
	m.workspaces.ClearFilter()
	// An ordinary idle row remains hidden; the pin alone is not a reveal.
	if m.workspaces.SelectID("s3") {
		t.Fatal("pin changed the visibility rule for an idle row")
	}
	if got := m.workspaces.Visible()[0].ID; got != "s1" {
		t.Fatalf("visible pin did not regain its ordered position: %s", got)
	}
}

func TestSidebarPinPruningWaitsForCompleteInventory(t *testing.T) {
	original := savePinnedWorkspaceIDs
	saves := 0
	savePinnedWorkspaceIDs = func([]string) error { saves++; return nil }
	t.Cleanup(func() { savePinnedWorkspaceIDs = original })
	m := catalogModel(t)
	m.workspaces.SetPinned([]string{"not-collected-yet", "s1"})
	m.loading = true
	m.syncWorkspaces()
	if got := m.workspaces.PinnedIDs(); !reflect.DeepEqual(got, []string{"not-collected-yet", "s1"}) || saves != 0 {
		t.Fatalf("partial inventory pruned durable pins: pins=%v saves=%d", got, saves)
	}
	m.loading = false
	m.syncWorkspaces()
	if got := m.workspaces.PinnedIDs(); !reflect.DeepEqual(got, []string{"s1"}) || saves != 1 {
		t.Fatalf("complete inventory failed to prune absent identity: pins=%v saves=%d", got, saves)
	}
}
