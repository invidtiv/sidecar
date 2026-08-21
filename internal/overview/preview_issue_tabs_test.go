package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func previewIssueTabIDs(issue *previewIssue) []string {
	if issue == nil {
		return nil
	}
	ids := make([]string, 0, len(issue.tabs.Items))
	for _, item := range issue.tabs.Items {
		if item.Value != nil {
			ids = append(ids, item.Value.IssueID())
		} else {
			ids = append(ids, item.Key)
		}
	}
	return ids
}

func openTwoPreviewIssues(t *testing.T, kind workspaceinventory.Kind) *Model {
	t.Helper()
	stubPreviewTd(t)
	m := linkPreviewModel(t, kind)
	run(t, m, m.openPreviewIssue("td-1111aa"))
	run(t, m, m.openPreviewIssue("td-2222bb"))
	issue := m.preview.issue
	if issue == nil {
		t.Fatal("no issue pane after opening two issues")
	}
	if got := previewIssueTabIDs(issue); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" {
		t.Fatalf("tabs after two opens = %v, want [td-1111aa td-2222bb]", got)
	}
	if issue.view() == nil || issue.view().IssueID() != "td-2222bb" || issue.tabs.Active != 1 {
		t.Fatalf("active after two opens = %q idx=%d, want td-2222bb", issue.view().IssueID(), issue.tabs.Active)
	}
	if issue.tabs.Items[0].Value.ModelID() == issue.tabs.Items[1].Value.ModelID() {
		t.Fatal("two tabs share a model ID")
	}
	return m
}

func addPreviewWorkspace(t *testing.T, m *Model, id, name string) {
	t.Helper()
	result := m.results["sidecar"]
	other := result.Workspaces[0]
	other.ID, other.Name = id, name
	result.Workspaces = append(result.Workspaces, other)
	m.results["sidecar"] = result
	m.syncBoard()
}

func visualPreviewIssueIDPoint(t *testing.T, m *Model, issueID string) (x, y int, ok bool) {
	t.Helper()
	if m.preview.issue == nil {
		return 0, 0, false
	}
	index := m.preview.issue.tabs.Find(issueID)
	if index < 0 {
		return 0, 0, false
	}
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		hit, isTab := region.Data.(previewIssueTabHit)
		if !isTab || hit.Close || hit.Index != index {
			continue
		}
		return region.Rect.X + max(region.Rect.W/2, 0), region.Rect.Y, true
	}
	return 0, 0, false
}

func TestGlobalIssueTabsClickAndCycle(t *testing.T) {
	for _, kind := range []workspaceinventory.Kind{workspaceinventory.KindWorktree, workspaceinventory.KindShell} {
		t.Run(string(kind), func(t *testing.T) {
			m := openTwoPreviewIssues(t, kind)
			issue := m.preview.issue
			m.WorkspacesView(previewWide, previewTall)

			x, y, ok := visualPreviewIssueIDPoint(t, m, "td-1111aa")
			if !ok {
				t.Fatal("td-1111aa is not on the issue header")
			}
			resolved := m.workspacesMouse.HitMap.Test(x, y)
			if hit, isTab := resolved.Data.(previewIssueTabHit); !isTab || hit.Close || hit.Index != 0 {
				t.Fatalf("visible ID at (%d,%d) resolves to %#v, want tab 0", x, y, resolved)
			}
			run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}))
			if issue.view().IssueID() != "td-1111aa" || issue.tabs.Active != 0 {
				t.Fatalf("clicking td-1111aa selected %q", issue.view().IssueID())
			}
			if len(issue.tabs.Items) != 2 {
				t.Fatalf("click created a tab: %v", previewIssueTabIDs(issue))
			}
			if !issue.focused || !m.PreviewFocused() {
				t.Fatal("tab click handed the keyboard back to the list")
			}

			handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: '}', Text: "}"})
			if !handled || issue.view().IssueID() != "td-2222bb" {
				t.Fatalf("} selected %q, want td-2222bb", issue.view().IssueID())
			}
			handled, _ = m.WorkspacesKey(tea.KeyPressMsg{Code: '{', Text: "{"})
			if !handled || issue.view().IssueID() != "td-1111aa" {
				t.Fatalf("{ selected %q, want td-1111aa", issue.view().IssueID())
			}
		})
	}
}

func TestGlobalIssueTabsKeepIndependentScroll(t *testing.T) {
	m := openTwoPreviewIssues(t, workspaceinventory.KindWorktree)
	issue := m.preview.issue

	second := issue.view()
	data := *second.Data()
	data.Description = strings.Repeat("second body\n\n", 30)
	second.SetData(&data)
	second.SetSize(40, 3)
	second.Scroll(4)
	scroll2 := second.ScrollOffset()
	if scroll2 == 0 {
		t.Fatal("second tab did not scroll")
	}

	if handled, _ := m.previewIssueKey(tea.KeyPressMsg{Code: '{', Text: "{"}); !handled {
		t.Fatal("{ did not cycle")
	}
	first := issue.view()
	if first == second || first.IssueID() != "td-1111aa" {
		t.Fatalf("cycle selected %q", first.IssueID())
	}
	data = *first.Data()
	data.Description = strings.Repeat("first body\n\n", 30)
	first.SetData(&data)
	first.SetSize(40, 3)
	if first.ScrollOffset() != 0 {
		t.Fatalf("first tab inherited second's scroll %d", first.ScrollOffset())
	}
	first.Scroll(2)
	scroll1 := first.ScrollOffset()

	m.previewIssueKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	if issue.view().ScrollOffset() != scroll2 {
		t.Fatalf("second tab scroll = %d, want %d", issue.view().ScrollOffset(), scroll2)
	}
	m.previewIssueKey(tea.KeyPressMsg{Code: '{', Text: "{"})
	if issue.view().ScrollOffset() != scroll1 {
		t.Fatalf("first tab scroll = %d, want %d", issue.view().ScrollOffset(), scroll1)
	}
}

func TestGlobalIssueOpenFocusesExistingIDWithoutDuplicating(t *testing.T) {
	m := openTwoPreviewIssues(t, workspaceinventory.KindShell)
	issue := m.preview.issue
	run(t, m, m.openPreviewIssue("td-1111aa"))
	if got := previewIssueTabIDs(issue); len(got) != 2 || issue.view().IssueID() != "td-1111aa" || issue.tabs.Active != 0 {
		t.Fatalf("reopen = tabs %v active=%d, want focus without a third tab", got, issue.tabs.Active)
	}
}

func TestGlobalIssueEnterOpensParentOrSubtaskAsATab(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewIssue("td-1111aa"))
	issue := m.preview.issue
	issue.view().SetData(&issueview.Data{
		ID: "td-1111aa", Title: "Parent", Status: "open", Type: "epic",
		Children: []issueview.Ref{{ID: "td-2222bb", Title: "Child", Status: "open", Type: "task"}},
	})
	issue.view().SetActive(true)
	issue.view().SetFocused(true)
	issue.view().HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	issue.view().HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if issue.view().SelectedID() != "td-2222bb" {
		t.Fatalf("selected %q, want the child row", issue.view().SelectedID())
	}

	handled, cmd := m.previewIssueKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || cmd == nil || issue.view().IssueID() != "td-2222bb" {
		t.Fatalf("enter: handled=%v cmd=%v issue=%q", handled, cmd != nil, issue.view().IssueID())
	}
	if got := previewIssueTabIDs(issue); len(got) != 2 || got[0] != "td-1111aa" || got[1] != "td-2222bb" {
		t.Fatalf("enter tabs = %v, want parent kept and child appended", got)
	}
	run(t, m, cmd)
	issue.view().SetData(&issueview.Data{
		ID: "td-2222bb", Title: "Child", Status: "open", Type: "task",
		ParentID: "td-1111aa",
		Parent:   &issueview.Ref{ID: "td-1111aa", Title: "Parent", Status: "open", Type: "epic"},
	})
	issue.view().SetActive(true)
	_, _ = issue.view().HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if issue.view().SelectedID() != "td-1111aa" {
		t.Fatalf("selected %q, want the parent row", issue.view().SelectedID())
	}

	handled, cmd = m.previewIssueKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled {
		t.Fatal("enter on the parent row was not handled")
	}
	if issue.view().IssueID() != "td-1111aa" || len(issue.tabs.Items) != 2 {
		t.Fatalf("enter on existing parent = %v, want a focus not a third tab", previewIssueTabIDs(issue))
	}
	if cmd != nil {
		t.Fatal("focusing an already-open parent scheduled a load")
	}
}

func TestGlobalIssueCloseTabThenLastTabClosesThePane(t *testing.T) {
	m := openTwoPreviewIssues(t, workspaceinventory.KindWorktree)
	issue := m.preview.issue

	handled, cmd := m.previewIssueKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !handled || cmd != nil || m.preview.issue == nil {
		t.Fatalf("x closed the pane with two tabs: handled=%v cmd=%v", handled, cmd != nil)
	}
	if got := previewIssueTabIDs(issue); len(got) != 1 || got[0] != "td-1111aa" {
		t.Fatalf("x left %v, want the first tab", got)
	}

	handled, cmd = m.previewIssueKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	run(t, m, cmd)
	if !handled || m.preview.issue != nil || m.PreviewFocused() {
		t.Fatalf("last x did not close the pane: handled=%v issue=%#v focused=%v", handled, m.preview.issue, m.PreviewFocused())
	}
}

func TestGlobalIssueTabsSurviveRowSwitchInMemoryOnly(t *testing.T) {
	m := openTwoPreviewIssues(t, workspaceinventory.KindShell)
	issue := m.preview.issue
	wantIDs := previewIssueTabIDs(issue)
	wantActive := issue.tabs.Active
	firstID := issue.tabs.Items[0].Value.ModelID()
	secondID := issue.tabs.Items[1].Value.ModelID()

	addPreviewWorkspace(t, m, "b", "beta")
	m.workspaces.SelectID("b")
	run(t, m, m.previewSync())
	if m.preview.issue != nil {
		t.Fatalf("workspace b inherited a's issue tabs: %#v", previewIssueTabIDs(m.preview.issue))
	}

	m.workspaces.SelectID("a")
	run(t, m, m.previewSync())
	restored := m.preview.issue
	if restored == nil {
		t.Fatal("switching back did not restore the in-memory issue tabs")
	}
	if got := previewIssueTabIDs(restored); len(got) != 2 || got[0] != wantIDs[0] || got[1] != wantIDs[1] || restored.tabs.Active != wantActive {
		t.Fatalf("restored tabs = %v active=%d, want %v active=%d", got, restored.tabs.Active, wantIDs, wantActive)
	}
	if restored.tabs.Items[0].Value.ModelID() != firstID || restored.tabs.Items[1].Value.ModelID() != secondID {
		t.Fatal("row switch allocated new models instead of restoring the cached tabs")
	}
}

func TestGlobalIssueQForgetsInMemoryTabs(t *testing.T) {
	m := openTwoPreviewIssues(t, workspaceinventory.KindWorktree)
	addPreviewWorkspace(t, m, "b", "beta")

	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	run(t, m, cmd)
	if !handled || m.preview.issue != nil {
		t.Fatalf("q handled=%v issue=%#v", handled, m.preview.issue)
	}

	m.workspaces.SelectID("b")
	run(t, m, m.previewSync())
	m.workspaces.SelectID("a")
	run(t, m, m.previewSync())
	if m.preview.issue != nil {
		t.Fatalf("q left a cache that row-switch restored: %v", previewIssueTabIDs(m.preview.issue))
	}
}

func TestGlobalIssueLoadFinishesWhileItsWorkspaceIsCached(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	load := m.openPreviewIssue("td-1111aa")
	if load == nil || m.preview.issue == nil {
		t.Fatal("issue did not begin loading")
	}
	addPreviewWorkspace(t, m, "b", "beta")
	m.workspaces.SelectID("b")
	run(t, m, m.previewSync())
	run(t, m, load)
	if m.preview.issue != nil {
		t.Fatal("cached load restored the issue onto workspace b")
	}
	m.workspaces.SelectID("a")
	run(t, m, m.previewSync())
	if m.preview.issue == nil || m.preview.issue.view() == nil ||
		m.preview.issue.view().Data() == nil || m.preview.issue.view().Data().ID != "td-1111aa" {
		t.Fatalf("cached issue load did not finish: %#v", m.preview.issue)
	}
}

func TestGlobalIssueStaleLoadForClosedTabOrOtherWorkspaceIsIgnored(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	firstCmd := m.openPreviewIssue("td-1111aa")
	first := previewIssueResult(t, firstCmd)
	if first.IssueID != "td-1111aa" {
		t.Fatalf("first load = %#v", first)
	}
	run(t, m, m.openPreviewIssue("td-2222bb"))
	issue := m.preview.issue
	secondID := issue.view().ModelID()

	if handled, _ := m.previewIssueKey(tea.KeyPressMsg{Code: '{', Text: "{"}); !handled {
		t.Fatal("could not select the first tab to close it")
	}
	m.previewIssueKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if issue = m.preview.issue; issue == nil || issue.view().IssueID() != "td-2222bb" {
		t.Fatalf("after closing first tab: %v", previewIssueTabIDs(issue))
	}

	m.Update(first)
	if issue.view().IssueID() != "td-2222bb" || issue.view().Data() == nil || issue.view().Data().ID != "td-2222bb" {
		t.Fatalf("closed tab's result landed on the survivor: %#v", issue.view().Data())
	}

	addPreviewWorkspace(t, m, "b", "beta")
	m.workspaces.SelectID("b")
	run(t, m, m.previewSync())
	run(t, m, m.openPreviewIssue("td-3333cc"))
	bIssue := m.preview.issue
	if bIssue == nil || bIssue.view().IssueID() != "td-3333cc" {
		t.Fatalf("workspace b issue = %v", previewIssueTabIDs(bIssue))
	}
	m.Update(previewIssueLoadedMsg{
		LoadedMsg: issueview.LoadedMsg{
			ModelID:           secondID,
			RequestGeneration: 1,
			Epoch:             bIssue.epoch,
			IssueID:           "td-2222bb",
			Data:              &issueview.Data{ID: "td-2222bb", Title: "stolen"},
		},
		WorkspaceID: "b",
	})
	if bIssue.view().IssueID() != "td-3333cc" {
		t.Fatalf("other workspace result retargeted b to %q", bIssue.view().IssueID())
	}
	m.Update(previewIssueLoadedMsg{
		LoadedMsg: issueview.LoadedMsg{
			ModelID:           bIssue.view().ModelID() + 99,
			RequestGeneration: 1,
			Epoch:             bIssue.epoch,
			IssueID:           "td-4444dd",
			Data:              &issueview.Data{ID: "td-4444dd", Title: "foreign"},
		},
		WorkspaceID: "b",
	})
	if bIssue.view().IssueID() != "td-3333cc" {
		t.Fatalf("foreign model id retargeted the tab to %q", bIssue.view().IssueID())
	}
}

func TestGlobalIssueHeaderHasNoCloseChipOrHint(t *testing.T) {
	m := openTwoPreviewIssues(t, workspaceinventory.KindWorktree)
	m.WorkspacesView(previewWide, previewTall)
	strip := issueview.LayoutTabStrip(m.preview.issue.tabs, 48, true)
	got := ansi.Strip(strip.Row)
	if strings.Contains(got, "q close") {
		t.Fatalf("issue strip still has chips/hints: %q", got)
	}
	if strings.Count(got, "×") != 2 {
		t.Fatalf("issue strip = %q, want one × per tab", got)
	}
	if !strings.Contains(got, "td-1111aa") || !strings.Contains(got, "td-2222bb") {
		t.Fatalf("issue strip dropped a tab: %q", got)
	}
}
