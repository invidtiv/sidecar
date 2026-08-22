package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
)

func TestIssueTabOwnerFieldsRoundTrip(t *testing.T) {
	p := &Plugin{}
	pane := &issuePane{leafID: 1, root: "/tmp/proj-a"}
	view := p.newIssueModel(pane)
	pane.tabs.Append("td-abc1", view)

	tabs, active := encodeIssueTabs(pane)
	if active != 0 || len(tabs) != 1 {
		t.Fatalf("tabs = %#v active = %d", tabs, active)
	}
	if tabs[0].OwnerName != "" || tabs[0].OwnerRoot != "" {
		t.Fatalf("local tab persisted owner fields: %#v", tabs[0])
	}

	// The decode path reinstates a persisted adoption; encode must persist it
	// back out so the badge and owning store survive another save.
	view.RestoreOwner("Proj-B", "/tmp/proj-b")
	tabs, _ = encodeIssueTabs(pane)
	if tabs[0].OwnerName != "Proj-B" || tabs[0].OwnerRoot != "/tmp/proj-b" {
		t.Fatalf("owner fields not persisted: %#v", tabs[0])
	}
	if name, root := view.Owner(); name != "Proj-B" || root != "/tmp/proj-b" {
		t.Fatalf("Owner() = %q, %q after restore", name, root)
	}
}

// TestLoadIssueViewHonorsAdoptedWorkDir proves a restored cross-project tab
// loads from its owning store, not the pane root that would re-run the search.
func TestLoadIssueViewHonorsAdoptedWorkDir(t *testing.T) {
	p := &Plugin{ctx: &plugin.Context{}}
	view := issueview.New(nil)
	view.RestoreOwner("Proj-B", "/tmp/proj-b")

	cmd := p.loadIssueView(view, "/tmp/proj-a", "td-abc1")
	if cmd == nil {
		t.Fatal("loadIssueView returned nil")
	}
	// The pure decision loadIssueView makes: an adopted card loads from its
	// owning store, everything else from the pane root it was given.
	if got := issueLoadRoot(view, "/tmp/proj-a"); got != "/tmp/proj-b" {
		t.Fatalf("issueLoadRoot = %q, want the adopted owner root /tmp/proj-b", got)
	}
	plain := issueview.New(nil)
	if got := issueLoadRoot(plain, "/tmp/proj-a"); got != "/tmp/proj-a" {
		t.Fatalf("issueLoadRoot = %q for an unadopted card, want the pane root", got)
	}
}

func TestDecodeLeafReinstatesPersistedOwner(t *testing.T) {
	p := &Plugin{ctx: &plugin.Context{}}
	p.issues = make(map[int]*issuePane)
	saved := &state.PaneLayoutJSON{
		Kind: contentKindIssue,
		IssueTabs: []state.PaneIssueTabJSON{
			{Issue: "td-abc1", Scroll: 3, OwnerName: "Proj-B", OwnerRoot: "/tmp/proj-b"},
		},
		Active: 0,
	}
	var loads []tea.Cmd
	node := p.decodeIssueLeaf(saved, "/tmp/proj-a", &loads)
	if node == nil {
		t.Fatal("decodeIssueLeaf returned nil")
	}
	pane := p.issues[node.ContentID]
	if pane == nil {
		t.Fatal("decoded leaf has no issue pane")
	}
	view := pane.view()
	if view == nil {
		t.Fatal("decoded pane has no view")
	}
	if name, root := view.Owner(); name != "Proj-B" || root != "/tmp/proj-b" {
		t.Fatalf("restored Owner() = %q, %q; want Proj-B at /tmp/proj-b", name, root)
	}
	if len(loads) != 1 {
		t.Fatalf("loads = %d commands, want the active tab's fetch", len(loads))
	}
}

func TestDecodeLeafLocalTabHasNoOwner(t *testing.T) {
	p := &Plugin{ctx: &plugin.Context{}}
	p.issues = make(map[int]*issuePane)
	saved := &state.PaneLayoutJSON{
		Kind:      contentKindIssue,
		IssueTabs: []state.PaneIssueTabJSON{{Issue: "td-abc2", Scroll: 0}},
		Active:    0,
	}
	var loads []tea.Cmd
	node := p.decodeIssueLeaf(saved, "/tmp/proj-a", &loads)
	if node == nil {
		t.Fatal("decodeIssueLeaf returned nil")
	}
	if name, root := p.issues[node.ContentID].view().Owner(); name != "" || root != "/tmp/proj-a" {
		t.Fatalf("local restore Owner() = %q, %q; want empty at the pane root", name, root)
	}
}
