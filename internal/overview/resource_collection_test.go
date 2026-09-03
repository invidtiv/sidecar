package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/pluginbrowser"
	"github.com/marcus/sidecar/internal/pluginhost"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceview"
)

// globalCollectionCalls is a plugin whose describe has already landed and whose
// list answers from memory: the whole pane journey without a process.
type globalCollectionCalls struct {
	lists int
	gets  int
}

func (c *globalCollectionCalls) callsFor() resourceview.CallsFor {
	desc := pluginhost.Description{
		Info: pluginhost.Info{Name: "Recall"},
		Collections: []pluginhost.Collection{{
			ID: "results", Title: "Results", Detail: true,
			Search:  pluginhost.SearchOptional,
			Columns: []pluginhost.Column{{ID: "title", Label: "Title"}},
			// A declared view gives the pane its View control, which is the
			// overlay the keyboard-ownership test opens.
			Views:   []pluginhost.View{{ID: "recent", Title: "Recent"}},
			Refresh: pluginhost.Refresh{EverySeconds: 30},
		}},
	}
	return func(string) pluginbrowser.Calls {
		return pluginbrowser.Calls{
			Describe: func(string) (pluginhost.Description, pluginhost.Status, bool) {
				return desc, pluginhost.Status{State: pluginhost.StateReady}, true
			},
			List: func(call pluginbrowser.ListCall) tea.Cmd {
				c.lists++
				return func() tea.Msg {
					return pluginbrowser.ListedMsg{
						Instance: call.Instance, Browser: call.Browser, Collection: call.Params.Collection,
						Generation: call.Generation,
						Page: pluginhost.Page{
							Outcome: pluginhost.OutcomeAnswered,
							Items: []pluginhost.Item{
								{ID: "rc:notes:1", Cells: map[string]string{"title": "dex schema notes"}},
							},
						},
					}
				}
			},
			Get: func(call pluginbrowser.GetCall) tea.Cmd {
				c.gets++
				return func() tea.Msg {
					return pluginbrowser.GotMsg{
						Instance: call.Instance, Browser: call.Browser, Collection: call.Params.Collection,
						ID: call.Params.ID, Generation: call.Generation,
						Document: resource.Document{Identity: call.Params.ID, Title: "dex schema notes"},
					}
				}
			},
		}
	}
}

// The global Sessions surface's half of the parity assertion: the Resource
// preview answers the collection shape here as well as on the project surface,
// through the same viewer. Its twin is in internal/plugins/workspace.
func TestGlobalSurfaceOpensACollectionTabInTheResourceLeaf(t *testing.T) {
	m := resourcePreviewModel(t)
	calls := &globalCollectionCalls{}
	run(t, m, m.SetPluginCalls(calls.callsFor()))

	run(t, m, m.OpenPreviewResource(resourceview.Ref{
		Instance: "recall", Collection: "results", Query: "dex",
	}))
	res := m.preview.resource
	if res == nil {
		t.Fatal("no Resource pane opened for a collection")
	}
	view := res.view()
	if view == nil || !view.IsPlugin() {
		t.Fatalf("the active tab is not a plugin-shaped tab: %#v", view)
	}
	if calls.lists == 0 {
		t.Fatal("opening a collection tab listed nothing")
	}
	if got := view.Reference(); got.Collection != "results" || got.Query != "dex" {
		t.Fatalf("reference = %#v, want the collection and its query", got)
	}
}

// Enter on a row opens the row as a sibling tab in the same pane, and a second
// Enter focuses it rather than fetching it again. Same contract, same keys, on
// both surfaces.
func TestGlobalEnterOnARowOpensASiblingTab(t *testing.T) {
	m := resourcePreviewModel(t)
	calls := &globalCollectionCalls{}
	run(t, m, m.SetPluginCalls(calls.callsFor()))
	run(t, m, m.OpenPreviewResource(resourceview.Ref{Instance: "recall", Collection: "results"}))

	res := m.preview.resource
	if res == nil || res.tabs == nil {
		t.Fatal("no Resource pane")
	}
	handled, cmd := res.pane.HandleKey("enter")
	if !handled {
		t.Fatal("Enter was not handled by the Resource pane")
	}
	run(t, m, cmd)

	res = m.preview.resource
	if res.tabs.Len() != 2 {
		t.Fatalf("pane holds %d tabs after Enter, want the collection plus the row", res.tabs.Len())
	}
	if got := res.tabs.Active().Reference(); got.Locator != "rc:notes:1" || got.Collection != "results" {
		t.Fatalf("the new tab is %#v, want the row of that collection", got)
	}
	gets := calls.gets

	run(t, m, res.pane.SelectTab(0))
	handled, cmd = res.pane.HandleKey("enter")
	if !handled {
		t.Fatal("the second Enter was not handled")
	}
	run(t, m, cmd)
	if m.preview.resource.tabs.Len() != 2 {
		t.Fatalf("the second Enter opened a third tab (%d)", m.preview.resource.tabs.Len())
	}
	if calls.gets != gets {
		t.Fatalf("the second Enter spent %d more fetches; focusing an open tab costs none", calls.gets-gets)
	}
}

// `sidecar plugin changed` re-lists a visible collection tab of the named
// plugin, and leaves every other plugin's tabs alone.
func TestGlobalPluginChangedRelistsTheNamedPlugin(t *testing.T) {
	m := resourcePreviewModel(t)
	calls := &globalCollectionCalls{}
	run(t, m, m.SetPluginCalls(calls.callsFor()))
	run(t, m, m.OpenPreviewResource(resourceview.Ref{Instance: "recall", Collection: "results"}))
	before := calls.lists

	run(t, m, func() tea.Msg {
		return pluginbrowser.ChangedMsg{Instance: "somebody-else"}
	})
	if calls.lists != before {
		t.Fatalf("a change to another plugin re-listed this one (%d → %d)", before, calls.lists)
	}

	run(t, m, func() tea.Msg {
		return pluginbrowser.ChangedMsg{Instance: "recall", Collection: "results"}
	})
	if calls.lists <= before {
		t.Fatalf("the named plugin's collection did not re-list (%d → %d)", before, calls.lists)
	}
}
