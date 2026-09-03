package contentpanes

import (
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/pluginbrowser"
	"github.com/marcus/sidecar/internal/pluginhost"
	"github.com/marcus/sidecar/internal/resourceview"
)

func collectionRef(instance, collection, query string) contentlink.Ref {
	return contentlink.Ref{
		Kind: contentlink.KindResource, Provider: instance,
		Collection: collection, Query: query,
	}
}

func itemRef(instance, collection, id string) contentlink.Ref {
	return contentlink.Ref{
		Kind: contentlink.KindResource, Provider: instance,
		Collection: collection, Value: id,
	}
}

// describedCalls is a plugin seam whose describe has already landed, so a
// collection tab has something to be a tab OF without any process running.
func describedCalls(collection string) resourceview.CallsFor {
	desc := pluginhost.Description{
		Collections: []pluginhost.Collection{{
			ID: collection, Title: "Results", Detail: true,
			Columns: []pluginhost.Column{{ID: "title", Label: "Title"}},
		}},
	}
	return func(string) pluginbrowser.Calls {
		return pluginbrowser.Calls{
			Describe: func(string) (pluginhost.Description, pluginhost.Status, bool) {
				return desc, pluginhost.Status{State: pluginhost.StateReady}, true
			},
		}
	}
}

// One Resource leaf, three tab shapes, one viewer. The deck must accept all
// three into the SAME leaf and hand back the same viewer type for each: that is
// what makes both workspace projections inherit the shapes without either
// pane_host.go or either content.go learning anything.
func TestResourceLeafAcceptsEveryTabShape(t *testing.T) {
	d := New(testContext(t.TempDir()), Config{PluginCalls: describedCalls("results")})
	refs := []contentlink.Ref{
		resourceRefForTest("ENG-1"),
		collectionRef("recall", "results", "dex"),
		itemRef("recall", "results", "rc:notes:1"),
	}
	leaf := 0
	for _, ref := range refs {
		out := d.Open(testContext(""), ref, testPlacement())
		if !out.Accepted() {
			t.Fatalf("deck refused %+v: %s", ref, out.Refusal)
		}
		if out.Kind != panelayout.Resource {
			t.Fatalf("%+v opened as %v, want the Resource leaf", ref, out.Kind)
		}
		if leaf == 0 {
			leaf = out.LeafID
		} else if out.LeafID != leaf {
			t.Fatalf("%+v opened a second leaf (%d, want %d)", ref, out.LeafID, leaf)
		}
	}
	tabs, _ := d.Tabs(leaf)
	if len(tabs) != len(refs) {
		t.Fatalf("one leaf holds %d tabs, want %d", len(tabs), len(refs))
	}
	for _, tab := range tabs {
		view, ok := tab.Viewer.(*resourceview.Model)
		if !ok {
			t.Fatalf("tab %+v is rendered by %T, not the shared Resource viewer", tab.Ref, tab.Viewer)
		}
		if view == nil {
			t.Fatalf("tab %+v has no viewer", tab.Ref)
		}
	}
}

// The viewer dispatches on the shape, and only on the shape. A matched document
// renders the resource card; both plugin shapes render the shared browser.
func TestResourceViewerDispatchesOnShape(t *testing.T) {
	d := New(testContext(t.TempDir()), Config{PluginCalls: describedCalls("results")})
	cases := []struct {
		name   string
		ref    contentlink.Ref
		plugin bool
	}{
		{"matched document", resourceRefForTest("ENG-1"), false},
		{"collection tab", collectionRef("recall", "results", "dex"), true},
		{"row tab", itemRef("recall", "results", "rc:notes:1"), true},
	}
	for _, tc := range cases {
		out := d.Open(testContext(""), tc.ref, testPlacement())
		if !out.Accepted() {
			t.Fatalf("%s: refused (%s)", tc.name, out.Refusal)
		}
		view, _ := d.Viewer(out.LeafID).(*resourceview.Model)
		if view == nil {
			t.Fatalf("%s: no viewer", tc.name)
		}
		if got := view.IsPlugin(); got != tc.plugin {
			t.Errorf("%s: IsPlugin() = %v, want %v", tc.name, got, tc.plugin)
		}
	}
}

// Identity is the shape plus what it names, and a collection's identity is
// deliberately NOT its query: retyping a query re-lists the tab that is open
// rather than forking a second one.
func TestCollectionIdentityIgnoresTheQuery(t *testing.T) {
	d := New(testContext(t.TempDir()), Config{PluginCalls: describedCalls("results")})
	first := d.Open(testContext(""), collectionRef("recall", "results", "dex"), testPlacement())
	if !first.Accepted() {
		t.Fatalf("first open refused: %s", first.Refusal)
	}
	again := d.Open(testContext(""), collectionRef("recall", "results", "ongoing"), testPlacement())
	if again.Status != StatusFocused {
		t.Fatalf("a re-queried collection opened a second tab (status %v)", again.Status)
	}
	tabs, _ := d.Tabs(first.LeafID)
	if len(tabs) != 1 {
		t.Fatalf("leaf holds %d tabs, want 1", len(tabs))
	}
	// A collection and a row of it are different tabs, though.
	row := d.Open(testContext(""), itemRef("recall", "results", "rc:notes:1"), testPlacement())
	if row.Status != StatusOpened {
		t.Fatalf("a row of an open collection did not open its own tab (status %v)", row.Status)
	}
}

// The persistence boundary carries the view position of whatever is on screen.
// Encode is what a relaunch reads back, so a query the user typed and never
// saved anywhere else has to be in it.
func TestEncodeKeepsTheCollectionsViewPosition(t *testing.T) {
	d := New(testContext(t.TempDir()), Config{PluginCalls: describedCalls("results")})
	out := d.Open(testContext(""), collectionRef("recall", "results", "dex"), testPlacement())
	if !out.Accepted() {
		t.Fatalf("open refused: %s", out.Refusal)
	}
	st := d.Encode()
	var found *TabState
	var walk func(*NodeState)
	walk = func(n *NodeState) {
		if n == nil || found != nil {
			return
		}
		if n.Kind == "resource" && n.Pane != nil && len(n.Pane.Tabs) > 0 {
			found = &n.Pane.Tabs[0]
			return
		}
		walk(n.A)
		walk(n.B)
	}
	walk(st.Root)
	if found == nil {
		t.Fatal("the encoded state has no resource tab")
	}
	if found.Ref.Collection != "results" {
		t.Fatalf("collection lost: %+v", found.Ref)
	}
	if found.Ref.Query != "dex" {
		t.Fatalf("query lost: %+v", found.Ref)
	}
}

// A collection tab's identity is the collection, so a second open focuses it.
// The view position that open named must still take effect: otherwise
// `sidecar open --plugin recall --collection results --query dex` would mean
// something different the second time it is run.
func TestOpeningAnOpenCollectionAppliesTheNewQuery(t *testing.T) {
	d := New(testContext(t.TempDir()), Config{PluginCalls: describedCalls("results")})
	first := d.Open(testContext(""), collectionRef("recall", "results", "dex"), testPlacement())
	if !first.Accepted() {
		t.Fatalf("first open refused: %s", first.Refusal)
	}
	view, _ := d.Viewer(first.LeafID).(*resourceview.Model)
	if view == nil {
		t.Fatal("no viewer")
	}
	if got := view.Reference().Query; got != "dex" {
		t.Fatalf("first query = %q, want dex", got)
	}
	again := d.Open(testContext(""), collectionRef("recall", "results", "ongoing"), testPlacement())
	if again.Status != StatusFocused {
		t.Fatalf("the second open created a tab instead of focusing (status %v)", again.Status)
	}
	if got := view.Reference().Query; got != "ongoing" {
		t.Fatalf("the focused tab kept query %q; the open named ongoing", got)
	}
	// An open naming no query focuses the tab as it is rather than clearing
	// what the user typed.
	bare := d.Open(testContext(""), collectionRef("recall", "results", ""), testPlacement())
	if bare.Status != StatusFocused {
		t.Fatalf("a bare open created a tab (status %v)", bare.Status)
	}
	if got := view.Reference().Query; got != "ongoing" {
		t.Fatalf("a bare open cleared the query to %q", got)
	}
}
