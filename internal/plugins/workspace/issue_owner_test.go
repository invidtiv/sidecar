package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/panecodec"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
)

func TestIssueTabOwnerFieldsRoundTrip(t *testing.T) {
	p := &Plugin{}
	pane := &issuePane{leafID: 1, root: "/tmp/proj-a"}
	view := p.newIssueModel(pane)
	pane.tabs.Append("td-abc1", view)

	local := panecodec.Encode(issueStateFromView("td-abc1", view), panecodec.Options{})
	if local == nil || len(local.IssueTabs) != 1 {
		t.Fatalf("tabs = %#v", local)
	}
	if local.IssueTabs[0].OwnerName != "" || local.IssueTabs[0].OwnerRoot != "" {
		t.Fatalf("local tab persisted owner fields: %#v", local.IssueTabs[0])
	}

	// The decode path reinstates a persisted adoption; encode must persist it
	// back out so the badge and owning store survive another save.
	view.RestoreOwner("Proj-B", "/tmp/proj-b")
	tabs := panecodec.Encode(issueStateFromView("td-abc1", view), panecodec.Options{})
	if tabs == nil || len(tabs.IssueTabs) != 1 {
		t.Fatalf("owned tabs = %#v", tabs)
	}
	if tabs.IssueTabs[0].OwnerName != "Proj-B" || tabs.IssueTabs[0].OwnerRoot != "/tmp/proj-b" {
		t.Fatalf("owner fields not persisted: %#v", tabs.IssueTabs[0])
	}
	if name, root := view.Owner(); name != "Proj-B" || root != "/tmp/proj-b" {
		t.Fatalf("Owner() = %q, %q after restore", name, root)
	}
}

func issueStateFromView(id string, view *issueview.Model) contentpanes.State {
	tab := contentpanes.TabState{Ref: contentlink.Ref{Kind: contentlink.KindIssue, Value: id}}
	if view != nil {
		tab.Scroll = view.ScrollOffset()
		if name, root := view.Owner(); name != "" && root != "" {
			tab.OwnerName, tab.OwnerRoot = name, root
		}
	}
	return contentpanes.State{Version: 1, Root: &contentpanes.NodeState{
		Kind: "issue",
		Pane: &contentpanes.PaneState{Kind: "issue", Tabs: []contentpanes.TabState{tab}},
	}}
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
	saved := &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
		B: &state.PaneLayoutJSON{
			Kind: contentKindIssue,
			IssueTabs: []state.PaneIssueTabJSON{
				{Issue: "td-abc1", Scroll: 3, OwnerName: "Proj-B", OwnerRoot: "/tmp/proj-b"},
			},
		},
	}}
	st, _ := panecodec.Decode(saved, panecodec.Options{})
	ctx := contentpanes.SurfaceContext{Root: "/tmp/proj-a", Surface: "shell:test", Epoch: 1}
	deck := contentpanes.Decode(ctx, contentpanes.Config{}, st)
	if deck.Leaf(panelayout.Issue) == 0 {
		t.Fatal("decode produced no issue leaf")
	}
	items, _ := deck.Tabs(deck.Leaf(panelayout.Issue))
	if len(items) != 1 {
		t.Fatalf("tabs = %#v", items)
	}
	view, ok := items[0].Viewer.(*issueview.Model)
	if !ok {
		t.Fatalf("viewer = %T", items[0].Viewer)
	}
	if name, root := view.Owner(); name != "Proj-B" || root != "/tmp/proj-b" {
		t.Fatalf("restored Owner() = %q, %q; want Proj-B at /tmp/proj-b", name, root)
	}
	loads := deck.LoadVisible()
	if len(loads) != 1 {
		t.Fatalf("loads = %d commands, want the active tab's fetch", len(loads))
	}
}

func TestDecodeLeafLocalTabHasNoOwner(t *testing.T) {
	saved := &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
		B: &state.PaneLayoutJSON{
			Kind:      contentKindIssue,
			IssueTabs: []state.PaneIssueTabJSON{{Issue: "td-abc2", Scroll: 0}},
		},
	}}
	st, _ := panecodec.Decode(saved, panecodec.Options{})
	ctx := contentpanes.SurfaceContext{Root: "/tmp/proj-a", Surface: "shell:test", Epoch: 1}
	deck := contentpanes.Decode(ctx, contentpanes.Config{}, st)
	items, _ := deck.Tabs(deck.Leaf(panelayout.Issue))
	if len(items) != 1 {
		t.Fatal("decode produced no issue tab")
	}
	view := items[0].Viewer.(*issueview.Model)
	loads := deck.LoadVisible()
	if len(loads) != 1 {
		t.Fatalf("loads = %d, want the active tab's fetch", len(loads))
	}
	if name, root := view.Owner(); name != "" || root != "/tmp/proj-a" {
		t.Fatalf("local restore Owner() = %q, %q; want empty at the pane root", name, root)
	}
}
