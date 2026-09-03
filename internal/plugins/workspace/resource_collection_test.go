package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/pluginbrowser"
	"github.com/marcus/sidecar/internal/pluginhost"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/state"
)

// collectionCalls is a plugin whose describe has already landed and whose list
// answers from memory. No process, no network, no credential: the whole pane
// journey is provable without one.
type collectionCalls struct {
	lists int
	gets  int
}

func (c *collectionCalls) callsFor() resourceview.CallsFor {
	desc := pluginhost.Description{
		Info: pluginhost.Info{Name: "Recall"},
		Collections: []pluginhost.Collection{{
			ID: "results", Title: "Results", Detail: true,
			Search:  pluginhost.SearchOptional,
			Columns: []pluginhost.Column{{ID: "title", Label: "Title"}},
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
						Instance: call.Instance, Collection: call.Params.Collection,
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
						Instance: call.Instance, Collection: call.Params.Collection,
						ID: call.Params.ID, Generation: call.Generation,
						Document: resource.Document{Identity: call.Params.ID, Title: "dex schema notes"},
					}
				}
			},
		}
	}
}

func collectionTestPlugin(t *testing.T) (*Plugin, *collectionCalls, string, string) {
	t.Helper()
	p := docPaneTestPlugin(t, t.TempDir(), true)
	calls := &collectionCalls{}
	p.SetPluginCalls(calls.callsFor())
	root, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("no selected terminal surface")
	}
	return p, calls, root, surface
}

// The project surface's half of the parity assertion: the Resource leaf answers
// the collection shape here as well as on the global Sessions surface, through
// the same viewer. Its twin lives in internal/overview.
func TestProjectSurfaceOpensACollectionTabInTheResourceLeaf(t *testing.T) {
	p, calls, root, surface := collectionTestPlugin(t)
	cmd := p.openRequestedResourcePaneForSurface(root, surface, resourceview.Ref{
		Instance: "recall", Collection: "results", Query: "dex",
	})
	runDeckCmd(t, p, cmd)

	res, leaf := p.activeResourcePane()
	if res == nil || leaf == nil {
		t.Fatal("no Resource leaf opened for a collection")
	}
	view := res.tabs.Active()
	if view == nil || !view.IsPlugin() {
		t.Fatalf("the active tab is not a plugin-shaped tab: %#v", view)
	}
	if calls.lists == 0 {
		t.Fatal("opening a collection tab listed nothing")
	}
	if got := view.Reference(); got.Collection != "results" || got.Query != "dex" {
		t.Fatalf("reference = %#v, want the collection and its query", got)
	}
	// The same leaf a matched document uses: adding a shape must not add a kind.
	if leaf.Kind != PaneResource {
		t.Fatalf("collection tab landed in a %v leaf", leaf.Kind)
	}
}

// Enter on a row opens the row as a SECOND tab in the same leaf, and a second
// Enter focuses it rather than fetching it again.
func TestEnterOnARowOpensASiblingTabAndASecondEnterFocusesIt(t *testing.T) {
	p, calls, root, surface := collectionTestPlugin(t)
	runDeckCmd(t, p, p.openRequestedResourcePaneForSurface(root, surface, resourceview.Ref{
		Instance: "recall", Collection: "results",
	}))
	res, leaf := p.activeResourcePane()
	if res == nil {
		t.Fatal("no Resource leaf")
	}
	p.paneFocus, p.activePane = leaf.ID, PanePreview

	handled, cmd := p.handleResourceKey(keyPressMsg("enter"))
	if !handled {
		t.Fatal("Enter was not handled by the focused Resource leaf")
	}
	runDeckCmd(t, p, cmd)

	res, _ = p.activeResourcePane()
	if res.tabs.Len() != 2 {
		t.Fatalf("leaf holds %d tabs after Enter, want the collection plus the row", res.tabs.Len())
	}
	if got := res.tabs.Active().Reference(); got.Locator != "rc:notes:1" || got.Collection != "results" {
		t.Fatalf("the new tab is %#v, want the row of that collection", got)
	}
	gets := calls.gets
	if gets == 0 {
		t.Fatal("Enter fetched no document")
	}

	// The row tab is now the active one; selecting the collection again and
	// pressing Enter on the same row must focus rather than refetch.
	runDeckCmd(t, p, p.selectResourceTab(res, 0))
	handled, cmd = p.handleResourceKey(keyPressMsg("enter"))
	if !handled {
		t.Fatal("the second Enter was not handled")
	}
	runDeckCmd(t, p, cmd)
	res, _ = p.activeResourcePane()
	if res.tabs.Len() != 2 {
		t.Fatalf("the second Enter opened a third tab (%d)", res.tabs.Len())
	}
	if calls.gets != gets {
		t.Fatalf("the second Enter spent %d more fetches; focusing an open tab costs none", calls.gets-gets)
	}
}

// Relaunch restores the collection tab with the query the user typed. The
// persisted record is what a fresh process reads, so the assertion is over the
// saved layout rather than over anything held in memory.
func TestACollectionTabPersistsItsQuery(t *testing.T) {
	p, _, root, surface := collectionTestPlugin(t)
	runDeckCmd(t, p, p.openRequestedResourcePaneForSurface(root, surface, resourceview.Ref{
		Instance: "recall", Collection: "results", Query: "dex",
	}))
	saved := p.paneLayoutJSON(p.paneRoot)
	tab := firstSavedResourceTab(saved)
	if tab == nil {
		t.Fatal("the saved layout has no resource tab")
	}
	if tab.Collection != "results" || tab.Query != "dex" {
		t.Fatalf("persisted %+v, want the collection and its query", *tab)
	}
	if tab.Matcher != "" || tab.Locator != "" {
		t.Fatalf("a collection tab persisted document fields: %+v", *tab)
	}
}

// The live-refresh binding watches what the PLUGIN declared and nothing else,
// and it reads it from the cached describe snapshot rather than resolving on
// the update goroutine.
func TestResourceWatchTargetsComeFromTheDescribeSnapshot(t *testing.T) {
	p, _, root, surface := collectionTestPlugin(t)
	runDeckCmd(t, p, p.openRequestedResourcePaneForSurface(root, surface, resourceview.Ref{
		Instance: "recall", Collection: "results",
	}))
	// Nothing is visible until a frame has been drawn, which is the same rule
	// every other kind's targets follow.
	if got := p.resourceWatchTargets(); len(got) != 0 {
		t.Fatalf("watch targets before any frame: %+v", got)
	}
	// The declared interval is what arms the poll ticker, and this plugin
	// declared one.
	view := p.resources[p.paneFocus]
	_ = view
	if seconds := activeCollectionPollSeconds(p); seconds != 30 {
		t.Fatalf("declared poll interval read as %d, want 30", seconds)
	}
}

func activeCollectionPollSeconds(p *Plugin) int {
	for _, res := range p.resources {
		if res == nil {
			continue
		}
		if view := res.view(); view != nil && view.Browser() != nil {
			return view.Browser().PanePollSeconds()
		}
	}
	return 0
}

func firstSavedResourceTab(j *state.PaneLayoutJSON) *state.PaneResourceTabJSON {
	if j == nil {
		return nil
	}
	if len(j.ResourceTabs) > 0 {
		return &j.ResourceTabs[0]
	}
	if j.Split == nil {
		return nil
	}
	if tab := firstSavedResourceTab(j.Split.A); tab != nil {
		return tab
	}
	return firstSavedResourceTab(j.Split.B)
}

// runDeckCmd drains a command and feeds every plugin-browser answer back in,
// which is what the app's update loop does for real.
func runDeckCmd(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	for depth := 0; cmd != nil && depth < 12; depth++ {
		msg := cmd()
		cmd = nil
		for _, m := range flattenMsgs(msg) {
			switch typed := m.(type) {
			case resourceview.OpenRowMsg:
				if next := p.openRowInResourceLeaf(typed.Ref); next != nil {
					cmd = next
				}
			default:
				if !pluginbrowser.IsBrowserMsg(m) {
					continue
				}
				if next := p.applyPluginBrowserMsg(m); next != nil {
					cmd = next
				}
			}
		}
	}
}

func flattenMsgs(msg tea.Msg) []tea.Msg {
	switch m := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, child := range m {
			if child == nil {
				continue
			}
			out = append(out, flattenMsgs(child())...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

func keyPressMsg(key string) tea.KeyPressMsg {
	if len([]rune(key)) == 1 {
		return tea.KeyPressMsg{Code: []rune(key)[0], Text: key}
	}
	switch key {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		return tea.KeyPressMsg{}
	}
}
