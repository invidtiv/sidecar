package overview

import (
	"regexp"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/terminallink"
)

// M1 of docs/plans/active/terminal-resource-providers.md: the global
// Workspaces browser answers an external resource key exactly as the project
// Workspace does, because both bind the same resourceview.Pane. What is proved
// here is the binding: that an unready provider changes nothing, that a click
// reaches the shared pane, that answers are scoped to the row that asked, and
// that none of it is written anywhere durable.

const resourceLine = "agent says CASH-1245 and CASH-1246 are ready"

// fakeResolver stands in for the provider manager the app wires. It records
// what it was asked and answers synchronously, so a test can run the command
// the view returned and see the result land.
type fakeResolver struct {
	mu    sync.Mutex
	calls []resourceview.Ref
	err   error
}

func (f *fakeResolver) resolve(modelID int, generation uint64, ref resourceview.Ref, refresh bool) tea.Cmd {
	f.mu.Lock()
	f.calls = append(f.calls, ref)
	err := f.err
	f.mu.Unlock()
	return func() tea.Msg {
		return resourceview.ResolvedMsg{
			ModelID: modelID, Generation: generation, Ref: ref, Refresh: refresh,
			Err: err,
			Document: resource.Document{
				Identity: ref.Locator,
				Title:    "Ticket " + ref.Locator,
			},
		}
	}
}

func (f *fakeResolver) refs() []resourceview.Ref {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]resourceview.Ref(nil), f.calls...)
}

func jiraMatchers() []terminallink.ResourceMatcher {
	return []terminallink.ResourceMatcher{{
		Provider: "jira-work",
		ID:       "project-key",
		Re:       regexp.MustCompile(`\bCASH-\d+\b`),
	}}
}

// resourcePreviewModel is the preview fixture with a resource key in the
// selected pane's output and no provider configured yet.
func resourcePreviewModel(t *testing.T) *Model {
	t.Helper()
	m, recorder := previewModel(t)
	recorder.output["%1"] = resourceLine + "\n"
	recorder.output["%2"] = "bravo has nothing to open\n"
	// Git-spec resolution would shell out for every word on the line; this
	// surface's own hook keeps the test off the developer's machine.
	m.previewSpecResolver = func(string, string) (string, bool) { return "", false }
	m.workspaces.SelectID("a")
	run(t, m, m.SetWorkspacesVisible(true))
	m.WorkspacesView(previewWide, previewTall)
	return m
}

func resourceTabLocators(res *previewResource) []string {
	if res == nil {
		return nil
	}
	var out []string
	for _, view := range res.tabs.All() {
		out = append(out, view.Reference().Locator)
	}
	return out
}

func clickResourceKey(t *testing.T, m *Model, key string) {
	t.Helper()
	action := previewNeedleAction(t, m, key)
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: action.X, Y: action.Y, Button: tea.MouseLeft}))
}

func TestGlobalUnreadyProviderLeavesResourceKeysPlain(t *testing.T) {
	m := resourcePreviewModel(t)

	for _, span := range m.decoratedPreviewSpans(resourceLine) {
		if span.Kind == terminallink.KindResource {
			t.Fatalf("a resource key was decorated with no matchers configured: %#v", span)
		}
	}
	view := m.WorkspacesView(previewWide, previewTall)
	if !strings.Contains(ansi.Strip(view), "CASH-1245") {
		t.Fatal("the fixture line is not on screen, so the underline check proves nothing")
	}
	clickResourceKey(t, m, "CASH-1245")
	if m.preview.resource != nil {
		t.Fatal("clicking plain text opened a Resource pane")
	}
}

func TestGlobalResourceClickOpensFocusesAndAddsTabs(t *testing.T) {
	m := resourcePreviewModel(t)
	resolver := &fakeResolver{}
	m.SetResourceMatchers(jiraMatchers())
	m.SetResourceResolver(resolver.resolve)

	spans := m.decoratedPreviewSpans(resourceLine)
	var keys []string
	for _, span := range spans {
		if span.Kind == terminallink.KindResource {
			keys = append(keys, span.Value)
		}
	}
	if len(keys) != 2 || keys[0] != "CASH-1245" || keys[1] != "CASH-1246" {
		t.Fatalf("decorated resource spans = %v, want both keys", keys)
	}

	clickResourceKey(t, m, "CASH-1245")
	res := m.preview.resource
	if res == nil {
		t.Fatal("clicking a resource key opened no Resource pane")
	}
	if got := resourceTabLocators(res); len(got) != 1 || got[0] != "CASH-1245" {
		t.Fatalf("tabs after first click = %v", got)
	}
	if !res.focused || !m.PreviewFocused() {
		t.Fatal("the click left the keyboard on the list")
	}
	if m.PreviewInteractive() {
		t.Fatal("a resource click started typing in the pane")
	}
	if refs := resolver.refs(); len(refs) != 1 ||
		refs[0] != (resourceview.Ref{Instance: "jira-work", Matcher: "project-key", Locator: "CASH-1245"}) {
		t.Fatalf("resolver asked for %#v", resolver.refs())
	}
	if view := res.view(); view == nil || view.State() != resourceview.StateReady {
		t.Fatalf("first tab did not resolve: %#v", res.view())
	}

	clickResourceKey(t, m, "CASH-1246")
	if got := resourceTabLocators(res); len(got) != 2 || got[1] != "CASH-1246" {
		t.Fatalf("tabs after second key = %v", got)
	}
	if res.tabs.ActiveIndex() != 1 {
		t.Fatalf("second key did not focus its own tab: active = %d", res.tabs.ActiveIndex())
	}

	// The same locator again is a focus, not a second tab.
	clickResourceKey(t, m, "CASH-1245")
	if got := resourceTabLocators(res); len(got) != 2 {
		t.Fatalf("re-clicking a key duplicated it: %v", got)
	}
	if res.tabs.ActiveIndex() != 0 {
		t.Fatalf("re-clicking CASH-1245 focused tab %d", res.tabs.ActiveIndex())
	}

	// The card is on screen and the pane still fits the box it was given.
	view := m.WorkspacesView(previewWide, previewTall)
	if !strings.Contains(ansi.Strip(view), "Ticket CASH-1245") {
		t.Fatal("the resolved document is not rendered in the Resource pane")
	}
	if lines := strings.Count(view, "\n") + 1; lines > previewTall {
		t.Fatalf("the Resource pane pushed the view to %d rows, over its %d", lines, previewTall)
	}
}

func TestGlobalResourcePaneDegradesWithNoResolver(t *testing.T) {
	m := resourcePreviewModel(t)
	m.SetResourceMatchers(jiraMatchers())

	clickResourceKey(t, m, "CASH-1245")
	res := m.preview.resource
	if res == nil {
		t.Fatal("clicking a resource key opened no Resource pane")
	}
	view := res.view()
	if view == nil || view.State() != resourceview.StateError {
		t.Fatalf("an unwired resolver left the tab in state %#v, want an error card", view)
	}
	if err := view.Err(); err == nil || err.Code != resource.CodeUnavailable {
		t.Fatalf("error card = %#v, want an unavailable provider", view.Err())
	}
}

func TestGlobalResourceResultAfterRowSwitchIsDiscarded(t *testing.T) {
	m := resourcePreviewModel(t)
	resolver := &fakeResolver{}
	m.SetResourceMatchers(jiraMatchers())
	m.SetResourceResolver(resolver.resolve)

	// Hold the resolve command rather than running it: this is the answer that
	// arrives after the user has moved on.
	inFlight := m.openPreviewResourceRef(resourceview.Ref{
		Instance: "jira-work", Matcher: "project-key", Locator: "CASH-1245",
	}, true)
	if inFlight == nil {
		t.Fatal("opening a resource produced no command")
	}
	opened := m.preview.resource
	if opened == nil || opened.view() == nil {
		t.Fatal("no Resource pane to strand")
	}

	m.workspaces.SelectID("b")
	run(t, m, m.previewSync())
	if m.preview.workspaceID != "b" {
		t.Fatalf("the row did not switch: %q", m.preview.workspaceID)
	}
	// The new row opens a Resource pane of its own, so the stale answer has a
	// live pane of the same kind to land in if the surface check is missing.
	run(t, m, m.openPreviewResourceRef(resourceview.Ref{
		Instance: "jira-work", Matcher: "project-key", Locator: "CASH-9999",
	}, true))
	other := m.preview.resource
	if other == nil || other == opened {
		t.Fatal("the second row did not get its own Resource pane")
	}

	run(t, m, inFlight)

	if state := opened.view().State(); state == resourceview.StateReady {
		t.Fatal("a provider answer landed in a pane the user had left")
	}
	if _, ok := opened.view().Document(); ok {
		t.Fatal("a discarded answer still set a document")
	}
	if got := resourceTabLocators(other); len(got) != 1 || got[0] != "CASH-9999" {
		t.Fatalf("the stale answer reached the other row's pane: %v", got)
	}
}

func TestGlobalResourceTabsAreMemoryOnly(t *testing.T) {
	m := resourcePreviewModel(t)
	m.SetResourceMatchers(jiraMatchers())
	m.SetResourceResolver((&fakeResolver{}).resolve)

	clickResourceKey(t, m, "CASH-1245")
	if m.preview.resource == nil {
		t.Fatal("no Resource pane opened")
	}
	// Persist is the host method the project surface uses to write references
	// to disk. Here it is deliberately nothing, so calling it must leave the
	// pane exactly as it was.
	before := resourceTabLocators(m.preview.resource)
	previewResourceHost{m: m, res: m.preview.resource}.Persist()
	if got := resourceTabLocators(m.preview.resource); len(got) != len(before) {
		t.Fatalf("Persist changed the pane: %v -> %v", before, got)
	}

	// A row switch keeps the tabs in paneCache — memory the process owns —
	// and nothing else.
	m.workspaces.SelectID("b")
	run(t, m, m.previewSync())
	if m.preview.resource != nil {
		t.Fatal("the other row inherited the Resource pane")
	}
	m.workspaces.SelectID("a")
	run(t, m, m.previewSync())
	if got := resourceTabLocators(m.preview.resource); len(got) != 1 || got[0] != "CASH-1245" {
		t.Fatalf("returning to the row lost the in-memory tabs: %v", got)
	}

	// A second model over the same environment starts with nothing, which is
	// what "not written down" means from the outside.
	fresh := resourcePreviewModel(t)
	if fresh.preview.resource != nil {
		t.Fatal("a fresh global browser restored resource tabs from somewhere")
	}
}

func TestGlobalResourcePaneClosesAndForgets(t *testing.T) {
	m := resourcePreviewModel(t)
	m.SetResourceMatchers(jiraMatchers())
	m.SetResourceResolver((&fakeResolver{}).resolve)

	clickResourceKey(t, m, "CASH-1245")
	clickResourceKey(t, m, "CASH-1246")
	if got := resourceTabLocators(m.preview.resource); len(got) != 2 {
		t.Fatalf("tabs before close = %v", got)
	}

	// x closes the active tab; the last one takes the pane with it.
	handled, cmd := m.WorkspacesKey(key("x"))
	if !handled {
		t.Fatal("x was not answered by the focused Resource pane")
	}
	run(t, m, cmd)
	if got := resourceTabLocators(m.preview.resource); len(got) != 1 {
		t.Fatalf("tabs after x = %v", got)
	}
	handled, cmd = m.WorkspacesKey(key("x"))
	if !handled {
		t.Fatal("x on the last tab was not answered")
	}
	run(t, m, cmd)
	if m.preview.resource != nil {
		t.Fatal("closing the last tab left the Resource pane behind")
	}
	if cached, ok := m.preview.paneCache[m.preview.workspaceID]; ok && cached.resource != nil {
		t.Fatal("a closed Resource pane stayed in the row cache")
	}
}

func TestGlobalResourceFooterMatchesTheSharedVocabulary(t *testing.T) {
	m := resourcePreviewModel(t)
	m.SetResourceMatchers(jiraMatchers())
	m.SetResourceResolver((&fakeResolver{}).resolve)
	clickResourceKey(t, m, "CASH-1245")

	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspacesResource {
		t.Fatalf("focus context = %q, want the resource context", got)
	}
	names := map[string]bool{}
	for _, cmd := range m.Commands() {
		names[cmd.Name] = true
	}
	for _, want := range resourceview.Commands() {
		if !names[want.Name] {
			t.Fatalf("footer is missing the shared %q command: %v", want.Name, names)
		}
	}
}
