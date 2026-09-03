package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/pluginbrowser"
	"github.com/marcus/sidecar/internal/pluginhost"
	"github.com/marcus/sidecar/internal/resourceview"
)

// collectionPluginOn builds the collection fixture over a caller-supplied root,
// so a second plugin can be built on the same workspace and stand in for the
// next process.
func collectionPluginOn(t *testing.T, root string) (*Plugin, *collectionCalls, string, string) {
	t.Helper()
	p := docPaneTestPlugin(t, root, true)
	calls := &collectionCalls{}
	p.SetPluginCalls(calls.callsFor())
	resolved, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("no selected terminal surface")
	}
	return p, calls, resolved, surface
}

// Closing sidecar with a collection tab open and reopening puts the tab back.
// The assertion runs the whole journey — open, save the record a next process
// would read, restore into a fresh plugin — because the codec round trip alone
// does not prove the surface writes the collection shape or reads it back.
func TestRelaunchRestoresACollectionTab(t *testing.T) {
	dir := t.TempDir()
	p, _, root, surface := collectionPluginOn(t, dir)
	runDeckCmd(t, p, p.openRequestedResourcePaneForSurface(root, surface, resourceview.Ref{
		Instance: "recall", Collection: "results", Query: "dex",
	}))

	layout := p.persistedPaneLayout()
	if firstSavedResourceTab(layout) == nil {
		t.Fatalf("no resource tab in the saved layout: %#v", layout)
	}

	restored, _, _, _ := collectionPluginOn(t, dir)
	if cmd := restored.restorePaneLayout(layout); cmd == nil {
		t.Fatal("restoring a layout holding a collection tab loaded nothing")
	}
	back, leaf := restored.activeResourcePane()
	if back == nil || leaf == nil {
		t.Fatal("the Resource leaf did not come back after a relaunch")
	}
	refs := back.tabs.References()
	if len(refs) != 1 {
		t.Fatalf("restored tabs = %#v, want the one collection tab", refs)
	}
	if refs[0].Provider != "recall" || refs[0].Collection != "results" || refs[0].Query != "dex" {
		t.Fatalf("restored reference = %#v, want the collection and the typed query", refs[0])
	}
	if refs[0].Matcher != "" || refs[0].Locator != "" {
		t.Fatalf("the collection came back wearing the matched shape: %#v", refs[0])
	}
	if view := back.tabs.Active(); view == nil || !view.IsPlugin() {
		t.Fatalf("the restored tab is not plugin-shaped: %#v", view)
	}
}

// A tab whose plugin cannot answer — disabled, not installed, a describe that
// never lands — is kept and saved again. Pruning it would delete the user's tab
// because a tool happened to be off when sidecar started.
func TestADisabledPluginKeepsItsArmedTab(t *testing.T) {
	dir := t.TempDir()
	p, _, root, surface := collectionPluginOn(t, dir)
	runDeckCmd(t, p, p.openRequestedResourcePaneForSurface(root, surface, resourceview.Ref{
		Instance: "recall", Collection: "results", Query: "dex",
	}))
	layout := p.persistedPaneLayout()

	// The next process finds the plugin disabled: no description, no status.
	silent := func(string) pluginbrowser.Calls {
		return pluginbrowser.Calls{
			Describe: func(string) (pluginhost.Description, pluginhost.Status, bool) {
				return pluginhost.Description{}, pluginhost.Status{}, false
			},
		}
	}
	restored := docPaneTestPlugin(t, dir, true)
	restored.SetPluginCalls(silent)
	restored.restorePaneLayout(layout)

	back, _ := restored.activeResourcePane()
	if back == nil {
		t.Fatal("a disabled plugin took the Resource leaf away with it")
	}
	refs := back.tabs.References()
	if len(refs) != 1 || refs[0].Collection != "results" || refs[0].Query != "dex" {
		t.Fatalf("armed tabs = %#v, want the collection tab preserved", refs)
	}
	// And it survives the next save, so the tab is still there the time after.
	again := firstSavedResourceTab(restored.persistedPaneLayout())
	if again == nil || again.Collection != "results" || again.Query != "dex" {
		t.Fatalf("re-saved tab = %#v, want the collection tab still recorded", again)
	}
	// Whatever it draws, it draws inside the box it was given.
	if view := back.tabs.Active(); view != nil {
		view.SetSize(60, 10)
		if got := countLines(view.View()); got > 10 {
			t.Fatalf("the loading card rendered %d lines into a 10-line pane", got)
		}
	}
}

// The map-to-deck projection carries all three tab shapes. It is the other half
// of the saved layout: a shape dropped here vanishes from the record whenever
// the deck is rebuilt from the pane maps, with nothing on screen having changed.
func TestThePaneMapProjectionCarriesACollectionTab(t *testing.T) {
	p, _, root, surface := collectionTestPlugin(t)
	runDeckCmd(t, p, p.openRequestedResourcePaneForSurface(root, surface, resourceview.Ref{
		Instance: "recall", Collection: "results", Query: "dex",
	}))
	res, _ := p.activeResourcePane()
	if res == nil {
		t.Fatal("no Resource leaf opened for a collection")
	}
	pane := resourcePaneState(res)
	if pane == nil || len(pane.Tabs) != 1 {
		t.Fatalf("projected pane = %#v, want the one collection tab", pane)
	}
	ref := pane.Tabs[0].Ref
	if ref.Kind != contentlink.KindResource || ref.Provider != "recall" ||
		ref.Collection != "results" || ref.Query != "dex" {
		t.Fatalf("projected ref = %#v, want the collection shape", ref)
	}
	if ref.Matcher != "" || ref.Value != "" {
		t.Fatalf("the collection was projected as a matched document: %#v", ref)
	}
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := 1
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}
	return n
}
