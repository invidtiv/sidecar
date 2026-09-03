package pluginbrowser

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/pluginhost"
	"github.com/marcus/sidecar/internal/resource"
)

// liveCalls wires the browser to a real Manager over the real fixture process:
// the same seam internal/app builds, with the same one-shot spawns, process
// groups, cache and cancellation behind it.
func liveCalls(t *testing.T, manager *pluginhost.Manager, opened *[]string) Calls {
	t.Helper()
	ctx := context.Background()
	return Calls{
		Describe: func(id string) (pluginhost.Description, pluginhost.Status, bool) {
			status, ok := manager.Status(id)
			if !ok {
				return pluginhost.Description{}, pluginhost.Status{}, false
			}
			desc, _ := manager.Description(id)
			return desc, status, true
		},
		List: func(call ListCall) tea.Cmd {
			return func() tea.Msg {
				page, err := manager.List(ctx, pluginhost.ListRequest{
					Instance: call.Instance, Params: call.Params,
					Context: call.Context, PaneKey: call.PaneKey,
				})
				return ListedMsg{
					Instance: call.Instance, Collection: call.Params.Collection,
					Generation: call.Generation, Append: call.Append, Page: page, Err: err,
				}
			}
		},
		Get: func(call GetCall) tea.Cmd {
			return func() tea.Msg {
				doc, err := manager.Get(ctx, pluginhost.GetRequest{
					Instance: call.Instance, Params: call.Params,
					Context: call.Context, Refresh: call.Refresh,
				})
				return GotMsg{
					Instance: call.Instance, Collection: call.Params.Collection,
					ID: call.Params.ID, Generation: call.Generation, Document: doc, Err: err,
				}
			}
		},
		Act: func(call ActCall) tea.Cmd {
			return func() tea.Msg {
				outcome, err := manager.Act(ctx, pluginhost.ActRequest{
					Instance: call.Instance, Params: call.Params, Context: call.Context,
				})
				return ActedMsg{
					Instance: call.Instance, Action: call.Params.Action,
					Generation: call.Generation, Outcome: outcome, Err: err,
				}
			}
		},
		OpenURL: func(url string) tea.Cmd {
			*opened = append(*opened, url)
			return nil
		},
		Context: func() *pluginhost.Context {
			return &pluginhost.Context{Project: &pluginhost.ProjectContext{
				Root: "/tmp/p", WorkDir: "/tmp/p", Name: "p",
			}}
		},
		Now: func() time.Time { return time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC) },
	}
}

// newLiveBrowser describes the real fixture and hands back a browser over it.
func newLiveBrowser(t *testing.T, opened *[]string, args ...string) *Model {
	t.Helper()
	instance := config.PluginInstance{
		PluginInstanceConfig: config.PluginInstanceConfig{
			ID:         "fixture",
			Command:    append([]string{fixtureBin}, args...),
			Enabled:    true,
			Scope:      config.PluginScopeGlobal,
			Placements: []string{config.PluginPlacementTab},
		},
		Source: config.PluginSourceExternal,
	}
	providers, disabled, err := pluginhost.FromInstances(
		[]config.PluginInstance{instance},
		pluginhost.Options{Dir: t.TempDir(), Home: t.TempDir()},
	)
	if err != nil {
		t.Fatalf("FromInstances: %v", err)
	}
	manager := pluginhost.NewManager(pluginhost.ManagerOptions{})
	manager.SetProviders(providers, disabled)
	manager.DescribeAll(context.Background())

	m := New("fixture", "fixture", liveCalls(t, manager, opened), nil)
	m.SetSize(160, 45)
	m.SetReservedKeys(map[string]bool{"1": true})
	run(t, m, m.Refresh())
	return m
}

// The fixture plugin driven end to end through the real host: describe, a
// query, a document, and an action with inputs, each one a real process.
func TestLiveFixtureQueryOpenAndAct(t *testing.T) {
	var opened []string
	m := newLiveBrowser(t, &opened)

	if !m.Described() {
		t.Fatalf("describe did not land: status = %+v", m.Status())
	}
	if got := m.Name(); got != "Fixture" {
		t.Fatalf("name = %q, want the plugin's own", got)
	}

	// The results collection declares project context, and the browser offers
	// project context, so it is listable.
	c, ok := m.ActiveCollection()
	if !ok || c.ID != "results" {
		t.Fatalf("active collection = %+v", c)
	}

	press(t, m, "/")
	for _, key := range []string{"d", "e", "x"} {
		m.HandleKey(keyPress(key))
	}
	press(t, m, "enter")

	view := strip(m.View())
	if !strings.Contains(view, "dex schema notes") {
		t.Fatalf("the live page did not render:\n%s", view)
	}
	if !strings.Contains(view, "answered") {
		t.Fatalf("the outcome is missing:\n%s", view)
	}

	press(t, m, "enter")
	view = strip(m.View())
	for _, want := range []string{"Fixture row", "── Evidence", "── Timeline"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the live document is missing %q:\n%s", want, view)
		}
	}

	run(t, m, m.startAction("log-note"))
	if m.overlay.kind != overlayForm {
		t.Fatalf("overlay = %v, want the form", m.overlay.kind)
	}
	m.overlay.inputs[0].area.SetValue("met at the workshop")
	run(t, m, m.submitAction())
	flash, isErr := m.Flash()
	if isErr || !strings.HasPrefix(flash, "Logged a note for ") {
		t.Fatalf("act outcome = %q err=%v", flash, isErr)
	}
}

// A degraded page and an abstained one are the fixture's own answers, and the
// host renders each honestly.
func TestLiveFixtureOutcomes(t *testing.T) {
	var opened []string
	m := newLiveBrowser(t, &opened)
	s := m.activeState()
	c, _ := m.ActiveCollection()

	s.query = "degraded"
	run(t, m, m.list(c, s, false))
	view := strip(m.View())
	if !strings.Contains(view, "degraded") || !strings.Contains(view, "did not answer") {
		t.Fatalf("degraded page:\n%s", view)
	}

	s.query = "nothing"
	run(t, m, m.list(c, s, false))
	view = strip(m.View())
	if !strings.Contains(view, "No matches.") {
		t.Fatalf("abstained page:\n%s", view)
	}

	// An outcome from a later protocol version must not read as a coverage
	// guarantee: the host coerces it to degraded.
	s.query = "future"
	run(t, m, m.list(c, s, false))
	view = strip(m.View())
	if !strings.Contains(view, "coverage was incomplete") {
		t.Fatalf("an unreadable outcome claimed to be an answer:\n%s", view)
	}
}

// A plugin whose describe fails is a setup card, not a frozen frame, and every
// hostile mode ends in one bounded card rather than a hang.
func TestLiveHostileDescribeModesEndInACard(t *testing.T) {
	for _, mode := range []string{"crash", "malformed", "no-output", "hang", "error-response", "wide-collection", "watch-escape"} {
		t.Run(mode, func(t *testing.T) {
			var opened []string
			m := newLiveBrowser(t, &opened, "-mode="+mode)
			if m.Described() {
				t.Fatalf("mode %q produced a usable description", mode)
			}
			view := strip(m.View())
			if strings.TrimSpace(view) == "" {
				t.Fatalf("mode %q rendered nothing", mode)
			}
			if !strings.Contains(view, "r  try again") {
				t.Fatalf("mode %q left no way forward:\n%s", mode, view)
			}
		})
	}
}

// A page whose every string is trying to escape the table is drawn inside the
// box like any other, because the host sanitized it before the browser saw it.
func TestLiveHostilePageStaysInsideTheBox(t *testing.T) {
	var opened []string
	m := newLiveBrowser(t, &opened, "-mode=hostile-page")
	s := m.activeState()
	c, _ := m.ActiveCollection()
	s.query = "dex"
	run(t, m, m.list(c, s, false))

	// Every size the tab can be, not just the one it was built at: a cell that
	// fits at 160 columns is the one that breaks the frame at 52.
	for _, size := range [][2]int{{160, 45}, {120, 30}, {80, 20}, {52, 18}} {
		assertBox(t, m, "hostile page", size[0], size[1])
	}

	m.SetSize(160, 45)
	view := m.View()
	// The frame's own styling is escape sequences, so the check is on what a
	// plugin's bytes could have added: BEL, OSC, a carriage return, or a tab,
	// each of which moves the cursor somewhere the box does not own.
	for _, forbidden := range []string{"\x07", "\x1b]", "\r", "\t"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("a control byte %q reached the frame", forbidden)
		}
	}
}

// The manager refuses a collection the newest describe never declared before it
// spawns anything, and the browser shows the typed refusal rather than an empty
// table that claims there is nothing there.
func TestLiveUndeclaredCollectionIsRefused(t *testing.T) {
	var opened []string
	m := newLiveBrowser(t, &opened)
	s := &collectionState{id: "ghost"}
	m.states["ghost"] = s
	cmd := m.list(pluginhost.Collection{ID: "ghost", Title: "Ghost"}, s, false)
	run(t, m, cmd)
	if s.err == nil || s.err.Code != resource.CodeInvalidRequest {
		t.Fatalf("err = %+v, want a typed refusal", s.err)
	}
}
